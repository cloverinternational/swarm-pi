package xai

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// ─── OIDC Discovery ──────────────────────────────────────────────────────────

// discoverEndpoints fetches the authorization and token endpoints from the
// xAI OIDC discovery document at https://auth.x.ai/.well-known/openid-configuration.
// Hermes calls this at the start of every login; endpoints are not hardcoded.
func discoverEndpoints(ctx context.Context) (authzEndpoint, tokenEndpoint string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, OAuthDiscoveryURL,
		http.NoBody)
	if err != nil {
		return "", "", fmt.Errorf("xAI OIDC discovery: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("xAI OIDC discovery: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("xAI OIDC discovery returned HTTP %d: %s",
			resp.StatusCode, string(raw))
	}

	var doc struct {
		AuthorizationEndpoint string `json:"authorization_endpoint"`
		TokenEndpoint         string `json:"token_endpoint"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return "", "", fmt.Errorf("xAI OIDC discovery: invalid JSON: %w", err)
	}
	if doc.AuthorizationEndpoint == "" || doc.TokenEndpoint == "" {
		return "", "", fmt.Errorf("xAI OIDC discovery: missing required endpoints in response")
	}
	return strings.TrimSpace(doc.AuthorizationEndpoint),
		strings.TrimSpace(doc.TokenEndpoint), nil
}

// ─── PKCE helpers ────────────────────────────────────────────────────────────

func generateCodeVerifier() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("xAI PKCE: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func codeChallenge(verifier string) string {
	h := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(h[:])
}

func randomHex() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// ─── CORS allowlist ──────────────────────────────────────────────────────────

// allowedCORSOrigin returns the echoed origin when it is on xAI's auth origin
// allowlist, or "" when the origin is not allowed.  Matches Hermes exactly.
func allowedCORSOrigin(origin string) string {
	switch origin {
	case "https://accounts.x.ai", "https://auth.x.ai":
		return origin
	}
	return ""
}

// ─── Loopback callback server ────────────────────────────────────────────────

// callbackResult is written by the loopback HTTP handler.
type callbackResult struct {
	Code             string
	State            string
	Error            string
	ErrorDescription string
}

// startCallbackServer binds an HTTP server on 127.0.0.1.
//
// It tries OAuthRedirectPort (56121) first.  If that port is occupied it
// falls back to an ephemeral port.  Matches Hermes _xai_start_callback_server.
func startCallbackServer() (srv *http.Server, ln net.Listener, result chan callbackResult, redirectURI string, err error) {
	resCh := make(chan callbackResult, 1)
	mux := http.NewServeMux()
	mux.HandleFunc(OAuthRedirectPath, func(w http.ResponseWriter, r *http.Request) {
		// ── CORS (must handle OPTIONS preflight before reading params) ──
		origin := r.Header.Get("Origin")
		if ao := allowedCORSOrigin(origin); ao != "" {
			w.Header().Set("Access-Control-Allow-Origin", ao)
			w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
			// Required for browser → loopback (private network access).
			w.Header().Set("Access-Control-Allow-Private-Network", "true")
			w.Header().Set("Vary", "Origin")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		q := r.URL.Query()
		code := q.Get("code")
		state := q.Get("state")
		oauthErr := q.Get("error")
		errDesc := q.Get("error_description")

		// Bare hit with no code and no error — user navigated here manually.
		// Do NOT close the server; keep waiting for the real callback.
		if code == "" && oauthErr == "" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`<!DOCTYPE html><html><body>
<h1>xAI authorization not received.</h1>
<p>No authorization code was present in this callback URL.
Return to Swarm and retry.</p></body></html>`))
			return
		}

		// Respond to the browser first, then send the result.
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		if oauthErr != "" {
			_, _ = w.Write([]byte(`<!DOCTYPE html><html><body>
<h1>xAI authorization failed.</h1>
<p>You can close this tab.</p></body></html>`))
		} else {
			_, _ = w.Write([]byte(`<!DOCTYPE html><html><body>
<h1>xAI authorization received.</h1>
<p>You can close this tab and return to Swarm.</p></body></html>`))
		}

		// Non-blocking send so a concurrent duplicate callback is dropped.
		select {
		case resCh <- callbackResult{
			Code:             code,
			State:            state,
			Error:            oauthErr,
			ErrorDescription: errDesc,
		}:
		default:
		}
	})

	srv = &http.Server{Handler: mux}

	// Try the fixed registered port first, fall back to ephemeral.
	for _, port := range []int{OAuthRedirectPort, 0} {
		addr := fmt.Sprintf("%s:%d", OAuthRedirectHost, port)
		ln, err = net.Listen("tcp", addr)
		if err == nil {
			break
		}
	}
	if err != nil {
		return nil, nil, nil, "", fmt.Errorf(
			"xAI OAuth: failed to bind callback server on %s: %w",
			OAuthRedirectHost, err)
	}

	actualPort := ln.Addr().(*net.TCPAddr).Port
	redirectURI = fmt.Sprintf("http://%s:%d%s", OAuthRedirectHost, actualPort, OAuthRedirectPath)

	go func() { _ = srv.Serve(ln) }()
	return srv, ln, resCh, redirectURI, nil
}

// ─── Authorize URL ────────────────────────────────────────────────────────────

// buildAuthorizeURL constructs the PKCE authorization URL.  Includes
// plan=generic and referrer=swarm-tui so xAI doesn't reject the loopback flow.
// Matches Hermes _xai_oauth_build_authorize_url exactly.
func buildAuthorizeURL(authzEndpoint, redirectURI, challenge, state, nonce string) string {
	params := url.Values{
		"response_type":         {"code"},
		"client_id":             {OAuthClientID},
		"redirect_uri":          {redirectURI},
		"scope":                 {OAuthScope},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"state":                 {state},
		"nonce":                 {nonce},
		// critical: opts into xAI's generic OAuth plan so loopback is accepted
		"plan": {"generic"},
		// attribution — identifies Swarm in xAI's auth logs
		"referrer": {"swarm-tui"},
	}
	return authzEndpoint + "?" + params.Encode()
}

// ─── Token exchange ───────────────────────────────────────────────────────────

// exchangeCode exchanges the authorization code for tokens.
//
// IMPORTANT: The token exchange body MUST include code_challenge +
// code_challenge_method in addition to code_verifier.  xAI's auth.x.ai
// implementation re-validates the challenge at the token step instead of
// relying solely on server-side session state from the authorize step — without
// them the server returns "code_challenge is required" even with a valid
// code_verifier.  This matches Hermes _xai_oauth_exchange_code_for_tokens.
func exchangeCode(ctx context.Context, tokenEndpoint, code, redirectURI, verifier, challenge string) (*OAuthToken, error) {
	if verifier == "" {
		return nil, fmt.Errorf("xAI token exchange refused: PKCE code_verifier is empty (bug)")
	}

	body := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"client_id":     {OAuthClientID},
		"code_verifier": {verifier},
		// Defense-in-depth: echo challenge back (required by xAI's token endpoint)
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenEndpoint,
		strings.NewReader(body.Encode()))
	if err != nil {
		return nil, fmt.Errorf("xAI token exchange: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("xAI token exchange: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		body := strings.TrimSpace(string(raw))
		if resp.StatusCode == http.StatusForbidden {
			return nil, fmt.Errorf(
				"xAI token exchange HTTP 403: this xAI account is not authorized for API "+
					"access via OAuth — xAI may restrict loopback OAuth to specific SuperGrok "+
					"tiers. Set XAI_API_KEY and use provider 'xai' (API-key path) instead, "+
					"or upgrade your subscription at https://x.ai/grok. Response: %s", body)
		}
		return nil, fmt.Errorf("xAI token exchange HTTP %d: %s", resp.StatusCode, body)
	}

	return parseTokenResponse(raw)
}

// parseTokenResponse decodes the JSON token response and computes ExpiresAt.
func parseTokenResponse(raw []byte) (*OAuthToken, error) {
	var resp struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		TokenType    string `json:"token_type"`
		ExpiresIn    int64  `json:"expires_in"`
		Scope        string `json:"scope"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("xAI OAuth: failed to parse token response: %w", err)
	}
	if resp.AccessToken == "" {
		return nil, fmt.Errorf("xAI OAuth: token response missing access_token")
	}

	token := &OAuthToken{
		AccessToken:  resp.AccessToken,
		RefreshToken: resp.RefreshToken,
		TokenType:    resp.TokenType,
		Scope:        resp.Scope,
	}
	if resp.ExpiresIn > 0 {
		token.ExpiresAt = time.Now().Unix() + resp.ExpiresIn
	}
	return token, nil
}

