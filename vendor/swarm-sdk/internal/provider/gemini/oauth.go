package gemini

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/Swarm-Code/mono/swarm-core/osutil"
	"sync"
	"time"
)

// OAuthTokens represents the OAuth token response and stored credentials.
type OAuthTokens struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token,omitempty"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int64  `json:"expires_in,omitempty"`
	ExpiresAt    int64  `json:"expires_at,omitempty"`
	Scope        string `json:"scope,omitempty"`
}

// IsExpired checks if the access token is expired or about to expire.
func (t *OAuthTokens) IsExpired() bool {
	if t.ExpiresAt == 0 {
		return true
	}
	// Consider token expired 5 minutes before actual expiry
	return time.Now().Unix() > (t.ExpiresAt - 300)
}

// ErrRefreshTokenInvalid is returned when a refresh attempt fails because the
// stored refresh token has been revoked or has expired (Google responds with an
// "invalid_grant" error). Callers should treat this as a signal to prompt the
// user to re-authenticate rather than retrying the refresh.
var ErrRefreshTokenInvalid = errors.New("gemini oauth: refresh token invalid or revoked")

// IsReauthRequired reports whether err indicates the stored refresh token is no
// longer usable (revoked/expired) and the user must complete an interactive
// login again. Callers (e.g. the TUI) can branch on this with a single check
// instead of matching the sentinel directly.
func IsReauthRequired(err error) bool {
	return errors.Is(err, ErrRefreshTokenInvalid)
}

// isInvalidGrant reports whether an OAuth token-endpoint error response
// indicates a revoked/expired grant. Google returns HTTP 400 with a JSON body
// of {"error":"invalid_grant", ...} in this case.
func isInvalidGrant(statusCode int, body []byte) bool {
	if statusCode != http.StatusBadRequest {
		return false
	}
	var errResp struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &errResp); err == nil {
		return errResp.Error == "invalid_grant"
	}
	// Fall back to a substring check if the body is not clean JSON.
	return strings.Contains(string(body), "invalid_grant")
}

// OAuthManager handles OAuth 2.0 authentication for Gemini API.
type OAuthManager struct {
	clientID     string
	clientSecret string
	scopes       []string
	tokenPath    string
	noBrowser    bool
	httpClient   *http.Client

	// tokenURL overrides the OAuth token endpoint. Empty means the default
	// Google endpoint (googleTokenURL). Primarily a seam for testing.
	tokenURL string

	mu     sync.RWMutex
	tokens *OAuthTokens
}

// googleTokenURL is the default OAuth 2.0 token endpoint.
const googleTokenURL = "https://oauth2.googleapis.com/token"

// resolveTokenURL returns the configured token endpoint or the Google default.
func (m *OAuthManager) resolveTokenURL() string {
	if m.tokenURL != "" {
		return m.tokenURL
	}
	return googleTokenURL
}

