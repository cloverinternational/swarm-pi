package cloud

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/atomicfile"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/paths"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider/anthropic"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider/openai"
)

const providerCredentialSyncTimeout time.Duration = 20 * time.Second

type tuiAccountFile struct {
	Accounts []struct {
		Provider  string          `json:"provider"`
		IsActive  bool            `json:"is_active"`
		AddedAt   int64           `json:"added_at"`
		TokenData json.RawMessage `json:"token_data"`
	} `json:"accounts"`
}

type providerCredentialSyncRequest struct {
	Provider  string            `json:"provider"`
	Token     string            `json:"token"`
	Source    string            `json:"source"`
	ExpiresAt string            `json:"expires_at,omitempty"`
	Metadata  map[string]string `json:"metadata,omitempty"`
}

func (c *Client) SyncHostedProviderCredential(ctx context.Context, provider string) error {
	if c == nil {
		return fmt.Errorf("cloud client is required")
	}
	if c.Config == nil {
		return fmt.Errorf("cloud config is required")
	}
	if c.TokenManager == nil {
		return fmt.Errorf("token manager is required")
	}

	requestBody, err := resolveHostedProviderCredential(ctx, provider)
	if err != nil {
		return err
	}

	accessToken, err := getValidAccessTokenWithHTTP(ctx, c.httpClient(), c.Config, c.TokenManager)
	if err != nil {
		return err
	}

	endpoint := strings.TrimRight(c.Config.APIBaseURL, "/") + "/v1/provider-credentials"
	body, err := json.Marshal(requestBody)
	if err != nil {
		return fmt.Errorf("marshal provider credential sync request: %w", err)
	}

	reqCtx, cancel := withTimeout(ctx, providerCredentialSyncTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build provider credential sync request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return fmt.Errorf("sync provider credential: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("sync provider credential failed: status %d (%s)", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return nil
}

func resolveHostedProviderCredential(ctx context.Context, provider string) (*providerCredentialSyncRequest, error) {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "openai", "codex":
		return resolveHostedOpenAICredential(ctx)
	case "anthropic", "claudecode", "claude-code", "claude_code":
		return resolveHostedAnthropicCredential()
	default:
		return nil, fmt.Errorf("unsupported hosted provider credential sync provider: %s", provider)
	}
}

func resolveHostedOpenAICredential(ctx context.Context) (*providerCredentialSyncRequest, error) {
	token, err := openai.RefreshAndStoreToken(ctx)
	if err != nil {
		token, err = openai.GetStoredOAuthToken()
	}
	if (err != nil || token == nil || (strings.TrimSpace(token.APIKey) == "" && strings.TrimSpace(token.AccessToken) == "")) &&
		loadOpenAITokenFromTUIAccounts(ctx) == nil {
		token, err = openai.RefreshAndStoreToken(ctx)
		if err != nil {
			token, err = openai.GetStoredOAuthToken()
		}
	}
	if err != nil || token == nil {
		return nil, fmt.Errorf("openai oauth token not available")
	}

	secret := strings.TrimSpace(token.APIKey)
	expiresAt := ""
	tokenKind := "api_key"
	if secret == "" {
		secret = strings.TrimSpace(token.AccessToken)
		tokenKind = "access_token"
		if token.ExpiresAt > 0 {
			expiresAt = time.Unix(token.ExpiresAt, 0).UTC().Format(time.RFC3339)
		}
	}
	if secret == "" {
		return nil, fmt.Errorf("openai oauth token not available")
	}

	accountID := strings.TrimSpace(token.AccountID)
	if accountID == "" && strings.TrimSpace(token.IDToken) != "" {
		accountID = strings.TrimSpace(openai.ExtractAccountIDFromIDToken(token.IDToken))
	}

	metadata := map[string]string{
		"is_oauth":   "true",
		"token_kind": tokenKind,
	}
	if accountID != "" {
		metadata["account_id"] = accountID
	}

	return &providerCredentialSyncRequest{
		Provider:  "openai",
		Token:     secret,
		Source:    "oauth",
		ExpiresAt: expiresAt,
		Metadata:  metadata,
	}, nil
}

func resolveHostedAnthropicCredential() (*providerCredentialSyncRequest, error) {
	token, err := anthropic.GetStoredOAuthToken()
	if err == nil && token != nil && token.Expiry > 0 && time.Now().Unix() >= token.Expiry && token.RefreshToken != "" {
		token, err = anthropic.RefreshAndStoreToken()
	}
	if (err != nil || token == nil || strings.TrimSpace(token.AccessToken) == "") && loadAnthropicTokenFromTUIAccounts() == nil {
		token, err = anthropic.GetStoredOAuthToken()
		if err == nil && token != nil && token.Expiry > 0 && time.Now().Unix() >= token.Expiry && token.RefreshToken != "" {
			token, err = anthropic.RefreshAndStoreToken()
		}
	}
	if err != nil || token == nil || strings.TrimSpace(token.AccessToken) == "" {
		return nil, fmt.Errorf("anthropic oauth token not available")
	}

	requestBody := &providerCredentialSyncRequest{
		Provider: "anthropic",
		Token:    strings.TrimSpace(token.AccessToken),
		Source:   "oauth",
		Metadata: map[string]string{
			"is_oauth": "true",
		},
	}
	if token.Expiry > 0 {
		requestBody.ExpiresAt = time.Unix(token.Expiry, 0).UTC().Format(time.RFC3339)
	}
	return requestBody, nil
}

func stageHostedProviderOAuthFiles(tempHome string, sourceHome string) error {
	if strings.TrimSpace(tempHome) == "" || strings.TrimSpace(sourceHome) == "" {
		return nil
	}
	// tempHome/sourceHome are arbitrary HOME roots (not the current process
	// home), so we mirror the canonical ~/.swarm layout by hand rather than via
	// the paths package. Relative locations match paths.OAuthFile/paths.In.
	sourceRoot := filepath.Join(sourceHome, ".swarm")
	targetRoot := filepath.Join(tempHome, ".swarm")
	relFiles := []string{
		filepath.Join("config", "oauth", "openai.json"),
		filepath.Join("config", "oauth", "anthropic.json"),
		"tui_accounts.json",
	}
	for _, rel := range relFiles {
		data, err := os.ReadFile(filepath.Join(sourceRoot, rel))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return err
		}
		targetPath := filepath.Join(targetRoot, rel)
		if err := os.MkdirAll(filepath.Dir(targetPath), 0o700); err != nil {
			return err
		}
		if err := atomicfile.Write(targetPath, data, atomicfile.AllowEmpty()); err != nil {
			return err
		}
	}
	return nil
}

