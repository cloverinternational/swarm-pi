// Database connection tool for obtaining database credentials and connection strings.
// Ported from ii-agent's database.py
package ii

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"time"

	sdkerr "github.com/Swarm-Code/mono/swarm-sdk/internal/sdkerr"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

const (
	// GetDatabaseConnectionToolName is the unique identifier for this tool
	GetDatabaseConnectionToolName = "get_database_connection"

	// GetDatabaseConnectionToolDisplayName is the human-readable name
	GetDatabaseConnectionToolDisplayName = "Get database connection"

	// DefaultDatabaseTimeout is the default timeout for database requests
	DefaultDatabaseTimeout = 120 * time.Second
)

// getDatabaseConnectionDescription provides detailed documentation for the tool
const getDatabaseConnectionDescription = `Get a database connection.
- Get connection details for database operations.
- Support multiple database types (currently: postgres).
- Provide connection string for use in applications.

The tool contacts a configured tool server to provision or retrieve database connection credentials.
The returned connection string can be used directly in application code to connect to the database.`

// DatabaseType represents supported database types
type DatabaseType string

const (
	// DatabaseTypePostgres represents PostgreSQL database
	DatabaseTypePostgres DatabaseType = "postgres"
)

// AvailableDatabaseTypes returns all supported database types
func AvailableDatabaseTypes() []string {
	return []string{string(DatabaseTypePostgres)}
}

// DatabaseCredentials contains the credentials needed to make database requests
type DatabaseCredentials struct {
	// SessionID is the current session identifier
	SessionID string

	// UserAPIKey is the API key for authentication
	UserAPIKey string
}

// DatabaseServerConfig contains configuration for the database tool server
type DatabaseServerConfig struct {
	// URL is the base URL of the tool server
	URL string

	// Timeout is the request timeout
	Timeout time.Duration
}

// DatabaseRequest represents a request to the database server
type DatabaseRequest struct {
	DatabaseType string `json:"database_type"`
	SessionID    string `json:"session_id"`
}

// DatabaseResponse represents a response from the database server
type DatabaseResponse struct {
	Success          bool   `json:"success"`
	ConnectionString string `json:"connection_string,omitempty"`
	Error            string `json:"error,omitempty"`
}

// GetDatabaseConnectionTool implements database connection functionality
type GetDatabaseConnectionTool struct {
	credentials  DatabaseCredentials
	serverConfig DatabaseServerConfig
	httpClient   *http.Client
}

// NewGetDatabaseConnectionTool creates a new GetDatabaseConnectionTool
func NewGetDatabaseConnectionTool(credentials DatabaseCredentials, serverURL string) *GetDatabaseConnectionTool {
	timeout := DefaultDatabaseTimeout
	return &GetDatabaseConnectionTool{
		credentials: credentials,
		serverConfig: DatabaseServerConfig{
			URL:     serverURL,
			Timeout: timeout,
		},
		httpClient: &http.Client{
			Timeout: timeout,
		},
	}
}

// NewGetDatabaseConnectionToolWithConfig creates a GetDatabaseConnectionTool with custom configuration
func NewGetDatabaseConnectionToolWithConfig(credentials DatabaseCredentials, config DatabaseServerConfig) *GetDatabaseConnectionTool {
	if config.Timeout == 0 {
		config.Timeout = DefaultDatabaseTimeout
	}
	return &GetDatabaseConnectionTool{
		credentials:  credentials,
		serverConfig: config,
		httpClient: &http.Client{
			Timeout: config.Timeout,
		},
	}
}

// Name returns the tool name
func (t *GetDatabaseConnectionTool) Name() string {
	return GetDatabaseConnectionToolName
}

// DisplayName returns the human-readable display name
func (t *GetDatabaseConnectionTool) DisplayName() string {
	return GetDatabaseConnectionToolDisplayName
}

// Description returns the tool description
func (t *GetDatabaseConnectionTool) Description() string {
	return getDatabaseConnectionDescription
}

// Parameters returns the JSON schema for tool parameters
func (t *GetDatabaseConnectionTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"database_type": map[string]any{
				"type":        "string",
				"description": "Type of the database to connect to",
				"enum":        AvailableDatabaseTypes(),
			},
		},
		"required": []string{"database_type"},
	}
}

