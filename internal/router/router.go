package router

import (
	"net/http"
	"path/filepath"
	"viberouter/internal/handler"
	"viberouter/internal/middleware"
	"viberouter/internal/service"

	"github.com/gin-gonic/gin"
)

func Setup() *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(middleware.CORSMiddleware())
	r.Use(middleware.RequestIDMiddleware())

	// Initialize services
	lb := service.GetLoadBalancer()
	callLogSvc := service.NewCallLogService()

	openAIHandler := handler.NewOpenAIHandler(lb, callLogSvc)
	anthropicHandler := handler.NewAnthropicHandler(lb, callLogSvc)
	adminHandler := handler.NewAdminHandler(callLogSvc)

	// Serve static web files
	r.Static("/static", "./web/static")
	r.GET("/", func(c *gin.Context) {
		c.File(filepath.Join(".", "web", "static", "index.html"))
	})
	r.GET("/favicon.ico", func(c *gin.Context) { c.Status(http.StatusNoContent) })

	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	// Auth (login is public; logout/me require a session)
	r.POST("/auth/login", adminHandler.Login)
	authed := r.Group("")
	authed.Use(middleware.AdminAuthMiddleware())
	{
		authed.POST("/auth/logout", adminHandler.Logout)
		authed.GET("/auth/me", adminHandler.Me)
	}

	// Admin management API (session-protected)
	admin := r.Group("/admin", middleware.AdminAuthMiddleware())
	{
		admin.GET("/config", adminHandler.GetConfig)

		admin.GET("/models", adminHandler.ListModels)
		admin.POST("/models", adminHandler.CreateModel)
		admin.PUT("/models", adminHandler.UpdateModel)
		admin.DELETE("/models", adminHandler.DeleteModel)

		admin.GET("/routing", adminHandler.GetRouting)
		admin.PUT("/routing", adminHandler.UpdateRouting)

		admin.GET("/keys", adminHandler.ListKeys)
		admin.POST("/keys", adminHandler.UpsertKey)
		admin.DELETE("/keys", adminHandler.DeleteKey)

		admin.GET("/logs", adminHandler.Logs)
	}

	// Proxy endpoints (API-key auth)
	v1 := r.Group("/v1")
	v1.Use(middleware.AuthMiddleware())
	{
		v1.POST("/messages", anthropicHandler.Messages)
		v1.POST("/chat/completions", openAIHandler.ChatCompletions)
		v1.POST("/completions", openAIHandler.Completions)
		v1.POST("/embeddings", openAIHandler.Embeddings)
		v1.GET("/models", openAIHandler.ListModels)
		v1.GET("/models/*model", openAIHandler.GetModel)
	}

	return r
}
