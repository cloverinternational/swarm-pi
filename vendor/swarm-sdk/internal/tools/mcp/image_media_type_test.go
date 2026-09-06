package mcp

import (
	"encoding/base64"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/vision"
)

func TestParseContentBlockReconcilesMislabeledImage(t *testing.T) {
	jpeg := []byte{0xff, 0xd8, 0xff, 0xe0, 0x00, 0x10, 'J', 'F', 'I', 'F', 0x00}
	encoded := base64.StdEncoding.EncodeToString(jpeg)

	block := parseContentBlock(map[string]any{
		"type":     "image",
		"data":     encoded,
		"mimeType": vision.MediaTypePNG,
	})

	if block.MimeType != vision.MediaTypeJPEG {
		t.Fatalf("parseContentBlock() MimeType = %q, want %q", block.MimeType, vision.MediaTypeJPEG)
	}
	if block.Text != encoded {
		t.Fatal("parseContentBlock() altered the base64 image payload")
	}
}
