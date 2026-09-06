package chat

import "testing"

func TestTakeInitialPromptConsumesValueExactlyOnce(t *testing.T) {
	app := NewAsyncAppWithOptions(AppOptions{InitialPrompt: "  inspect this repository  "})

	if got := app.takeInitialPrompt(); got != "inspect this repository" {
		t.Fatalf("first takeInitialPrompt() = %q", got)
	}
	if got := app.takeInitialPrompt(); got != "" {
		t.Fatalf("second takeInitialPrompt() = %q, want empty", got)
	}
}

func TestConsumeInitialPromptIgnoresDuplicateMessage(t *testing.T) {
	app := &App{}
	msg := initialPromptMsg{prompt: "inspect this repository"}

	prompt, accepted := app.consumeInitialPrompt(msg)
	if !accepted || prompt != msg.prompt {
		t.Fatalf("first consume = (%q, %t), want accepted prompt", prompt, accepted)
	}
	prompt, accepted = app.consumeInitialPrompt(msg)
	if accepted || prompt != "" {
		t.Fatalf("second consume = (%q, %t), want rejected empty prompt", prompt, accepted)
	}
}