// ─── Public loopback flow ────────────────────────────────────────────────────

// LoopbackFlow holds in-progress PKCE state so the caller can show the auth
// URL in the UI before waiting for the callback.
type LoopbackFlow struct {
	server      *http.Server
	listener    net.Listener
	resultCh    chan callbackResult
	redirectURI string
	verifier    string
	challenge   string
	state       string
	nonce       string
	authzEP     string
	tokenEP     string
}

// NewLoopbackFlow performs OIDC discovery, generates PKCE material, binds the
// callback server, and returns a flow ready to display via AuthorizeURL().
// Matches the setup phase of Hermes _xai_oauth_loopback_login.
func NewLoopbackFlow(ctx context.Context) (*LoopbackFlow, error) {
	// 1. OIDC discovery — get live endpoints (not hardcoded).
	authzEP, tokenEP, err := discoverEndpoints(ctx)
	if err != nil {
		return nil, err
	}

	// 2. PKCE material.
	verifier, err := generateCodeVerifier()
	if err != nil {
		return nil, err
	}
	challenge := codeChallenge(verifier)
	state := randomHex()
	nonce := randomHex()

	// 3. Start the callback server on port 56121 (registered with xAI).
	srv, ln, resCh, redirectURI, err := startCallbackServer()
	if err != nil {
		return nil, err
	}

	return &LoopbackFlow{
		server:      srv,
		listener:    ln,
		resultCh:    resCh,
		redirectURI: redirectURI,
		verifier:    verifier,
		challenge:   challenge,
		state:       state,
		nonce:       nonce,
		authzEP:     authzEP,
		tokenEP:     tokenEP,
	}, nil
}

