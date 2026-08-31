// Package http provides HTTP-based provider implementations for connecting to LLM APIs.
// This package includes authentication strategies, request building, and response handling.
package http

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"strings"
	"sync"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
)

// AuthStrategy defines the interface for HTTP authentication strategies.
// Implementations include NoAuth, APIKeyAuth, and CustomHeaderAuth.
type AuthStrategy interface {
	// Name returns the name of the authentication strategy.
	Name() string

	// Apply applies the authentication to an HTTP request.
	// Returns an error if authentication fails or is invalid.
	Apply(ctx context.Context, req *http.Request) error
}

// NoAuth is an authentication strategy for unauthenticated requests.
// This is used for local models, development environments, or public APIs.
// NoAuth implements the AuthStrategy interface.
type NoAuth struct {
	logger observability.Logger
}

// NewNoAuth creates a new NoAuth strategy.
// This strategy applies no authentication to requests.
// logger is used for observability (optional, uses minimal fallback if nil).
func NewNoAuth(logger observability.Logger) *NoAuth {
	if logger == nil {
		logger = newMinimalLogger()
	}
	return &NoAuth{
		logger: logger,
	}
}

// Name returns the name of the auth strategy.
func (n *NoAuth) Name() string {
	return "no_auth"
}

// Apply applies no-op authentication to the request.
// This strategy does not modify the request.
func (n *NoAuth) Apply(ctx context.Context, req *http.Request) error {
	// Debug logging removed to avoid terminal output clutter
	return nil
}

// APIKeyAuth is an authentication strategy using Bearer token or custom scheme authentication.
// The API key is sent in the Authorization header (e.g., "Bearer <token>").
// This is commonly used by OpenAI, Anthropic, and other LLM APIs.
// APIKeyAuth implements the AuthStrategy interface.
type APIKeyAuth struct {
	apiKey string
	logger observability.Logger
	scheme string // e.g., "Bearer" or custom scheme
}

// NewAPIKeyAuth creates a new APIKeyAuth strategy with Bearer scheme.
// apiKey is the authentication token (required).
// logger is used for observability (optional, uses minimal fallback if nil).
func NewAPIKeyAuth(apiKey string, logger observability.Logger) *APIKeyAuth {
	if logger == nil {
		logger = newMinimalLogger()
	}
	return &APIKeyAuth{
		apiKey: apiKey,
		logger: logger,
		scheme: "Bearer",
	}
}

// NewAPIKeyAuthWithScheme creates a new APIKeyAuth strategy with a custom scheme.
// scheme is the authorization scheme (e.g., "Bearer", "Token", "Basic").
// apiKey is the authentication token (required).
// logger is used for observability (optional, uses minimal fallback if nil).
func NewAPIKeyAuthWithScheme(scheme, apiKey string, logger observability.Logger) *APIKeyAuth {
	if logger == nil {
		logger = newMinimalLogger()
	}
	return &APIKeyAuth{
		apiKey: apiKey,
		logger: logger,
		scheme: scheme,
	}
}

// Name returns the name of the auth strategy.
func (a *APIKeyAuth) Name() string {
	return "api_key"
}

// Apply applies Bearer token authentication to the request.
// The token is added to the Authorization header.
func (a *APIKeyAuth) Apply(ctx context.Context, req *http.Request) error {
	if err := validateAPIKeyAuth(a); err != nil {
		a.logger.Error(ctx, "http.auth.failed",
			observability.F("strategy", "api_key"),
			observability.F("error", err.Error()),
		)
		return err
	}

	authHeader := fmt.Sprintf("%s %s", a.scheme, a.apiKey)
	req.Header.Set("Authorization", authHeader)

	a.logger.Trace(ctx, "http.auth.applied",
		observability.F("strategy", "api_key"),
		observability.F("scheme", a.scheme),
		observability.F("method", req.Method),
		observability.F("url", redactURL(req.URL.String())),
		observability.F("has_token", true),
	)
	return nil
}

// validateAPIKeyAuth validates that the APIKeyAuth strategy is properly configured.
// Returns an error if the configuration is missing required fields or invalid.
func validateAPIKeyAuth(a *APIKeyAuth) error {
	if strings.TrimSpace(a.apiKey) == "" {
		return fmt.Errorf("api_key_auth: api key cannot be empty")
	}
	if strings.TrimSpace(a.scheme) == "" {
		return fmt.Errorf("api_key_auth: scheme cannot be empty")
	}
	return nil
}

// CustomHeaderAuth is an authentication strategy using arbitrary HTTP headers.
// This allows providers to define custom authentication methods.
// For example, some APIs use custom headers like "X-API-Key" instead of Authorization.
// CustomHeaderAuth implements the AuthStrategy interface.
type CustomHeaderAuth struct {
	headers   map[string]string
	headersMu sync.Mutex // Protects headers from concurrent access
	logger    observability.Logger
}