// NewOAuthManager creates a new OAuth manager.
func NewOAuthManager(config *Config) *OAuthManager {
	return &OAuthManager{
		clientID:     config.ClientID,
		clientSecret: config.ClientSecret,
		scopes:       DefaultScopes,
		tokenPath:    config.TokenPath,
		noBrowser:    config.NoBrowser,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// GetAccessToken returns a valid access token, refreshing or authenticating as needed.
func (m *OAuthManager) AccessToken(ctx context.Context) (string, error) {
	m.mu.RLock()
	tokens := m.tokens
	m.mu.RUnlock()

	// Try to load cached tokens if not in memory
	if tokens == nil {
		var err error
		tokens, err = m.loadTokens()
		if err == nil && tokens != nil {
			m.mu.Lock()
			m.tokens = tokens
			m.mu.Unlock()
		}
	}

	// If we have valid tokens, return the access token
	if tokens != nil && !tokens.IsExpired() {
		return tokens.AccessToken, nil
	}

	// Try to refresh if we have a refresh token
	if tokens != nil && tokens.RefreshToken != "" {
		newTokens, err := m.refreshTokens(ctx, tokens.RefreshToken)
		if err == nil {
			m.mu.Lock()
			m.tokens = newTokens
			m.mu.Unlock()
			if err := m.SaveTokens(newTokens); err != nil {
				// Log but don't fail
				fmt.Fprintf(os.Stderr, "Warning: failed to save tokens: %v\n", err)
			}
			return newTokens.AccessToken, nil
		}
		// If the refresh token itself is invalid/revoked, surface a clear typed
		// error so the caller can prompt for re-authentication. Do NOT loop or
		// silently fall through to a new interactive flow on this signal.
		if errors.Is(err, ErrRefreshTokenInvalid) {
			return "", err
		}
		// Refresh failed for a transient/other reason, need to re-authenticate
		fmt.Fprintf(os.Stderr, "Token refresh failed: %v\n", err)
	}

	// Need to authenticate
	newTokens, err := m.authenticate(ctx)
	if err != nil {
		return "", fmt.Errorf("authentication failed: %w", err)
	}

	m.mu.Lock()
	m.tokens = newTokens
	m.mu.Unlock()

	if err := m.SaveTokens(newTokens); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to save tokens: %v\n", err)
	}

	return newTokens.AccessToken, nil
}

// GeminiTokenSource yields a valid OAuth access token on demand. Each call to
// Token transparently refreshes (and persists) the token if it has expired, so
// callers never need to inspect expiry themselves. It mirrors the
// "savingTokenSource" pattern: always fresh, always persisted.
type GeminiTokenSource interface {
	// Token returns a currently-valid access token, refreshing and saving as
	// needed. It returns ErrRefreshTokenInvalid (see IsReauthRequired) when the
	// refresh token has been revoked/expired and interactive re-auth is required.
	Token() (string, error)
}

// savingTokenSource wraps an OAuthManager and binds it to a context, so that
// every Token() call goes through AccessToken (which refreshes + persists) and
// returns a guaranteed-fresh token. Concurrency safety is provided by the
// underlying OAuthManager (its mu guards the cached tokens).
type savingTokenSource struct {
	manager *OAuthManager
	ctx     context.Context
}

// Token implements GeminiTokenSource. It delegates to OAuthManager.AccessToken,
// which checks expiry, refreshes via the refresh token when needed, and
// persists any newly obtained tokens to disk before returning them.
func (s *savingTokenSource) Token() (string, error) {
	ctx := s.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return s.manager.AccessToken(ctx)
}

// TokenSource returns a GeminiTokenSource bound to ctx. The returned source
// always yields a valid (auto-refreshed and persisted) access token, formalizing
// the "always fresh + persisted" contract over the existing AccessToken logic.
// It is safe for concurrent use: refresh/save is serialized by the manager's
// internal mutex.
func (m *OAuthManager) TokenSource(ctx context.Context) GeminiTokenSource {
	return &savingTokenSource{manager: m, ctx: ctx}
}

// authenticate performs the OAuth flow to get new tokens.
func (m *OAuthManager) authenticate(ctx context.Context) (*OAuthTokens, error) {
	if m.noBrowser {
		return m.authenticateWithDeviceCode(ctx)
	}
	return m.authenticateWithBrowser(ctx)
}

// StartBrowserAuth starts the browser-based OAuth flow and returns control to the caller.
// It returns the auth URL to open, channels for the result, and a cleanup function.
func (m *OAuthManager) StartBrowserAuth(ctx context.Context) (authURL string, resultChan <-chan *OAuthTokens, errChan <-chan error, cleanup func(), err error) {
	// Generate PKCE verifier and challenge
	verifier, challenge, err := generatePKCE()
	if err != nil {
		return "", nil, nil, nil, fmt.Errorf("failed to generate PKCE: %w", err)
	}

	// Generate state for CSRF protection
	state, err := generateState()
	if err != nil {
		return "", nil, nil, nil, fmt.Errorf("failed to generate state: %w", err)
	}

	// Find available port
	listener, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		return "", nil, nil, nil, fmt.Errorf("failed to start callback server: %w", err)
	}

	port := listener.Addr().(*net.TCPAddr).Port
	redirectURI := fmt.Sprintf("http://localhost:%d/oauth2callback", port)

	// Build auth URL
	authURL = m.buildAuthURL(redirectURI, state, challenge)

	// Channels for results
	resCh := make(chan *OAuthTokens, 1)
	errCh := make(chan error, 1)

	// Start callback server
	server := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !strings.HasPrefix(r.URL.Path, "/oauth2callback") {
				http.Error(w, "Not found", http.StatusNotFound)
				return
			}

			query := r.URL.Query()

			// Check for error
			if errMsg := query.Get("error"); errMsg != "" {
				errDesc := query.Get("error_description")
				errCh <- fmt.Errorf("OAuth error: %s - %s", errMsg, errDesc)
				http.Redirect(w, r, "https://developers.google.com/gemini-code-assist/auth_failure_gemini", http.StatusFound)
				return
			}

			// Verify state
			if query.Get("state") != state {
				errCh <- errors.New("state mismatch - possible CSRF attack")
				http.Error(w, "State mismatch", http.StatusBadRequest)
				return
			}

			// Get authorization code
			code := query.Get("code")
			if code == "" {
				errCh <- errors.New("no authorization code received")
				http.Error(w, "No code received", http.StatusBadRequest)
				return
			}

			// Exchange code for tokens
			// We do this in the handler to ensure we capture the result before redirecting
			// Note: This might block the handler briefly
			tokens, err := m.exchangeCode(r.Context(), code, redirectURI, verifier)
			if err != nil {
				errCh <- err
				http.Redirect(w, r, "https://developers.google.com/gemini-code-assist/auth_failure_gemini", http.StatusFound)
				return
			}

			resCh <- tokens
			http.Redirect(w, r, "https://developers.google.com/gemini-code-assist/auth_success_gemini", http.StatusFound)
		}),
	}

	// Start server in background
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	cleanup = func() {
		server.Shutdown(context.Background())
		listener.Close()
	}

	return authURL, resCh, errCh, cleanup, nil
}