// AuthorizeURL returns the URL the user should open in their browser.
func (f *LoopbackFlow) AuthorizeURL() string {
	return buildAuthorizeURL(f.authzEP, f.redirectURI, f.challenge, f.state, f.nonce)
}

// RedirectURI returns the loopback URI this flow is listening on.
func (f *LoopbackFlow) RedirectURI() string { return f.redirectURI }

// WaitForCallback blocks until the browser posts the authorization code to the
// loopback server or the context is cancelled.  On success it exchanges the
// code for tokens and returns them (without storing).
func (f *LoopbackFlow) WaitForCallback(ctx context.Context) (*OAuthToken, error) {
	defer func() {
		_ = f.server.Shutdown(context.Background())
	}()

	select {
	case cb := <-f.resultCh:
		if cb.Error != "" {
			detail := cb.ErrorDescription
			if detail == "" {
				detail = cb.Error
			}
			return nil, fmt.Errorf("xAI authorization failed: %s", detail)
		}
		// Exchange the code; pass both verifier AND challenge.
		return exchangeCode(ctx, f.tokenEP, cb.Code, f.redirectURI, f.verifier, f.challenge)
	case <-ctx.Done():
		return nil, fmt.Errorf("xAI OAuth: timed out waiting for browser callback")
	}
}

// ─── Token refresh ────────────────────────────────────────────────────────────

