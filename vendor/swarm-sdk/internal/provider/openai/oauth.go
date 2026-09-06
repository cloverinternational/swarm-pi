package openai

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

// oauthTokenURL is a var so tests can point token exchange at a fake server.
var oauthTokenURL = oauthIssuer + "/oauth/token"

const (
	oauthIssuer             = "https://auth.openai.com"
	oauthDeviceUserCodeURL  = oauthIssuer + "/api/accounts/deviceauth/usercode"
	oauthDeviceTokenURL     = oauthIssuer + "/api/accounts/deviceauth/token"
	oauthDeviceRedirectURI  = oauthIssuer + "/deviceauth/callback"
	oauthDeviceVerification = oauthIssuer + "/codex/device"
	// Request full scope required for model access and Responses API writes.
	// model.request may not appear in JWT claims, but it must be requested so the
	// minted API key carries it.
	oauthDefaultScope        = "openid profile email offline_access model.request api.responses.write"
	codexClientID            = "app_EMoamEEZ73f0CkXaXp7hrann"
	maxDevicePollDuration    = 15 * time.Minute
	devicePollSleepFallback  = 5 * time.Second
	tokenExpiryBufferSeconds = 300 // 5 minutes
)

// OAuthDeviceVerificationURL returns the verification URL for device flow.
func OAuthDeviceVerificationURL() string {
	return oauthDeviceVerification
}

