package anthropic

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeOAuthFile writes a minimal OAuth config to the canonical per-provider
// store (GetOAuthConfigPath() == ~/.swarm/config/oauth/anthropic.json). The
// caller isolates the root via t.Setenv("SWARM_HOME", home).
func writeOAuthFile(t *testing.T, home, accessToken string) string {
	t.Helper()
	_ = home // path derives from GetOAuthConfigPath()/SWARM_HOME
	p, err := GetOAuthConfigPath()
	if err != nil {
		t.Fatalf("GetOAuthConfigPath: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	cfg := OAuthConfig{
		Token: &OAuthToken{
			AccessToken:  accessToken,
			TokenType:    "Bearer",
			ExpiresIn:    3600,
			RefreshToken: "refresh-xyz",
			Scope:        "user:inference",
			Expiry:       time.Now().Unix() + 3600,
		},
		ModelProfiles: DefaultOAuthModels,
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	return p
}

// newManagedProvider builds an OAuth provider whose APIKey is an OAuth token so
// IsOAuth is true, without performing any network calls.
func newManagedProvider(t *testing.T, initialToken string) *Provider {
	t.Helper()
	p, err := New(Config{
		APIKey:  initialToken,
		IsOAuth: true,
		BaseURL: "https://example.invalid",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return p
}

func TestParseManagedEnv(t *testing.T) {
	for _, v := range []string{"1", "true", "TRUE", "yes", "on"} {
		if !parseManagedEnv(v) {
			t.Errorf("parseManagedEnv(%q) = false, want true", v)
		}
	}
	for _, v := range []string{"", "0", "false", "no", "off", "garbage"} {
		if parseManagedEnv(v) {
			t.Errorf("parseManagedEnv(%q) = true, want false", v)
		}
	}
}

// TestManagedReloadPicksUpRotatedToken verifies the core invariant: when the
// oauth.json on disk changes, a managed provider picks up the new access token
// on the next reload, and rebuilds its client header accordingly.
func TestManagedReloadPicksUpRotatedToken(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SWARM_HOME", home)
	setManagedModeForTest(true)
	defer setManagedModeForTest(false)

	writeOAuthFile(t, home, "sk-ant-oat-OLD")
	p := newManagedProvider(t, "sk-ant-oat-OLD")

	if !p.isManaged() {
		t.Fatal("provider should be managed")
	}

	// First reload: should load OLD from disk (matches initial, but populates cache).
	p.reloadManagedToken(context.Background(), false)
	if p.config.APIKey != "sk-ant-oat-OLD" {
		t.Fatalf("after first reload APIKey=%q, want OLD", p.config.APIKey)
	}

	// Central refresher rotates the token on disk.
	// Sleep a hair so the mtime is guaranteed to differ even on coarse clocks,
	// then rewrite.
	time.Sleep(10 * time.Millisecond)
	writeOAuthFile(t, home, "sk-ant-oat-NEW")

	// Non-forced reload must notice the mtime change and pick up NEW.
	p.reloadManagedToken(context.Background(), false)
	if p.config.APIKey != "sk-ant-oat-NEW" {
		t.Fatalf("after rotation reload APIKey=%q, want NEW", p.config.APIKey)
	}
}

// TestManagedReloadMtimeCacheSkips verifies the reload is a no-op (no client
// rebuild) when the file is unchanged: the cached access token is retained.
func TestManagedReloadMtimeCacheSkips(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SWARM_HOME", home)
	setManagedModeForTest(true)
	defer setManagedModeForTest(false)

	writeOAuthFile(t, home, "sk-ant-oat-A")
	p := newManagedProvider(t, "sk-ant-oat-A")

	p.reloadManagedToken(context.Background(), false)
	firstClient := p.client
	firstMtime := p.managedTokenMtime

	// Reload again without touching the file: mtime unchanged, client untouched.
	p.reloadManagedToken(context.Background(), false)
	if p.client != firstClient {
		t.Error("client was rebuilt despite unchanged oauth.json")
	}
	if p.managedTokenMtime != firstMtime {
		t.Error("mtime cache changed despite unchanged file")
	}
}

// TestManagedDoesNotWriteOAuthFile verifies a managed provider never mutates
// oauth.json on disk (it is a read-only consumer).
func TestManagedDoesNotWriteOAuthFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SWARM_HOME", home)
	setManagedModeForTest(true)
	defer setManagedModeForTest(false)

	path := writeOAuthFile(t, home, "sk-ant-oat-RO")
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	p := newManagedProvider(t, "sk-ant-oat-RO")
	p.reloadManagedToken(context.Background(), false)
	p.reloadManagedToken(context.Background(), true) // forced reload too

	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !before.ModTime().Equal(after.ModTime()) {
		t.Error("managed reload modified oauth.json mtime — must be read-only")
	}
}

// TestNonManagedReloadIsNoop verifies that when managed mode is OFF, the reload
// helper does nothing (does not read disk / change the token), preserving the
// byte-for-byte legacy behavior.
func TestNonManagedReloadIsNoop(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SWARM_HOME", home)
	setManagedModeForTest(false)

	writeOAuthFile(t, home, "sk-ant-oat-DISK")
	p := newManagedProvider(t, "sk-ant-oat-CONFIG")

	if p.isManaged() {
		t.Fatal("provider must NOT be managed when env disabled")
	}
	p.reloadManagedToken(context.Background(), false)
	if p.config.APIKey != "sk-ant-oat-CONFIG" {
		t.Fatalf("non-managed reload changed APIKey to %q; want unchanged CONFIG", p.config.APIKey)
	}
}
