# OAuth MCP Server Implementation Comparison: OpenCode vs SwarmCode TUI/SDK

## Executive Summary

**OpenCode** uses the official MCP SDK's OAuth implementation, providing a straightforward integration with pre-existing OAuth infrastructure.

**SwarmCode TUI/SDK** has a **custom, more sophisticated OAuth implementation** with unique features:
- **Auto-discovery** - Automatically detects when OAuth is needed
- **Full RFC compliance** - Implements OAuth 2.0 and OpenID Connect discovery
- **Advanced transport abstraction** - Clean separation of concerns

---

## Architecture Comparison

### OpenCode Architecture

```
┌─────────────────────────────────────────────┐
│  OpenCode Application                       │
│                                             │
│  ┌─────────────────────────────────────┐   │
│  │ McpOAuthProvider                    │   │
│  │ (implements OAuthClientProvider)    │   │
│  │  - clientInformation()              │   │
│  │  - tokens()                         │   │
│  │  - saveTokens()                     │   │
│  │  - redirectToAuthorization()       │   │
│  └──────────────┬──────────────────────┘   │
│                 │                           │
│  ┌──────────────▼──────────────────────┐   │
│  │ @modelcontextprotocol/sdk           │   │
│  │  - Handles OAuth flow               │   │
│  │  - PKCE generation                  │   │
│  │  - Token exchange                   │   │
│  └──────────────┬──────────────────────┘   │
│                 │                           │
│  ┌──────────────▼──────────────────────┐   │
│  │ McpAuth (Storage Layer)             │   │
│  │  - mcp-auth.json                    │   │
│  │  - Tracks serverUrl per entry       │   │
│  │  - URL validation                   │   │
│  └─────────────────────────────────────┘   │
│                                             │
│  ┌─────────────────────────────────────┐   │
│  │ McpOAuthCallback (Singleton)        │   │
│  │  - Port 19876 (fixed)               │   │
│  │  - Shared across all MCP servers    │   │
│  │  - State-based routing              │   │
│  └─────────────────────────────────────┘   │
└─────────────────────────────────────────────┘
```

### SwarmCode TUI/SDK Architecture

```
┌──────────────────────────────────────────────────────────────┐
│  SwarmCode TUI/SDK                                           │
│                                                              │
│  ┌────────────────────────────────────────────────────────┐ │
│  │ AutoHTTPTransport (UNIQUE FEATURE)                     │ │
│  │  1. Try plain HTTP first                              │ │
│  │  2. If 401 → Auto-discover OAuth                      │ │
│  │  3. Parse WWW-Authenticate header                     │ │
│  │  4. Automatically start OAuth flow                    │ │
│  └──────────────┬─────────────────────────────────────────┘ │
│                 │                                            │
│  ┌──────────────▼─────────────────────────────────────────┐ │
│  │ OAuthHTTPTransport (Explicit OAuth)                    │ │
│  │  - Manual OAuth configuration                          │ │
│  │  - Token refresh handling                              │ │
│  │  - 401 re-authentication                               │ │
│  └──────────────┬─────────────────────────────────────────┘ │
│                 │                                            │
│  ┌──────────────▼─────────────────────────────────────────┐ │
│  │ OAuth Package (sdk/tools/mcp/oauth/)                   │ │
│  │                                                         │ │
│  │  ┌────────────────────────────────────────────────┐    │ │
│  │  │ Flow - Orchestrates full OAuth process        │    │ │
│  │  │  - Authenticate()                              │    │ │
│  │  │  - Token caching & refresh                     │    │ │
│  │  │  - Browser opener                              │    │ │
│  │  └────────────────────────────────────────────────┘    │ │
│  │                                                         │ │
│  │  ┌────────────────────────────────────────────────┐    │ │
│  │  │ DiscoveryClient - RFC 8414 compliant           │    │ │
│  │  │  - DiscoverProtectedResource()                 │    │ │
│  │  │  - DiscoverAuthorizationServer()              │    │ │
│  │  │  - DiscoverFromWWWAuthenticate()              │    │ │
│  │  │  - Fallback to OpenID Connect                 │    │ │
│  │  └────────────────────────────────────────────────┘    │ │
│  │                                                         │ │
│  │  ┌────────────────────────────────────────────────┐    │ │
│  │  │ TokenClient - Token operations                 │    │ │
│  │  │  - ExchangeCode()                              │    │ │
│  │  │  - RefreshToken()                              │    │ │
│  │  │  - RevokeToken()                               │    │ │
│  │  └────────────────────────────────────────────────┘    │ │
│  │                                                         │ │
│  │  ┌────────────────────────────────────────────────┐    │ │
│  │  │ CallbackServer - Dynamic per-flow              │    │ │
│  │  │  - Auto port selection                         │    │ │
│  │  │  - PKCE verification                           │    │ │
│  │  │  - State validation                            │    │ │
│  │  └────────────────────────────────────────────────┘    │ │
│  │                                                         │ │
│  │  ┌────────────────────────────────────────────────┐    │ │
│  │  │ TokenStorage - File-based                      │    │ │
│  │  │  - LoadToken()                                 │    │ │
│  │  │  - SaveToken()                                 │    │ │
│  │  │  - DeleteToken()                               │    │ │
│  │  └────────────────────────────────────────────────┘    │ │
│  └─────────────────────────────────────────────────────────┘ │
│                                                              │
│  ┌────────────────────────────────────────────────────────┐ │
│  │ RuntimeManager (TUI Integration)                       │ │
│  │  - Server lifecycle management                         │ │
│  │  - Token storage integration                           │ │
│  │  - Reconnection logic                                  │ │
│  └────────────────────────────────────────────────────────┘ │
└──────────────────────────────────────────────────────────────┘
```

