package commands

import (
	"errors"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider/openai"
)

func TestAuthCommandOpenAICodeBounds(t *testing.T) {
	var cmd *AuthCommand = NewAuthCommand()
	cmd.interactive = true
	cmd.state = "openai_polling"
	cmd.openaiUserCode = "ABCD-EFGH"
	cmd.width = 120
	cmd.height = 40

	var startX int
	var startY int
	var width int
	var ok bool
	startX, startY, width, ok = cmd.openAICodeBounds()
	if !ok {
		t.Fatalf("expected code bounds to be found")
	}
	if width != len(cmd.openaiUserCode) {
		t.Fatalf("expected width %d, got %d", len(cmd.openaiUserCode), width)
	}
	if !cmd.openAICodeHit(startX, startY) {
		t.Fatalf("expected hit at code bounds")
	}
	if cmd.openAICodeHit(startX+width, startY) {
		t.Fatalf("expected no hit past code width")
	}
	if cmd.openAICodeHit(startX, startY+1) {
		t.Fatalf("expected no hit on different line")
	}
}

func TestAuthCommandOpenAICompletionReportsCredentialChangeFailure(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cmd := NewAuthCommand()
	cmd.interactive = true
	cmd.SetOnComplete(func(provider string) error {
		if provider != "codex" {
			t.Fatalf("provider = %q, want codex", provider)
		}
		return errors.New("provider rebuild failed")
	})

	updated, _ := cmd.Update(openaiResultMsg{token: &openai.OAuthToken{
		AccessToken: "access-token",
		AccountID:   "account-id",
	}})
	got := updated.(*AuthCommand)
	if got.state != "login_error" {
		t.Fatalf("state = %q, want login_error", got.state)
	}
	if got.errorMessage != "provider rebuild failed" {
		t.Fatalf("errorMessage = %q", got.errorMessage)
	}
	if !got.interactive {
		t.Fatal("dialog closed despite provider rebuild failure")
	}
}
