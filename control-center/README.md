# WorkBuddy Control Center

本地、独立的 WorkBuddy 账号控制台。它遵循 workbuddy2api Issue #61 的边界：

- 只共享 `auths/` 账号凭据目录。
- 账号、积分、模型、活动和账号云端定时任务均直接请求 WorkBuddy 上游。
- 不读取、不写入 `workbuddy2api/config.json`，不调用网关 HTTP 接口，不使用 Docker Socket。

## 已实现

- 登录保护、HttpOnly + SameSite=Strict 会话、同源写操作校验、登录限速。
- 国内版和国际版 OAuth 添加账号，凭据原子写入 `auths/`。
- 账号列表、单账号刷新凭据、签到、旅行巡检。
- 积分总览和按账号查询。今日区域展示上游“套餐发放、已用、剩余”，不把套餐切片伪造为交易流水。
- 直接查询每账号可用模型、成长活动任务和新任务探测。
- 面板自身的持久化定时任务开关、立即执行和运行记录。
- 每个账号的 WorkBuddy 云端 scheduler 定时任务只读列表，接口为 `/console/as/tasks`。
- 当上游 scheduler 尚未授权时，提供明确标注的本地 Mock 任务骨架，可创建、编辑、启停和删除；仅写入本控制台的 `data/state.json`，不会向 WorkBuddy 提交任务变更。

账号云端 scheduler 的创建 API 为 `/v2/as/scheduler/tasks`。逆向资料没有经认证的请求体契约，且实际账号当前返回 `access_denied`，因此真实云端任务保持只读。Mock 数据不会自动同步到上游，待权限和契约确认后再以独立适配器接入。

## 本机运行

复制配置并填写一个强密码：

```sh
cp config.example.json control-center.json
# 编辑 control-center.json：至少设置 password 和 auth_dir
docker build -t workbuddy-control-center .
docker run --rm -p 127.0.0.1:8787:8787 \
  -e WBCC_PASSWORD='替换为强密码' \
  -e WBCC_AUTH_DIR=/auths \
  -v ../auths:/auths \
  -v "$PWD/data:/data" \
  workbuddy-control-center
```

访问 `http://127.0.0.1:8787`。使用 Compose 时，以本机环境变量注入密码：`WBCC_PASSWORD='独立强密码' docker compose up -d --build`。默认只绑定本机；若经反向代理开放到局域网或公网，请使用 HTTPS、独立强密码和受限访问策略。

## 自动化所有权

为了避免同一账号双跑，控制台默认只启用“新活动探测”。如果要让本面板接管签到、旅行、保活，需要先在部署层关闭网关中同名定时项；这不改网关源码，但必须由操作者确认完成。
