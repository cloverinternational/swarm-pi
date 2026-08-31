package mcp

import (
	"fmt"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
)

// NewTransport creates a transport based on the provided configuration.
func NewTransport(config *TransportConfig, logger observability.Logger, tracer observability.Tracer) (Transport, error) {
	if config == nil {
		return nil, fmt.Errorf("transport config is required")
	}

	switch config.Type {
	case TransportStdio:
		return NewStdioTransport(config, logger), nil
	case TransportHTTPStream, TransportSSE:
		return NewHTTPStreamTransport(config, logger, tracer), nil
	case TransportOAuthHTTP:
		return NewOAuthHTTPTransport(config, logger, tracer)
	default:
		return nil, fmt.Errorf("unsupported transport type: %s", config.Type)
	}
}
