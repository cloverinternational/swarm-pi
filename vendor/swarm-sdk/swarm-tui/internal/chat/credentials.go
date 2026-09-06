package chat

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider/credentials"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/vault"
)

// hasProviderCredentials checks whether we have any credentials for the given
// provider — the loose "can we drive this wire family at all" check used for
// startup auto-selection. Detection is delegated to the SDK's catalog-driven
// engine (env vars, OAuth token stores, credentials.json, account registries);
// providers.json api_key entries (where the settings UI stores keys) are the
// one TUI-owned store layered on top.
func hasProviderCredentials(providerName string) bool {
	if credentials.HasAnyForFamily(providerName) {
		return true
	}
	return hasAPIKeyInProviders(providerName)
}

// hasAPIKeyInProviders checks if providers.json has a non-empty api_key for the given provider name.
func hasAPIKeyInProviders(providerName string) bool {
	key, _ := getAPIKeyFromProviders(providerName)
	return key != ""
}

// getAPIKeyFromProviders reads the api_key (and base_url) from providers.json for the given provider.
func getAPIKeyFromProviders(providerName string) (apiKey, baseURL string) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", ""
	}
	data, err := os.ReadFile(filepath.Join(home, ".swarmos", "providers.json"))
	if err != nil {
		return "", ""
	}
	var providers []struct {
		Name            string `json:"name"`
		APIKey          string `json:"api_key"`
		APIKeySecretRef string `json:"api_key_secret_ref"`
		BaseURL         string `json:"base_url"`
	}
	if err := json.Unmarshal(data, &providers); err != nil {
		return "", ""
	}
	normalized := strings.ToLower(strings.TrimSpace(providerName))
	for _, p := range providers {
		if strings.ToLower(strings.TrimSpace(p.Name)) != normalized {
			continue
		}
		if p.APIKey != "" {
			return p.APIKey, p.BaseURL
		}
		if p.APIKeySecretRef != "" {
			provider := vault.GetDefaultVaultProvider()
			if provider != nil && provider.IsEnabled() && provider.GetVault() != nil {
				cred, err := provider.GetVault().ResolveCredential(context.Background(), p.APIKeySecretRef, provider.GetProjectID())
				if err == nil && cred != nil && !cred.IsExpired() {
					return cred.Secret, p.BaseURL
				}
			}
		}
		return "", p.BaseURL
	}
	return "", ""
}

func getOpenRouterAPIKey() string {
	var envKey string = os.Getenv("OPENROUTER_API_KEY")
	if envKey != "" {
		return envKey
	}

	var home string
	var err error
	home, err = os.UserHomeDir()
	if err != nil {
		return ""
	}

	var credsPath string = filepath.Join(home, ".swarmos", "credentials.json")
	var data []byte
	data, err = os.ReadFile(credsPath)
	if err != nil {
		return ""
	}

	var creds struct {
		Providers map[string]struct {
			APIKey string `json:"api_key"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(data, &creds); err != nil {
		return ""
	}

	for key, cred := range creds.Providers {
		if strings.EqualFold(key, "openrouter") && cred.APIKey != "" {
			return cred.APIKey
		}
	}

	return ""
}
