package chat

import (
	"bytes"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
)

func TestImageAttachmentsRestoreFromPersistedToolResultContent(t *testing.T) {
	original := []byte{1, 2, 3, 4}
	attachments := imageAttachmentsFromConversationContent([]conversation.ContentBlock{
		{Type: "text", Text: "ignored"},
		{Type: "image", Data: original, MimeType: "image/png", Name: "restored.png"},
	})
	if len(attachments) != 1 {
		t.Fatalf("attachments = %d, want 1", len(attachments))
	}
	got := attachments[0]
	if got.FileName != "restored.png" || got.MimeType != "image/png" || !bytes.Equal(got.Content, original) {
		t.Fatalf("restored attachment = %+v", got)
	}
	original[0] = 9
	if got.Content[0] != 1 {
		t.Fatal("restored attachment aliases persisted content bytes")
	}
}
