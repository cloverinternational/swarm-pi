package oauth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DiscoveryClient handles OAuth metadata discovery.
type DiscoveryClient struct {
	httpClient *http.Client
}

// NewDiscoveryClient creates a new discovery client.
func NewDiscoveryClient() *DiscoveryClient {
	return &DiscoveryClient{
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// DiscoverProtectedResource fetches the OAuth 2.0 Protected Resource Metadata
// from the MCP server. This tells us which authorization server to use.
func (d *DiscoveryClient) DiscoverProtectedResource(ctx context.Context, serverURL string) (*ProtectedResourceMetadata, error) {
	// Parse and build the well-known URL
	parsed, err := url.Parse(serverURL)
	if err != nil {
		return nil, fmt.Errorf("invalid server URL: %w", err)
	}

	// Build well-known URL: https://server/.well-known/oauth-protected-resource
	wellKnownURL := fmt.Sprintf("%s://%s/.well-known/oauth-protected-resource", parsed.Scheme, parsed.Host)

	req, err := http.NewRequestWithContext(ctx, "GET", wellKnownURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Accept", "application/json")

	resp, err := d.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("protected resource metadata not found (server may not require OAuth)")
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(body))
	}

	var metadata ProtectedResourceMetadata
	if err := json.NewDecoder(resp.Body).Decode(&metadata); err != nil {
		return nil, fmt.Errorf("failed to decode metadata: %w", err)
	}

	if len(metadata.AuthorizationServers) == 0 {
		return nil, fmt.Errorf("no authorization servers specified in metadata")
	}

	return &metadata, nil
}

// DiscoverAuthorizationServer fetches the OAuth 2.0 Authorization Server Metadata
// from the authorization server. This tells us the endpoints for authorization.
func (d *DiscoveryClient) DiscoverAuthorizationServer(ctx context.Context, authServerURL string) (*ServerMetadata, error) {
	// Parse and build the well-known URL
	parsed, err := url.Parse(authServerURL)
	if err != nil {
		return nil, fmt.Errorf("invalid auth server URL: %w", err)
	}

	// Build well-known URL: https://authserver/.well-known/oauth-authorization-server
	wellKnownURL := fmt.Sprintf("%s://%s/.well-known/oauth-authorization-server", parsed.Scheme, parsed.Host)

	// If there's a path, it might be the issuer path
	if parsed.Path != "" && parsed.Path != "/" {
		// RFC 8414: /.well-known/oauth-authorization-server/{issuer-path}
		wellKnownURL = fmt.Sprintf("%s://%s/.well-known/oauth-authorization-server%s",
			parsed.Scheme, parsed.Host, parsed.Path)
	}

	req, err := http.NewRequestWithContext(ctx, "GET", wellKnownURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Accept", "application/json")

	resp, err := d.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		// Try fallback: OpenID Connect discovery
		return d.discoverOpenIDConnect(ctx, authServerURL)
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(body))
	}

	var metadata ServerMetadata
	if err := json.NewDecoder(resp.Body).Decode(&metadata); err != nil {
		return nil, fmt.Errorf("failed to decode metadata: %w", err)
	}

	if metadata.AuthorizationEndpoint == "" || metadata.TokenEndpoint == "" {
		return nil, fmt.Errorf("metadata missing required endpoints")
	}

	return &metadata, nil
}

// discoverOpenIDConnect tries OpenID Connect discovery as a fallback.
func (d *DiscoveryClient) discoverOpenIDConnect(ctx context.Context, authServerURL string) (*ServerMetadata, error) {
	parsed, err := url.Parse(authServerURL)
	if err != nil {
		return nil, fmt.Errorf("invalid auth server URL: %w", err)
	}

	// OpenID Connect: /.well-known/openid-configuration
	wellKnownURL := fmt.Sprintf("%s://%s/.well-known/openid-configuration", parsed.Scheme, parsed.Host)

	req, err := http.NewRequestWithContext(ctx, "GET", wellKnownURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Accept", "application/json")

	resp, err := d.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("authorization server metadata not found")
	}

	var metadata ServerMetadata
	if err := json.NewDecoder(resp.Body).Decode(&metadata); err != nil {
		return nil, fmt.Errorf("failed to decode metadata: %w", err)
	}

	return &metadata, nil
}

// DiscoverFromWWWAuthenticate extracts the resource metadata URL from a
// WWW-Authenticate header returned with a 401 response.
func (d *DiscoveryClient) DiscoverFromWWWAuthenticate(ctx context.Context, header string) (*ProtectedResourceMetadata, error) {
	// Parse WWW-Authenticate: Bearer resource_metadata="URL"
	if !strings.HasPrefix(strings.ToLower(header), "bearer ") {
		return nil, fmt.Errorf("not a Bearer authentication challenge")
	}

	// Extract resource_metadata URL
	params := header[7:] // Skip "Bearer "
	var resourceMetadataURL string

	for param := range strings.SplitSeq(params, ",") {
		param = strings.TrimSpace(param)
		if after, ok := strings.CutPrefix(param, "resource_metadata="); ok {
			value := after
			// Remove quotes if present
			resourceMetadataURL = strings.Trim(value, "\"")
			break
		}
	}

	if resourceMetadataURL == "" {
		return nil, fmt.Errorf("no resource_metadata in WWW-Authenticate header")
	}

	// Fetch the resource metadata
	req, err := http.NewRequestWithContext(ctx, "GET", resourceMetadataURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Accept", "application/json")

	resp, err := d.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to fetch resource metadata: %d", resp.StatusCode)
	}

	var metadata ProtectedResourceMetadata
	if err := json.NewDecoder(resp.Body).Decode(&metadata); err != nil {
		return nil, fmt.Errorf("failed to decode metadata: %w", err)
	}

	return &metadata, nil
}

// Discover performs full OAuth discovery for an MCP server.
// It returns the authorization server metadata needed for authentication.
func (d *DiscoveryClient) Discover(ctx context.Context, serverURL string) (*ServerMetadata, error) {
	// Step 1: Get protected resource metadata from MCP server
	resourceMeta, err := d.DiscoverProtectedResource(ctx, serverURL)
	if err != nil {
		return nil, fmt.Errorf("failed to discover protected resource: %w", err)
	}

	// Step 2: Use the first authorization server
	if len(resourceMeta.AuthorizationServers) == 0 {
		return nil, fmt.Errorf("no authorization servers configured")
	}
	authServerURL := resourceMeta.AuthorizationServers[0]

	// Step 3: Get authorization server metadata
	serverMeta, err := d.DiscoverAuthorizationServer(ctx, authServerURL)
	if err != nil {
		return nil, fmt.Errorf("failed to discover authorization server: %w", err)
	}

	return serverMeta, nil
}
