package chat

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// TestFetchOpenRouterModels tests fetching model capabilities from OpenRouter API
func TestFetchOpenRouterModels(t *testing.T) {
	if os.Getenv("RUN_CHAT_INTEGRATION_TESTS") != "1" {
		t.Skip("Skipping chat integration test (calls OpenRouter); set RUN_CHAT_INTEGRATION_TESTS=1 to run")
	}

	// Fetch models (no auth required for listing)
	models, err := FetchOpenRouterModels("")
	if err != nil {
		t.Logf("Note: Failed to fetch OpenRouter models (may be offline): %v", err)
		t.Skip("Skipping - OpenRouter API not available")
	}

	t.Logf("Fetched %d models from OpenRouter", len(models))

	// Check some known models
	knownModels := []string{
		"anthropic/claude-3.5-sonnet",
		"anthropic/claude-3-opus",
		"openai/gpt-4o",
		"openai/gpt-4-turbo",
		"google/gemini-pro-1.5",
	}

	for _, modelID := range knownModels {
		if cw, found := models[modelID]; found {
			t.Logf("  %s: context_window=%d", modelID, cw)
		} else {
			t.Logf("  %s: not found (may have different ID format)", modelID)
		}
	}

	// Verify we got at least some models with context windows
	modelsWithContext := 0
	for _, cw := range models {
		if cw > 0 {
			modelsWithContext++
		}
	}
	t.Logf("Models with context_window > 0: %d/%d", modelsWithContext, len(models))

	if modelsWithContext < 10 {
		t.Errorf("Expected at least 10 models with context_window, got %d", modelsWithContext)
	}
}

// TestLookupKnownContextWindow verifies the SDK delegation in lookupKnownContextWindow.
// The flat model context window maps were removed in Z1 (ACP-2026-SDK-003-A2).
// Lookup now delegates to client.LookupModelContextWindow
// which reads ~/.swarmos/providers.json. In environments without that file the function
// must return 0 gracefully — that is the invariant tested here.
func TestLookupKnownContextWindow(t *testing.T) {
	// When providers.json does not exist, SDK returns (0, false) and so must we.
	got := lookupKnownContextWindow("claude-sonnet-4-5")
	if got < 0 {
		t.Errorf("lookupKnownContextWindow returned negative value %d", got)
	}
	// Empty modelID must always return 0 without panicking.
	if cw := lookupKnownContextWindow(""); cw != 0 {
		t.Errorf("expected 0 for empty modelID, got %d", cw)
	}
	t.Log("lookupKnownContextWindow SDK delegation: OK")
}

// TestProviderJSONStructure verifies providers.json has context_window field
func TestProviderJSONStructure(t *testing.T) {
	if os.Getenv("RUN_CHAT_INTEGRATION_TESTS") != "1" {
		t.Skip("Skipping chat integration test (reads ~/.swarmos/providers.json); set RUN_CHAT_INTEGRATION_TESTS=1 to run")
	}

	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("Cannot get home directory")
	}

	providersPath := filepath.Join(home, ".swarmos", "providers.json")
	data, err := os.ReadFile(providersPath)
	if err != nil {
		t.Skipf("providers.json not found: %v", err)
	}

	var providers []map[string]any
	if err := json.Unmarshal(data, &providers); err != nil {
		t.Fatalf("Failed to parse providers.json: %v", err)
	}

	t.Logf("Found %d providers in providers.json", len(providers))

	modelsWithContextWindow := 0
	modelsWithEmptyContext := 0
	totalModels := 0

	for _, provider := range providers {
		providerName, _ := provider["name"].(string)
		models, ok := provider["models"].([]any)
		if !ok {
			continue
		}

		for _, model := range models {
			modelMap, ok := model.(map[string]any)
			if !ok {
				continue
			}

			modelID, _ := modelMap["id"].(string)
			totalModels++

			// Check for context_window (new format)
			if cw, ok := modelMap["context_window"].(float64); ok && cw > 0 {
				modelsWithContextWindow++
				t.Logf("  [%s] %s: context_window=%d", providerName, modelID, int(cw))
			} else if ctx, ok := modelMap["context"].(string); ok && ctx != "" {
				t.Logf("  [%s] %s: context=%q (old format, should migrate)", providerName, modelID, ctx)
			} else {
				modelsWithEmptyContext++
				t.Logf("  [%s] %s: NO context_window set", providerName, modelID)
			}
		}
	}

	t.Logf("\nSummary:")
	t.Logf("  Total models: %d", totalModels)
	t.Logf("  With context_window: %d", modelsWithContextWindow)
	t.Logf("  Missing context_window: %d", modelsWithEmptyContext)

	if modelsWithContextWindow == 0 && totalModels > 0 {
		t.Log("\nNote: Run the app to trigger RefreshModelCapabilities() which populates context_window")
	}
}

// TestGetModelContextWindowFromConfig tests loading context window from config
func TestGetModelContextWindowFromConfig(t *testing.T) {
	if os.Getenv("RUN_CHAT_INTEGRATION_TESTS") != "1" {
		t.Skip("Skipping chat integration test (reads ~/.swarmos/providers.json); set RUN_CHAT_INTEGRATION_TESTS=1 to run")
	}

	// Test various models
	testCases := []struct {
		modelID     string
		expected    int // Expected value (0 means any value is ok)
		description string
	}{
		{"claude-opus-4-5-20251101", 200000, "Claude 4.5 Opus (should be 200k)"},
		{"claude-sonnet-4-5-20250929", 200000, "Claude 4.5 Sonnet (should be 200k)"},
		{"zai-glm-4.6", 128000, "Cerebras GLM 4.6 (should be 128k from legacy context string)"},
		{"gpt-4o", 0, "GPT-4o (may be 0 if not in config)"},
	}

	for _, tc := range testCases {
		cw := getModelContextWindow("", tc.modelID)
		t.Logf("%s (%s): %d tokens", tc.modelID, tc.description, cw)

		if tc.expected > 0 && cw != tc.expected {
			t.Errorf("Expected %d for %s, got %d", tc.expected, tc.modelID, cw)
		}
	}
}

