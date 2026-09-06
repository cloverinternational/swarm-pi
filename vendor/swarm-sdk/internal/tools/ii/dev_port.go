// Port registration tool for exposing local development servers to the public internet.
// Ported from ii-agent's register_port.py
package ii

import (
	"context"
	"fmt"

	sdkerr "github.com/Swarm-Code/mono/swarm-sdk/internal/sdkerr"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

const (
	// RegisterPortToolName is the unique identifier for this tool
	RegisterPortToolName = "register_deployment"

	// RegisterPortToolDisplayName is the human-readable name
	RegisterPortToolDisplayName = "Register deployment"
)

// registerPortDescription provides detailed documentation for the tool
const registerPortDescription = `Register a port for deployment and get public access URL.

PURPOSE:
- Expose local development servers to public internet
- Enable sharing of web applications for testing/demo
- Support multiple concurrent deployments

WORKFLOW:
1. Start your server on a local port (e.g., 3000, 8000)
2. Register the port with this tool
3. Receive public URL for external access

COMMON PORTS:
- 3000-3999: Frontend development servers
- 8000-8999: Backend API servers
- 5000-5999: Flask/Python applications

RETURNS:
- Public URL accessible from internet
- URL remains active while server is running`

// PortExposer is an interface for exposing ports.
// This abstracts the underlying port exposure mechanism (e.g., sandbox, tunnel service).
type PortExposer interface {
	// ExposePort makes a local port accessible and returns the public URL
	ExposePort(ctx context.Context, port int) (string, error)

	// UnexposePort removes public access to a port
	UnexposePort(ctx context.Context, port int) error

	// ListExposedPorts returns all currently exposed ports and their URLs
	ListExposedPorts(ctx context.Context) (map[int]string, error)
}

// DefaultPortExposer provides a no-op implementation for testing
// and environments where port exposure is not available.
type DefaultPortExposer struct{}

// ExposePort returns a placeholder URL (no actual exposure)
func (d *DefaultPortExposer) ExposePort(ctx context.Context, port int) (string, error) {
	return fmt.Sprintf("http://localhost:%d", port), nil
}

// UnexposePort is a no-op
func (d *DefaultPortExposer) UnexposePort(ctx context.Context, port int) error {
	return nil
}

// ListExposedPorts returns an empty map
func (d *DefaultPortExposer) ListExposedPorts(ctx context.Context) (map[int]string, error) {
	return make(map[int]string), nil
}

// RegisterPortTool implements port registration functionality
type RegisterPortTool struct {
	portExposer PortExposer
}

// NewRegisterPortTool creates a new RegisterPortTool with the given port exposer
func NewRegisterPortTool(exposer PortExposer) *RegisterPortTool {
	if exposer == nil {
		exposer = &DefaultPortExposer{}
	}
	return &RegisterPortTool{portExposer: exposer}
}

// Name returns the tool name
func (t *RegisterPortTool) Name() string {
	return RegisterPortToolName
}

// DisplayName returns the human-readable display name
func (t *RegisterPortTool) DisplayName() string {
	return RegisterPortToolDisplayName
}

// Description returns the tool description
func (t *RegisterPortTool) Description() string {
	return registerPortDescription
}

// Parameters returns the JSON schema for tool parameters
func (t *RegisterPortTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"port": map[string]any{
				"type":        "integer",
				"description": "The port to register for public access",
				"minimum":     1,
				"maximum":     65535,
			},
		},
		"required": []string{"port"},
	}
}

// Execute registers a port and returns the public URL
func (t *RegisterPortTool) Execute(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
	// Check context
	select {
	case <-ctx.Done():
		return nil, sdkerr.Wrap(ctx.Err(), "register_port.context_cancelled")
	default:
	}

	// Extract port parameter
	var port int
	switch v := params["port"].(type) {
	case int:
		port = v
	case int64:
		port = int(v)
	case float64:
		port = int(v)
	default:
		return nil, sdkerr.Permanent("register_port.invalid_port", "port must be an integer")
	}

	// Validate port range
	if port < 1 || port > 65535 {
		return nil, sdkerr.Permanent("register_port.invalid_port_range",
			fmt.Sprintf("port must be between 1 and 65535, got %d", port))
	}

	// Expose the port
	url, err := t.portExposer.ExposePort(ctx, port)
	if err != nil {
		return tools.NewToolResult(fmt.Sprintf("ERROR: Failed to expose port %d: %s", port, err.Error())), nil
	}

	// Create result with port information
	result := tools.NewToolResult(fmt.Sprintf("Successfully registered port %d. Public URL: %s", port, url))
	result.WithMetadata("port", port)
	result.WithMetadata("url", url)

	return result, nil
}

// Validate checks if the given parameters are valid
func (t *RegisterPortTool) Validate(params map[string]any) error {
	portVal, ok := params["port"]
	if !ok {
		return fmt.Errorf("port is required")
	}

	var port int
	switch v := portVal.(type) {
	case int:
		port = v
	case int64:
		port = int(v)
	case float64:
		port = int(v)
	default:
		return fmt.Errorf("port must be an integer")
	}

	if port < 1 || port > 65535 {
		return fmt.Errorf("port must be between 1 and 65535, got %d", port)
	}

	return nil
}

// IsIdempotent returns true as registering the same port twice is safe
func (t *RegisterPortTool) IsIdempotent() bool {
	return true
}

// IsReadOnly returns false as this tool modifies network state
func (t *RegisterPortTool) IsReadOnly() bool {
	return false
}

// RequiresPermission returns the required permissions
func (t *RegisterPortTool) RequiresPermission() []tools.Permission {
	return []tools.Permission{tools.PermissionNetworkAccess}
}

// SupportedContentTypes returns the content types this tool can produce
func (t *RegisterPortTool) SupportedContentTypes() []tools.ContentType {
	return []tools.ContentType{tools.ContentTypeText}
}

// OptimizationHints provides guidance for efficient tool use
func (t *RegisterPortTool) OptimizationHints() *tools.OptimizationHints {
	return nil
}

// ShouldConfirmExecute returns confirmation details for port registration
func (t *RegisterPortTool) ShouldConfirmExecute(params map[string]any) *ConfirmationDetails {
	port, _ := params["port"].(int)
	return &ConfirmationDetails{
		Type:    ConfirmationTypeBash,
		Message: fmt.Sprintf("Expose port %d to the public internet", port),
	}
}

// Metadata returns the tool metadata
func (t *RegisterPortTool) Metadata() map[string]any {
	return map[string]any{
		"category": "development",
		"tags":     []string{"port", "deployment", "network"},
	}
}

// SetPortExposer allows changing the port exposer (useful for testing)
func (t *RegisterPortTool) SetPortExposer(exposer PortExposer) {
	t.portExposer = exposer
}
