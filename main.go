package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"
	"viberouter/internal/config"
	"viberouter/internal/router"
	"viberouter/internal/service"

	"github.com/gin-gonic/gin"
)

func main() {
	// Load config
	cfg, err := config.Load("")
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	// Ensure a login-able admin user exists (seeds admin/admin on first run).
	config.EnsureDefaultAdmin(cfg)

	// Set Gin mode
	gin.SetMode(cfg.Server.Mode)

	// Initialize file logger (JSON Lines at cfg.Log.File)
	if err := service.InitFileLogger(cfg.Log.File); err != nil {
		log.Fatalf("failed to initialize file logger: %v", err)
	}

	// Initialize load balancer with config-based backend models
	service.InitLoadBalancer(cfg)

	// Setup router
	r := router.Setup()

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		addr := cfg.Server.Address
		if addr == "" {
			addr = ":8080"
		}
		log.Printf("Server starting on %s", addr)
		if err := r.Run(addr); err != nil {
			log.Fatalf("failed to start server: %v", err)
		}
	}()

	<-quit
	log.Println("Shutting down server...")
}