func loadOpenAITokenFromTUIAccounts(ctx context.Context) error {
	tokenFile, err := loadTUIAccountFile()
	if err != nil {
		return err
	}
	for _, account := range preferredTUIAccounts(tokenFile, "openai") {
		var token openai.OAuthToken
		if err := json.Unmarshal(account.TokenData, &token); err != nil {
			continue
		}
		if strings.TrimSpace(token.APIKey) == "" && strings.TrimSpace(token.AccessToken) == "" {
			continue
		}
		if err := openai.StoreOAuthToken(&token); err != nil {
			return err
		}
		if _, err := openai.RefreshAndStoreToken(ctx); err == nil {
			return nil
		}
		return nil
	}
	return fmt.Errorf("openai oauth token not available")
}

func loadAnthropicTokenFromTUIAccounts() error {
	tokenFile, err := loadTUIAccountFile()
	if err != nil {
		return err
	}
	for _, account := range preferredTUIAccounts(tokenFile, "anthropic") {
		var token anthropic.OAuthToken
		if err := json.Unmarshal(account.TokenData, &token); err != nil {
			continue
		}
		if strings.TrimSpace(token.AccessToken) == "" {
			continue
		}
		return anthropic.StoreOAuthToken(&token)
	}
	return fmt.Errorf("anthropic oauth token not available")
}

func loadTUIAccountFile() (*tuiAccountFile, error) {
	data, err := os.ReadFile(paths.In("tui_accounts.json"))
	if err != nil {
		return nil, err
	}
	var file tuiAccountFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, err
	}
	if len(file.Accounts) == 0 {
		return nil, fmt.Errorf("no tui accounts found")
	}
	return &file, nil
}

func preferredTUIAccounts(file *tuiAccountFile, provider string) []struct {
	Provider  string          `json:"provider"`
	IsActive  bool            `json:"is_active"`
	AddedAt   int64           `json:"added_at"`
	TokenData json.RawMessage `json:"token_data"`
} {
	var active []struct {
		Provider  string          `json:"provider"`
		IsActive  bool            `json:"is_active"`
		AddedAt   int64           `json:"added_at"`
		TokenData json.RawMessage `json:"token_data"`
	}
	var inactive []struct {
		Provider  string          `json:"provider"`
		IsActive  bool            `json:"is_active"`
		AddedAt   int64           `json:"added_at"`
		TokenData json.RawMessage `json:"token_data"`
	}
	for _, account := range file.Accounts {
		if !strings.EqualFold(account.Provider, provider) {
			continue
		}
		if account.IsActive {
			active = append(active, account)
			continue
		}
		inactive = append(inactive, account)
	}
	return append(active, inactive...)
}