---

## Feature Comparison

| Feature | OpenCode | SwarmCode TUI/SDK | Winner |
|---------|----------|-------------------|---------|
| **Auto-Detection** | ❌ Manual config required | ✅ Tries plain HTTP, auto-detects OAuth | **SwarmCode** 🏆 |
| **Discovery Mechanism** | ❌ Relies on SDK | ✅ Full RFC 8414 implementation | **SwarmCode** 🏆 |
| **WWW-Authenticate Parsing** | ❌ No | ✅ Yes, extracts metadata URL & scopes | **SwarmCode** 🏆 |
| **OpenID Connect Fallback** | ❌ No | ✅ Yes, tries OpenID if OAuth fails | **SwarmCode** 🏆 |
| **Token Refresh** | ✅ Via SDK | ✅ Custom implementation | **Tie** |
| **PKCE Support** | ✅ Via SDK | ✅ Custom implementation | **Tie** |
| **Callback Server** | ✅ Singleton, fixed port | ✅ Dynamic port, per-flow | **SwarmCode** 🏆 |
| **Client Registration** | ✅ Dynamic via SDK | ⚠️ Manual config required | **OpenCode** 🏆 |
| **URL Validation** | ✅ Tracks serverUrl, invalidates on change | ⚠️ No explicit URL tracking | **OpenCode** 🏆 |
| **Storage** | JSON file (`mcp-auth.json`) | File-based token storage | **Tie** |
| **Dependencies** | `@modelcontextprotocol/sdk` | Zero external OAuth deps | **SwarmCode** 🏆 |
| **Ease of Integration** | ✅ Simple, leverages SDK | ⚠️ More complex, custom | **OpenCode** 🏆 |
| **Flexibility** | ⚠️ SDK-dependent | ✅ Full control, pluggable | **SwarmCode** 🏆 |

---

## Detailed Feature Analysis

### 1. Auto-Discovery (SwarmCode Unique Feature)

**SwarmCode's `AutoHTTPTransport`** is a standout feature:

```go
// Step 1: Try plain HTTP first
plainTransport := NewHTTPStreamTransport(config)
err := plainTransport.Connect(probeCtx)

if err == nil {
    // Success! No OAuth needed
    return nil
}

// Step 2: Check if error is 401
if !strings.Contains(errStr, "401") {
    return err // Some other error
}

// Step 3: Get WWW-Authenticate header
wwwAuth, resourceURL, err := getAuthChallenge(ctx)

// Step 4: Discover OAuth configuration
resourceMeta, err := discoveryClient.DiscoverFromWWWAuthenticate(ctx, wwwAuth)

// Step 5: Automatically start OAuth flow
token, err := oauthFlow.Authenticate(ctx, config)
```

**Benefits:**
- Zero configuration for simple servers
- Graceful fallback to OAuth only when needed
- User-friendly: "it just works"

