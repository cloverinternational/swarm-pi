# OAuth Improvements - OpenCode Integration

This document describes the three major OAuth improvements added to the SwarmCode SDK, inspired by OpenCode's implementation.

## Overview

Three key features have been added to enhance the OAuth implementation:

1. **URL Validation** - Prevents token reuse when server URLs change
2. **Client Secret Expiry Tracking** - Tracks and validates client secret expiration
3. **Optional Singleton Callback Server** - Configurable shared callback server

All features are **backward compatible** - existing code continues to work without changes.

---

## Feature 1: URL Validation

### What It Does

Tokens now track which server URL they were issued for. When loading a token, the system validates that the token's URL matches the requested URL.

### Why It Matters

**Security:** Prevents accidental token reuse if a server URL changes (e.g., `http://` → `https://`, domain changes, or port changes).

**Example Scenario:**
- User connects to `http://api.example.com`
- Server later upgrades to `https://api.example.com`
- Without URL validation: old token might be used incorrectly
- With URL validation: system detects URL mismatch and re-authenticates

### How to Use

**Automatic!** No code changes needed. The system automatically:
- Sets `ServerURL` when saving tokens
- Validates URL when loading tokens
- Re-authenticates if URL mismatch detected

### Implementation Details

**New Field:**
```go
type TokenData struct {
    // ... existing fields ...
    ServerURL string `json:"server_url,omitempty"`
}
```

**New Method:**
```go
// LoadTokenForURL loads a token and validates it matches the server URL
func (s *TokenStorage) LoadTokenForURL(serverURL string) (*TokenData, error)
```

**Backward Compatibility:**
- Old tokens without `ServerURL` still work (graceful migration)
- Empty `ServerURL` is treated as "no validation" for compatibility

---

## Feature 2: Client Secret Expiry Tracking

### What It Does

Tracks OAuth client registration information including client secret expiration dates. Automatically detects when client secrets expire and triggers re-registration.

### Why It Matters

**Compliance:** Properly handles OAuth 2.0 dynamic client registration with expiring secrets.

**Robustness:** Prevents auth failures due to expired client secrets.

**Example Scenario:**
- Server dynamically registers client with 30-day secret expiry
- After 30 days, system detects expiration
- Automatically re-registers or prompts for new credentials

### How to Use

**For Dynamic Registration:**
```go
flow, _ := oauth.NewFlow()

// After dynamic registration, save client info
clientInfo := &oauth.ClientInfo{
    ClientID:              "client123",
    ClientSecret:          "secret456",
    ClientSecretExpiresAt: time.Now().Add(30 * 24 * time.Hour),
}
flow.SaveClientInfo("https://api.example.com", clientInfo)

// Later, load and validate
clientInfo, err := flow.GetClientInfo(ctx, &oauth.AuthenticateConfig{
    ServerURL: "https://api.example.com",
})
// Returns error if client secret expired
```

**For Pre-Registered Clients:**
```go
// Just provide client ID/secret in config - expiry tracking is optional
config := &oauth.AuthenticateConfig{
    ServerURL:    "https://api.example.com",
    ClientID:     "my-client-id",
    ClientSecret: "my-client-secret",
}
```

### Implementation Details

**New Type:**
```go
type ClientInfo struct {
    ClientID              string
    ClientSecret          string
    ClientIDIssuedAt      time.Time
    ClientSecretExpiresAt time.Time
}

func (c *ClientInfo) IsExpired() bool
func (c *ClientInfo) IsValid() bool
```

**New Storage Methods:**
```go
SaveClientInfo(serverURL string, clientInfo *ClientInfo) error
LoadClientInfo(serverURL string) (*ClientInfo, error)
DeleteClientInfo(serverURL string) error
```

**Storage Format:**
- File storage: Stored in JSON alongside tokens
- Credential storage: Stored in credential headers with `oauth.client.*` prefix

---

## Feature 3: Optional Singleton Callback Server

### What It Does