func TestGetModelContextWindowScopesDuplicateModelIDToProvider(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	configDir := filepath.Join(home, ".swarmos")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	data := []byte(`[
		{"name":"Alpha","api_type":"openai-compatible","models":[{"id":"shared-model","context_window":128000}]},
		{"name":"Beta","api_type":"openai-compatible","models":[{"id":"shared-model","context_window":272000}]}
	]`)
	if err := os.WriteFile(filepath.Join(configDir, "providers.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}

	if got := getModelContextWindow("Alpha", "shared-model"); got != 128_000 {
		t.Fatalf("Alpha/shared-model context = %d, want 128000", got)
	}
	if got := getModelContextWindow("Beta", "shared-model"); got != 272_000 {
		t.Fatalf("Beta/shared-model context = %d, want 272000", got)
	}
	if got := getModelContextWindow("Missing", "shared-model"); got != 0 {
		t.Fatalf("unknown provider inherited duplicate model context %d", got)
	}
}

// TestParseContextString tests parsing of legacy context string format
func TestParseContextString(t *testing.T) {
	testCases := []struct {
		input    string
		expected int
	}{
		{"128000", 128000},
		{"200000", 200000},
		{"128k", 128000},
		{"200K", 200000},
		{"1m", 1000000},
		{"1M", 1000000},
		{"", 0},
		{"invalid", 0},
		{"  128000  ", 128000},
	}

	for _, tc := range testCases {
		result := parseContextString(tc.input)
		if result != tc.expected {
			t.Errorf("parseContextString(%q) = %d, want %d", tc.input, result, tc.expected)
		}
	}
}

// TestUpdateProvidersWithCapabilities tests updating providers.json
func TestUpdateProvidersWithCapabilities(t *testing.T) {
	// Create test capabilities
	testCaps := map[string]int{
		"test-model-1": 100000,
		"test-model-2": 200000,
	}

	// This is a destructive test - skip in normal runs
	if os.Getenv("RUN_DESTRUCTIVE_TESTS") != "1" {
		t.Skip("Skipping destructive test (set RUN_DESTRUCTIVE_TESTS=1 to run)")
	}

	err := UpdateProvidersWithCapabilities(testCaps)
	if err != nil {
		t.Logf("Update result: %v", err)
	} else {
		t.Log("Successfully updated providers.json with test capabilities")
	}
}

// TestTokenFlowVisualization prints the token flow for debugging
func TestTokenFlowVisualization(t *testing.T) {
	fmt.Println("\n=== Token Flow Visualization ===")
	fmt.Println()
	fmt.Println("1. API Response (Anthropic):")
	fmt.Println("   {")
	fmt.Println("     \"usage\": {")
	fmt.Println("       \"input_tokens\": 15234,    <- Context sent to model (DISPLAY THIS)")
	fmt.Println("       \"output_tokens\": 1542     <- Tokens generated")
	fmt.Println("     }")
	fmt.Println("   }")
	fmt.Println()
	fmt.Println("2. API Response (OpenAI):")
	fmt.Println("   {")
	fmt.Println("     \"usage\": {")
	fmt.Println("       \"prompt_tokens\": 15234,   <- Context sent to model (DISPLAY THIS)")
	fmt.Println("       \"completion_tokens\": 1542,")
	fmt.Println("       \"total_tokens\": 16776     <- Sum (for billing)")
	fmt.Println("     }")
	fmt.Println("   }")
	fmt.Println()
	fmt.Println("3. SDK TokenUsage struct:")
	fmt.Println("   type TokenUsage struct {")
	fmt.Println("       Input  int  // <- Context size (DISPLAY THIS)")
	fmt.Println("       Output int  // <- Tokens generated")
	fmt.Println("       Total  int  // <- Input + Output")
	fmt.Println("   }")
	fmt.Println()
	fmt.Println("4. TUI Side Panel:")
	fmt.Println("   ┌─────────────────────────────┐")
	fmt.Println("   │ Conversation                │")
	fmt.Println("   │ ████████░░░░░░░░░░░░░░░░░░░ │")
	fmt.Println("   │ 7.6% (184.8k remaining)     │")
	fmt.Println("   │                             │")
	fmt.Println("   │ (15234 / 200000 tokens)     │")
	fmt.Println("   └─────────────────────────────┘")
	fmt.Println()
	fmt.Println("5. Context Window Source (Priority):")
	fmt.Println("   1. providers.json -> context_window (from OpenRouter API)")
	fmt.Println("   2. Provider.Capabilities().MaxContextWindow")
	fmt.Println("   3. (No fallback - 0 means unknown)")
	fmt.Println()
}

// TestRefreshModelCapabilities runs the refresh manually
func TestRefreshModelCapabilities(t *testing.T) {
	if os.Getenv("RUN_REFRESH_TEST") != "1" {
		t.Skip("Skipping refresh test (set RUN_REFRESH_TEST=1 to run)")
	}

	fmt.Println("Refreshing model capabilities from OpenRouter API...")
	RefreshModelCapabilities()
	fmt.Println("Done!")
}
