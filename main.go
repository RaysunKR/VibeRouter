package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/viberouter/viberouter/internal/auth"
	"github.com/viberouter/viberouter/internal/config"
	"github.com/viberouter/viberouter/internal/logger"
	"github.com/viberouter/viberouter/internal/router"
	"github.com/viberouter/viberouter/internal/server"
	"github.com/viberouter/viberouter/internal/upstream"
)

func main() {
	configPath := flag.String("config", "config.yaml", "path to config.yaml")
	envPath := flag.String("env", ".env", "path to .env")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	am, seeded, err := auth.LoadEnv(*envPath)
	if err != nil {
		log.Fatalf("load .env: %v", err)
	}
	if seeded {
		log.Println("==> 首次启动：已生成默认管理员账号 admin/admin，请尽快在 .env 中修改 ADMIN_PASSWORD_HASH")
	}

	snap := cfg.Snapshot()
	fl, err := logger.Open(snap.Logs.File, snap.Logs.RetentionDays)
	if err != nil {
		log.Fatalf("open log file: %v", err)
	}
	defer func() { _ = fl.Close() }()

	rt := router.New(cfg, upstream.Dispatcher(cfg))
	srv := server.New(cfg, rt, am, fl)

	httpSrv := &http.Server{Addr: snap.Server.Addr, Handler: srv.Handler()}
	go func() {
		log.Printf("VibeRouter listening on %s", snap.Server.Addr)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("serve: %v", err)
		}
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
	log.Println("shutting down")
	_ = httpSrv.Close()
}
