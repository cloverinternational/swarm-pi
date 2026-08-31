//go:build cloud_integration

package cloudsync

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/cloud"
)

func integrationConfig() *cloud.CloudConfig {
	cfg := cloud.DefaultConfig()

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

func TestCloudIntegration_CloudSyncGetKeys_Smoke(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	refresh := requireRefreshToken(t)
	cfg := integrationConfig()

	tm, err := cloud.NewTokenManager()
	if err != nil {
		t.Fatalf("NewTokenManager: %v", err)
	}
	if err := tm.SaveTokens(&cloud.TokenSet{
		RefreshToken: refresh,
		TokenType:    "Bearer",
		ExpiresAt:    time.Now().Add(-2 * time.Hour).Unix(),
	}); err != nil {
		t.Fatalf("SaveTokens: %v", err)
	}

	client := NewAPIClient(cfg, tm, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	resp, err := client.GetKeys(ctx)
	if err != nil {
		t.Fatalf("GetKeys: %v", err)
	}
	if resp == nil || resp.Status == "" {
		t.Fatalf("unexpected keys response: %+v", resp)
	}
}
