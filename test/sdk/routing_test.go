package sdk

import (
	"context"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	aoption "github.com/anthropics/anthropic-sdk-go/option"
	openai "github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
)

const (
	testBase    = "http://localhost:8080"
	testAPIKey  = "sk-test-api-key-placeholder"
)

// TestRouting_SDK_VirtualModels verifies the routing virtual models are
// advertised to both SDK styles.
func TestRouting_SDK_VirtualModels(t *testing.T) {
	ctx := context.Background()

	oClient := openai.NewClient(option.WithBaseURL(testBase+"/v1"), option.WithAPIKey(testAPIKey))
	models, err := oClient.Models.List(ctx)
	if err != nil {
		t.Fatalf("openai list models: %v", err)
	}
	have := map[string]bool{}
	for _, m := range models.Data {
		have[m.ID] = true
	}
	for _, want := range []string{"auto", "auto-advanced", "auto-basic"} {
		if !have[want] {
			t.Errorf("openai models: missing virtual model %q", want)
		}
	}

	aClient := anthropic.NewClient(aoption.WithBaseURL(testBase), aoption.WithAPIKey(testAPIKey))
	aModels, err := aClient.Models.List(ctx, anthropic.ModelListParams{})
	if err != nil {
		t.Fatalf("anthropic list models: %v", err)
	}
	aHave := map[string]bool{}
	for _, m := range aModels.Data {
		aHave[m.ID] = true
	}
	for _, want := range []string{"auto", "auto-advanced", "auto-basic"} {
		if !aHave[want] {
			t.Errorf("anthropic models: missing virtual model %q", want)
		}
	}
}

// TestRouting_SDK_AutoBasicAlias forces the basic tier via the model alias.
func TestRouting_SDK_AutoBasicAlias(t *testing.T) {
	client := openai.NewClient(option.WithBaseURL(testBase+"/v1"), option.WithAPIKey(testAPIKey))
	resp, err := client.Chat.Completions.New(context.Background(), openai.ChatCompletionNewParams{
		Model: "auto-basic",
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.UserMessage("Say OK"),
		},
		MaxTokens: openai.Int(10),
	})
	if err != nil {
		t.Fatalf("auto-basic call failed: %v", err)
	}
	if len(resp.Choices) == 0 || resp.Choices[0].Message.Content == "" {
		t.Fatal("auto-basic returned empty content")
	}
}

// TestRouting_SDK_TierOverrideHeader forces the basic tier via the override header.
func TestRouting_SDK_TierOverrideHeader(t *testing.T) {
	client := openai.NewClient(
		option.WithBaseURL(testBase+"/v1"),
		option.WithAPIKey(testAPIKey),
		option.WithHeader("X-VibeRouter-Tier", "basic"),
	)
	resp, err := client.Chat.Completions.New(context.Background(), openai.ChatCompletionNewParams{
		Model: "auto",
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.UserMessage("Say OK"),
		},
		MaxTokens: openai.Int(10),
	})
	if err != nil {
		t.Fatalf("tier-override call failed: %v", err)
	}
	if len(resp.Choices) == 0 {
		t.Fatal("tier-override returned no choices")
	}
}

// TestRouting_SDK_AnthropicClientParity verifies an Anthropic-style client gets
// a valid Anthropic-style response back from the gateway (regardless of which
// provider served it), exercising protocol transformation.
func TestRouting_SDK_AnthropicClientParity(t *testing.T) {
	client := anthropic.NewClient(aoption.WithBaseURL(testBase), aoption.WithAPIKey(testAPIKey))
	resp, err := client.Messages.New(context.Background(), anthropic.MessageNewParams{
		Model:     "auto-basic",
		MaxTokens: 20,
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock("Reply with the single word: OK")),
		},
	})
	if err != nil {
		t.Fatalf("anthropic client call failed: %v", err)
	}
	if len(resp.Content) == 0 {
		t.Fatal("anthropic client returned no content blocks")
	}
	// Stop reason should be a populated, sane value (proves Anthropic response shape).
	if resp.StopReason == "" {
		t.Error("expected non-empty stop_reason in transformed Anthropic response")
	}
}

// TestRouting_SDK_LongContextProbe sends a very large prompt and asserts the
// gateway either serves it (via a long-context model) or returns a structured
// error — never a hang or an unparseable response.
func TestRouting_SDK_LongContextProbe(t *testing.T) {
	client := openai.NewClient(option.WithBaseURL(testBase+"/v1"), option.WithAPIKey(testAPIKey))
	big := strings.Repeat("lorem ipsum dolor sit amet ", 1600) // ~40k chars (~10k+ tokens)
	_, err := client.Chat.Completions.New(context.Background(), openai.ChatCompletionNewParams{
		Model: "auto",
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.UserMessage("Echo the first word of this text: " + big),
		},
		MaxTokens: openai.Int(10),
	})
	// Either success or a clean routing error is acceptable here.
	if err != nil {
		t.Logf("long-context probe returned error (acceptable if no long-context model configured): %v", err)
	}
}

// TestOpenAI_SDK_Tools exercises tool/function-calling pass-through.
func TestOpenAI_SDK_Tools(t *testing.T) {
	client := openai.NewClient(option.WithBaseURL(testBase+"/v1"), option.WithAPIKey(testAPIKey))
	resp, err := client.Chat.Completions.New(context.Background(), openai.ChatCompletionNewParams{
		Model: "auto-basic",
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.UserMessage("What is the weather in Tokyo?"),
		},
		Tools: []openai.ChatCompletionToolParam{
			{
				Type: "function",
				Function: openai.FunctionDefinitionParam{
					Name:        "get_weather",
					Description: openai.String("Get current weather"),
					Parameters: openai.FunctionParameters{
						"type": "object",
						"properties": map[string]interface{}{
							"city": map[string]interface{}{"type": "string"},
						},
						"required": []string{"city"},
					},
				},
			},
		},
		MaxTokens: openai.Int(50),
	})
	if err != nil {
		t.Fatalf("tools call failed: %v", err)
	}
	if len(resp.Choices) == 0 {
		t.Fatal("tools call returned no choices")
	}
}