// OAuthToken holds OpenAI OAuth credentials and derived API key.
type OAuthToken struct {
	IDToken      string `json:"id_token"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	APIKey       string `json:"api_key"`
	ExpiresAt    int64  `json:"expires_at"` // unix seconds
	ClientID     string `json:"client_id"`
	AccountID    string `json:"account_id,omitempty"`
}

// DeviceFlow contains device auth metadata for the user prompt.
type DeviceFlow struct {
	DeviceAuthID string
	UserCode     string
	Interval     int
	Verification string
}

// CodeExchange contains the authorization code + PKCE verifier pair.
type CodeExchange struct {
	AuthorizationCode string
	CodeVerifier      string
	CodeChallenge     string
}

// StartDeviceFlow begins the device authorization flow and returns user-facing details.
func StartDeviceFlow(ctx context.Context) (*DeviceFlow, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	reqBody := map[string]string{
		"client_id": codexClientID,
		"scope":     oauthDefaultScope,
	}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, oauthDeviceUserCodeURL, strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		snippet := strings.TrimSpace(string(raw))
		if len(snippet) > 240 {
			snippet = snippet[:240] + "..."
		}
		// Avoid dumping full HTML pages into the UI; surface a short hint instead.
		lower := strings.ToLower(snippet)
		if strings.Contains(lower, "<html") || strings.Contains(lower, "<!doctype") {
			snippet = "received HTML error page"
		}
		return nil, fmt.Errorf("device auth request failed: status %d (%s)", resp.StatusCode, snippet)
	}

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var parsed map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, err
	}

	deviceAuthID, _ := parsed["device_auth_id"].(string)
	userCode, _ := parsed["user_code"].(string)
	interval := parseInterval(parsed["interval"])

	return &DeviceFlow{
		DeviceAuthID: deviceAuthID,
		UserCode:     userCode,
		Interval:     interval,
		Verification: oauthDeviceVerification,
	}, nil
}

// PollForAuthorizationCode waits for the user to complete the device flow and returns the code + verifier.
func PollForAuthorizationCode(ctx context.Context, flow *DeviceFlow) (*CodeExchange, error) {
	return PollForAuthorizationCodeWithCallback(ctx, flow, nil)
}

// PollForAuthorizationCodeWithCallback polls and invokes cb with status/body for observability.
func PollForAuthorizationCodeWithCallback(ctx context.Context, flow *DeviceFlow, cb func(status int, body string)) (*CodeExchange, error) {
	if flow == nil {
		return nil, errors.New("device flow is nil")
	}

	payload := map[string]string{
		"device_auth_id": flow.DeviceAuthID,
		"user_code":      flow.UserCode,
	}
	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	interval := time.Duration(flow.Interval) * time.Second
	if interval <= 0 {
		interval = devicePollSleepFallback
	}

	deadline := time.Now().Add(maxDevicePollDuration)

	for {
		if time.Now().After(deadline) {
			return nil, errors.New("device auth timed out after waiting for user approval")
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, oauthDeviceTokenURL, strings.NewReader(string(bodyBytes)))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")

		client := &http.Client{Timeout: 15 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}

		var parsed struct {
			AuthorizationCode string `json:"authorization_code"`
			CodeVerifier      string `json:"code_verifier"`
			CodeChallenge     string `json:"code_challenge"`
		}

		bodyRaw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if cb != nil {
			snippet := string(bodyRaw)
			if len(snippet) > 512 {
				snippet = snippet[:512] + "..."
			}
			cb(resp.StatusCode, snippet)
		}

		if resp.StatusCode == http.StatusOK {
			if err := json.Unmarshal(bodyRaw, &parsed); err != nil {
				return nil, err
			}
			return &CodeExchange{
				AuthorizationCode: parsed.AuthorizationCode,
				CodeVerifier:      parsed.CodeVerifier,
				CodeChallenge:     parsed.CodeChallenge,
			}, nil
		}

		// 403/404 means still pending; wait and retry.
		time.Sleep(interval)
	}
}

// ExchangeCodeForToken exchanges the authorization code for OAuth tokens.
func ExchangeCodeForToken(ctx context.Context, exchange *CodeExchange) (*OAuthToken, error) {
	if exchange == nil {
		return nil, errors.New("code exchange details missing")
	}

	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", exchange.AuthorizationCode)
	form.Set("redirect_uri", oauthDeviceRedirectURI)
	form.Set("client_id", codexClientID)
	form.Set("code_verifier", exchange.CodeVerifier)
	form.Set("scope", oauthDefaultScope)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, oauthTokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token exchange failed: status %d", resp.StatusCode)
	}

	var parsed struct {
		IDToken      string `json:"id_token"`
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, err
	}

	expiry := time.Now().Unix() + parsed.ExpiresIn
	if parsed.ExpiresIn == 0 {
		expiry = extractExpiry(parsed.AccessToken)
	}

	apiKey, err := exchangeIDTokenForAPIKey(ctx, parsed.IDToken)
	if err != nil {
		apiKey = ""
	}
	accountID := extractAccountID(parsed.IDToken)

	return &OAuthToken{
		IDToken:      parsed.IDToken,
		AccessToken:  parsed.AccessToken,
		RefreshToken: parsed.RefreshToken,
		APIKey:       apiKey,
		ExpiresAt:    expiry,
		ClientID:     codexClientID,
		AccountID:    accountID,
	}, nil
}

// RefreshAccessToken refreshes tokens using the refresh token.
func RefreshAccessToken(ctx context.Context, token *OAuthToken) (*OAuthToken, error) {
	if token == nil || token.RefreshToken == "" {
		return nil, errors.New("no refresh token available")
	}

	// codex-rs sends a JSON refresh body without a scope parameter
	// (login/src/auth/manager.rs RefreshRequest); mirror it exactly so any
	// backend tightening to that contract does not break swarm refresh.
	body, err := json.Marshal(map[string]string{
		"client_id":     codexClientID,
		"grant_type":    "refresh_token",
		"refresh_token": token.RefreshToken,
	})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, oauthTokenURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token refresh failed: status %d", resp.StatusCode)
	}

	var parsed struct {
		IDToken      string `json:"id_token"`
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, err
	}

	expiry := time.Now().Unix() + parsed.ExpiresIn
	if parsed.ExpiresIn == 0 {
		expiry = extractExpiry(parsed.AccessToken)
	}

	apiKey, err := exchangeIDTokenForAPIKey(ctx, parsed.IDToken)
	if err != nil || apiKey == "" {
		apiKey = token.APIKey
	}
	accountID := extractAccountID(parsed.IDToken)

	return &OAuthToken{
		IDToken:      parsed.IDToken,
		AccessToken:  parsed.AccessToken,
		RefreshToken: chooseString(parsed.RefreshToken, token.RefreshToken),
		APIKey:       chooseString(apiKey, token.APIKey),
		ExpiresAt:    expiry,
		ClientID:     codexClientID,
		AccountID:    chooseString(accountID, token.AccountID),
	}, nil
}

// IsTokenExpired checks expiry with a small buffer.
func IsTokenExpired(token *OAuthToken) bool {
	if token == nil {
		return true
	}
	if token.ExpiresAt == 0 {
		return false
	}
	return time.Now().Unix() >= (token.ExpiresAt - tokenExpiryBufferSeconds)
}

// exchangeIDTokenForAPIKey performs a token exchange to mint an API key usable with the OpenAI API.
func exchangeIDTokenForAPIKey(ctx context.Context, idToken string) (string, error) {
	if idToken == "" {
		return "", errors.New("id token missing")
	}

	form := buildAPIKeyExchangeForm(idToken)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, oauthTokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("api key exchange failed: status %d", resp.StatusCode)
	}

	var parsed struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return "", err
	}

	return parsed.AccessToken, nil
}

func buildAPIKeyExchangeForm(idToken string) url.Values {
	form := url.Values{}
	form.Set("grant_type", "urn:ietf:params:oauth:grant-type:token-exchange")
	form.Set("client_id", codexClientID)
	form.Set("requested_token", "openai-api-key")
	form.Set("subject_token", idToken)
	form.Set("subject_token_type", "urn:ietf:params:oauth:token-type:id_token")
	// Explicitly request model.request so the minted API key can call models.
	form.Set("scope", "model.request")
	if organizationID := extractOrganizationID(idToken); organizationID != "" {
		form.Set("organization_id", organizationID)
	}
	return form
}

// extractExpiry attempts to read exp from the JWT payload.
func extractExpiry(token string) int64 {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return 0
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return 0
	}
	var claims struct {
		Exp int64 `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return 0
	}
	return claims.Exp
}

