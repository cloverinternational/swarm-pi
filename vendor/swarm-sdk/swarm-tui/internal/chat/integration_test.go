package chat

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/commands"
)

// TestModelRefreshIntegration is a comprehensive test that:
// 1. Sets up ~/.swarmos directory
// 2. Initializes providers.json with default providers
// 3. Calls RefreshOpenRouterModels to fetch from OpenRouter API (with fallback to models.dev)
// 4. Calls RefreshModelCapabilities to populate context windows
// 5. Loads and verifies the results
// 6. Checks if all providers have context windows populated
func TestModelRefreshIntegration(t *testing.T) {
	if os.Getenv("RUN_CHAT_INTEGRATION_TESTS") != "1" {
		t.Skip("Skipping chat integration test (writes ~/.swarmos, may call network); set RUN_CHAT_INTEGRATION_TESTS=1 to run")
	}

	// Get home directory
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("Failed to get home directory: %v", err)
	}

	swarmsDir := filepath.Join(home, ".swarmos")
	providersPath := filepath.Join(swarmsDir, "providers.json")

	// Ensure directory exists
	if err := os.MkdirAll(swarmsDir, 0755); err != nil {
		t.Fatalf("Failed to create .swarmos directory: %v", err)
	}

	// Step 1: Create initial providers.json with defaults
	t.Log("Step 1: Creating initial providers.json with defaults")
	configMgr, err := commands.NewConfigManager()
	if err != nil {
		t.Fatalf("Failed to create ConfigManager: %v", err)
	}

	providers, err := configMgr.LoadProviders()
	if err != nil {
		t.Fatalf("Failed to load providers: %v", err)
	}

	t.Logf("Loaded %d default providers", len(providers))
	for _, p := range providers {
		t.Logf("  - %s: %d models", p.DisplayName, len(p.Models))
	}

	// Step 2: Refresh OpenRouter models
	t.Log("\nStep 2: Refreshing OpenRouter models from API")
	orCount, orErr := RefreshOpenRouterModels()
	if orErr != nil {
		t.Logf("OpenRouter refresh failed: %v (trying models.dev fallback)", orErr)
		// Try explicit models.dev refresh
		devCount, devErr := RefreshFromModelsDev()
		if devErr != nil {
			t.Logf("Models.dev refresh also failed: %v", devErr)
			t.Logf("WARNING: Model refresh unavailable - continuing with test")
		} else {
			t.Logf("Successfully fetched %d models from models.dev", devCount)
		}
	} else {
		t.Logf("Successfully fetched %d models from OpenRouter API", orCount)
	}

	// Step 3: Refresh model capabilities (context windows)
	t.Log("\nStep 3: Refreshing model capabilities (context windows)")
	RefreshModelCapabilities()

	// Step 4: Load and verify providers.json
	t.Log("\nStep 4: Loading and verifying providers.json")
	data, err := os.ReadFile(providersPath)
	if err != nil {
		t.Fatalf("Failed to read providers.json: %v", err)
	}

	var providersConfig []map[string]any
	if err := json.Unmarshal(data, &providersConfig); err != nil {
		t.Fatalf("Failed to parse providers.json: %v", err)
	}

	t.Logf("Found %d providers in providers.json", len(providersConfig))

	// Step 5: Analyze each provider
	type ProviderStats struct {
		Name                 string
		ModelCount           int
		ModelsWithContext    int
		ModelsWithoutContext int
		Source               string
		LastRefreshed        string
	}

	var stats []ProviderStats

	for _, provider := range providersConfig {
		name, _ := provider["name"].(string)
		displayName, _ := provider["display_name"].(string)
		source, _ := provider["source"].(string)
		lastRefreshed, _ := provider["last_refreshed"].(string)

		models, ok := provider["models"].([]any)
		if !ok {
			continue
		}

		modelCount := len(models)
		withContext := 0
		withoutContext := 0

		for _, m := range models {
			modelMap, ok := m.(map[string]any)
			if !ok {
				continue
			}

			// Check for context_window field
			if cw, ok := modelMap["context_window"].(float64); ok && cw > 0 {
				withContext++
			} else {
				withoutContext++
			}
		}

		stat := ProviderStats{
			Name:                 name,
			ModelCount:           modelCount,
			ModelsWithContext:    withContext,
			ModelsWithoutContext: withoutContext,
			Source:               source,
			LastRefreshed:        lastRefreshed,
		}
		stats = append(stats, stat)

		t.Logf("\nProvider: %s (%s)", displayName, name)
		if source != "" {
			t.Logf("  Source: %s", source)
		}
		if lastRefreshed != "" {
			t.Logf("  Last Refreshed: %s", lastRefreshed)
		}
		t.Logf("  Models: %d total", modelCount)
		t.Logf("    - With context_window: %d", withContext)
		t.Logf("    - Without context_window: %d", withoutContext)

		// Show first 5 models
		if modelCount > 0 {
			t.Logf("  First 5 models:")
			for i := 0; i < 5 && i < modelCount; i++ {
				if m, ok := models[i].(map[string]any); ok {
					id, _ := m["id"].(string)
					displayName, _ := m["display_name"].(string)
					cw, hasContext := m["context_window"].(float64)
					context, _ := m["context"].(string)

					if hasContext {
						t.Logf("    - %s (%s): %d tokens", id, displayName, int(cw))
					} else if context != "" {
						t.Logf("    - %s (%s): %s (legacy format)", id, displayName, context)
					} else {
						t.Logf("    - %s (%s): NO CONTEXT", id, displayName)
					}
				}
			}
		}
	}

	// Step 6: Verify OpenRouter has been populated
	t.Log("\n" + strings.Repeat("=", 80))
	t.Log("VERIFICATION SUMMARY")
	t.Log(strings.Repeat("=", 80))

	openRouterFound := false
	for _, stat := range stats {
		if stat.Name == "OpenRouter" || stat.Name == "openrouter" {
			openRouterFound = true
			t.Logf("✓ OpenRouter: %d models (%d with context)", stat.ModelCount, stat.ModelsWithContext)
			if stat.Source != "" {
				t.Logf("  Source: %s", stat.Source)
			}
			if stat.ModelCount > 10 {
				t.Logf("✓ Models list populated (expected > 10, got %d)", stat.ModelCount)
			} else {
				t.Logf("✗ Models list NOT properly populated (expected > 10, got %d)", stat.ModelCount)
			}
		}
	}

	if !openRouterFound {
		t.Error("✗ OpenRouter provider not found in providers.json")
	}

	// Check OAuth providers for context windows
	t.Log("\nOAuth Providers Context Window Status:")
	oauthProviders := []string{"ClaudeCode", "Anthropic", "OpenAI", "Gemini"}
	for _, oauthName := range oauthProviders {
		for _, stat := range stats {
			if stat.Name == oauthName {
				if stat.ModelsWithContext > 0 {
					t.Logf("✓ %s: %d/%d models have context_window", oauthName, stat.ModelsWithContext, stat.ModelCount)
				} else {
					t.Logf("⚠ %s: 0/%d models have context_window", oauthName, stat.ModelCount)
				}
				break
			}
		}
	}

	// Write a detailed report
	reportPath := filepath.Join(swarmsDir, "model_refresh_report.json")
	report := map[string]any{
		"timestamp": os.Getenv("TIMESTAMP"),
		"stats":     stats,
	}
	reportData, _ := json.MarshalIndent(report, "", "  ")
	os.WriteFile(reportPath, reportData, 0644)
	t.Logf("\nDetailed report written to: %s", reportPath)
}