// authenticateWithBrowser performs OAuth using a local callback server.
func (m *OAuthManager) authenticateWithBrowser(ctx context.Context) (*OAuthTokens, error) {
	authURL, resCh, errCh, cleanup, err := m.StartBrowserAuth(ctx)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	// Open browser
	fmt.Printf("\nOpening browser for authentication...\n")
	fmt.Printf("If the browser doesn't open, visit this URL:\n\n%s\n\n", authURL)

	if err := openBrowser(authURL); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to open browser: %v\n", err)
	}

	// Wait for result or timeout
	select {
	case tokens := <-resCh:
		return tokens, nil
	case err := <-errCh:
		return nil, err
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(5 * time.Minute):
		return nil, errors.New("authentication timed out")
	}
}

// authenticateWithDeviceCode performs OAuth using manual code entry.
func (m *OAuthManager) authenticateWithDeviceCode(ctx context.Context) (*OAuthTokens, error) {
	// Generate PKCE verifier and challenge
	verifier, challenge, err := generatePKCE()
	if err != nil {
		return nil, fmt.Errorf("failed to generate PKCE: %w", err)
	}

	// Generate state
	state, err := generateState()
	if err != nil {
		return nil, fmt.Errorf("failed to generate state: %w", err)
	}

	redirectURI := "https://codeassist.google.com/authcode"
	authURL := m.buildAuthURL(redirectURI, state, challenge)

	fmt.Printf("\nPlease visit the following URL to authorize the application:\n\n%s\n\n", authURL)
	fmt.Print("Enter the authorization code: ")

	// Read code from stdin
	reader := bufio.NewReader(os.Stdin)
	code, err := reader.ReadString('\n')
	if err != nil {
		return nil, fmt.Errorf("failed to read authorization code: %w", err)
	}
	code = strings.TrimSpace(code)

	if code == "" {
		return nil, errors.New("authorization code is required")
	}

	return m.exchangeCode(ctx, code, redirectURI, verifier)
}

// buildAuthURL constructs the Google OAuth authorization URL.
func (m *OAuthManager) buildAuthURL(redirectURI, state, codeChallenge string) string {
	params := url.Values{
		"client_id":             {m.clientID},
		"redirect_uri":          {redirectURI},
		"response_type":         {"code"},
		"scope":                 {strings.Join(m.scopes, " ")},
		"access_type":           {"offline"},
		"state":                 {state},
		"code_challenge":        {codeChallenge},
		"code_challenge_method": {"S256"},
	}
	return "https://accounts.google.com/o/oauth2/v2/auth?" + params.Encode()
}

