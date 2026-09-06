package client

import (
	"context"
	"os"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/paths"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/vault"
)

func TestReadProvidersJSONResolvesPlexusVaultReference(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SWARM_HOME", home)
	configDir := paths.Config()
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.ProvidersFile(), []byte(`[
		{"name":"plexus","display_name":"Plexus Gateway","base_url":"http://plexus.test/v1","api_key_secret_ref":"provider-plexus-api-key"}
	]`), 0o600); err != nil {
		t.Fatal(err)
	}

	const secret = "sk-plexus-vault-client-test"
	storage := vault.NewMemoryStorage()
	if err := storage.Store(context.Background(), vault.Credential{
		ID: "provider-plexus-api-key", Name: "Plexus API key",
		Kind: vault.CredentialKindAPIKey, Secret: secret, Scope: vault.ScopeGlobal,
	}); err != nil {
		t.Fatal(err)
	}
	v := vault.NewVault(storage, vault.VaultConfig{Enabled: true, DefaultMode: vault.ModeYOLO})
	executor := vault.NewExecutor(v, vault.VaultConfig{DefaultMode: vault.ModeYOLO}, nil)
	previous := vault.GetDefaultVaultProvider()
	vault.SetDefaultVaultProvider(vault.NewVaultProvider(executor, v, ""))
	defer vault.SetDefaultVaultProvider(previous)

	key, baseURL := readProvidersJSON("plexus")
	if key != secret {
		t.Fatal("client did not resolve the Vault-backed Plexus key")
	}
	if baseURL != "http://plexus.test/v1" {
		t.Fatalf("base URL = %q", baseURL)
	}
}

func TestPlexusBuiltinOwnsEndpointAndRetries(t *testing.T) {
	builtin, ok := provider.LookupBuiltinProvider("plexus")
	if !ok {
		t.Fatal("Plexus builtin provider is missing")
	}
	if got := defaultBaseURLFor("plexus"); got != builtin.BaseURL {
		t.Fatalf("default base URL = %q, builtin = %q", got, builtin.BaseURL)
	}
	if builtin.HTTPMaxRetries == nil || *builtin.HTTPMaxRetries != 0 {
		t.Fatalf("Plexus HTTPMaxRetries = %v, want 0", builtin.HTTPMaxRetries)
	}
}
