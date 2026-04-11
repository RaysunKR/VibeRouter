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

	// Serve static web files
	r.Static("/static", "./web/static")
	r.GET("/", func(c *gin.Context) {
		c.File(filepath.Join(".", "web", "static", "index.html"))
	})

	// Health check
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	// Model routing - unified group for OpenAI and Anthropic compatible endpoints
	v1 := r.Group("/v1")
	v1.Use(middleware.AuthMiddleware())
	{
		// Anthropic endpoints (more specific)
		v1.POST("/messages", anthropicHandler.Messages)

		// OpenAI endpoints
		v1.POST("/chat/completions", openAIHandler.ChatCompletions)
		v1.POST("/completions", openAIHandler.Completions)
		v1.POST("/embeddings", openAIHandler.Embeddings)
		v1.GET("/models", openAIHandler.ListModels)
		v1.GET("/models/*model", openAIHandler.GetModel)
	}

	return r
}
