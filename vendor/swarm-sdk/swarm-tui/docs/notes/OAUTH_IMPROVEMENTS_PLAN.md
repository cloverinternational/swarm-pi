# OAuth Improvements Implementation Plan

## Overview

This plan adds three key features from OpenCode to enhance the SwarmCode OAuth implementation:

1. **URL Validation** - Prevent token reuse when server URLs change
2. **Client Secret Expiry Tracking** - Track and validate client secret expiration
3. **Optional Singleton Callback Server** - Configurable shared callback server

---

## Phase 1: URL Validation

### 1.1 Update TokenData Structure

**File:** `sdk/tools/mcp/oauth/types.go`

**Changes:**
```go
// TokenData holds OAuth token information.
type TokenData struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	TokenType    string    `json:"token_type"`
	ExpiresIn    int       `json:"expires_in,omitempty"`
	ExpiresAt    time.Time `json:"expires_at"`
	Scope        string    `json:"scope,omitempty"`

	// NEW: Track which URL this token is for (security)
	ServerURL    string    `json:"server_url"`
}
```

**Reasoning:** Storing the URL with the token allows validation on load.

---

### 1.2 Add URL Validation to Storage

**File:** `sdk/tools/mcp/oauth/storage.go`

**Changes:**

```go
// LoadTokenForURL loads a token for a server URL and validates it matches.
// Returns nil if the token exists but is for a different URL (invalid).
func (s *FileTokenStorage) LoadTokenForURL(serverURL string) (*TokenData, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	store, err := s.loadStore()
	if err != nil {
		return nil, err
	}

	token, ok := store.Tokens[serverURL]
	if !ok {
		return nil, nil // Not found
	}

	// Validate URL matches (security check)
	if token.ServerURL != "" && token.ServerURL != serverURL {
		// Token exists but URL changed - invalid
		return nil, fmt.Errorf("token URL mismatch: stored for %s, requested for %s",
			token.ServerURL, serverURL)
	}

	return token, nil
}
```

**Update SaveToken to set ServerURL:**
```go
func (s *FileTokenStorage) SaveToken(serverURL string, token *TokenData) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Ensure ServerURL is set
	token.ServerURL = serverURL

	store, err := s.loadStore()
	if err != nil {
		return err
	}

	store.Tokens[serverURL] = token

	return s.saveStore(store)
}
```

**Update TokenStorage interface:**
```go
type TokenStorage interface {
	SaveToken(serverURL string, token *TokenData) error
	LoadToken(serverURL string) (*TokenData, error)

	// NEW: Validates URL matches
	LoadTokenForURL(serverURL string) (*TokenData, error)

	DeleteToken(serverURL string) error
	ListTokens() ([]string, error)
}
```

---

### 1.3 Update Flow to Use LoadTokenForURL

**File:** `sdk/tools/mcp/oauth/flow.go`

**Changes:**
```go
func (f *Flow) Authenticate(ctx context.Context, config *AuthenticateConfig) (*TokenData, error) {
	// Step 1: Check for existing valid token WITH URL validation
	token, err := f.storage.LoadTokenForURL(config.ServerURL)
	if err != nil {
		// Log URL mismatch but continue with new auth
		// Don't fail hard - just re-authenticate
		// (Could optionally delete the invalid token here)
	}

	if token != nil && token.IsValid() {
		return token, nil
	}

	// ... rest of flow
}
```

---

## Phase 2: Client Secret Expiry Tracking

### 2.1 Add ClientInfo Structure

**File:** `sdk/tools/mcp/oauth/types.go`

**New Types:**
```go
// ClientInfo holds OAuth client registration information.
// This is used for dynamic client registration or tracking
// pre-registered client credentials.
type ClientInfo struct {
	ClientID              string    `json:"client_id"`
	ClientSecret          string    `json:"client_secret,omitempty"`
	ClientIDIssuedAt      time.Time `json:"client_id_issued_at,omitempty"`
	ClientSecretExpiresAt time.Time `json:"client_secret_expires_at,omitempty"`
}

// IsExpired checks if the client secret has expired.
func (c *ClientInfo) IsExpired() bool {
	if c.ClientSecretExpiresAt.IsZero() {
		return false // No expiry set
	}
	return time.Now().After(c.ClientSecretExpiresAt)
}

// IsValid checks if client info is valid and not expired.
func (c *ClientInfo) IsValid() bool {
	return c.ClientID != "" && !c.IsExpired()
}
```

