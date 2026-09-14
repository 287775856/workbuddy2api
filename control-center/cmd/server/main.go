// workbuddy-control-center is an independent local panel. It never imports or
// mutates the workbuddy2api gateway configuration.
package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"workbuddy-control-center/internal/authstore"
	"workbuddy-control-center/internal/control"
	"workbuddy-control-center/internal/upstream"
	"workbuddy-control-center/internal/webui"
)

var version = "dev"

func main() {
	path := flag.String("config", "control-center.json", "控制台配置文件")
	flag.Parse()
	cfg, err := control.LoadConfig(*path)
	if err != nil {
		log.Fatalf("控制台配置无效: %v", err)
	}
	store, err := authstore.New(cfg.AuthDir)
	if err != nil {
		log.Fatalf("初始化 auths 失败: %v", err)
	}
	state, err := control.NewState(cfg.DataDir)
	if err != nil {
		log.Fatalf("初始化控制台状态失败: %v", err)
	}
	svc := control.NewService(cfg, store, upstream.New(cfg.Timeout()), state)
	static, err := webui.Handler()
	if err != nil {
		log.Fatalf("初始化前端资源失败: %v", err)
	}
	server := control.NewServer(svc, static)
	httpServer := &http.Server{Addr: cfg.Listen, Handler: server.Handler(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				svc.Tick(ctx)
			}
		}
	}()
	log.Printf("WorkBuddy Control Center %s", version)
	log.Printf("  面板地址: http://%s", cfg.Listen)
	log.Printf("  账号凭据: %s", store.Dir())
	log.Printf("  网关配置: 未读取；网关代码: 未调用")
	go func() {
		<-ctx.Done()
		shutdown, c := context.WithTimeout(context.Background(), 5*time.Second)
		defer c()
		_ = httpServer.Shutdown(shutdown)
	}()
	if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("服务启动失败: %v", err)
	}
}
