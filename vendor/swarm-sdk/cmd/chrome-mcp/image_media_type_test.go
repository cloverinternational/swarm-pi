package main

import (
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/vision"
)

func TestConvertExtensionResultReconcilesMislabeledImage(t *testing.T) {
	jpeg := []byte{0xff, 0xd8, 0xff, 0xe0, 0x00, 0x10, 'J', 'F', 'I', 'F', 0x00}
	encoded := base64.StdEncoding.EncodeToString(jpeg)
	raw, err := json.Marshal(map[string]any{
		"content": []map[string]any{{
			"type":     "image",
			"data":     encoded,
			"mimeType": vision.MediaTypePNG,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}

	result := convertExtensionResult(raw)
	content, ok := result["content"].([]any)
	if !ok || len(content) != 1 {
		t.Fatalf("convertExtensionResult() content = %#v, want one image block", result["content"])
	}
	image, ok := content[0].(map[string]any)
	if !ok {
		t.Fatalf("convertExtensionResult() image = %T, want map[string]any", content[0])
	}
	if got := image["mimeType"]; got != vision.MediaTypeJPEG {
		t.Fatalf("convertExtensionResult() mimeType = %q, want %q", got, vision.MediaTypeJPEG)
	}
}