// NewCustomHeaderAuth creates a new CustomHeaderAuth strategy.
// headers is a map of header names to values (required, must not be empty).
// logger is used for observability (optional, uses minimal fallback if nil).
func NewCustomHeaderAuth(headers map[string]string, logger observability.Logger) *CustomHeaderAuth {
	if logger == nil {
		logger = newMinimalLogger()
	}
	if headers == nil {
		headers = make(map[string]string)
	}
	return &CustomHeaderAuth{
		headers: headers,
		logger:  logger,
	}
}

// AddHeader adds or updates a custom header.
// name is the header name (case-insensitive, follows HTTP header conventions).
// value is the header value.
// This method is thread-safe and can be called concurrently.
func (c *CustomHeaderAuth) AddHeader(name, value string) {
	c.headersMu.Lock()
	defer c.headersMu.Unlock()

	if c.headers == nil {
		c.headers = make(map[string]string)
	}
	c.headers[name] = value
}

// Name returns the name of the auth strategy.
func (c *CustomHeaderAuth) Name() string {
	return "custom_header"
}

// Apply applies custom headers to the request.
// All configured headers are added to the request.
// This method is thread-safe and creates a snapshot of headers under lock.
func (c *CustomHeaderAuth) Apply(ctx context.Context, req *http.Request) error {
	// Create a snapshot of headers under lock to avoid holding lock during validation and application
	c.headersMu.Lock()
	headerSnapshot := make(map[string]string, len(c.headers))
	maps.Copy(headerSnapshot, c.headers)
	c.headersMu.Unlock()

	// Validate using snapshot (validation doesn't need lock)
	if len(headerSnapshot) == 0 {
		err := fmt.Errorf("custom_header_auth: at least one header must be configured")
		c.logger.Error(ctx, "http.auth.failed",
			observability.F("strategy", "custom_header"),
			observability.F("error", err.Error()),
		)
		return err
	}

	// Apply headers from snapshot
	headerCount := 0
	for name, value := range headerSnapshot {
		if strings.TrimSpace(name) == "" {
			err := fmt.Errorf("custom_header_auth: header name cannot be empty")
			c.logger.Error(ctx, "http.auth.failed",
				observability.F("strategy", "custom_header"),
				observability.F("error", err.Error()),
			)
			return err
		}
		if strings.TrimSpace(value) == "" {
			err := fmt.Errorf("custom_header_auth: header value for '%s' cannot be empty", name)
			c.logger.Error(ctx, "http.auth.failed",
				observability.F("strategy", "custom_header"),
				observability.F("error", err.Error()),
			)
			return err
		}
		req.Header.Set(name, value)
		headerCount++
	}

	c.logger.Trace(ctx, "http.auth.applied",
		observability.F("strategy", "custom_header"),
		observability.F("header_count", headerCount),
		observability.F("method", req.Method),
		observability.F("url", redactURL(req.URL.String())),
	)
	return nil
}

// redactURL is a helper function to redact sensitive information from URLs for logging.
// Removes query parameters and sensitive path components to avoid logging secrets.
func redactURL(rawURL string) string {
	// Remove query parameters to avoid logging sensitive data
	if before, _, ok := strings.Cut(rawURL, "?"); ok {
		return before
	}
	return rawURL
}

// minimalLogger is a fallback logger that discards all log entries.
// This is used internally when no logger is provided to auth strategies.
type minimalLogger struct{}

// newMinimalLogger creates a minimal logger implementation.
func newMinimalLogger() observability.Logger {
	return &minimalLogger{}
}

// Log discards the log entry.
func (m *minimalLogger) Log(ctx context.Context, level observability.Level, event string, fields ...observability.Field) {
}

// Trace discards the log entry.
func (m *minimalLogger) Trace(ctx context.Context, event string, fields ...observability.Field) {
}

// Debug discards the log entry.
func (m *minimalLogger) Debug(ctx context.Context, event string, fields ...observability.Field) {
}

// Info discards the log entry.
func (m *minimalLogger) Info(ctx context.Context, event string, fields ...observability.Field) {
}

// Warn discards the log entry.
func (m *minimalLogger) Warn(ctx context.Context, event string, fields ...observability.Field) {
}

// Error discards the log entry.
func (m *minimalLogger) Error(ctx context.Context, event string, fields ...observability.Field) {
}

// Fatal discards the log entry.
func (m *minimalLogger) Fatal(ctx context.Context, event string, fields ...observability.Field) {
}

// WithFields returns the same logger (fields are discarded).
func (m *minimalLogger) WithFields(fields ...observability.Field) observability.Logger {
	return m
}

// SetLevel is a no-op.
func (m *minimalLogger) SetLevel(level observability.Level) {
}