// exchangeCode exchanges an authorization code for tokens.
func (m *OAuthManager) exchangeCode(ctx context.Context, code, redirectURI, verifier string) (*OAuthTokens, error) {
	data := url.Values{
		"client_id":     {m.clientID},
		"client_secret": {m.clientSecret},
		"code":          {code},
		"code_verifier": {verifier},
		"grant_type":    {"authorization_code"},
		"redirect_uri":  {redirectURI},
	}

	req, err := http.NewRequestWithContext(ctx, "POST", m.resolveTokenURL(), strings.NewReader(data.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token exchange failed: %s - %s", resp.Status, string(body))
	}

	var tokens OAuthTokens
	if err := json.Unmarshal(body, &tokens); err != nil {
		return nil, err
	}

	// Calculate expiry time
	if tokens.ExpiresIn > 0 {
		tokens.ExpiresAt = time.Now().Unix() + tokens.ExpiresIn
	}

	return &tokens, nil
}

// refreshTokens uses a refresh token to get new access tokens.
func (m *OAuthManager) refreshTokens(ctx context.Context, refreshToken string) (*OAuthTokens, error) {
	data := url.Values{
		"client_id":     {m.clientID},
		"client_secret": {m.clientSecret},
		"refresh_token": {refreshToken},
		"grant_type":    {"refresh_token"},
	}

	req, err := http.NewRequestWithContext(ctx, "POST", m.resolveTokenURL(), strings.NewReader(data.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		// Detect a revoked/expired refresh token. Google returns HTTP 400 with
		// an "invalid_grant" error code in this case. Surface a clear typed
		// error so callers (e.g. the TUI) can prompt re-authentication instead
		// of silently failing or looping on a dead refresh token.
		if isInvalidGrant(resp.StatusCode, body) {
			return nil, fmt.Errorf("%w: %s", ErrRefreshTokenInvalid, strings.TrimSpace(string(body)))
		}
		return nil, fmt.Errorf("token refresh failed: %s - %s", resp.Status, string(body))
	}

	var tokens OAuthTokens
	if err := json.Unmarshal(body, &tokens); err != nil {
		return nil, err
	}

	// Keep the refresh token if the response doesn't include a new one
	if tokens.RefreshToken == "" {
		tokens.RefreshToken = refreshToken
	}

	// Calculate expiry time
	if tokens.ExpiresIn > 0 {
		tokens.ExpiresAt = time.Now().Unix() + tokens.ExpiresIn
	}

	return &tokens, nil
}

// loadTokens loads cached tokens from disk.
func (m *OAuthManager) loadTokens() (*OAuthTokens, error) {
	data, err := os.ReadFile(m.tokenPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var tokens OAuthTokens
	if err := json.Unmarshal(data, &tokens); err != nil {
		return nil, err
	}

	// Backfill ExpiresAt for tokens persisted with only ExpiresIn. Without this,
	// IsExpired() would treat ExpiresAt==0 as already expired and force an
	// unnecessary refresh/re-auth on every load. We approximate the deadline from
	// the file's mtime + ExpiresIn so the 5-minute buffer in IsExpired() still
	// applies meaningfully.
	if tokens.ExpiresAt == 0 && tokens.ExpiresIn > 0 {
		base := time.Now().Unix()
		if info, statErr := os.Stat(m.tokenPath); statErr == nil {
			base = info.ModTime().Unix()
		}
		tokens.ExpiresAt = base + tokens.ExpiresIn
	}

	return &tokens, nil
}

// SaveTokens saves tokens to disk.
func (m *OAuthManager) SaveTokens(tokens *OAuthTokens) error {
	// Ensure directory exists
	dir := filepath.Dir(m.tokenPath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}

	data, err := json.MarshalIndent(tokens, "", "  ")
	if err != nil {
		return err
	}

	// Write with restricted permissions
	return os.WriteFile(m.tokenPath, data, 0600)
}

// ClearTokens removes cached tokens.
func (m *OAuthManager) ClearTokens() error {
	m.mu.Lock()
	m.tokens = nil
	m.mu.Unlock()

	if err := os.Remove(m.tokenPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// HasStoredOAuthToken checks if a valid-looking OAuth token is stored on disk.
func HasStoredOAuthToken() bool {
	path := defaultTokenPath()
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}

	var tokens OAuthTokens
	if err := json.Unmarshal(data, &tokens); err != nil {
		return false
	}

	return tokens.AccessToken != ""
}

// PKCE helpers

// generatePKCE generates a PKCE verifier and challenge.
func generatePKCE() (verifier, challenge string, err error) {
	// Generate 32 random bytes for the verifier
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", err
	}

	verifier = base64.RawURLEncoding.EncodeToString(b)

	// Generate S256 challenge
	h := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(h[:])

	return verifier, challenge, nil
}

// generateState generates a random state string for CSRF protection.
func generateState() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// openBrowser opens the default browser to the specified URL.
func openBrowser(url string) error {
	return osutil.OpenURL(url)
}