// TestFetchModelsDevAPI tests the models.dev API directly
func TestFetchModelsDevAPI(t *testing.T) {
	if os.Getenv("RUN_CHAT_INTEGRATION_TESTS") != "1" {
		t.Skip("Skipping chat integration test (may call network); set RUN_CHAT_INTEGRATION_TESTS=1 to run")
	}

	t.Log("Testing models.dev API fetch")

	models, err := FetchModelsDevModelsDetailed()
	if err != nil {
		t.Logf("Failed to fetch from models.dev: %v", err)
		t.Skip("models.dev API not available")
	}

	t.Logf("Successfully fetched %d models from models.dev", len(models))

	if len(models) == 0 {
		t.Error("No models returned from models.dev API")
		return
	}

	// Show first 10 models
	t.Log("First 10 models from models.dev:")
	for i := 0; i < 10 && i < len(models); i++ {
		m := models[i]
		t.Logf("  %d. %s (%s) - context: %d", i+1, m.Name, m.ID, m.ContextLength)
	}
}

// TestOpenRouterVsModelsDev compares the two APIs
func TestOpenRouterVsModelsDev(t *testing.T) {
	if os.Getenv("RUN_CHAT_INTEGRATION_TESTS") != "1" {
		t.Skip("Skipping chat integration test (may call network); set RUN_CHAT_INTEGRATION_TESTS=1 to run")
	}

	t.Log("Comparing OpenRouter vs models.dev APIs")

	// Fetch from OpenRouter
	t.Log("\n1. Fetching from OpenRouter...")
	orModels, orErr := FetchOpenRouterModelsDetailed("")
	if orErr != nil {
		t.Logf("OpenRouter error: %v", orErr)
	} else {
		t.Logf("OpenRouter: %d models", len(orModels))
	}

	// Fetch from models.dev
	t.Log("\n2. Fetching from models.dev...")
	devModels, devErr := FetchModelsDevModelsDetailed()
	if devErr != nil {
		t.Logf("models.dev error: %v", devErr)
	} else {
		t.Logf("models.dev: %d models", len(devModels))
	}

	// Compare
	if orErr == nil && devErr == nil {
		t.Logf("\nComparison:")
		t.Logf("  OpenRouter models: %d", len(orModels))
		t.Logf("  models.dev models: %d", len(devModels))
		t.Logf("  Difference: %d models", len(orModels)-len(devModels))
	}
}