// Execute requests a database connection from the tool server
func (t *GetDatabaseConnectionTool) Execute(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
	// Check context
	select {
	case <-ctx.Done():
		return nil, sdkerr.Wrap(ctx.Err(), "get_database_connection.context_cancelled")
	default:
	}

	// Extract parameters
	databaseType, ok := params["database_type"].(string)
	if !ok || databaseType == "" {
		return nil, sdkerr.Permanent("get_database_connection.missing_database_type", "database_type is required")
	}

	// Validate database type
	validType := slices.Contains(AvailableDatabaseTypes(), databaseType)
	if !validType {
		return nil, sdkerr.Permanent("get_database_connection.invalid_database_type",
			fmt.Sprintf("invalid database_type '%s'. Available: %v", databaseType, AvailableDatabaseTypes()))
	}

	// Check if server is configured
	if t.serverConfig.URL == "" {
		return tools.NewToolResult("ERROR: Database server URL is not configured"), nil
	}

	// Build request
	reqBody := DatabaseRequest{
		DatabaseType: databaseType,
		SessionID:    t.credentials.SessionID,
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, sdkerr.Permanent("get_database_connection.marshal_failed", err.Error())
	}

	// Create HTTP request
	url := fmt.Sprintf("%s/database", t.serverConfig.URL)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, sdkerr.Wrap(err, "get_database_connection.request_failed")
	}

	req.Header.Set("Content-Type", "application/json")
	if t.credentials.UserAPIKey != "" {
		req.Header.Set("Authorization", "Bearer "+t.credentials.UserAPIKey)
	}

	// Execute request
	resp, err := t.httpClient.Do(req)
	if err != nil {
		return tools.NewToolResult(fmt.Sprintf("ERROR: Failed to get database connection. Error: %s", err.Error())), nil
	}
	defer resp.Body.Close()

	// Read response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return tools.NewToolResult(fmt.Sprintf("ERROR: Failed to read response. Error: %s", err.Error())), nil
	}

	// Parse response
	var dbResp DatabaseResponse
	if err := json.Unmarshal(body, &dbResp); err != nil {
		return tools.NewToolResult(fmt.Sprintf("ERROR: Failed to parse response. Error: %s", err.Error())), nil
	}

	// Check for errors
	if resp.StatusCode != http.StatusOK || !dbResp.Success {
		errMsg := dbResp.Error
		if errMsg == "" {
			errMsg = fmt.Sprintf("HTTP %d", resp.StatusCode)
		}
		return tools.NewToolResult(fmt.Sprintf("ERROR: Failed to get database connection. Error: %s", errMsg)), nil
	}

	// Create success result
	result := tools.NewToolResult(fmt.Sprintf("Successfully got database connection. Connection string: %s", dbResp.ConnectionString))
	result.WithMetadata("database_type", databaseType)
	result.WithMetadata("connection_string", dbResp.ConnectionString)

	return result, nil
}

// Validate checks if the given parameters are valid
func (t *GetDatabaseConnectionTool) Validate(params map[string]any) error {
	databaseType, ok := params["database_type"].(string)
	if !ok || databaseType == "" {
		return fmt.Errorf("database_type is required")
	}

	validType := slices.Contains(AvailableDatabaseTypes(), databaseType)
	if !validType {
		return fmt.Errorf("invalid database_type '%s'. Available: %v", databaseType, AvailableDatabaseTypes())
	}

	return nil
}

// IsIdempotent returns true as getting connection info is idempotent
func (t *GetDatabaseConnectionTool) IsIdempotent() bool {
	return true
}

// IsReadOnly returns true as this tool only retrieves information
func (t *GetDatabaseConnectionTool) IsReadOnly() bool {
	return true
}

// RequiresPermission returns the required permissions
func (t *GetDatabaseConnectionTool) RequiresPermission() []tools.Permission {
	return []tools.Permission{tools.PermissionNetworkAccess, tools.PermissionDatabaseRead}
}

// SupportedContentTypes returns the content types this tool can produce
func (t *GetDatabaseConnectionTool) SupportedContentTypes() []tools.ContentType {
	return []tools.ContentType{tools.ContentTypeText}
}

// OptimizationHints provides guidance for efficient tool use
func (t *GetDatabaseConnectionTool) OptimizationHints() *tools.OptimizationHints {
	return &tools.OptimizationHints{
		// Can run in parallel with other tools
		PreferSequential: false,
		// Can batch multiple database info requests
		CanBatch: true,
	}
}

// ShouldConfirmExecute returns nil as getting database info doesn't need confirmation
func (t *GetDatabaseConnectionTool) ShouldConfirmExecute(params map[string]any) *ConfirmationDetails {
	// Getting database connection info doesn't need user confirmation
	return nil
}

// Metadata returns the tool metadata
func (t *GetDatabaseConnectionTool) Metadata() map[string]any {
	return map[string]any{
		"category": "development",
		"tags":     []string{"database", "connection", "postgres"},
	}
}

// SetCredentials allows updating the credentials
func (t *GetDatabaseConnectionTool) SetCredentials(credentials DatabaseCredentials) {
	t.credentials = credentials
}

// SetServerURL allows updating the server URL
func (t *GetDatabaseConnectionTool) SetServerURL(url string) {
	t.serverConfig.URL = url
}

// SetHTTPClient allows setting a custom HTTP client (useful for testing)
func (t *GetDatabaseConnectionTool) SetHTTPClient(client *http.Client) {
	t.httpClient = client
}