**OpenCode doesn't have this** - OAuth must be explicitly configured.

---

### 2. Discovery Implementation

#### SwarmCode (RFC 8414 Compliant)

Implements the full OAuth 2.0 Authorization Server Metadata discovery spec:

```go
// 1. Protected Resource Metadata
GET https://server/.well-known/oauth-protected-resource
→ Returns: { "authorization_servers": ["https://auth.example.com"] }

// 2. Authorization Server Metadata
GET https://auth.example.com/.well-known/oauth-authorization-server
→ Returns: {
    "authorization_endpoint": "...",
    "token_endpoint": "...",
    "revocation_endpoint": "...",
    "scopes_supported": [...]
  }

// 3. Fallback: OpenID Connect Discovery
GET https://auth.example.com/.well-known/openid-configuration
```

Additionally supports **WWW-Authenticate header parsing**:
```
WWW-Authenticate: Bearer resource_metadata="https://server/.well-known/resource"
                         scope="read write"
```

#### OpenCode

Uses the MCP SDK's built-in OAuth provider. Discovery is handled by the SDK.

**Winner:** SwarmCode - More transparent, RFC-compliant, debuggable

---

### 3. Callback Server Architecture

#### SwarmCode: Dynamic, Per-Flow

```go
// Each OAuth flow gets its own callback server
callbackServer, err := NewCallbackServer(0) // Port 0 = auto-select
callbackServer.Start()
redirectURI := callbackServer.RedirectURI() // e.g., http://127.0.0.1:54321/callback
```

**Pros:**
- No port conflicts
- Can run multiple OAuth flows simultaneously
- Each flow is isolated

**Cons:**
- More complex

#### OpenCode: Singleton, Fixed Port

```typescript
// Single callback server for all MCP servers
const OAUTH_CALLBACK_PORT = 19876
const OAUTH_CALLBACK_PATH = "/mcp/oauth/callback"

// Uses state parameter to route callbacks
pendingAuths.set(oauthState, { resolve, reject, timeout })
```

**Pros:**
- Simpler implementation
- Consistent callback URL
- Less resource usage

**Cons:**
- Port 19876 must be available
- Can't run multiple instances on same machine

**Winner:** Depends on use case
- **OpenCode** for simplicity
- **SwarmCode** for flexibility and multi-instance support

---

### 4. URL Validation & Security

#### OpenCode: Explicit URL Tracking

```typescript
export const Entry = z.object({
  tokens: Tokens.optional(),
  clientInfo: ClientInfo.optional(),
  serverUrl: z.string().optional(), // Track the URL these credentials are for
})

// Validates credentials match the current server URL
export async function getForUrl(mcpName: string, serverUrl: string) {
  const entry = await get(mcpName)
  if (!entry) return undefined

  // If URL has changed, credentials are invalid
  if (entry.serverUrl !== serverUrl) return undefined

  return entry
}
```

**Security benefit:** Prevents credential reuse if server URL changes (e.g., http → https, domain change)

#### SwarmCode: URL as Storage Key

```go
// Token is keyed by serverURL
func (s *FileTokenStorage) LoadToken(serverURL string) (*TokenData, error) {
    // ...
    filename := s.tokenFilename(serverURL)
    // ...
}
```

**Security benefit:** Implicit isolation - different URLs get different tokens

**Winner:** OpenCode - More explicit validation with better error messages

---

### 5. Client Registration

#### OpenCode: Dynamic Registration Supported

```typescript
async clientInformation(): Promise<OAuthClientInformation | undefined> {
  // Check config first (pre-registered client)
  if (this.config.clientId) {
    return { client_id: this.config.clientId, ... }
  }

  // Check stored client info (from dynamic registration)
  const entry = await McpAuth.getForUrl(this.mcpName, this.serverUrl)
  if (entry?.clientInfo) {
    return entry.clientInfo
  }

  // No client info - will trigger dynamic registration
  return undefined
}
```

The SDK handles dynamic client registration automatically.

#### SwarmCode: Manual Configuration Required

