// Package quirks provides provider-specific adapters for OpenAI-compatible APIs.
package quirks

import (
	"context"
)

// Adapter handles provider-specific transformations.
// Adapters allow code-based customization for providers with complex quirks
// that cannot be expressed via configuration alone.
type Adapter interface {
	// Name returns the adapter identifier.
	Name() string

	// TransformRequest modifies the request before sending.
	// Returns the modified request or error.
	TransformRequest(ctx context.Context, req any) (any, error)

	// TransformResponse modifies the response after receiving.
	// Returns the modified response or error.
	TransformResponse(ctx context.Context, resp any) (any, error)

	// TransformError categorizes provider-specific errors.
	// Returns a categorized SDK error.
	TransformError(ctx context.Context, err error) error

	// BuildURL constructs the request URL from profile config.
	// Allows custom URL patterns (e.g., Azure's deployment URLs).
	BuildURL(baseURL string, endpoint string, params map[string]string) string

	// ValidateModel checks if the model name is valid for this provider.
	ValidateModel(model string) error
}

// PassthroughAdapter is a no-op adapter that doesn't modify anything.
// Used as default for providers that strictly follow OpenAI protocol.
type PassthroughAdapter struct{}

// NewPassthroughAdapter creates a new passthrough adapter.
func NewPassthroughAdapter() *PassthroughAdapter {
	return &PassthroughAdapter{}
}

// Name implements Adapter.
func (a *PassthroughAdapter) Name() string {
	return "passthrough"
}

// TransformRequest implements Adapter (no-op).
func (a *PassthroughAdapter) TransformRequest(ctx context.Context, req any) (any, error) {
	return req, nil
}

// TransformResponse implements Adapter (no-op).
func (a *PassthroughAdapter) TransformResponse(ctx context.Context, resp any) (any, error) {
	return resp, nil
}

// TransformError implements Adapter (no-op).
func (a *PassthroughAdapter) TransformError(ctx context.Context, err error) error {
	return err
}

// BuildURL implements Adapter (standard concatenation).
func (a *PassthroughAdapter) BuildURL(baseURL string, endpoint string, params map[string]string) string {
	return baseURL + "/" + endpoint
}

// ValidateModel implements Adapter (no validation).
func (a *PassthroughAdapter) ValidateModel(model string) error {
	return nil
}
