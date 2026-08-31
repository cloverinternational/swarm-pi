package cloud

import (
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
	"strings"
	"syscall"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/launch"
)

const (
	defaultRedirectURL string = "http://127.0.0.1:38100/callback"
	defaultListenAddr  string = "127.0.0.1:38100"
)

var defaultAuthTimeout time.Duration = 2 * time.Minute

const tokenRequestTimeout time.Duration = 15 * time.Second

// ErrCallbackPortInUse indicates the login callback listener port is busy.
var ErrCallbackPortInUse error = errors.New("callback port already in use")

// LoginOptions configures the Hosted UI login flow.
type LoginOptions struct {
	RedirectURL     string
	ListenAddr      string
	Scopes          []string
	Timeout         time.Duration
	OpenBrowser     bool
	AuthURLCallback func(string)
}

// DefaultLoginOptions returns default login options.
func DefaultLoginOptions() LoginOptions {
	var scopes []string = []string{"openid", "email", "profile"}
	return LoginOptions{
		RedirectURL: defaultRedirectURL,
		ListenAddr:  defaultListenAddr,
		Scopes:      scopes,
		Timeout:     defaultAuthTimeout,
		OpenBrowser: true,
	}
}

type authCallback struct {
	code  string
	state string
	err   string
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	IDToken      string `json:"id_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	Scope        string `json:"scope"`
	ExpiresIn    int64  `json:"expires_in"`
}

// LoginError wraps login failures with the generated auth URL for fallback flows.
type LoginError struct {
	Err     error
	AuthURL string
}

// Error returns the error message.
func (e *LoginError) Error() string {
	return e.Err.Error()
}

// Unwrap returns the underlying error.
func (e *LoginError) Unwrap() error {
	return e.Err
}

// LoginWithHostedUI performs the OAuth PKCE flow and stores tokens.
func LoginWithHostedUI(ctx context.Context, config *CloudConfig, tokenManager *TokenManager) (*TokenSet, error) {
	var options LoginOptions = DefaultLoginOptions()
	return loginWithOptions(ctx, config, tokenManager, options)
}

// LoginWithHostedUIOptions performs the Hosted UI login flow using custom options.
func LoginWithHostedUIOptions(ctx context.Context, config *CloudConfig, tokenManager *TokenManager, options LoginOptions) (*TokenSet, error) {
	return loginWithOptions(ctx, config, tokenManager, options)
}

// RefreshTokens refreshes stored tokens using the refresh token.
func RefreshTokens(ctx context.Context, config *CloudConfig, tokenManager *TokenManager) (*TokenSet, error) {
	return NewClient(config, tokenManager, nil).RefreshTokens(ctx)
}

func refreshTokensWithHTTP(ctx context.Context, httpClient *http.Client, config *CloudConfig, tokenManager *TokenManager) (*TokenSet, error) {
	if config == nil {
		return nil, fmt.Errorf("cloud config is required")
	}
	if tokenManager == nil {
		return nil, fmt.Errorf("token manager is required")
	}

	var current *TokenSet
	var err error
	current, err = tokenManager.LoadTokens()
	if err != nil {
		return nil, err
	}
	if current == nil || current.RefreshToken == "" {
		return nil, fmt.Errorf("refresh token missing; run /cloud login")
	}

	var tokenURL string
	tokenURL, err = buildHostedUIEndpoint(config.HostedUIURL, "/oauth2/token")
	if err != nil {
		return nil, err
	}

	var form url.Values = url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("client_id", config.PublicClientID)
	form.Set("refresh_token", current.RefreshToken)

	var response tokenResponse
	response, err = executeTokenRequest(ctx, httpClient, tokenURL, form)
	if err != nil {
		return nil, err
	}

	var refreshed TokenSet = buildTokenSet(response, current.RefreshToken)
	if err = tokenManager.SaveTokens(&refreshed); err != nil {
		return nil, err
	}

	return &refreshed, nil
}

// GetValidAccessToken returns a valid access token, refreshing if needed.
func GetValidAccessToken(ctx context.Context, config *CloudConfig, tokenManager *TokenManager) (string, error) {
	return NewClient(config, tokenManager, nil).GetValidAccessToken(ctx)
}

func getValidAccessTokenWithHTTP(ctx context.Context, httpClient *http.Client, config *CloudConfig, tokenManager *TokenManager) (string, error) {
	tokens, err := getValidTokensWithHTTP(ctx, httpClient, config, tokenManager)
	if err != nil {
		return "", err
	}
	if tokens.AccessToken == "" {
		return "", fmt.Errorf("access token missing; run /cloud login")
	}
	return tokens.AccessToken, nil
}

// GetValidTokens returns stored tokens, refreshing if expired.
func GetValidTokens(ctx context.Context, config *CloudConfig, tokenManager *TokenManager) (*TokenSet, error) {
	return NewClient(config, tokenManager, nil).GetValidTokens(ctx)
}

func getValidTokensWithHTTP(ctx context.Context, httpClient *http.Client, config *CloudConfig, tokenManager *TokenManager) (*TokenSet, error) {
	if config == nil {
		return nil, fmt.Errorf("cloud config is required")
	}
	if tokenManager == nil {
		return nil, fmt.Errorf("token manager is required")
	}

	var tokens *TokenSet
	var err error
	tokens, err = tokenManager.LoadTokens()
	if err != nil {
		return nil, err
	}
	if tokens == nil {
		return nil, fmt.Errorf("no cloud tokens found; run /cloud login")
	}

	if tokens.IsExpired() {
		return refreshTokensWithHTTP(ctx, httpClient, config, tokenManager)
	}

	return tokens, nil
}

func loginWithOptions(ctx context.Context, config *CloudConfig, tokenManager *TokenManager, options LoginOptions) (*TokenSet, error) {
	if config == nil {
		return nil, fmt.Errorf("cloud config is required")
	}
	if tokenManager == nil {
		return nil, fmt.Errorf("token manager is required")
	}
	if config.PublicClientID == "" {
		return nil, fmt.Errorf("public client id missing")
	}
	if options.RedirectURL == "" || options.ListenAddr == "" {
		return nil, fmt.Errorf("redirect url or listen address missing")
	}

	var codeVerifier string
	var codeChallenge string
	var err error
	codeVerifier, codeChallenge, err = generatePKCE()
	if err != nil {
		return nil, err
	}

	var state string
	state, err = generateState(24)
	if err != nil {
		return nil, err
	}

	var authURL string
	authURL, err = buildAuthorizeURL(config, options, state, codeChallenge)
	if err != nil {
		return nil, err
	}

	var callbackCh chan authCallback = make(chan authCallback, 1)
	var serverErrCh chan error = make(chan error, 1)
	var server *http.Server = &http.Server{
		Handler: buildCallbackHandler(callbackCh),
	}

	var listener net.Listener
	listener, err = net.Listen("tcp", options.ListenAddr)
	if err != nil {
		if errors.Is(err, syscall.EADDRINUSE) {
			return nil, &LoginError{Err: ErrCallbackPortInUse}
		}
		return nil, fmt.Errorf("failed to start local callback listener: %w", err)
	}

	go func() {
		var serveErr error = server.Serve(listener)
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			serverErrCh <- serveErr
		}
	}()

	var needsManual bool = false
	if options.OpenBrowser {
		err = launch.Open(authURL)
		if err != nil {
			if options.AuthURLCallback == nil {
				_ = server.Shutdown(context.Background())
				return nil, &LoginError{
					Err:     fmt.Errorf("failed to open browser: %w", err),
					AuthURL: authURL,
				}
			}
			needsManual = true
		}
	} else {
		needsManual = true
		if options.AuthURLCallback == nil {
			_ = server.Shutdown(context.Background())
			return nil, &LoginError{
				Err:     fmt.Errorf("browser launch disabled; open manually"),
				AuthURL: authURL,
			}
		}
	}

	if needsManual && options.AuthURLCallback != nil {
		options.AuthURLCallback(authURL)
	}

	var waitCtx context.Context
	var cancel context.CancelFunc
	waitCtx, cancel = context.WithTimeout(ctx, options.Timeout)
	defer cancel()

	var callback authCallback
	select {
	case callback = <-callbackCh:
		// continue
	case err = <-serverErrCh:
		_ = server.Shutdown(context.Background())
		return nil, fmt.Errorf("callback server error: %w", err)
	case <-waitCtx.Done():
		_ = server.Shutdown(context.Background())
		return nil, &LoginError{
			Err:     fmt.Errorf("login timed out"),
			AuthURL: authURL,
		}
	}

	_ = server.Shutdown(context.Background())

	if callback.err != "" {
		return nil, fmt.Errorf("auth error: %s", callback.err)
	}
	if callback.state != state {
		return nil, fmt.Errorf("invalid state returned from auth callback")
	}
	if callback.code == "" {
		return nil, fmt.Errorf("authorization code missing")
	}

	var tokenURL string
	tokenURL, err = buildHostedUIEndpoint(config.HostedUIURL, "/oauth2/token")
	if err != nil {
		return nil, err
	}

	var form url.Values = url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("client_id", config.PublicClientID)
	form.Set("code_verifier", codeVerifier)
	form.Set("code", callback.code)
	form.Set("redirect_uri", options.RedirectURL)

	var response tokenResponse
	response, err = executeTokenRequest(ctx, nil, tokenURL, form)
	if err != nil {
		return nil, err
	}

	var tokens TokenSet = buildTokenSet(response, response.RefreshToken)
	if err = tokenManager.SaveTokens(&tokens); err != nil {
		return nil, err
	}

	return &tokens, nil
}

func buildAuthorizeURL(config *CloudConfig, options LoginOptions, state string, codeChallenge string) (string, error) {
	var endpoint string
	var err error
	endpoint, err = buildHostedUIEndpoint(config.HostedUIURL, "/oauth2/authorize")
	if err != nil {
		return "", err
	}

	var endpointURL *url.URL
	endpointURL, err = url.Parse(endpoint)
	if err != nil {
		return "", err
	}

	var scopes string = strings.Join(options.Scopes, " ")
	var query url.Values = url.Values{}
	query.Set("response_type", "code")
	query.Set("client_id", config.PublicClientID)
	query.Set("redirect_uri", options.RedirectURL)
	query.Set("scope", scopes)
	query.Set("state", state)
	query.Set("code_challenge", codeChallenge)
	query.Set("code_challenge_method", "S256")
	endpointURL.RawQuery = query.Encode()

	return endpointURL.String(), nil
}

func buildHostedUIEndpoint(base string, path string) (string, error) {
	var baseURL *url.URL
	var err error
	baseURL, err = url.Parse(base)
	if err != nil {
		return "", fmt.Errorf("invalid hosted ui url: %w", err)
	}

	var refURL *url.URL
	refURL, err = url.Parse(path)
	if err != nil {
		return "", fmt.Errorf("invalid hosted ui path: %w", err)
	}

	var resolved *url.URL = baseURL.ResolveReference(refURL)
	return resolved.String(), nil
}

func buildCallbackHandler(callbackCh chan<- authCallback) http.Handler {
	var mux *http.ServeMux = http.NewServeMux()
	mux.HandleFunc("/callback", func(writer http.ResponseWriter, req *http.Request) {
		var query url.Values = req.URL.Query()
		var callback authCallback
		callback.code = query.Get("code")
		callback.state = query.Get("state")
		callback.err = query.Get("error")
		if callback.err == "" {
			callback.err = query.Get("error_description")
		}
		select {
		case callbackCh <- callback:
		default:
		}

		writer.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(writer, "<html><body><h3>Swarm Cloud login complete.</h3><p>You can close this window.</p></body></html>")
	})

	return mux
}

func executeTokenRequest(ctx context.Context, httpClient *http.Client, tokenURL string, form url.Values) (tokenResponse, error) {
	httpClient = ensureHTTPClient(httpClient)

	var empty tokenResponse
	var body string = form.Encode()

	reqCtx, cancel := withTimeout(ctx, tokenRequestTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, tokenURL, strings.NewReader(body))
	if err != nil {
		return empty, fmt.Errorf("failed to build token request: %w", err)
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := httpClient.Do(req)
	if err != nil {
		return empty, fmt.Errorf("token request failed: %w", err)
	}
	defer resp.Body.Close()

	var respBody []byte
	respBody, err = io.ReadAll(resp.Body)
	if err != nil {
		return empty, fmt.Errorf("failed to read token response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return empty, fmt.Errorf("token request failed: %s", strings.TrimSpace(string(respBody)))
	}

	var parsed tokenResponse
	if err = json.Unmarshal(respBody, &parsed); err != nil {
		return empty, fmt.Errorf("failed to parse token response: %w", err)
	}

	return parsed, nil
}

func buildTokenSet(response tokenResponse, refreshToken string) TokenSet {
	if strings.TrimSpace(response.RefreshToken) != "" {
		refreshToken = response.RefreshToken
	}

	var expiresAt int64 = 0
	if response.ExpiresIn > 0 {
		var expiry time.Time = time.Now().Add(time.Duration(response.ExpiresIn) * time.Second)
		expiresAt = expiry.Unix()
	}

	return TokenSet{
		AccessToken:  response.AccessToken,
		IDToken:      response.IDToken,
		RefreshToken: refreshToken,
		TokenType:    response.TokenType,
		Scope:        response.Scope,
		ExpiresAt:    expiresAt,
	}
}

func generatePKCE() (string, string, error) {
	var verifierBytes []byte = make([]byte, 32)
	var read int
	var err error
	read, err = rand.Read(verifierBytes)
	if err != nil {
		return "", "", fmt.Errorf("failed to generate code verifier: %w", err)
	}
	if read != len(verifierBytes) {
		return "", "", fmt.Errorf("failed to generate code verifier")
	}

	var verifier string = base64.RawURLEncoding.EncodeToString(verifierBytes)
	var sum [32]byte = sha256.Sum256([]byte(verifier))
	var challenge string = base64.RawURLEncoding.EncodeToString(sum[:])
	return verifier, challenge, nil
}

func generateState(length int) (string, error) {
	if length <= 0 {
		return "", fmt.Errorf("invalid state length")
	}

	var stateBytes []byte = make([]byte, length)
	var read int
	var err error
	read, err = rand.Read(stateBytes)
	if err != nil {
		return "", fmt.Errorf("failed to generate state: %w", err)
	}
	if read != len(stateBytes) {
		return "", fmt.Errorf("failed to generate state")
	}

	var state string = base64.RawURLEncoding.EncodeToString(stateBytes)
	return state, nil
}
