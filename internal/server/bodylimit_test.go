package server

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"workbuddy2api/internal/auth"
)

// TestChatOversizedBodyReturns413 请求体超限必须返回 413，而不是静默截断喂给上游。
//
// 背景（issue #41）：旧逻辑用 io.LimitReader(r.Body, 8<<20) 静默截断，半截 JSON
// 发给上游触发 11101 unexpected EOF，网关却把它归为客户端错误罚号 + 轮转耗尽 503
// —— 根因在网关却让账号背锅。现在超限直接 413，且不打上游、不罚账号、不轮转。
func TestChatOversizedBodyReturns413(t *testing.T) {
	var calls int
	up := newFakeUpstream(t, func(authz string) (int, string, bool) {
		calls++
		return 200, sseOK, true
	})
	p := testPoolWith(&auth.Auth{UID: "u1", AccessToken: "at1", ExpiresAt: 9999999999})
	h := NewHandler(Config{Pool: p, Upstream: up, MaxBodyBytes: 100})

	const prefix = `{"model":"glm-5.2","messages":[],"pad":"`
	const suffix = `"}`
	// 构造刚好 101 字节 > 100 上限的请求体。
	pad := strings.Repeat("a", 100-len(prefix)-len(suffix)+1)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/v1/chat/completions",
		strings.NewReader(prefix+pad+suffix)))

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("code=%d body=%s want 413", rec.Code, rec.Body)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "request_body_too_large") {
		t.Errorf("响应应带 request_body_too_large: %s", body)
	}
	if !strings.Contains(body, "server.max_body_mb") {
		t.Errorf("413 文案应指出配置项 server.max_body_mb: %s", body)
	}
	if calls != 0 {
		t.Errorf("413 不应调用上游, got %d", calls)
	}
	st, _ := p.Status("u1")
	if st.Cooling || st.Disabled || st.ErrTotal != 0 {
		t.Errorf("413 不应惩罚账号: %+v", st)
	}
}

// TestChatOversizedBodyDefaultLimit 未显式配置时兜底 8MB：8MB+1 必须 413。
func TestChatOversizedBodyDefaultLimit(t *testing.T) {
	var calls int
	up := newFakeUpstream(t, func(authz string) (int, string, bool) {
		calls++
		return 200, sseOK, true
	})
	p := testPoolWith(&auth.Auth{UID: "u1", AccessToken: "at1", ExpiresAt: 9999999999})
	h := NewHandler(Config{Pool: p, Upstream: up}) // 不注入 MaxBodyBytes → 默认 8MB

	body := make([]byte, 8<<20+1) // 8MB+1
	copy(body, `{"model":"glm-5.2","messages":[]}`)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewReader(body)))

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("code=%d want 413（8MB+1 必须被拒）", rec.Code)
	}
	if calls != 0 {
		t.Errorf("不应调用上游, got %d", calls)
	}
}

// TestChatBodyAtLimitAllowed 恰好等于上限的请求体必须正常放行
// （探测逻辑用 limit+1，不能在边界上误杀）。
func TestChatBodyAtLimitAllowed(t *testing.T) {
	var calls int
	up := newFakeUpstream(t, func(authz string) (int, string, bool) {
		calls++
		return 200, sseOK, true
	})
	p := testPoolWith(&auth.Auth{UID: "u1", AccessToken: "at1", ExpiresAt: 9999999999})

	const prefix = `{"model":"glm-5.2","messages":[],"pad":"`
	const suffix = `"}`
	limit := 200
	pad := strings.Repeat("a", limit-len(prefix)-len(suffix))
	payload := prefix + pad + suffix
	if len(payload) != limit {
		t.Fatalf("测试构造错误: len=%d want %d", len(payload), limit)
	}

	h := NewHandler(Config{Pool: p, Upstream: up, MaxBodyBytes: int64(limit)})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(payload)))

	if rec.Code == http.StatusRequestEntityTooLarge {
		t.Fatalf("恰好等于上限不应 413: %s", rec.Body)
	}
	if calls == 0 {
		t.Error("等于上限的请求应正常转发到上游")
	}
}

// TestChatBadParamsRotatesWithoutPenalty 上游 400 + 11101（请求体解析失败）应：
// 换号重试并成功，且被换掉的账号**不留任何惩罚痕迹**（无冷却/熔断/errTotal）。
//
// 背景（issue #41 连带）：这本质是"发给上游的 body 有问题"，与账号健康无关；
// 旧分类把它归为通用 ErrClient，一旦 body 同时命中限流文案还会被误判 soft_rate 罚号。
func TestChatBadParamsRotatesWithoutPenalty(t *testing.T) {
	calls := map[string]int{}
	up := newFakeUpstream(t, func(authz string) (int, string, bool) {
		calls[authz]++
		if authz == "Bearer at-bad" {
			return 400, `{"code":11101,"msg":"Unmarshal chat params failed with error: unexpected EOF"}`, false
		}
		return 200, sseOK, true
	})
	p := testPoolWith(
		&auth.Auth{UID: "bad", AccessToken: "at-bad", ExpiresAt: 9999999999},
		&auth.Auth{UID: "good", AccessToken: "at-good", ExpiresAt: 9999999999},
	)
	// 让 bad 积分更高被优先选中（确定性轮转：bad 失败 → good）。
	p.SetCredits("bad", 2000)
	p.SetCredits("good", 1000)

	h := NewHandler(Config{Pool: p, Upstream: up})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/v1/chat/completions",
		strings.NewReader(`{"model":"glm-5.2","messages":[]}`)))

	if rec.Code != 200 {
		t.Fatalf("code=%d body=%s（应换号到 good 后成功）", rec.Code, rec.Body)
	}
	if calls["Bearer at-bad"] != 1 || calls["Bearer at-good"] != 1 {
		t.Errorf("calls=%v want bad/good 各 1 次", calls)
	}
	// 关键断言：bad 账号完好无损。
	st, _ := p.Status("bad")
	if st.Cooling || st.Disabled || st.ErrTotal != 0 || st.BreakerFails != 0 {
		t.Errorf("11101 不应惩罚账号: %+v", st)
	}
}

// TestChatAllBadParams503CarriesUpstreamBody 全部账号都返回 11101 时，
// 503 文案必须带上上游原始信息（便于定位是客户端 body 问题）。
func TestChatAllBadParams503CarriesUpstreamBody(t *testing.T) {
	up := newFakeUpstream(t, func(authz string) (int, string, bool) {
		return 400, `{"code":11101,"msg":"Unmarshal chat params failed with error: unexpected EOF"}`, false
	})
	p := testPoolWith(&auth.Auth{UID: "u1", AccessToken: "at1", ExpiresAt: 9999999999})
	h := NewHandler(Config{Pool: p, Upstream: up})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/v1/chat/completions",
		strings.NewReader(`{"model":"glm-5.2","messages":[]}`)))

	if rec.Code != 503 {
		t.Fatalf("code=%d body=%s want 503", rec.Code, rec.Body)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "11101") || !strings.Contains(body, "Unmarshal chat params failed") {
		t.Errorf("503 文案应包含上游 11101 信息: %s", body)
	}
}