---

### 2.2 Extend Storage for Client Info

**File:** `sdk/tools/mcp/oauth/storage.go`

**Update tokenStore:**
```go
type tokenStore struct {
	Tokens      map[string]*TokenData   `json:"tokens"`
	ClientInfos map[string]*ClientInfo  `json:"client_infos"` // NEW
}
```

**Add to TokenStorage interface:**
```go
type TokenStorage interface {
	SaveToken(serverURL string, token *TokenData) error
	LoadToken(serverURL string) (*TokenData, error)
	LoadTokenForURL(serverURL string) (*TokenData, error)
	DeleteToken(serverURL string) error
	ListTokens() ([]string, error)

	// NEW: Client info management
	SaveClientInfo(serverURL string, clientInfo *ClientInfo) error
	LoadClientInfo(serverURL string) (*ClientInfo, error)
	DeleteClientInfo(serverURL string) error
}
```

**Implement methods:**
```go
func (s *FileTokenStorage) SaveClientInfo(serverURL string, clientInfo *ClientInfo) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	store, err := s.loadStore()
	if err != nil {
		return err
	}

	if store.ClientInfos == nil {
		store.ClientInfos = make(map[string]*ClientInfo)
	}

	store.ClientInfos[serverURL] = clientInfo

	return s.saveStore(store)
}

func (s *FileTokenStorage) LoadClientInfo(serverURL string) (*ClientInfo, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	store, err := s.loadStore()
	if err != nil {
		return nil, err
	}

	clientInfo, ok := store.ClientInfos[serverURL]
	if !ok {
		return nil, nil // Not found
	}

	// Check if expired
	if clientInfo.IsExpired() {
		return nil, fmt.Errorf("client secret expired at %s",
			clientInfo.ClientSecretExpiresAt.Format(time.RFC3339))
	}

	return clientInfo, nil
}

func (s *FileTokenStorage) DeleteClientInfo(serverURL string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	store, err := s.loadStore()
	if err != nil {
		return err
	}

	delete(store.ClientInfos, serverURL)

	return s.saveStore(store)
}
```

**Update loadStore:**
```go
func (s *FileTokenStorage) loadStore() (*tokenStore, error) {
	store := &tokenStore{
		Tokens:      make(map[string]*TokenData),
		ClientInfos: make(map[string]*ClientInfo), // NEW
	}

	// ... rest of loading logic

	if store.ClientInfos == nil {
		store.ClientInfos = make(map[string]*ClientInfo)
	}

	return store, nil
}
```

---

### 2.3 Update Flow to Use Client Info

**File:** `sdk/tools/mcp/oauth/flow.go`

**Add methods:**
```go
// GetClientInfo retrieves client info from storage or config.
func (f *Flow) GetClientInfo(ctx context.Context, config *AuthenticateConfig) (*ClientInfo, error) {
	// Priority 1: Check storage for saved client info
	clientInfo, err := f.storage.LoadClientInfo(config.ServerURL)
	if err != nil {
		// Expired or error - fall through
	} else if clientInfo != nil {
		return clientInfo, nil
	}

	// Priority 2: Use configured client ID/secret
	if config.ClientID != "" {
		return &ClientInfo{
			ClientID:     config.ClientID,
			ClientSecret: config.ClientSecret,
		}, nil
	}

	return nil, fmt.Errorf("no client credentials available")
}

// SaveClientInfo saves client registration information.
func (f *Flow) SaveClientInfo(serverURL string, clientInfo *ClientInfo) error {
	return f.storage.SaveClientInfo(serverURL, clientInfo)
}
```

---

## Phase 3: Optional Singleton Callback Server

### 3.1 Create Singleton Callback Server

**File:** `sdk/tools/mcp/oauth/callback_singleton.go` (NEW)

