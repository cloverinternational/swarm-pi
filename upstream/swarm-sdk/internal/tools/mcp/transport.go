package mcp

import (
	"context"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/mcp/oauth"
)

// Transport defines the interface for MCP communication
type Transport interface {
	// Connect establishes connection to MCP server
	Connect(ctx context.Context) error

	// Close terminates the connection
	Close() error

	// Send sends a JSON-RPC message
	Send(ctx context.Context, message any) error

	// Receive receives a JSON-RPC message
	Receive(ctx context.Context) (any, error)

	// IsConnected checks connection status
	IsConnected() bool

	// TransportType returns the transport type
	TransportType() TransportType
}

// TransportType represents different transport mechanisms
type TransportType string

const (
	// TransportStdio uses standard input/output
	TransportStdio TransportType = "stdio"

	// TransportHTTPStream uses HTTP with streaming
	TransportHTTPStream TransportType = "http_stream"

	// TransportSSE uses Server-Sent Events (legacy)
	TransportSSE TransportType = "sse"

	// TransportOAuthHTTP uses OAuth-authenticated HTTP
	TransportOAuthHTTP TransportType = "oauth_http"
)

// TransportConfig holds transport configuration
type TransportConfig struct {
	// Type of transport
	Type TransportType

	// For stdio transport
	Command    string
	Args       []string
	Env        map[string]string
	WorkingDir string // Working directory for command execution

	// For HTTP transports
	URL       string
	Headers   map[string]string
	SessionID string

	// For OAuth
	OAuthConfig *OAuthConfig

	// Common settings
	Timeout time.Duration
	Retries int
}

// OAuthConfig holds OAuth 2.0 configuration
type OAuthConfig struct {
	ClientID     string
	ClientSecret string
	TokenURL     string
	AuthURL      string
	Scopes       []string
	RedirectURL  string
	TokenStorage oauth.TokenStorage
}

// DefaultTransportConfig returns sensible defaults
func DefaultTransportConfig() *TransportConfig {
	return &TransportConfig{
		Type:    TransportStdio,
		Timeout: 30 * time.Second,
		Retries: 3,
		Env:     make(map[string]string),
		Headers: make(map[string]string),
	}
}