// RefreshToken uses a stored refresh_token to obtain a new access token.
// Stores the refreshed token to disk on success.
func RefreshToken(ctx context.Context, refreshToken string) (*OAuthToken, error) {
	// Discover the token endpoint at refresh time so endpoint rotation is handled.
	_, tokenEP, err := discoverEndpoints(ctx)
	if err != nil {
		return nil, fmt.Errorf("xAI OAuth refresh: discovery failed: %w", err)
	}

	body := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"client_id":     {OAuthClientID},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenEP,
		strings.NewReader(body.Encode()))
	if err != nil {
		return nil, fmt.Errorf("xAI OAuth refresh: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("xAI OAuth refresh: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("xAI OAuth refresh failed HTTP %d: %s",
			resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	token, err := parseTokenResponse(raw)
	if err != nil {
		return nil, err
	}
	if token.RefreshToken == "" {
		token.RefreshToken = refreshToken
	}
	if err := StoreOAuthToken(token); err != nil {
		return nil, fmt.Errorf("xAI OAuth: failed to persist refreshed token: %w", err)
	}
	return token, nil
}

// RefreshAndStoreToken loads the persisted token and refreshes it if expired.
func RefreshAndStoreToken(ctx context.Context) (*OAuthToken, error) {
	token, err := GetStoredOAuthToken()
	if err != nil {
		return nil, err
	}
	if !token.IsExpired() {
		return token, nil
	}
	if token.RefreshToken == "" {
		return nil, fmt.Errorf("xAI OAuth: token expired and no refresh token available")
	}
	return RefreshToken(ctx, token.RefreshToken)
}

// ─── Grok CLI import ─────────────────────────────────────────────────────────

// GrokCLIToken is the shape of one entry inside ~/.grok/auth.json.
// The file maps "https://auth.x.ai::CLIENT_ID" → one of these entries.
type GrokCLIToken struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token,omitempty"`
	TokenType    string `json:"token_type,omitempty"`
	ExpiresAt    any    `json:"expires_at,omitempty"`
	Scope        string `json:"scope,omitempty"`
}

// ImportFromGrokCLI reads credentials from the official Grok CLI store and
// returns the first usable entry for OAuthClientID.
// Returns (nil, nil) when no Grok CLI store is present.
func ImportFromGrokCLI(authPath string) (*OAuthToken, error) {
	if authPath == "" {
		authPath = DefaultGrokCLIAuthPath()
	}

	data, err := os.ReadFile(authPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // not installed
		}
		return nil, fmt.Errorf("xAI: failed to read Grok CLI auth: %w", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("xAI: failed to parse Grok CLI auth: %w", err)
	}

	targetKey := GrokCLIAuthKeyPrefix + OAuthClientID
	for key, val := range raw {
		if key != targetKey {
			continue
		}
		var entry GrokCLIToken
		if err := json.Unmarshal(val, &entry); err != nil {
			continue
		}
		if entry.AccessToken == "" {
			continue
		}
		return &OAuthToken{
			AccessToken:  entry.AccessToken,
			RefreshToken: entry.RefreshToken,
			TokenType:    entry.TokenType,
			Scope:        entry.Scope,
			ExpiresAt:    parseExpiresAt(entry.ExpiresAt),
		}, nil
	}
	return nil, nil
}

// parseExpiresAt coerces the Grok CLI's expires_at (float64 unix or ISO-8601 string).
func parseExpiresAt(v any) int64 {
	if v == nil {
		return 0
	}
	switch t := v.(type) {
	case float64:
		return int64(t)
	case string:
		normalized := strings.Replace(t, "Z", "+00:00", 1)
		if parsed, err := time.Parse(time.RFC3339, normalized); err == nil {
			return parsed.Unix()
		}
	}
	return 0
}

// ImportAndStoreFromGrokCLI imports Grok CLI credentials and stores them.
// Returns (false, nil) when no credentials are present.
func ImportAndStoreFromGrokCLI(authPath string) (imported bool, err error) {
	token, err := ImportFromGrokCLI(authPath)
	if err != nil {
		return false, err
	}
	if token == nil {
		return false, nil
	}
	if err := StoreOAuthToken(token); err != nil {
		return false, err
	}
	return true, nil
}