```go
package oauth

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"
)

// SingletonCallbackServer is a shared callback server for multiple OAuth flows.
// It uses state parameter routing to direct callbacks to the correct flow.
type SingletonCallbackServer struct {
	port       int
	server     *http.Server
	listener   net.Listener
	mu         sync.Mutex
	started    bool

	// Map of state -> result channel
	pendingAuths map[string]chan *CallbackResult
	authMu       sync.RWMutex
}

var (
	globalCallbackServer     *SingletonCallbackServer
	globalCallbackServerOnce sync.Once
	globalCallbackServerMu   sync.Mutex
)

// DefaultCallbackPort is the default port for the singleton callback server.
const DefaultCallbackPort = 19876

// GetSingletonCallbackServer returns the global singleton callback server.
// It creates and starts the server on first call.
func GetSingletonCallbackServer(port int) (*SingletonCallbackServer, error) {
	globalCallbackServerMu.Lock()
	defer globalCallbackServerMu.Unlock()

	if globalCallbackServer != nil {
		return globalCallbackServer, nil
	}

	if port == 0 {
		port = DefaultCallbackPort
	}

	server, err := newSingletonCallbackServer(port)
	if err != nil {
		return nil, err
	}

	if err := server.Start(); err != nil {
		return nil, err
	}

	globalCallbackServer = server
	return globalCallbackServer, nil
}

// newSingletonCallbackServer creates a new singleton callback server.
func newSingletonCallbackServer(port int) (*SingletonCallbackServer, error) {
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return nil, fmt.Errorf("failed to listen on port %d: %w", port, err)
	}

	actualPort := listener.Addr().(*net.TCPAddr).Port

	return &SingletonCallbackServer{
		port:         actualPort,
		listener:     listener,
		pendingAuths: make(map[string]chan *CallbackResult),
	}, nil
}

// Port returns the port the server is listening on.
func (s *SingletonCallbackServer) Port() int {
	return s.port
}

// RedirectURI returns the full redirect URI for OAuth configuration.
func (s *SingletonCallbackServer) RedirectURI() string {
	return fmt.Sprintf("http://127.0.0.1:%d/callback", s.port)
}

// Start starts the singleton callback server.
func (s *SingletonCallbackServer) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.started {
		return nil // Already started
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/callback", s.handleCallback)

	s.server = &http.Server{
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	s.started = true

	go func() {
		if err := s.server.Serve(s.listener); err != nil && err != http.ErrServerClosed {
			// Log error but don't crash
			fmt.Printf("Callback server error: %v\n", err)
		}
	}()

	return nil
}

// handleCallback processes OAuth callbacks and routes them by state parameter.
func (s *SingletonCallbackServer) handleCallback(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()

	state := query.Get("state")
	if state == "" {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, "Missing state parameter")
		return
	}

	result := &CallbackResult{
		Code:  query.Get("code"),
		State: state,
		Error: query.Get("error"),
	}

	if errDesc := query.Get("error_description"); errDesc != "" && result.Error != "" {
		result.Error = result.Error + ": " + errDesc
	}

	// Find the pending auth for this state
	s.authMu.RLock()
	resultChan, ok := s.pendingAuths[state]
	s.authMu.RUnlock()

	if !ok {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, "Invalid or expired state parameter")
		return
	}

	// Send result
	select {
	case resultChan <- result:
	default:
		// Channel full or closed
	}

	// Clean up
	s.authMu.Lock()
	delete(s.pendingAuths, state)
	s.authMu.Unlock()

	// Respond with HTML
	if result.Error != "" {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, `<!DOCTYPE html>
<html>
<head><title>Authorization Failed</title></head>
<body style="font-family: system-ui, sans-serif; text-align: center; padding: 50px;">
<h1>❌ Authorization Failed</h1>
<p>%s</p>
<p>You can close this window.</p>
</body>
</html>`, result.Error)
		return
	}

	w.Header().Set("Content-Type", "text/html")
	fmt.Fprintf(w, `<!DOCTYPE html>
<html>
<head><title>Authorization Successful</title></head>
<body style="font-family: system-ui, sans-serif; text-align: center; padding: 50px;">
<h1>✅ Authorization Successful</h1>
<p>You have been authenticated. You can close this window and return to SwarmOS.</p>
</body>
</html>`)
}

// RegisterPendingAuth registers a pending OAuth flow by state.
func (s *SingletonCallbackServer) RegisterPendingAuth(state string) <-chan *CallbackResult {
	s.authMu.Lock()
	defer s.authMu.Unlock()

	resultChan := make(chan *CallbackResult, 1)
	s.pendingAuths[state] = resultChan
	return resultChan
}

// UnregisterPendingAuth removes a pending OAuth flow.
func (s *SingletonCallbackServer) UnregisterPendingAuth(state string) {
	s.authMu.Lock()
	defer s.authMu.Unlock()

	if ch, ok := s.pendingAuths[state]; ok {
		close(ch)
		delete(s.pendingAuths, state)
	}
}

// Stop gracefully shuts down the singleton callback server.
func (s *SingletonCallbackServer) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.started || s.server == nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	s.started = false

	// Close all pending channels
	s.authMu.Lock()
	for state, ch := range s.pendingAuths {
		close(ch)
		delete(s.pendingAuths, state)
	}
	s.authMu.Unlock()

	return s.server.Shutdown(ctx)
}
```

