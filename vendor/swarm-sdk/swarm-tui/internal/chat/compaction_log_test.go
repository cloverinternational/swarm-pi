package chat

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCompactionLogEntryPersistsHashesWithoutPayloadContent(t *testing.T) {
	const secret = "conversation content that must never be persisted"
	errorCategory, errorHash := compactionErrorMetadata("provider timeout: " + secret)
	entry := compactionLogEntry{
		Event:         "compaction_request",
		Provider:      "test-provider",
		Model:         "test-model",
		BodyBytes:     len(secret),
		BodySHA256:    compactionPayloadSHA256([]byte(secret)),
		SummaryLen:    len(secret),
		SummarySHA256: compactionPayloadSHA256([]byte(secret)),
		Error:         errorCategory,
		ErrorSHA256:   errorHash,
	}

	encoded, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	text := string(encoded)
	if strings.Contains(text, secret) {
		t.Fatalf("serialized compaction log leaked payload content: %s", text)
	}
	for _, field := range []string{
		`"body_bytes"`,
		`"body_sha256"`,
		`"summary_len"`,
		`"summary_sha256"`,
		`"error":"timeout"`,
		`"error_sha256"`,
		`"provider":"test-provider"`,
		`"model":"test-model"`,
	} {
		if !strings.Contains(text, field) {
			t.Errorf("serialized compaction log missing %s: %s", field, text)
		}
	}
	if len(entry.BodySHA256) != 64 || entry.BodySHA256 != entry.SummarySHA256 {
		t.Fatalf("unexpected SHA-256 metadata: body=%q summary=%q",
			entry.BodySHA256, entry.SummarySHA256)
	}
}
