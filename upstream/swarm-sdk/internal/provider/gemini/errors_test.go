package gemini

import (
	"errors"
	"testing"
	"time"

	sdkerr "github.com/Swarm-Code/mono/swarm-sdk/internal/sdkerr"
)

func TestParseGeminiError_RateLimit(t *testing.T) {
	tests := []struct {
		name            string
		statusCode      int
		body            string
		expectedType    string
		expectedRetry   time.Duration
		expectRetryable bool
	}{
		{
			name:       "429 with quota reset message",
			statusCode: 429,
			body: `{
				"error": {
					"code": 429,
					"message": "You have exhausted your capacity on this model. Your quota will reset after 31s.",
					"status": "RESOURCE_EXHAUSTED",
					"details": [
						{
							"@type": "type.googleapis.com/google.rpc.ErrorInfo",
							"reason": "RATE_LIMIT_EXCEEDED",
							"domain": "cloudcode-pa.googleapis.com"
						}
					]
				}
			}`,
			expectedType:    "gemini.rate_limited",
			expectedRetry:   31 * time.Second,
			expectRetryable: true,
		},
		{
			name:       "429 without explicit reset time",
			statusCode: 429,
			body: `{
				"error": {
					"code": 429,
					"message": "Rate limit exceeded",
					"status": "RESOURCE_EXHAUSTED"
				}
			}`,
			expectedType:    "gemini.rate_limited",
			expectedRetry:   60 * time.Second, // default
			expectRetryable: true,
		},
		{
			name:       "RESOURCE_EXHAUSTED status (may not be 429)",
			statusCode: 503,
			body: `{
				"error": {
					"code": 503,
					"message": "Resource exhausted, retry after 45s",
					"status": "RESOURCE_EXHAUSTED"
				}
			}`,
			expectedType:    "gemini.rate_limited",
			expectedRetry:   45 * time.Second,
			expectRetryable: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ParseGeminiError(tt.statusCode, []byte(tt.body))
			if err == nil {
				t.Fatal("expected error, got nil")
			}

			if sdkerr.GetType(err) != tt.expectedType {
				t.Errorf("expected type %q, got %q", tt.expectedType, sdkerr.GetType(err))
			}

			if sdkerr.IsRetryable(err) != tt.expectRetryable {
				t.Errorf("expected retryable=%v, got %v", tt.expectRetryable, sdkerr.IsRetryable(err))
			}

			var sdkErr *sdkerr.Error
			if !errors.As(err, &sdkErr) {
				t.Fatal("expected SDK error type")
			}

			if sdkErr.RetryAfter != tt.expectedRetry {
				t.Errorf("expected retry after %v, got %v", tt.expectedRetry, sdkErr.RetryAfter)
			}
		})
	}
}

func TestParseGeminiError_AuthErrors(t *testing.T) {
	tests := []struct {
		name            string
		statusCode      int
		body            string
		expectedType    string
		expectRetryable bool
	}{
		{
			name:       "401 unauthenticated",
			statusCode: 401,
			body: `{
				"error": {
					"code": 401,
					"message": "Request had invalid authentication credentials",
					"status": "UNAUTHENTICATED"
				}
			}`,
			expectedType:    "gemini.authentication_failed",
			expectRetryable: false,
		},
		{
			name:       "403 permission denied",
			statusCode: 403,
			body: `{
				"error": {
					"code": 403,
					"message": "The caller does not have permission",
					"status": "PERMISSION_DENIED"
				}
			}`,
			expectedType:    "gemini.permission_denied",
			expectRetryable: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ParseGeminiError(tt.statusCode, []byte(tt.body))
			if err == nil {
				t.Fatal("expected error, got nil")
			}

			if sdkerr.GetType(err) != tt.expectedType {
				t.Errorf("expected type %q, got %q", tt.expectedType, sdkerr.GetType(err))
			}

			if sdkerr.IsRetryable(err) != tt.expectRetryable {
				t.Errorf("expected retryable=%v, got %v", tt.expectRetryable, sdkerr.IsRetryable(err))
			}
		})
	}
}

func TestParseGeminiError_InvalidRequest(t *testing.T) {
	body := `{
		"error": {
			"code": 400,
			"message": "Invalid request: missing required field",
			"status": "INVALID_ARGUMENT"
		}
	}`

	err := ParseGeminiError(400, []byte(body))
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	if sdkerr.GetType(err) != "gemini.invalid_request" {
		t.Errorf("expected type gemini.invalid_request, got %q", sdkerr.GetType(err))
	}

	if sdkerr.IsRetryable(err) {
		t.Error("expected non-retryable error for invalid request")
	}
}

func TestParseGeminiError_ServerError(t *testing.T) {
	tests := []struct {
		name         string
		statusCode   int
		body         string
		expectedType string
	}{
		{
			name:       "500 internal error",
			statusCode: 500,
			body: `{
				"error": {
					"code": 500,
					"message": "Internal server error",
					"status": "INTERNAL"
				}
			}`,
			expectedType: "gemini.server_error",
		},
		{
			name:       "503 unavailable",
			statusCode: 503,
			body: `{
				"error": {
					"code": 503,
					"message": "Service temporarily unavailable",
					"status": "UNAVAILABLE"
				}
			}`,
			expectedType: "gemini.server_error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ParseGeminiError(tt.statusCode, []byte(tt.body))
			if err == nil {
				t.Fatal("expected error, got nil")
			}

			if sdkerr.GetType(err) != tt.expectedType {
				t.Errorf("expected type %q, got %q", tt.expectedType, sdkerr.GetType(err))
			}

			if !sdkerr.IsRetryable(err) {
				t.Error("expected retryable error for server error")
			}
		})
	}
}

func TestParseGeminiError_MalformedJSON(t *testing.T) {
	body := `not valid json`

	err := ParseGeminiError(500, []byte(body))
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	if sdkerr.GetType(err) != "gemini.unknown_error" {
		t.Errorf("expected type gemini.unknown_error, got %q", sdkerr.GetType(err))
	}
}

func TestExtractRetryDuration(t *testing.T) {
	tests := []struct {
		name     string
		message  string
		expected time.Duration
	}{
		{
			name:     "reset after Ns",
			message:  "Your quota will reset after 31s.",
			expected: 31 * time.Second,
		},
		{
			name:     "retry after N seconds",
			message:  "Please retry after 45 seconds",
			expected: 45 * time.Second,
		},
		{
			name:     "reset in Ns",
			message:  "Rate limit exceeded, reset in 60s",
			expected: 60 * time.Second,
		},
		{
			name:     "retry in N sec",
			message:  "retry in 15 sec",
			expected: 15 * time.Second,
		},
		{
			name:     "no duration found",
			message:  "Rate limit exceeded",
			expected: 60 * time.Second, // default
		},
		{
			name:     "empty message",
			message:  "",
			expected: 60 * time.Second, // default
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractRetryDuration(tt.message)
			if result != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, result)
			}
		})
	}
}