---

### 3.2 Make Callback Server Configurable in Flow

**File:** `sdk/tools/mcp/oauth/flow.go`

**Add option:**
```go
// WithSingletonCallback configures the flow to use a shared singleton callback server.
// This is useful for simpler deployments where port conflicts aren't a concern.
func WithSingletonCallback(port int) FlowOption {
	return func(f *Flow) {
		f.useSingletonCallback = true
		f.singletonCallbackPort = port
	}
}
```

**Update Flow struct:**
```go
type Flow struct {
	storage        TokenStorage
	discovery      *DiscoveryClient
	tokenClient    *TokenClient
	browserOpener  func(url string) error
	callbackPort   int // 0 = auto-select for per-flow callback
	authTimeout    time.Duration

	// NEW: Singleton callback server options
	useSingletonCallback  bool
	singletonCallbackPort int
}
```

**Update startAuthorizationFlow:**
```go
func (f *Flow) startAuthorizationFlow(ctx context.Context, config *AuthenticateConfig) (*TokenData, error) {
	// ... discovery logic ...

	// Step 2: Generate PKCE and state
	flowState, err := GenerateFlowState()
	if err != nil {
		return nil, fmt.Errorf("failed to generate flow state: %w", err)
	}

	// Step 3: Start callback server (singleton or per-flow)
	var redirectURI string
	var waitForCallback func(context.Context) (*CallbackResult, error)
	var cleanup func()

	if f.useSingletonCallback {
		// Use singleton callback server
		singleton, err := GetSingletonCallbackServer(f.singletonCallbackPort)
		if err != nil {
			return nil, fmt.Errorf("failed to get singleton callback server: %w", err)
		}

		redirectURI = singleton.RedirectURI()
		resultChan := singleton.RegisterPendingAuth(flowState.State)

		waitForCallback = func(ctx context.Context) (*CallbackResult, error) {
			select {
			case result := <-resultChan:
				if result.Error != "" {
					return nil, fmt.Errorf("authorization failed: %s", result.Error)
				}
				return result, nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}

		cleanup = func() {
			singleton.UnregisterPendingAuth(flowState.State)
		}
	} else {
		// Use per-flow callback server (existing behavior)
		callbackServer, err := NewCallbackServer(f.callbackPort)
		if err != nil {
			return nil, fmt.Errorf("failed to start callback server: %w", err)
		}

		if err := callbackServer.Start(); err != nil {
			return nil, fmt.Errorf("failed to start callback server: %w", err)
		}

		redirectURI = callbackServer.RedirectURI()

		waitForCallback = func(ctx context.Context) (*CallbackResult, error) {
			return callbackServer.WaitForCallback(ctx)
		}

		cleanup = func() {
			callbackServer.Stop()
		}
	}

	defer cleanup()

	// ... rest of flow (authorization URL, browser, token exchange) ...
}
```

---

## Phase 4: Update Memory Storage Implementation

**File:** `sdk/tools/mcp/oauth/storage.go`

Update `MemoryTokenStorage` to implement new interfaces:

