package observability

import (
	"strings"
	"testing"
)

func TestDefaultRedactorMasksSensitiveKeysAndTruncates(t *testing.T) {
	redactor := NewDefaultRedactor()

	longValue := strings.Repeat("a", redactor.MaxStringLength+32)
	attrs := map[string]any{
		"api_key":       "secret-key",
		"authorization": "Bearer abc",
		"nested": map[string]any{
			"token":     "nested-token",
			"safe_info": "ok",
		},
		"payload": longValue,
	}

	redacted := redactor.RedactAttributes(attrs)
	if redacted == nil {
		t.Fatal("expected redacted attributes")
	}

	if redacted["api_key"] != redactedValue {
		t.Fatalf("expected api_key to be redacted, got %#v", redacted["api_key"])
	}
	if redacted["authorization"] != redactedValue {
		t.Fatalf("expected authorization to be redacted, got %#v", redacted["authorization"])
	}

	nested, ok := redacted["nested"].(map[string]any)
	if !ok {
		t.Fatalf("expected nested map, got %T", redacted["nested"])
	}
	if nested["token"] != redactedValue {
		t.Fatalf("expected nested token to be redacted, got %#v", nested["token"])
	}
	if nested["safe_info"] != "ok" {
		t.Fatalf("expected nested safe_info to be preserved, got %#v", nested["safe_info"])
	}

	payload, ok := redacted["payload"].(string)
	if !ok {
		t.Fatalf("expected payload string, got %T", redacted["payload"])
	}
	if len(payload) > redactor.MaxStringLength {
		t.Fatalf("expected payload length <= %d, got %d", redactor.MaxStringLength, len(payload))
	}
	if !strings.HasSuffix(payload, "...") {
		t.Fatalf("expected truncated payload to end with ellipsis, got %q", payload)
	}
}
