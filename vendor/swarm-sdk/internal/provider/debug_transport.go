package provider

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// DebugTransport is an http.RoundTripper that logs raw HTTP requests and responses.
// It wraps another RoundTripper and outputs debug information to the configured writer.
type DebugTransport struct {
	// Base is the underlying transport to use. If nil, http.DefaultTransport is used.
	Base http.RoundTripper
	// Output is where debug output is written. Required.
	Output io.Writer
}

// RoundTrip implements http.RoundTripper.
func (t *DebugTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.Base
	if base == nil {
		base = http.DefaultTransport
	}

	start := time.Now()

	// Log request
	t.logRequest(req)

	// Read and restore request body for logging
	var reqBody []byte
	if req.Body != nil {
		reqBody, _ = io.ReadAll(req.Body)
		req.Body.Close()
		req.Body = io.NopCloser(bytes.NewReader(reqBody))
	}

	if len(reqBody) > 0 {
		t.logJSON("REQUEST BODY", reqBody)
	}

	// Execute request
	resp, err := base.RoundTrip(req)
	if err != nil {
		t.logError(err)
		return nil, err
	}

	elapsed := time.Since(start)

	// Log response
	t.logResponse(resp, elapsed)

	// Read and restore response body for logging
	var respBody []byte
	if resp.Body != nil {
		respBody, _ = io.ReadAll(resp.Body)
		resp.Body.Close()
		resp.Body = io.NopCloser(bytes.NewReader(respBody))
	}

	if len(respBody) > 0 {
		t.logJSON("RESPONSE BODY", respBody)
	}

	return resp, nil
}

func (t *DebugTransport) logRequest(req *http.Request) {
	fmt.Fprintf(t.Output, "\n%s\n", strings.Repeat("═", 70))
	fmt.Fprintf(t.Output, "[REQUEST] %s %s\n", req.Method, req.URL.String())
	fmt.Fprintf(t.Output, "%s\n", strings.Repeat("─", 70))
}

func (t *DebugTransport) logResponse(resp *http.Response, elapsed time.Duration) {
	fmt.Fprintf(t.Output, "\n%s\n", strings.Repeat("═", 70))
	fmt.Fprintf(t.Output, "[RESPONSE] %s (%s)\n", resp.Status, elapsed.Round(time.Millisecond))
	fmt.Fprintf(t.Output, "%s\n", strings.Repeat("─", 70))
}

func (t *DebugTransport) logJSON(label string, data []byte) {
	// Try to pretty-print JSON
	var prettyJSON bytes.Buffer
	if err := json.Indent(&prettyJSON, data, "", "  "); err == nil {
		fmt.Fprintf(t.Output, "%s\n", prettyJSON.String())
	} else {
		// Fall back to raw output if not valid JSON
		fmt.Fprintf(t.Output, "%s\n", string(data))
	}
	fmt.Fprintf(t.Output, "%s\n", strings.Repeat("─", 70))
}

func (t *DebugTransport) logError(err error) {
	fmt.Fprintf(t.Output, "\n%s\n", strings.Repeat("═", 70))
	fmt.Fprintf(t.Output, "[ERROR] %v\n", err)
	fmt.Fprintf(t.Output, "%s\n", strings.Repeat("═", 70))
}

// NewDebugTransport creates a new DebugTransport that wraps the given transport.
// If base is nil, http.DefaultTransport is used.
func NewDebugTransport(base http.RoundTripper, output io.Writer) *DebugTransport {
	return &DebugTransport{
		Base:   base,
		Output: output,
	}
}
