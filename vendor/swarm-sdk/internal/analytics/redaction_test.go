package analytics

import "testing"

func TestRedactorRedactsSensitiveKeysAndPaths(t *testing.T) {
	redactor := NewRedactor()
	result := redactor.Redact(map[string]any{
		"api_key": "secret",
		"path":    "/Users/test/.aws/credentials",
		"nested": map[string]any{
			"token": "Bearer abcdefghijklmnop",
		},
	})

	payload := result.Value.(map[string]any)
	if payload["api_key"] != "[REDACTED:secret]" {
		t.Fatalf("expected api_key redaction, got %#v", payload["api_key"])
	}
	if payload["path"] != "[REDACTED:path]" {
		t.Fatalf("expected path redaction, got %#v", payload["path"])
	}
	nested := payload["nested"].(map[string]any)
	if nested["token"] != "[REDACTED:secret]" {
		t.Fatalf("expected nested token redaction, got %#v", nested["token"])
	}
	if len(result.Flags) == 0 {
		t.Fatal("expected redaction flags")
	}
}

func TestHashMachineIDDeterministic(t *testing.T) {
	left := HashMachineID("swarm.analytics.v1", "machine-123")
	right := HashMachineID("swarm.analytics.v1", "machine-123")
	if left != right {
		t.Fatalf("expected deterministic hash, got %q and %q", left, right)
	}
	if left == "machine-123" {
		t.Fatal("hash must not equal raw machine id")
	}
}
