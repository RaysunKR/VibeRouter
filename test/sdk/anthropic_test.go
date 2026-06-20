package sdk

import (
	"context"
	"fmt"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// TestAnthropic_SDK_Messages tests the Anthropic SDK with VibeRouter's /v1/messages endpoint
func TestAnthropic_SDK_Messages(t *testing.T) {
	// Create client pointing to VibeRouter
	// Note: SDK appends /v1/messages automatically, so base URL should not include /v1
	client := anthropic.NewClient(
		option.WithBaseURL("http://localhost:8080"),
		option.WithAPIKey("sk-test-api-key-placeholder"),
	)

	ctx := context.Background()

	// Test 1: Non-streaming messages
	t.Run("NonStreaming", func(t *testing.T) {
		msgResp, err := client.Messages.New(ctx, anthropic.MessageNewParams{
			Model:     "auto",
			MaxTokens: 100,
			Messages: []anthropic.MessageParam{
				anthropic.NewUserMessage(anthropic.NewTextBlock("Say 'Hello, Anthropic!' in exactly 4 words")),
			},
		})
		if err != nil {
			t.Fatalf("Messages failed: %v", err)
		}

		if len(msgResp.Content) == 0 {
			t.Fatal("No content returned")
		}

		textBlock := msgResp.Content[0].Text
		fmt.Printf("Non-streaming response: %s\n", textBlock)
	})

	// Test 2: Streaming messages
	t.Run("Streaming", func(t *testing.T) {
		msgStream := client.Messages.NewStreaming(ctx, anthropic.MessageNewParams{
			Model:     "auto",
			MaxTokens: 100,
			Messages: []anthropic.MessageParam{
				anthropic.NewUserMessage(anthropic.NewTextBlock("Count from 1 to 3")),
			},
		})

		var fullText string
		var eventCount int

		for msgStream.Next() {
			event := msgStream.Current()
			eventCount++

			switch e := event.Type; e {
			case "content_block_delta":
				delta := event.AsContentBlockDelta()
				fullText += delta.Delta.Text
			case "message_start":
				fmt.Printf("Message started: %s\n", event.AsMessageStart().Message.ID)
			case "message_delta":
				delta := event.AsMessageDelta()
				fmt.Printf("Message delta, output tokens: %d\n", delta.Usage.OutputTokens)
			}
		}

		if err := msgStream.Err(); err != nil {
			t.Fatalf("Stream error: %v", err)
		}

		msgStream.Close()

		fmt.Printf("Streaming response (%d events): %s\n", eventCount, fullText)
	})

	// Test 3: With system message
	t.Run("WithSystem", func(t *testing.T) {
		msgResp, err := client.Messages.New(ctx, anthropic.MessageNewParams{
			Model:     "auto",
			MaxTokens: 100,
			System: []anthropic.TextBlockParam{
				{
					Text: "You are a helpful assistant that responds with only facts",
				},
			},
			Messages: []anthropic.MessageParam{
				anthropic.NewUserMessage(anthropic.NewTextBlock("What is the capital of France?")),
			},
		})
		if err != nil {
			t.Fatalf("Messages with system failed: %v", err)
		}

		textBlock := msgResp.Content[0].Text
		fmt.Printf("With system: %s\n", textBlock)
	})

	// Test 4: With temperature
	t.Run("WithTemperature", func(t *testing.T) {
		msgResp, err := client.Messages.New(ctx, anthropic.MessageNewParams{
			Model:       "auto",
			MaxTokens:   10,
			Temperature: anthropic.Float(0.0),
			Messages: []anthropic.MessageParam{
				anthropic.NewUserMessage(anthropic.NewTextBlock("What is 2+2?")),
			},
		})
		if err != nil {
			t.Fatalf("Messages with temperature failed: %v", err)
		}

		textBlock := msgResp.Content[0].Text
		fmt.Printf("With temperature (0.0): %s\n", textBlock)
	})
}

// TestAnthropic_SDK_ListModels tests the models endpoint via SDK
func TestAnthropic_SDK_ListModels(t *testing.T) {
	client := anthropic.NewClient(
		option.WithBaseURL("http://localhost:8080"),
		option.WithAPIKey("sk-test-api-key-placeholder"),
	)

	ctx := context.Background()

	models, err := client.Models.List(ctx, anthropic.ModelListParams{})
	if err != nil {
		t.Fatalf("List models failed: %v", err)
	}

	fmt.Printf("Models count: %d\n", len(models.Data))
	for _, m := range models.Data {
		fmt.Printf("  - %s\n", m.ID)
	}

	if len(models.Data) == 0 {
		t.Fatal("No models returned")
	}
}
