package openai_test

import (
	"context"
	"fmt"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider/openai"
	"github.com/Swarm-Code/mono/swarm-sdk/tests/provider/http/mocks"
)

// ExampleNew demonstrates creating an OpenAI provider.
func ExampleNew() {
	logger := mocks.NewLogger()
	tracer := mocks.NewTracer()

	provider, err := openai.New(openai.Config{
		APIKey: "sk-...",
		Logger: logger,
		Tracer: tracer,
	})
	if err != nil {
		panic(err)
	}

	fmt.Println(provider.Name())
	// Output: openai
}

// ExampleProvider_Chat demonstrates a basic chat completion.
func ExampleProvider_Chat() {
	logger := mocks.NewLogger()
	tracer := mocks.NewTracer()

	p, _ := openai.New(openai.Config{
		APIKey: "sk-...",
		Logger: logger,
		Tracer: tracer,
	})

	ctx := context.Background()
	req := provider.ChatRequest{
		Model: "gpt-4",
		Messages: []*conversation.Message{
			{
				ID:        "msg-1",
				Timestamp: time.Now(),
				Role:      conversation.RoleUser,
				Content:   "Hello, how are you?",
			},
		},
	}

	// In a real scenario, this would call the API
	// For this example, we just show the structure
	_, _ = p.Chat(ctx, req)
	fmt.Println("Chat request prepared")
	// Output: Chat request prepared
}

// ExampleProvider_Chat_withTools demonstrates function calling.
func ExampleProvider_Chat_withTools() {
	logger := mocks.NewLogger()
	tracer := mocks.NewTracer()

	p, _ := openai.New(openai.Config{
		APIKey: "sk-...",
		Logger: logger,
		Tracer: tracer,
	})

	ctx := context.Background()
	req := provider.ChatRequest{
		Model: "gpt-4",
		Messages: []*conversation.Message{
			{
				ID:        "msg-1",
				Timestamp: time.Now(),
				Role:      conversation.RoleUser,
				Content:   "What's the weather in San Francisco?",
			},
		},
		Tools: []provider.Tool{
			{
				Name:        "get_weather",
				Description: "Get current weather for a location",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"location": map[string]string{
							"type":        "string",
							"description": "City name",
						},
					},
					"required": []string{"location"},
				},
			},
		},
	}

	_, _ = p.Chat(ctx, req)
	fmt.Println("Tool calling request prepared")
	// Output: Tool calling request prepared
}

// ExampleProvider_Capabilities demonstrates getting provider capabilities.
func ExampleProvider_Capabilities() {
	logger := mocks.NewLogger()
	tracer := mocks.NewTracer()

	p, _ := openai.New(openai.Config{
		APIKey: "sk-...",
		Logger: logger,
		Tracer: tracer,
	})

	caps := p.Capabilities()
	fmt.Printf("Function Calling: %v\n", caps.FunctionCalling)
	fmt.Printf("Vision: %v\n", caps.Vision)
	fmt.Printf("Max Context Window: %d\n", caps.MaxContextWindow)
	// Output:
	// Function Calling: true
	// Vision: true
	// Max Context Window: 300000
}

// ExampleConfig demonstrates configuration options.
func ExampleConfig() {
	logger := mocks.NewLogger()
	tracer := mocks.NewTracer()

	config := openai.Config{
		APIKey:         "sk-...",
		BaseURL:        "https://api.openai.com/v1",
		OrganizationID: "org-...",
		Logger:         logger,
		Tracer:         tracer,
	}

	err := config.Validate()
	fmt.Printf("Valid: %v\n", err == nil)
	// Output: Valid: true
}

// ExampleTranslateRequest demonstrates request translation.
func ExampleTranslateRequest() {
	temp := 0.7
	req := provider.ChatRequest{
		Model:       "gpt-4",
		Temperature: &temp,
		Messages: []*conversation.Message{
			{
				ID:        "msg-1",
				Timestamp: time.Now(),
				Role:      conversation.RoleUser,
				Content:   "Hello!",
			},
		},
	}

	openaiReq, err := openai.TranslateRequest(req)
	if err != nil {
		panic(err)
	}

	fmt.Printf("Model: %s\n", openaiReq.Model)
	fmt.Printf("Temperature: %.1f\n", *openaiReq.Temperature)
	// Output:
	// Model: gpt-4
	// Temperature: 0.7
}
