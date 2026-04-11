package sdk

import (
	"context"
	"fmt"
	"testing"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
)

// TestOpenAI_SDK_ChatCompletions tests the OpenAI SDK with VibeRouter's /v1/chat/completions endpoint
func TestOpenAI_SDK_ChatCompletions(t *testing.T) {
	// Create client pointing to VibeRouter
	// Note: OpenAI SDK appends path directly, so base URL needs /v1
	client := openai.NewClient(
		option.WithBaseURL("http://localhost:8080/v1"),
		option.WithAPIKey("sk-d2479fc9142191ca6bdb7db90c16043ff75063032e3b76fabee05dd4e70cd8a4"),
	)

	ctx := context.Background()

	// Test 1: Non-streaming chat completions
	t.Run("NonStreaming", func(t *testing.T) {
		chatResp, err := client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
			Model: "auto",
			Messages: []openai.ChatCompletionMessageParamUnion{
				openai.UserMessage("Say 'Hello, World!' in exactly 3 words"),
			},
			MaxTokens: openai.Int(50),
		})
		if err != nil {
			t.Fatalf("Chat completions failed: %v", err)
		}

		if len(chatResp.Choices) == 0 {
			t.Fatal("No choices returned")
		}

		content := chatResp.Choices[0].Message.Content
		if content == "" {
			t.Fatal("Empty content returned")
		}
		fmt.Printf("Non-streaming response: %s\n", content)
	})

	// Test 2: Streaming chat completions
	t.Run("Streaming", func(t *testing.T) {
		chatStream := client.Chat.Completions.NewStreaming(ctx, openai.ChatCompletionNewParams{
			Model: "auto",
			Messages: []openai.ChatCompletionMessageParamUnion{
				openai.UserMessage("Count from 1 to 3"),
			},
			MaxTokens: openai.Int(50),
		})
		defer chatStream.Close()

		var fullContent string
		var chunkCount int

		for chatStream.Next() {
			chunk := chatStream.Current()
			if len(chunk.Choices) > 0 && chunk.Choices[0].Delta.Content != "" {
				fullContent += chunk.Choices[0].Delta.Content
				chunkCount++
			}
		}

		if err := chatStream.Err(); err != nil {
			t.Fatalf("Stream error: %v", err)
		}

		if chunkCount == 0 {
			t.Fatal("No chunks received")
		}
		fmt.Printf("Streaming response (%d chunks): %s\n", chunkCount, fullContent)
	})

	// Test 3: With system message
	t.Run("WithSystemMessage", func(t *testing.T) {
		chatResp, err := client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
			Model: "auto",
			Messages: []openai.ChatCompletionMessageParamUnion{
				openai.SystemMessage("You are a helpful assistant that speaks in emoji"),
				openai.UserMessage("Say hello"),
			},
			MaxTokens: openai.Int(50),
		})
		if err != nil {
			t.Fatalf("Chat completions with system message failed: %v", err)
		}

		content := chatResp.Choices[0].Message.Content
		fmt.Printf("With system message: %s\n", content)
	})

	// Test 4: With temperature
	t.Run("WithTemperature", func(t *testing.T) {
		chatResp, err := client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
			Model: "auto",
			Messages: []openai.ChatCompletionMessageParamUnion{
				openai.UserMessage("What is 2+2?"),
			},
			MaxTokens:   openai.Int(10),
			Temperature: openai.Float(0.0),
		})
		if err != nil {
			t.Fatalf("Chat completions with temperature failed: %v", err)
		}

		content := chatResp.Choices[0].Message.Content
		fmt.Printf("With temperature (0.0): %s\n", content)
	})
}

// TestOpenAI_SDK_ListModels tests the models endpoint via SDK
func TestOpenAI_SDK_ListModels(t *testing.T) {
	client := openai.NewClient(
		option.WithBaseURL("http://localhost:8080/v1"),
		option.WithAPIKey("sk-d2479fc9142191ca6bdb7db90c16043ff75063032e3b76fabee05dd4e70cd8a4"),
	)

	ctx := context.Background()

	models, err := client.Models.List(ctx)
	if err != nil {
		t.Fatalf("List models failed: %v", err)
	}

	fmt.Printf("Models count: %d\n", len(models.Data))
	for _, m := range models.Data {
		fmt.Printf("  - %s (%s)\n", m.ID, m.Object)
	}

	if len(models.Data) == 0 {
		t.Fatal("No models returned")
	}
}

// TestOpenAI_SDK_Embeddings tests the embeddings endpoint
// Note: This test may fail if the backend doesn't support embeddings for the configured model
func TestOpenAI_SDK_Embeddings(t *testing.T) {
	// Skip if backend doesn't support embeddings
	// The SiliconFlow backend used in this setup may not support embeddings
	t.Skip("Skipping embeddings test - backend may not support embeddings for configured model")

	client := openai.NewClient(
		option.WithBaseURL("http://localhost:8080/v1"),
		option.WithAPIKey("sk-d2479fc9142191ca6bdb7db90c16043ff75063032e3b76fabee05dd4e70cd8a4"),
	)

	ctx := context.Background()

	embedResp, err := client.Embeddings.New(ctx, openai.EmbeddingNewParams{
		Input: openai.EmbeddingNewParamsInputUnion{
			OfString: openai.String("Hello world"),
		},
	})
	if err != nil {
		t.Fatalf("Embeddings failed: %v", err)
	}

	if len(embedResp.Data) == 0 {
		t.Fatal("No embeddings returned")
	}

	embedding := embedResp.Data[0]
	fmt.Printf("Embedding for '%s', dimensions: %d\n", "Hello world", len(embedding.Embedding))
}
