package anthropic

import (
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/sdkerr"
)

// TestCategorizeAPIError_IncludesType verifies that categorizeAPIError appends
// the Anthropic error type to the surfaced message for every category. This is
// the fix for the "continuing conversations doesn't continue" masking bug: a
// 404 for a nonexistent model returns the bare message "model: <name>", which
// without the type reads like a truncated log line instead of a not-found error.
func TestCategorizeAPIError_IncludesType(t *testing.T) {
	cases := []struct {
		name       string
		errType    string
		message    string
		statusCode int
		wantCode   string
	}{
		{"not_found", "not_found_error", "model: claude-opus-4-20250514", 404, "anthropic.not_found"},
		{"auth", "authentication_error", "invalid bearer token", 401, "anthropic.authentication_failed"},
		{"permission", "permission_error", "not allowed", 403, "anthropic.permission_denied"},
		{"rate_limit", "rate_limit_error", "slow down", 429, "anthropic.rate_limited"},
		{"api", "api_error", "internal", 500, "anthropic.api_error"},
		{"overloaded", "overloaded_error", "busy", 529, "anthropic.overloaded"},
		{"invalid_request", "invalid_request_error", "bad body", 400, "anthropic.invalid_request"},
		{"unknown", "some_new_error", "future thing", 418, "anthropic.unknown_error"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			apiErr := &APIError{Type: "error"}
			apiErr.Error.Type = tc.errType
			apiErr.Error.Message = tc.message

			err := categorizeAPIError(apiErr, tc.statusCode)
			if err == nil {
				t.Fatal("expected non-nil error")
			}
			if code := sdkerr.GetCode(err); code != tc.wantCode {
				t.Fatalf("expected code %q, got %q", tc.wantCode, code)
			}
			msg := err.Error()
			if !strings.Contains(msg, tc.message) {
				t.Fatalf("expected message to contain original %q, got %q", tc.message, msg)
			}
			if !strings.Contains(msg, "(type: "+tc.errType+")") {
				t.Fatalf("expected message to include type %q, got %q", tc.errType, msg)
			}
		})
	}
}

// TestCategorizeAPIError_EmptyType ensures a missing error type degrades
// gracefully to the bare message (no dangling "(type: )").
func TestCategorizeAPIError_EmptyType(t *testing.T) {
	apiErr := &APIError{Type: "error"}
	apiErr.Error.Type = ""
	apiErr.Error.Message = "opaque failure"

	err := categorizeAPIError(apiErr, 500)
	msg := err.Error()
	if !strings.Contains(msg, "opaque failure") {
		t.Fatalf("expected bare message, got %q", msg)
	}
	if strings.Contains(msg, "(type:") {
		t.Fatalf("did not expect a type suffix for empty type, got %q", msg)
	}
}
