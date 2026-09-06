// Package openai provides HTTP client creation helper.
package openai

import (
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	httplib "github.com/Swarm-Code/mono/swarm-sdk/internal/provider/http"
)

// HTTPClientConfig provides configuration for creating HTTP clients.
type HTTPClientConfig struct {
	BaseURL        string
	Logger         observability.Logger
	Tracer         observability.Tracer
	HTTPMaxRetries *int
}

// NewHTTPClient creates a new HTTP client for OpenAI-compatible providers.
func NewHTTPClient(config HTTPClientConfig) (*httplib.Client, error) {
	return httplib.NewClient(httplib.ClientConfig{
		Logger:          config.Logger,
		Tracer:          config.Tracer,
		OperationPrefix: "openai",
		HTTPMaxRetries:  config.HTTPMaxRetries,
	})
}