```go
// MemoryTokenStorage implements TokenStorage using in-memory storage.
type MemoryTokenStorage struct {
	tokens      map[string]*TokenData
	clientInfos map[string]*ClientInfo
	mu          sync.RWMutex
}

func NewMemoryTokenStorage() *MemoryTokenStorage {
	return &MemoryTokenStorage{
		tokens:      make(map[string]*TokenData),
		clientInfos: make(map[string]*ClientInfo),
	}
}

// SaveToken saves a token for a server URL.
func (s *MemoryTokenStorage) SaveToken(serverURL string, token *TokenData) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	token.ServerURL = serverURL
	s.tokens[serverURL] = token
	return nil
}

// LoadTokenForURL loads and validates token URL.
func (s *MemoryTokenStorage) LoadTokenForURL(serverURL string) (*TokenData, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	token := s.tokens[serverURL]
	if token == nil {
		return nil, nil
	}

	if token.ServerURL != "" && token.ServerURL != serverURL {
		return nil, fmt.Errorf("token URL mismatch")
	}

	return token, nil
}

// SaveClientInfo, LoadClientInfo, DeleteClientInfo implementations
func (s *MemoryTokenStorage) SaveClientInfo(serverURL string, clientInfo *ClientInfo) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clientInfos[serverURL] = clientInfo
	return nil
}

func (s *MemoryTokenStorage) LoadClientInfo(serverURL string) (*ClientInfo, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	clientInfo := s.clientInfos[serverURL]
	if clientInfo == nil {
		return nil, nil
	}

	if clientInfo.IsExpired() {
		return nil, fmt.Errorf("client secret expired")
	}

	return clientInfo, nil
}

func (s *MemoryTokenStorage) DeleteClientInfo(serverURL string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.clientInfos, serverURL)
	return nil
}
```

---

## Phase 5: Add Tests

### 5.1 URL Validation Tests

**File:** `sdk/tools/mcp/oauth/storage_test.go`

```go
func TestURLValidation(t *testing.T) {
	storage := NewMemoryTokenStorage()

	// Save token for URL1
	token := &TokenData{
		AccessToken: "token1",
		ServerURL:   "https://server1.com",
	}
	storage.SaveToken("https://server1.com", token)

	// Load with correct URL - should succeed
	loaded, err := storage.LoadTokenForURL("https://server1.com")
	assert.NoError(t, err)
	assert.Equal(t, "token1", loaded.AccessToken)

	// Load with different URL - should fail
	_, err = storage.LoadTokenForURL("https://server2.com")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "URL mismatch")
}
```

### 5.2 Client Secret Expiry Tests

**File:** `sdk/tools/mcp/oauth/types_test.go`

```go
func TestClientInfoExpiry(t *testing.T) {
	// Not expired
	clientInfo := &ClientInfo{
		ClientID:              "client123",
		ClientSecretExpiresAt: time.Now().Add(1 * time.Hour),
	}
	assert.False(t, clientInfo.IsExpired())
	assert.True(t, clientInfo.IsValid())

	// Expired
	clientInfo.ClientSecretExpiresAt = time.Now().Add(-1 * time.Hour)
	assert.True(t, clientInfo.IsExpired())
	assert.False(t, clientInfo.IsValid())
}
```

### 5.3 Singleton Callback Server Tests

**File:** `sdk/tools/mcp/oauth/callback_singleton_test.go`

```go
func TestSingletonCallbackServer(t *testing.T) {
	server, err := GetSingletonCallbackServer(0)
	assert.NoError(t, err)
	assert.NotNil(t, server)

	// Register two pending auths
	state1 := "state1"
	state2 := "state2"

	ch1 := server.RegisterPendingAuth(state1)
	ch2 := server.RegisterPendingAuth(state2)

	// Simulate callbacks
	// (would need to make HTTP requests to test fully)

	// Cleanup
	server.UnregisterPendingAuth(state1)
	server.UnregisterPendingAuth(state2)
	server.Stop()
}
```

---

## Implementation Order

### Week 1: URL Validation
1. ✅ Update `TokenData` struct (types.go)
2. ✅ Add `LoadTokenForURL` method (storage.go)
3. ✅ Update `SaveToken` to set ServerURL (storage.go)
4. ✅ Update `Flow.Authenticate` to use `LoadTokenForURL` (flow.go)
5. ✅ Update `MemoryTokenStorage` (storage.go)
6. ✅ Write tests (storage_test.go)

### Week 2: Client Secret Expiry
1. ✅ Add `ClientInfo` struct (types.go)
2. ✅ Extend `tokenStore` structure (storage.go)
3. ✅ Add client info methods to `TokenStorage` interface (storage.go)
4. ✅ Implement client info methods (storage.go)
5. ✅ Add `GetClientInfo`/`SaveClientInfo` to Flow (flow.go)
6. ✅ Update `MemoryTokenStorage` (storage.go)
7. ✅ Write tests (types_test.go, storage_test.go)

### Week 3: Singleton Callback Server
1. ✅ Create `callback_singleton.go`
2. ✅ Implement `SingletonCallbackServer`
3. ✅ Add `WithSingletonCallback` option (flow.go)
4. ✅ Update `startAuthorizationFlow` (flow.go)
5. ✅ Write tests (callback_singleton_test.go)
6. ✅ Update documentation

