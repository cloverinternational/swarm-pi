package chat

import (
	"context"
	"testing"
)

// TestCronPromptSinkBuffersUntilHandler verifies prompts fired before the app
// attaches its handler are buffered and flushed in order on attach — the
// scheduler starts before the Bubble Tea runtime is ready.
func TestCronPromptSinkBuffersUntilHandler(t *testing.T) {
	sink := &tuiCronPromptSink{}

	if err := sink.EnqueuePrompt(context.Background(), "first"); err != nil {
		t.Fatalf("enqueue before handler: %v", err)
	}
	if err := sink.EnqueuePrompt(context.Background(), "second"); err != nil {
		t.Fatalf("enqueue before handler: %v", err)
	}

	var delivered []string
	sink.SetHandler(func(p string) { delivered = append(delivered, p) })

	if len(delivered) != 2 || delivered[0] != "first" || delivered[1] != "second" {
		t.Fatalf("buffered prompts not flushed in order: %v", delivered)
	}

	// After attach, delivery is direct.
	if err := sink.EnqueuePrompt(context.Background(), "third"); err != nil {
		t.Fatalf("enqueue after handler: %v", err)
	}
	if len(delivered) != 3 || delivered[2] != "third" {
		t.Fatalf("direct delivery failed: %v", delivered)
	}
}

// TestCronPromptSinkNilHandlerSafe verifies SetHandler(nil) keeps buffering
// instead of panicking or losing prompts.
func TestCronPromptSinkNilHandlerSafe(t *testing.T) {
	sink := &tuiCronPromptSink{}
	sink.SetHandler(nil)

	if err := sink.EnqueuePrompt(context.Background(), "queued"); err != nil {
		t.Fatalf("enqueue with nil handler: %v", err)
	}

	var delivered []string
	sink.SetHandler(func(p string) { delivered = append(delivered, p) })
	if len(delivered) != 1 || delivered[0] != "queued" {
		t.Fatalf("prompt lost across nil handler: %v", delivered)
	}
}
