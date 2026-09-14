package control

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"workbuddy-control-center/internal/authstore"
	"workbuddy-control-center/internal/upstream"
)

func newTestHTTPServer(t *testing.T, readOnly bool, withAccount bool) *httptest.Server {
	t.Helper()
	dir := t.TempDir()
	store, err := authstore.New(dir + "/auths")
	if err != nil {
		t.Fatal(err)
	}
	if withAccount {
		err = store.Save(&authstore.Account{
			UID:          "account-123456",
			Nickname:     "测试账号",
			AccessToken:  "test-access-token",
			RefreshToken: "test-refresh-token",
			ExpiresAt:    time.Now().Add(time.Hour).Unix(),
			Domain:       "https://example.invalid",
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	state, err := NewState(dir + "/data")
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{Username: "admin", Password: "test-password", AuthDir: dir + "/auths", DataDir: dir + "/data", TimeoutSeconds: 5, Timezone: "Asia/Shanghai", ReadOnly: readOnly}
	svc := NewService(cfg, store, upstream.New(cfg.Timeout()), state)
	return httptest.NewServer(NewServer(svc, http.NotFoundHandler()).Handler())
}

func request(t *testing.T, client *http.Client, method, url string, payload any, cookie *http.Cookie, origin bool) *http.Response {
	t.Helper()
	var body *bytes.Reader
	if payload == nil {
		body = bytes.NewReader(nil)
	} else {
		raw, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		t.Fatal(err)
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	if origin {
		req.Header.Set("Origin", url[:len(url)-len(req.URL.RequestURI())])
	}
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func login(t *testing.T, ts *httptest.Server) *http.Cookie {
	t.Helper()
	res := request(t, ts.Client(), http.MethodPost, ts.URL+"/api/login", map[string]string{"username": "admin", "password": "test-password"}, nil, false)
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d", res.StatusCode)
	}
	cookies := res.Cookies()
	if len(cookies) != 1 {
		t.Fatalf("expected one session cookie, got %#v", cookies)
	}
	if !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatalf("insecure session cookie: %#v", cookies[0])
	}
	return cookies[0]
}

func TestServerAuthenticationAndSameOriginGuard(t *testing.T) {
	ts := newTestHTTPServer(t, false, false)
	defer ts.Close()

	res := request(t, ts.Client(), http.MethodGet, ts.URL+"/api/overview", nil, nil, false)
	if res.StatusCode != http.StatusUnauthorized {
		res.Body.Close()
		t.Fatalf("unauthenticated overview status = %d", res.StatusCode)
	}
	res.Body.Close()

	cookie := login(t, ts)
	res = request(t, ts.Client(), http.MethodGet, ts.URL+"/api/overview", nil, cookie, false)
	if res.StatusCode != http.StatusOK {
		res.Body.Close()
		t.Fatalf("authenticated overview status = %d", res.StatusCode)
	}
	res.Body.Close()

	res = request(t, ts.Client(), http.MethodPut, ts.URL+"/api/automations/activity-probe", map[string]any{"enabled": true, "every_minutes": 30}, cookie, false)
	if res.StatusCode != http.StatusForbidden {
		res.Body.Close()
		t.Fatalf("write without origin status = %d", res.StatusCode)
	}
	res.Body.Close()

	res = request(t, ts.Client(), http.MethodPut, ts.URL+"/api/automations/activity-probe", map[string]any{"enabled": true, "every_minutes": 30}, cookie, true)
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("same-origin write status = %d", res.StatusCode)
	}
}

func TestMockSchedulerCRUDAndReadOnlyGuard(t *testing.T) {
	ts := newTestHTTPServer(t, false, true)
	defer ts.Close()
	cookie := login(t, ts)
	input := map[string]any{"account_uid": "account-123456", "name": "每日摘要", "cron": "0 9 * * *", "prompt": "本地演练", "enabled": true}

	res := request(t, ts.Client(), http.MethodPost, ts.URL+"/api/mock-scheduler-tasks", input, cookie, true)
	if res.StatusCode != http.StatusCreated {
		res.Body.Close()
		t.Fatalf("mock create status = %d", res.StatusCode)
	}
	var created MockSchedulerTaskView
	if err := json.NewDecoder(res.Body).Decode(&created); err != nil {
		res.Body.Close()
		t.Fatal(err)
	}
	res.Body.Close()
	if created.ID == "" || created.Nickname != "测试账号" {
		t.Fatalf("unexpected mock task: %#v", created)
	}

	input["name"] = "晚间摘要"
	res = request(t, ts.Client(), http.MethodPut, ts.URL+"/api/mock-scheduler-tasks/"+created.ID, input, cookie, true)
	if res.StatusCode != http.StatusOK {
		res.Body.Close()
		t.Fatalf("mock update status = %d", res.StatusCode)
	}
	res.Body.Close()
	res = request(t, ts.Client(), http.MethodDelete, ts.URL+"/api/mock-scheduler-tasks/"+created.ID, nil, cookie, true)
	if res.StatusCode != http.StatusOK {
		res.Body.Close()
		t.Fatalf("mock delete status = %d", res.StatusCode)
	}
	res.Body.Close()

	readOnly := newTestHTTPServer(t, true, true)
	defer readOnly.Close()
	readOnlyCookie := login(t, readOnly)
	res = request(t, readOnly.Client(), http.MethodPost, readOnly.URL+"/api/mock-scheduler-tasks", input, readOnlyCookie, true)
	if res.StatusCode != http.StatusForbidden {
		res.Body.Close()
		t.Fatalf("read-only mock create status = %d", res.StatusCode)
	}
	res.Body.Close()
	res = request(t, readOnly.Client(), http.MethodPost, readOnly.URL+"/api/automations/activity-probe/run", nil, readOnlyCookie, true)
	if res.StatusCode != http.StatusForbidden {
		res.Body.Close()
		t.Fatalf("read-only automation run status = %d", res.StatusCode)
	}
	res.Body.Close()
}