---

## Migration Guide

### For Existing Users

**Old Code:**
```go
flow, _ := oauth.NewFlow()
token, err := flow.Authenticate(ctx, &oauth.AuthenticateConfig{
    ServerURL: "https://api.example.com",
    ClientID:  "client123",
})
```

**New Code (Same, backward compatible):**
```go
flow, _ := oauth.NewFlow()
token, err := flow.Authenticate(ctx, &oauth.AuthenticateConfig{
    ServerURL: "https://api.example.com",
    ClientID:  "client123",
})
// URL validation happens automatically
```

**Using Singleton Callback Server:**
```go
flow, _ := oauth.NewFlow(
    oauth.WithSingletonCallback(19876), // Use fixed port
)
token, err := flow.Authenticate(ctx, &oauth.AuthenticateConfig{
    ServerURL: "https://api.example.com",
    ClientID:  "client123",
})
```

---

## Benefits Summary

### 1. URL Validation
- **Security:** Prevents accidental token reuse when URLs change
- **Debugging:** Clear errors when URL mismatches occur
- **Correctness:** Ensures tokens are only used for their intended server

### 2. Client Secret Expiry
- **Compliance:** Properly handles client secret rotation
- **Robustness:** Automatically re-registers when secrets expire
- **Standards:** Follows OAuth 2.0 dynamic client registration spec

### 3. Singleton Callback Server
- **Simplicity:** Single port for all OAuth flows
- **Compatibility:** Works like OpenCode for familiar UX
- **Flexibility:** Optional - keeps per-flow as default
- **Multi-instance:** Can still use per-flow for multiple instances

---

## Testing Plan

### Unit Tests
- ✅ URL validation in storage layer
- ✅ Client info expiry checks
- ✅ Singleton callback server registration/routing
- ✅ Backward compatibility (existing tokens without ServerURL)

### Integration Tests
- ✅ Full OAuth flow with URL validation
- ✅ Client secret expiry during authentication
- ✅ Singleton callback with multiple concurrent flows
- ✅ Migration from old token format to new

### Manual Testing
- ✅ Connect to MCP server, change URL, verify re-auth required
- ✅ Client secret expires, verify automatic re-registration
- ✅ Multiple OAuth flows with singleton callback server
- ✅ Port conflict handling with singleton server

---

## Documentation Updates

### README.md
- Document URL validation feature
- Document client secret expiry handling
- Document singleton callback server option

### CHANGELOG.md
```markdown
## [Unreleased]

### Added
- URL validation for OAuth tokens (security improvement from OpenCode)
- Client secret expiry tracking and validation
- Optional singleton callback server (configurable via WithSingletonCallback)

### Changed
- TokenData now includes ServerURL field for validation
- Storage format extended with ClientInfo tracking
- Backward compatible with existing token storage

### Security
- Tokens are now validated against server URL to prevent reuse
- Client secret expiration is tracked and validated
```

---

## Risk Assessment

### Low Risk
- ✅ URL validation (backward compatible)
- ✅ Client info tracking (separate from tokens)

### Medium Risk
- ⚠️ Singleton callback server (new code path, but optional)
- ⚠️ Storage format changes (need migration handling)

### Mitigation
- Default behavior unchanged (per-flow callback servers)
- Old tokens without ServerURL still work (graceful migration)
- Extensive unit and integration tests
- Feature flags for gradual rollout

---

## Timeline

- **Week 1:** URL Validation implementation + tests
- **Week 2:** Client Secret Expiry implementation + tests
- **Week 3:** Singleton Callback Server implementation + tests
- **Week 4:** Integration testing, documentation, code review

**Total:** 4 weeks

---

## Success Criteria

- ✅ All existing tests pass
- ✅ New features have >90% test coverage
- ✅ Backward compatible with existing token storage
- ✅ Documentation updated
- ✅ Code review approved
- ✅ Manual testing completed successfully

---

## Questions for User

1. **Priority:** Should we implement all three features, or prioritize some?
2. **Timeline:** Is 4 weeks acceptable, or do we need faster delivery?
3. **Singleton Port:** Should we use port 19876 like OpenCode, or make it fully configurable?
4. **Migration:** Should we auto-migrate old tokens to include ServerURL, or require re-authentication?

---

Ready to proceed with implementation! 🚀
