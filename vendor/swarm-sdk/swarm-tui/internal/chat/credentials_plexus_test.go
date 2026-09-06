package chat

import (
	"context"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/vault"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/commands"
)

func TestGetAPIKeyFromProvidersResolvesVaultReference(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	const secret = "sk-plexus-vault-only"

	storage := vault.NewMemoryStorage()
	if err := storage.Store(context.Background(), vault.Credential{
		ID: "provider-plexus-api-key", Name: "Plexus API key",
		Kind: vault.CredentialKindAPIKey, Secret: secret, Scope: vault.ScopeGlobal,
	}); err != nil {
		t.Fatal(err)
	}
	v := vault.NewVault(storage, vault.VaultConfig{Enabled: true, DefaultMode: vault.ModeYOLO})
	executor := vault.NewExecutor(v, vault.VaultConfig{DefaultMode: vault.ModeYOLO}, nil)
	oldProvider := vault.GetDefaultVaultProvider()
	vault.SetDefaultVaultProvider(vault.NewVaultProvider(executor, v, ""))
	defer vault.SetDefaultVaultProvider(oldProvider)

	cm, err := commands.NewConfigManager()
	if err != nil {
		t.Fatal(err)
	}
	if err := cm.SaveProviders([]commands.ProviderConfig{{
		Name: "plexus", DisplayName: "Plexus Gateway", Type: "api_key",
		APIType: "openai-compatible", BaseURL: "http://plexus.test/v1",
		APIKeySecretRef: "provider-plexus-api-key",
	}}); err != nil {
		t.Fatal(err)
	}

	key, baseURL := getAPIKeyFromProviders("plexus")
	if key != secret {
		t.Fatalf("resolved key mismatch")
	}
	if baseURL != "http://plexus.test/v1" {
		t.Fatalf("base URL = %q", baseURL)
	}
}