// extractScopes returns OAuth scopes from the JWT. Handles both scp (Azure-style)
// and scope (OIDC standard) claims, in string or array form.
func extractScopes(token string) []string {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil
	}
	var raw map[string]any
	if err := json.Unmarshal(payload, &raw); err != nil {
		return nil
	}

	for _, key := range []string{"scp", "scope"} {
		val, ok := raw[key]
		if !ok {
			continue
		}

		switch t := val.(type) {
		case []any:
			var scopes []string
			for _, v := range t {
				if s, ok := v.(string); ok {
					scopes = append(scopes, s)
				}
			}
			return scopes
		case []string:
			return t
		case string:
			// Space-delimited string
			fields := strings.Fields(t)
			return fields
		}
	}

	return nil
}

// hasScope returns true if token includes the given scope.
func hasScope(token, scope string) bool {
	// Minted API keys are opaque (not JWTs) and implicitly carry model.request.
	if !strings.Contains(token, ".") {
		return true
	}
	return slices.Contains(extractScopes(token), scope)
}

// HasModelRequestScope reports whether the token includes model.request.
func HasModelRequestScope(token string) bool {
	return hasScope(token, "model.request")
}

// extractAccountID pulls chatgpt_account_id from the JWT custom claims.
func extractAccountID(token string) string {
	authObj := extractOpenAIAuth(token)
	if authObj == nil {
		return ""
	}
	if val, ok := authObj["chatgpt_account_id"].(string); ok {
		return val
	}
	return ""
}

func extractOrganizationID(token string) string {
	authObj := extractOpenAIAuth(token)
	if authObj == nil {
		return ""
	}
	orgs, ok := authObj["organizations"].([]any)
	if !ok {
		return ""
	}
	first := ""
	for _, raw := range orgs {
		org, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		id, _ := org["id"].(string)
		if id == "" {
			continue
		}
		if first == "" {
			first = id
		}
		if isDefault, _ := org["is_default"].(bool); isDefault {
			return id
		}
	}
	return first
}

func extractOpenAIAuth(token string) map[string]any {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil
	}
	if authRaw, ok := claims["https://api.openai.com/auth"]; ok {
		if authObj, ok := authRaw.(map[string]any); ok {
			return authObj
		}
	}
	return nil
}

// ExtractAccountIDFromIDToken returns the ChatGPT account id from an ID token.
func ExtractAccountIDFromIDToken(idToken string) string {
	return extractAccountID(idToken)
}

func chooseString(primary, fallback string) string {
	if primary != "" {
		return primary
	}
	return fallback
}

// parseInterval accepts numeric or string intervals; returns 0 on failure.
func parseInterval(v any) int {
	switch t := v.(type) {
	case nil:
		return 0
	case float64:
		return int(t)
	case json.Number:
		if i, err := t.Int64(); err == nil {
			return int(i)
		}
	case string:
		if i, err := strconv.Atoi(strings.TrimSpace(t)); err == nil {
			return i
		}
	}
	return 0
}
