package ollama

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	sdkprovider "github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
)

func TestOllamaCloudChat(t *testing.T) {
	apiKey := os.Getenv("OLLAMA_API_KEY")
	if apiKey == "" {
		t.Skip("OLLAMA_API_KEY not set")
	}

	client := NewWithAPIKey(apiKey)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	resp, err := client.Chat(ctx, sdkprovider.ChatRequest{
		Model: "glm-5",
		Messages: []*conversation.Message{
			{
				Role:      conversation.RoleUser,
				Content:   "Say 'Hello, I am GLM-5 from Ollama Cloud!' and nothing else.",
				Timestamp: time.Now(),
			},
		},
	})

	if err != nil {
		t.Fatalf("Chat request failed: %v", err)
	}

	fmt.Printf("Response: %s\n", resp.Message.Content)
	fmt.Printf("Model: %s\n", resp.Message.Model)
	fmt.Printf("Finish reason: %s\n", resp.FinishReason)

	if resp.Message.Content == "" {
		t.Error("Expected non-empty response")
	}
}

func TestOllamaCloudStream(t *testing.T) {
	apiKey := os.Getenv("OLLAMA_API_KEY")
	if apiKey == "" {
		t.Skip("OLLAMA_API_KEY not set")
	}

	client := NewWithAPIKey(apiKey)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	chunkChan, err := client.Stream(ctx, sdkprovider.ChatRequest{
		Model: "glm-5",
		Messages: []*conversation.Message{
			{
				Role:      conversation.RoleUser,
				Content:   "Count from 1 to 5, one number per line.",
				Timestamp: time.Now(),
			},
		},
	})

	if err != nil {
		t.Fatalf("Stream request failed: %v", err)
	}

	fmt.Println("Streaming response:")
	for chunk := range chunkChan {
		if chunk.Error != nil {
			t.Fatalf("Stream error: %v", chunk.Error)
		}
		fmt.Print(chunk.Delta)
		if chunk.Done {
			fmt.Println("\n[Done]")
		}
	}
}

func TestOllamaCloudListModels(t *testing.T) {
	apiKey := os.Getenv("OLLAMA_API_KEY")
	if apiKey == "" {
		t.Skip("OLLAMA_API_KEY not set")
	}

	client := NewWithAPIKey(apiKey)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	models, err := client.ListModels(ctx)
	if err != nil {
		t.Fatalf("ListModels failed: %v", err)
	}

	fmt.Println("Available models:")
	for _, m := range models {
		fmt.Printf("  - %s (%s)\n", m.Name, m.Details.Family)
	}
}