```go
func (t *AutoHTTPTransport) getClientID(serverMeta *oauth.ServerMetadata) string {
    // If configured, use that
    if t.config.OAuthConfig != nil && t.config.OAuthConfig.ClientID != "" {
        return t.config.OAuthConfig.ClientID
    }

    // For MCP spec compliance, we should use Client ID Metadata Documents
    // This would be a hosted URL like: https://app.example.com/mcp/client-metadata.json
    // For now, return empty to indicate configuration is needed
    return ""
}
```

**Winner:** OpenCode - Better UX for servers supporting dynamic registration

---

## Code Quality & Design

### OpenCode

**Strengths:**
- Leverages well-tested MCP SDK
- Clean separation: Provider interface implementation
- Simple, maintainable
- Good TypeScript typing

**Weaknesses:**
- Tied to SDK's implementation decisions
- Less control over OAuth flow details
- SDK is a dependency

### SwarmCode

**Strengths:**
- Zero external OAuth dependencies
- Full control over every aspect of OAuth
- Highly modular: DiscoveryClient, TokenClient, Flow, CallbackServer, Storage
- Excellent observability (tracing, logging)
- RFC-compliant implementation
- Pluggable storage (interface-based)

**Weaknesses:**
- More code to maintain
- More complex
- Need to stay current with OAuth specs

**Winner:** Depends on priorities
- **OpenCode** for fast implementation
- **SwarmCode** for control and customization

---

## User Experience Comparison

### Scenario 1: Connecting to an OAuth-enabled MCP Server

#### OpenCode
```typescript
// User must configure OAuth in advance
{
  "mcpServers": {
    "my-server": {
      "url": "https://api.example.com/mcp",
      "oauth": {
        "clientId": "...",
        "clientSecret": "...",
        "scope": "read write"
      }
    }
  }
}
```

1. User adds server with OAuth config
2. OpenCode creates OAuth provider
3. Browser opens for authorization
4. Callback received, token stored
5. Connection established

#### SwarmCode (with AutoHTTPTransport)
```go
// User just provides the URL
config := &TransportConfig{
    URL: "https://api.example.com/mcp",
}

transport := NewAutoHTTPTransport(config, logger, tracer)
transport.Connect(ctx)
```

1. SwarmCode tries plain HTTP
2. Receives 401
3. **Automatically** discovers OAuth configuration
4. Prompts user (if clientID not configured)
5. Browser opens for authorization
6. Connection established

**Winner:** SwarmCode - "Zero-config" OAuth for many cases

---

### Scenario 2: Token Expiry & Refresh

#### OpenCode
SDK handles refresh automatically via the `OAuthClientProvider` interface.

#### SwarmCode
```go
// Before each send, check token validity
if token != nil && token.IsExpired() {
    newToken, err := t.authenticate(ctx)
    // Update inner transport with new token
}
```

**Winner:** Tie - Both handle refresh automatically

---

## Security Comparison

| Security Feature | OpenCode | SwarmCode |
|-----------------|----------|-----------|
| **PKCE (RFC 7636)** | ✅ Via SDK | ✅ Custom impl |
| **State Parameter (CSRF)** | ✅ Via SDK | ✅ Custom impl |
| **Token Encryption** | ⚠️ Plain JSON | ⚠️ Plain file |
| **URL Validation** | ✅ Explicit | ⚠️ Implicit |
| **Client Secret Expiry** | ✅ Checked | ❌ Not tracked |
| **Timeout Protection** | ✅ 5 min | ✅ 5 min |
| **Secure Storage Permissions** | ✅ 0600 mode | ✅ File perms |

**Winner:** Tie - Both are secure, OpenCode slightly better on client secret expiry

---

## Performance Comparison

### Memory Footprint

- **OpenCode:** SDK + Provider + Callback Server (singleton)
- **SwarmCode:** Custom OAuth package + Per-flow callback servers

**Winner:** OpenCode (lighter due to singleton callback server)

### Network Efficiency

- **OpenCode:** SDK handles requests
- **SwarmCode:** Multiple discovery requests (protected resource → auth server → endpoints)

**Winner:** OpenCode (fewer requests in pre-configured scenarios)

### Connection Speed

- **OpenCode:** Immediate OAuth if configured
- **SwarmCode:** Tries HTTP first (adds latency if OAuth is required)

**Winner:** OpenCode (faster if OAuth is known)

---

## Recommendations

### When to Use OpenCode's Approach