// TestProviderConfigLoading tests loading provider config with ConfigManager
func TestProviderConfigLoading(t *testing.T) {
	if os.Getenv("RUN_CHAT_INTEGRATION_TESTS") != "1" {
		t.Skip("Skipping chat integration test (reads ~/.swarmos); set RUN_CHAT_INTEGRATION_TESTS=1 to run")
	}

	t.Log("Testing provider config loading")

	cm, err := commands.NewConfigManager()
	if err != nil {
		t.Fatalf("Failed to create ConfigManager: %v", err)
	}

	providers, err := cm.LoadProviders()
	if err != nil {
		t.Fatalf("Failed to load providers: %v", err)
	}

	t.Logf("Loaded %d providers", len(providers))

	for _, p := range providers {
		t.Logf("\nProvider: %s (%s)", p.DisplayName, p.Name)
		t.Logf("  Type: %s / %s", p.Type, p.APIType)
		t.Logf("  Available: %v", p.Available)
		t.Logf("  Models: %d", len(p.Models))

		// Check context windows
		modelsWithContext := 0
		for _, m := range p.Models {
			if m.ContextWindow > 0 {
				modelsWithContext++
			}
		}
		t.Logf("    - With context_window: %d", modelsWithContext)

		// Show first 3 models
		for i := 0; i < 3 && i < len(p.Models); i++ {
			m := p.Models[i]
			if m.ContextWindow > 0 {
				t.Logf("    - %s: %d tokens", m.ID, m.ContextWindow)
			} else if m.Context != "" {
				t.Logf("    - %s: %s (legacy)", m.ID, m.Context)
			} else {
				t.Logf("    - %s: NO CONTEXT", m.ID)
			}
		}
	}
}