Provides an optional shared callback server (like OpenCode's port 19876) as an alternative to per-flow callback servers.

### Why It Matters

**Simplicity:** Single callback port for all OAuth flows (easier firewall configuration).

**Compatibility:** Matches OpenCode's behavior for familiar UX.

**Flexibility:** Optional - keeps per-flow servers as default for multi-instance support.

### How to Use

**Default Behavior (Per-Flow Servers):**
```go
// Creates a new callback server with dynamic port for each auth flow
flow, _ := oauth.NewFlow()
token, err := flow.Authenticate(ctx, config)
```

**Singleton Callback Server:**
```go
// Uses shared callback server on port 19876 (or custom port)
flow, _ := oauth.NewFlow(
    oauth.WithSingletonCallback(19876),
)
token, err := flow.Authenticate(ctx, config)
```

**Custom Port:**
```go
flow, _ := oauth.NewFlow(
    oauth.WithSingletonCallback(8080), // Custom port
)
```

**Default Port (0 = 19876):**
```go
flow, _ := oauth.NewFlow(
    oauth.WithSingletonCallback(0), // Uses DefaultCallbackPort (19876)
)
```

### How It Works

**Per-Flow (Default):**
- Each `Authenticate()` call creates a new callback server on a random port
- Server is stopped after auth completes
- Supports multiple instances on same machine

**Singleton:**
- First `Authenticate()` call creates global callback server on configured port
- Subsequent calls reuse the same server
- Uses state parameter to route callbacks to correct flow
- Server persists across multiple auth flows

### State-Based Routing

The singleton server uses the OAuth `state` parameter to route callbacks:

1. Flow registers pending auth with unique state
2. Browser redirects to callback URL with state parameter
3. Server looks up pending auth by state and delivers result
4. Flow receives callback result

### When to Use Each

**Use Per-Flow (Default) When:**
- Running multiple instances on same machine
- Maximum security isolation desired
- Dynamic port selection preferred

**Use Singleton When:**
- Single instance deployment
- Fixed callback port required (firewall rules, reverse proxy)
- OpenCode-like behavior desired
- Simplicity over flexibility

---

## Backward Compatibility

All features are fully backward compatible:

### URL Validation
- ✅ Old tokens without `ServerURL` still work
- ✅ Existing code needs no changes
- ✅ Graceful migration (tokens get `ServerURL` on next save)

### Client Secret Expiry
- ✅ Optional feature - works without client info
- ✅ Existing storage files compatible
- ✅ New fields are `omitempty` in JSON

### Singleton Callback
- ✅ Default behavior unchanged (per-flow servers)
- ✅ Opt-in via `WithSingletonCallback` option
- ✅ No breaking changes to existing flows

---

## Migration Guide

### Existing Users

**No action required!** Your code continues to work as-is.

**Optional Improvements:**

1. **Enable URL Validation:**
   - Already enabled automatically
   - Tokens will include `ServerURL` on next save

2. **Track Client Secrets:**
   ```go
   // If using dynamic registration, save client info
   flow.SaveClientInfo(serverURL, &oauth.ClientInfo{...})
   ```

3. **Use Singleton Callback:**
   ```go
   // Add one line to use singleton
   flow, _ := oauth.NewFlow(oauth.WithSingletonCallback(19876))
   ```

### Upgrading from Old Token Format

**Automatic!** Old tokens are gracefully migrated:

```go
// Old token (no ServerURL)
{
    "access_token": "...",
    "expires_at": "..."
}

// After first use, becomes:
{
    "access_token": "...",
    "expires_at": "...",
    "server_url": "https://api.example.com"  // Added automatically
}
```

---

## Examples

### Example 1: Basic Usage (No Changes)

```go
// Works exactly as before - all improvements automatic
flow, _ := oauth.NewFlow()
token, err := flow.Authenticate(ctx, &oauth.AuthenticateConfig{
    ServerURL: "https://api.example.com",
    ClientID:  "my-client-id",
    Scopes:    []string{"read", "write"},
})
```

### Example 2: Using Singleton Callback

```go
// Use shared callback server on port 19876
flow, _ := oauth.NewFlow(
    oauth.WithSingletonCallback(19876),
)

// Rest is the same
token, err := flow.Authenticate(ctx, &oauth.AuthenticateConfig{
    ServerURL: "https://api.example.com",
    ClientID:  "my-client-id",
})
```

### Example 3: Dynamic Client Registration with Expiry

```go
flow, _ := oauth.NewFlow()

// Register client dynamically
clientInfo := &oauth.ClientInfo{
    ClientID:              registerResponse.ClientID,
    ClientSecret:          registerResponse.ClientSecret,
    ClientIDIssuedAt:      time.Now(),
    ClientSecretExpiresAt: registerResponse.ExpiresAt,
}

// Save for future use
flow.SaveClientInfo("https://api.example.com", clientInfo)

// Later authentication uses saved client info
token, err := flow.Authenticate(ctx, &oauth.AuthenticateConfig{
    ServerURL: "https://api.example.com",
    // ClientID optional - will use saved client info
})
```

### Example 4: URL Change Detection

```go
flow, _ := oauth.NewFlow()

// Initial auth
token1, _ := flow.Authenticate(ctx, &oauth.AuthenticateConfig{
    ServerURL: "http://api.example.com",  // HTTP
    ClientID:  "my-client",
})

// Server upgrades to HTTPS
token2, _ := flow.Authenticate(ctx, &oauth.AuthenticateConfig{
    ServerURL: "https://api.example.com",  // HTTPS (different URL)
    ClientID:  "my-client",
})
// URL mismatch detected - automatically re-authenticates
```

---

## Testing

### Unit Tests

All features have comprehensive unit tests:

```bash
cd sdk/tools/mcp/oauth
go test -v
```

### Testing URL Validation

```go
func TestURLValidation(t *testing.T) {
    storage := oauth.NewMemoryTokenStorage()

    // Save token for URL1
    token := &oauth.TokenData{
        AccessToken: "token1",
        ServerURL:   "https://server1.com",
    }
    storage.SaveToken("https://server1.com", token)

    // Load with correct URL - succeeds
    loaded, _ := storage.LoadTokenForURL("https://server1.com")
    assert.NotNil(t, loaded)

    // Load with different URL - fails
    _, err := storage.LoadTokenForURL("https://server2.com")
    assert.Error(t, err)
}
```

### Testing Client Secret Expiry

```go
func TestClientSecretExpiry(t *testing.T) {
    clientInfo := &oauth.ClientInfo{
        ClientID:              "client123",
        ClientSecretExpiresAt: time.Now().Add(-1 * time.Hour), // Expired
    }

    assert.True(t, clientInfo.IsExpired())
    assert.False(t, clientInfo.IsValid())
}
```

### Testing Singleton Callback

```go
func TestSingletonCallback(t *testing.T) {
    // First flow
    flow1, _ := oauth.NewFlow(oauth.WithSingletonCallback(19876))

    // Second flow (reuses same server)
    flow2, _ := oauth.NewFlow(oauth.WithSingletonCallback(19876))

    // Both should share the same callback server
    // State-based routing ensures callbacks go to correct flow
}
```

---

## Performance Impact

### URL Validation
- **Minimal:** Single string comparison on token load
- **Storage:** +~20 bytes per token (ServerURL field)

### Client Secret Expiry
- **Minimal:** Single timestamp comparison on client info load
- **Storage:** +~100 bytes per server (client info)

### Singleton Callback Server
- **Memory:** Shared server uses less memory than multiple per-flow servers
- **Startup:** First auth slightly slower (server creation), subsequent auths faster
- **Network:** Single port vs multiple ports

---

## Security Considerations

### URL Validation
- ✅ **Prevents:** Token reuse across different server URLs
- ✅ **Protects:** Against accidental credential leakage
- ⚠️ **Note:** URL-based, not domain-based (subdomains are different)

### Client Secret Expiry
- ✅ **Ensures:** Expired secrets are detected and rejected
- ✅ **Prompts:** Re-registration when secrets expire
- ⚠️ **Note:** Clock skew may affect expiry checks

### Singleton Callback Server
- ✅ **CSRF Protection:** State parameter validation
- ✅ **Timeout:** 5-minute timeout per auth flow
- ⚠️ **Port Binding:** Single instance limitation
- ⚠️ **Localhost Only:** Binds to 127.0.0.1 (not exposed externally)

---

## Troubleshooting

### "token URL mismatch" Error

**Cause:** Token's ServerURL doesn't match requested URL.

**Solutions:**
1. Re-authenticate (automatic if using `LoadTokenForURL`)
2. Check if server URL changed
3. Manually delete old token if intentional URL change

### "client secret expired" Error

**Cause:** Stored client secret has expired.

**Solutions:**
1. Re-register client (dynamic registration)
2. Update client credentials in config
3. Delete expired client info: `storage.DeleteClientInfo(serverURL)`

### Singleton Callback Port Already in Use

**Cause:** Another process is using port 19876.

**Solutions:**
1. Use custom port: `WithSingletonCallback(8080)`
2. Stop other process using the port
3. Switch to per-flow servers (remove `WithSingletonCallback`)

---

## Credits

These improvements were inspired by [OpenCode](https://github.com/anomalyco/opencode)'s OAuth implementation, specifically:
- URL validation pattern from `mcp-auth.json` storage
- Client secret expiry tracking
- Singleton callback server design

---

## Related Documentation

- [OAuth 2.0 RFC 6749](https://tools.ietf.org/html/rfc6749)
- [OAuth 2.0 Authorization Server Metadata RFC 8414](https://tools.ietf.org/html/rfc8414)
- [PKCE RFC 7636](https://tools.ietf.org/html/rfc7636)
- [OAuth 2.0 Dynamic Client Registration RFC 7591](https://tools.ietf.org/html/rfc7591)
- [MCP OAuth Specification](https://modelcontextprotocol.io/docs/spec/oauth)

---

**Implementation Date:** February 2026
**Version:** SDK v0.6.3+
**Status:** ✅ Stable & Production Ready