✅ Building a new MCP client quickly
✅ Leveraging the official MCP ecosystem
✅ Simple, straightforward OAuth flows
✅ Don't need multi-instance support
✅ Dynamic client registration is important

### When to Use SwarmCode's Approach

✅ Need "zero-config" auto-discovery
✅ Want full control over OAuth flow
✅ Building a production system with complex requirements
✅ Need to support multiple instances on same machine
✅ Want to avoid external dependencies
✅ Need RFC-compliant discovery for interoperability
✅ Building a library/SDK that others will use

---

## Hybrid Approach: Best of Both Worlds

Consider combining the strengths:

```go
// 1. Use OpenCode's URL validation pattern
type TokenEntry struct {
    Token     *TokenData
    ServerURL string  // Track what URL this token is for
}

func (s *Storage) GetForURL(name, url string) (*TokenData, error) {
    entry := s.Load(name)
    if entry.ServerURL != url {
        return nil, ErrURLChanged
    }
    return entry.Token, nil
}

// 2. Use SwarmCode's auto-discovery
transport := NewAutoHTTPTransport(config)
// Tries plain HTTP first, auto-discovers OAuth if needed

// 3. Use OpenCode's singleton callback server pattern (for simplicity)
// But make it configurable
var callbackServer *CallbackServer
var callbackPort = 19876 // default, but can be overridden
```

---

## Conclusion

### Overall Winner: 🏆 **SwarmCode TUI/SDK**

**Reasoning:**
1. **Auto-discovery** is a game-changer for UX
2. **RFC-compliant implementation** ensures interoperability
3. **Zero dependencies** means full control
4. **More flexible architecture** for future requirements

**However, OpenCode has advantages:**
- Faster to implement (leverage SDK)
- Better URL validation
- Dynamic client registration support

### Recommendation for SwarmCode

**Keep the current implementation, but add:**

1. ✅ **URL tracking** from OpenCode (security improvement)
2. ✅ **Client secret expiry checking**
3. ✅ **Optional singleton callback server mode** (configurable)
4. ✅ **Dynamic client registration support**

This would make SwarmCode's implementation the **definitive OAuth MCP solution**.

---

## Code Examples: How They Compare

### Connecting to an MCP Server

#### OpenCode
```typescript
import { Client } from "@modelcontextprotocol/sdk/client/index.js"
import { SSEClientTransport } from "@modelcontextprotocol/sdk/client/sse.js"
import { McpOAuthProvider } from "./mcp/oauth-provider"

const oauthProvider = new McpOAuthProvider(
  "my-server",
  "https://api.example.com",
  { clientId: "...", scope: "read write" },
  { onRedirect: (url) => openBrowser(url) }
)

const transport = new SSEClientTransport(
  new URL("https://api.example.com/mcp"),
  { oauth: oauthProvider }
)

const client = new Client({ name: "OpenCode", version: "1.0" }, {})
await client.connect(transport)
```

#### SwarmCode
```go
// Option 1: Auto-discovery (zero config)
config := &mcp.TransportConfig{
    URL: "https://api.example.com/mcp",
}
transport := mcp.NewAutoHTTPTransport(config, logger, tracer)
err := transport.Connect(ctx)

// Option 2: Explicit OAuth
config := &mcp.TransportConfig{
    URL: "https://api.example.com/mcp",
    OAuthConfig: &mcp.OAuthConfig{
        ClientID: "...",
        Scopes:   []string{"read", "write"},
    },
}
transport := mcp.NewOAuthHTTPTransport(config, logger, tracer)
err := transport.Connect(ctx)
```

**Winner:** SwarmCode - More flexible, less boilerplate

---

## Final Thoughts

Both implementations are **high quality** and **production-ready**:

- **OpenCode** takes the pragmatic approach: use the official SDK, focus on app features
- **SwarmCode** takes the engineering approach: full control, RFC compliance, zero dependencies

The **auto-discovery feature** in SwarmCode is particularly impressive and sets it apart. It provides a significantly better developer experience for MCP server connections.

If I were building a new MCP client today, I would:
1. Start with **OpenCode's approach** for rapid prototyping
2. Migrate to **SwarmCode's approach** for production (with the additions mentioned above)

Or better yet: **Contribute SwarmCode's auto-discovery implementation to the MCP SDK** so everyone benefits! 🚀
