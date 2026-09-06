package gemini

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"time"

	sdkerr "github.com/Swarm-Code/mono/swarm-sdk/internal/sdkerr"
)

// GeminiErrorResponse represents an error response from Gemini API.
// Example:
//
//	{
//	  "error": {
//	    "code": 429,
//	    "message": "You have exhausted your capacity on this model. Your quota will reset after 31s.",
//	    "status": "RESOURCE_EXHAUSTED",
//	    "details": [
//	      {
//	        "@type": "type.googleapis.com/google.rpc.ErrorInfo",
//	        "reason": "RATE_LIMIT_EXCEEDED",
//	        "domain": "cloudcode-pa.googleapis.com"
//	      }
//	    ]
//	  }
//	}
type GeminiErrorResponse struct {
	Error struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Status  string `json:"status"`
		Details []struct {
			Type     string            `json:"@type"`
			Reason   string            `json:"reason"`
			Domain   string            `json:"domain"`
			Metadata map[string]string `json:"metadata,omitempty"`
		} `json:"details,omitempty"`
	} `json:"error"`
}

// retryDurationRegex extracts seconds from messages like "Your quota will reset after 31s"
var retryDurationRegex = regexp.MustCompile(`(?:reset|retry)\s+(?:after|in)\s+(\d+)\s*(?:s|sec|seconds?)`)

// ParseGeminiError parses a Gemini API error response and returns a typed SDK error.
// It categorizes errors appropriately for retry handling:
// - 429 / RESOURCE_EXHAUSTED: Transient with RetryAfter extracted from message
// - 401 / 403: Permanent authentication/authorization errors
// - 400: Permanent invalid request errors
// - 500+: Transient server errors
func ParseGeminiError(statusCode int, body []byte) error {
	var errResp GeminiErrorResponse

	// Try to parse as JSON error response
	if err := json.Unmarshal(body, &errResp); err != nil {
		// Fall back to plain text error if JSON parsing fails
		return sdkerr.Permanent(
			"gemini.unknown_error",
			fmt.Sprintf("HTTP %d: %s", statusCode, truncateBody(body, 200)),
			sdkerr.WithContext("status_code", statusCode),
		)
	}

	message := errResp.Error.Message
	errorStatus := errResp.Error.Status
	errorCode := errResp.Error.Code

	// Use HTTP status code if error code is not present
	if errorCode == 0 {
		errorCode = statusCode
	}

	// Extract reason from details if available
	reason := ""
	for _, detail := range errResp.Error.Details {
		if detail.Reason != "" {
			reason = detail.Reason
			break
		}
	}

	// Handle rate limit errors (429 or RESOURCE_EXHAUSTED)
	if statusCode == 429 || errorStatus == "RESOURCE_EXHAUSTED" || reason == "RATE_LIMIT_EXCEEDED" {
		retryAfter := extractRetryDuration(message)
		return sdkerr.Transient("gemini.rate_limited", message,
			sdkerr.WithRetryAfter(retryAfter),
			sdkerr.WithContext("status_code", statusCode),
			sdkerr.WithContext("error_status", errorStatus),
			sdkerr.WithContext("reason", reason),
		)
	}

	// Handle authentication errors (permanent)
	if statusCode == 401 || errorStatus == "UNAUTHENTICATED" {
		return sdkerr.Permanent(
			"gemini.authentication_failed",
			message,
			sdkerr.WithContext("status_code", statusCode),
			sdkerr.WithContext("error_status", errorStatus),
		)
	}

	// Handle authorization errors (permanent)
	if statusCode == 403 || errorStatus == "PERMISSION_DENIED" {
		return sdkerr.Permanent(
			"gemini.permission_denied",
			message,
			sdkerr.WithContext("status_code", statusCode),
			sdkerr.WithContext("error_status", errorStatus),
		)
	}

	// Handle invalid request errors (permanent)
	if statusCode == 400 || errorStatus == "INVALID_ARGUMENT" {
		return sdkerr.Permanent(
			"gemini.invalid_request",
			message,
			sdkerr.WithContext("status_code", statusCode),
			sdkerr.WithContext("error_status", errorStatus),
		)
	}

	// Handle not found errors (permanent)
	if statusCode == 404 || errorStatus == "NOT_FOUND" {
		return sdkerr.Permanent(
			"gemini.not_found",
			message,
			sdkerr.WithContext("status_code", statusCode),
			sdkerr.WithContext("error_status", errorStatus),
		)
	}

	// Handle server errors (transient - retry with default backoff)
	if statusCode >= 500 || errorStatus == "INTERNAL" || errorStatus == "UNAVAILABLE" {
		return sdkerr.Transient("gemini.server_error", message,
			sdkerr.WithRetryAfter(30*time.Second),
			sdkerr.WithContext("status_code", statusCode),
			sdkerr.WithContext("error_status", errorStatus),
		)
	}

	// Default to permanent error for unknown cases
	return sdkerr.Permanent(
		"gemini.unknown_error",
		message,
		sdkerr.WithContext("status_code", statusCode),
		sdkerr.WithContext("error_status", errorStatus),
	)
}

// extractRetryDuration extracts the retry duration from Gemini error messages.
// It looks for patterns like:
// - "Your quota will reset after 31s"
// - "retry after 45 seconds"
// - "reset in 60s"
// Returns a default of 60 seconds if no duration can be extracted.
func extractRetryDuration(message string) time.Duration {
	matches := retryDurationRegex.FindStringSubmatch(message)
	if len(matches) > 1 {
		if seconds, err := strconv.Atoi(matches[1]); err == nil {
			return time.Duration(seconds) * time.Second
		}
	}

	// Default to 60 seconds if we can't parse
	return 60 * time.Second
}

// truncateBody truncates the body to maxLen characters for error messages.
func truncateBody(body []byte, maxLen int) string {
	if len(body) > maxLen {
		return string(body[:maxLen]) + "..."
	}
	return string(body)
}
