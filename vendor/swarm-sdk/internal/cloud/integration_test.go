//go:build cloud_integration

package cloud

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

var integrationRefreshTokenState struct {
	sync.Mutex
	value string
}

func integrationConfig() *CloudConfig {
	cfg := DefaultConfig()

	if v := os.Getenv("SWARM_CLOUD_API_BASE_URL"); v != "" {
		cfg.APIBaseURL = v
	}
	if v := os.Getenv("SWARM_CLOUD_API_FALLBACK_URL"); v != "" {
		cfg.APIFallbackURL = v
	}
	if v := os.Getenv("SWARM_CLOUD_HOSTED_UI_URL"); v != "" {
		cfg.HostedUIURL = v
	}
	if v := os.Getenv("SWARM_CLOUD_PUBLIC_CLIENT_ID"); v != "" {
		cfg.PublicClientID = v
	}
	if v := os.Getenv("SWARM_CLOUD_ISSUER_URL"); v != "" {
		cfg.AuthIssuerURL = v
	}
	if v := os.Getenv("SWARM_CLOUD_REGION"); v != "" {
		cfg.Region = v
	}

	return &cfg
}

func requireRefreshToken(t *testing.T) string {
	t.Helper()
	refresh := os.Getenv("SWARM_CLOUD_REFRESH_TOKEN")
	if refresh == "" {
		t.Skip("SWARM_CLOUD_REFRESH_TOKEN not set; skipping cloud integration test")
	}
	return refresh
}

func seedIntegrationTokens(
	t *testing.T,
	cfg *CloudConfig,
	tokenManager *TokenManager,
) {
	t.Helper()
	if cfg == nil {
		t.Fatal("cloud config is required")
	}
	if tokenManager == nil {
		t.Fatal("token manager is required")
	}

	integrationRefreshTokenState.Lock()
	refresh := integrationRefreshTokenState.value
	if refresh == "" {
		refresh = requireRefreshToken(t)
		integrationRefreshTokenState.value = refresh
	}
	integrationRefreshTokenState.Unlock()

	tokens, err := tokenManager.LoadTokens()
	if err != nil {
		t.Fatalf("LoadTokens: %v", err)
	}
	if tokens != nil && tokens.AccessToken != "" && !tokens.IsExpired() {
		return
	}
	if tokens != nil && tokens.RefreshToken != "" {
		refresh = tokens.RefreshToken
	}

	if err := tokenManager.SaveTokens(&TokenSet{
		RefreshToken: refresh,
		TokenType:    "Bearer",
		ExpiresAt:    time.Now().Add(-2 * time.Hour).Unix(),
	}); err != nil {
		t.Fatalf("SaveTokens: %v", err)
	}

	client := NewClient(cfg, tokenManager, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	tokens, err = client.RefreshTokens(ctx)
	if err != nil {
		t.Fatalf("RefreshTokens: %v", err)
	}
	if tokens == nil || tokens.RefreshToken == "" {
		t.Fatalf("expected rotated refresh token, got %+v", tokens)
	}

	integrationRefreshTokenState.Lock()
	integrationRefreshTokenState.value = tokens.RefreshToken
	integrationRefreshTokenState.Unlock()

	if err := persistIntegrationSourceTokens(tokens); err != nil {
		t.Fatalf("persistIntegrationSourceTokens: %v", err)
	}
}

func persistIntegrationSourceTokens(tokens *TokenSet) error {
	if tokens == nil {
		return nil
	}
	sourcePath := os.Getenv("SWARM_CLOUD_STAGE_SOURCE_PATH")
	if sourcePath == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(sourcePath), 0o700); err != nil {
		return err
	}
	encoded, err := json.Marshal(tokens)
	if err != nil {
		return err
	}
	return os.WriteFile(sourcePath, append(encoded, '\n'), 0o600)
}

func TestCloudIntegration_DeviceLinkStart_Smoke(t *testing.T) {
	cfg := integrationConfig()
	client := NewClient(cfg, nil, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	got, err := client.StartDeviceLink(ctx, DeviceLinkInfo{
		DeviceID:   "integration-test",
		DeviceName: "integration",
		App:        "tui",
		AppVersion: "0.7",
	})
	if err != nil {
		t.Fatalf("StartDeviceLink: %v", err)
	}
	if got == nil || got.DeviceCode == "" || got.UserCode == "" || got.VerificationURI == "" || got.VerificationURIComplete == "" {
		t.Fatalf("unexpected device link start result: %+v", got)
	}
}

func TestCloudIntegration_RefreshTokens_AndFetchCatalog_Smoke(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	refresh := requireRefreshToken(t)
	cfg := integrationConfig()

	tm, err := NewTokenManager()
	if err != nil {
		t.Fatalf("NewTokenManager: %v", err)
	}
	// Seed an expired token set that includes only the refresh token so the test must refresh.
	if err := tm.SaveTokens(&TokenSet{
		RefreshToken: refresh,
		TokenType:    "Bearer",
		ExpiresAt:    time.Now().Add(-2 * time.Hour).Unix(),
	}); err != nil {
		t.Fatalf("SaveTokens: %v", err)
	}

	client := NewClient(cfg, tm, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	tokens, err := client.RefreshTokens(ctx)
	if err != nil {
		t.Fatalf("RefreshTokens: %v", err)
	}
	if tokens == nil || tokens.AccessToken == "" || tokens.IDToken == "" || tokens.TokenType == "" || tokens.ExpiresAt <= time.Now().Unix() {
		t.Fatalf("unexpected refreshed tokens (redacted): expiresAt=%d tokenType=%q accessLen=%d idLen=%d", tokens.ExpiresAt, tokens.TokenType, len(tokens.AccessToken), len(tokens.IDToken))
	}

	result, err := client.FetchCatalog(ctx)
	if err != nil {
		t.Fatalf("FetchCatalog: %v", err)
	}
	if result == nil || result.Bytes <= 0 {
		t.Fatalf("unexpected catalog result: %+v", result)
	}
}

func TestCloudIntegration_SendTelemetry_Smoke(t *testing.T) {
	if os.Getenv("SWARM_CLOUD_ENABLE_TELEMETRY_SMOKE") != "1" {
		t.Skip("Set SWARM_CLOUD_ENABLE_TELEMETRY_SMOKE=1 to enable telemetry integration smoke test")
	}

	t.Setenv("HOME", t.TempDir())

	refresh := requireRefreshToken(t)
	cfg := integrationConfig()

	tm, err := NewTokenManager()
	if err != nil {
		t.Fatalf("NewTokenManager: %v", err)
	}
	if err := tm.SaveTokens(&TokenSet{
		RefreshToken: refresh,
		TokenType:    "Bearer",
		ExpiresAt:    time.Now().Add(-2 * time.Hour).Unix(),
	}); err != nil {
		t.Fatalf("SaveTokens: %v", err)
	}

	client := NewClient(cfg, tm, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	req := &TelemetryRequest{
		Events: []TelemetryEvent{{
			Name:       "catalog_fetch",
			Ts:         time.Now().Unix(),
			App:        "tui",
			AppVersion: "0.7",
			OS:         "mac",
			DeviceID:   "integration-test",
			DurationMS: 0,
			ErrorCode:  "unknown",
		}},
	}
	if err := client.SendTelemetry(ctx, req); err != nil {
		t.Fatalf("SendTelemetry: %v", err)
	}
}
