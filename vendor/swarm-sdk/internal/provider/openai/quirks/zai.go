package quirks

import (
	"context"
	"strings"

	sdkerr "github.com/Swarm-Code/mono/swarm-sdk/internal/sdkerr"
)

// ZAIAdapter handles z.ai (GLM Coding Plan) specific quirks.
// z.ai's PAAS proxy wraps some network-layer failures as application-level
// error messages in the response body.  TransformError reclassifies those
// as transient so the retry logic can act on them.
type ZAIAdapter struct{}

// NewZAIAdapter creates a new z.ai quirk adapter.
func NewZAIAdapter() *ZAIAdapter {
	return &ZAIAdapter{}
}

// Name implements Adapter.
func (a *ZAIAdapter) Name() string {
	return "zai"
}

// TransformRequest implements Adapter (no-op — z.ai follows OpenAI protocol).
func (a *ZAIAdapter) TransformRequest(ctx context.Context, req any) (any, error) {
	return req, nil
}

// TransformResponse implements Adapter (no-op).
func (a *ZAIAdapter) TransformResponse(ctx context.Context, resp any) (any, error) {
	return resp, nil
}

// TransformError implements Adapter.
// z.ai surfaces proxy errors as HTTP 500 bodies containing "Network error, error id:".
// These are transient upstream failures — reclassify them so retry logic fires.
func (a *ZAIAdapter) TransformError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}

	msg := err.Error()

	// z.ai proxy error messages that indicate transient upstream failure
	zaiTransient := []string{
		"network error, error id:",  // PAAS proxy upstream failure
		"upstream connect error",    // nginx upstream unavailable
		"no healthy upstream",       // upstream pool exhausted
		"connection reset by peer",  // TCP reset from proxy
		"unexpected eof",            // connection closed mid-stream
		"context deadline exceeded", // request timed out
	}

	msgLower := strings.ToLower(msg)
	for _, pattern := range zaiTransient {
		if strings.Contains(msgLower, pattern) {
			// Re-wrap as a transient SDK error so callers can check IsRetryable().
			// sdkerr.Transient() creates a CategoryTransient error that the retry
			// loop in client.Do() and agent_execute.go will act on.
			return sdkerr.Wrap(err, "provider.zai.transient_error")
		}
	}

	return err
}

// BuildURL implements Adapter (standard concatenation — no custom URL patterns).
func (a *ZAIAdapter) BuildURL(baseURL string, endpoint string, params map[string]string) string {
	return baseURL + "/" + endpoint
}

// ValidateModel implements Adapter (no validation — z.ai accepts any model name).
func (a *ZAIAdapter) ValidateModel(model string) error {
	return nil
}
