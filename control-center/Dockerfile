# WorkBuddy Control Center —— 独立本地控制台。
# 它不读取网关 config.json，不调用 Docker，也不挂载 docker.sock。

# ── 阶段 1：构建前端 ────────────────────────────────────────────
FROM node:20-alpine AS web
WORKDIR /src
# 先只拷 manifest，让依赖层可缓存（源码改动不会触发重新 npm ci）。
COPY web/package.json web/package-lock.json* ./web/
RUN cd web && (npm ci --no-audit --no-fund || npm install --no-audit --no-fund)
# 拷源码与 embed 占位目录的父路径（vite outDir 指向 ../internal/webui/dist）。
COPY web ./web
COPY internal/webui/dist ./internal/webui/dist
RUN cd web && npm run build

# ── 阶段 2：编译 Go 后端（含前端产物 embed）────────────────────
FROM golang:1.23-alpine AS build
ARG VERSION=dev
WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY . .
# 用前端阶段产出的真实 dist 覆盖源码树里的占位文件。
COPY --from=web /src/internal/webui/dist ./internal/webui/dist
RUN CGO_ENABLED=0 go build -trimpath \
      -ldflags="-s -w -X main.version=${VERSION}" \
      -o /out/workbuddy-control-center ./cmd/server

# ── 阶段 3：运行镜像 ────────────────────────────────────────────
FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata wget \
 && adduser -D -u 10001 app \
 && mkdir -p /app /data \
 && chown -R app:app /app /data
WORKDIR /app
COPY --from=build /out/workbuddy-control-center /app/workbuddy-control-center
# 默认以非 root 运行。
USER app
EXPOSE 8787
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s \
  CMD wget -qO- http://127.0.0.1:8787/api/session >/dev/null || exit 1
ENTRYPOINT ["/app/workbuddy-control-center"]
