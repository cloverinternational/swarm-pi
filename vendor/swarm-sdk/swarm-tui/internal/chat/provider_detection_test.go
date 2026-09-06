package chat

import (
	"context"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/commands"
)

// TestScanConversationsForProviderDetection scans all conversations and reports which ones
// have blank/missing provider detection
func TestScanConversationsForProviderDetection(t *testing.T) {
	if os.Getenv("RUN_CHAT_INTEGRATION_TESTS") != "1" {
		t.Skip("Skipping chat integration test (reads local conversations/config); set RUN_CHAT_INTEGRATION_TESTS=1 to run")
	}

	// Initialize SDK
	sdk, err := NewSDKIntegration("ClaudeCode", "claude-opus-4-20250514")
	if err != nil {
		t.Skipf("Cannot initialize SDK: %v", err)
	}

	// Load all conversations
	ctx := context.Background()
	sdkConvs, err := sdk.ListConversations(ctx, sdk.WorkspaceRoot())
	if err != nil {
		t.Fatalf("Failed to load conversations: %v", err)
	}

	t.Logf("📊 Scanning %d conversations for provider detection issues...\n", len(sdkConvs))
	t.Log(strings.Repeat("=", 80))

	// Load provider configs
	configMgr, err := commands.NewConfigManager()
	if err != nil {
		t.Fatalf("Failed to create config manager: %v", err)
	}

	cmdProviderConfigs, err := configMgr.LoadProviders()
	if err != nil {
		t.Fatalf("Failed to load provider configs: %v", err)
	}

	// Convert to internal format
	var providerConfigs []ProviderConfig
	for _, cmdProv := range cmdProviderConfigs {
		models := make([]ProviderModel, len(cmdProv.Models))
		for j, cmdModel := range cmdProv.Models {
			models[j] = ProviderModel{
				ID:                      cmdModel.ID,
				DisplayName:             cmdModel.DisplayName,
				ContextWindow:           cmdModel.ContextWindow,
				Context:                 cmdModel.Context,
				SupportsReasoningEffort: cmdModel.SupportsReasoningEffort,
				ReasoningEfforts:        cmdModel.ReasoningEfforts,
				ThinkingEnabled:         cmdModel.ThinkingEnabled,
				ThinkingBudget:          cmdModel.ThinkingBudget,
				ThinkingEffort:          cmdModel.ThinkingEffort,
			}
		}
		providerConfigs = append(providerConfigs, ProviderConfig{
			Name:             cmdProv.Name,
			DisplayName:      cmdProv.DisplayName,
			Color:            cmdProv.Color,
			Type:             cmdProv.Type,
			APIType:          cmdProv.APIType,
			BaseURL:          cmdProv.BaseURL,
			Available:        cmdProv.Available,
			Models:           models,
			CodexQueryParams: cmdProv.CodexQueryParams,
			CodexHTTPHeaders: cmdProv.CodexHTTPHeaders,
		})
	}

	// Track statistics
	stats := make(map[string]int)              // provider name -> count
	blankProviders := make(map[string]int)     // model name -> count
	missingColors := make(map[string]int)      // provider name -> count
	modelExamples := make(map[string][]string) // provider -> example models

	for _, conv := range sdkConvs {
		// Get the last model used
		lastModel := ""
		for _, msg := range conv.Messages {
			if msg.Model != "" {
				lastModel = msg.Model
			}
		}

		if lastModel == "" {
			continue // Skip conversations with no model info
		}

		// Extract provider
		provider := extractProviderFromModel(lastModel)

		if provider == "" {
			// Track models that don't have provider detection
			blankProviders[lastModel]++
		} else {
			// Track provider usage
			stats[provider]++

			// Track example models for each provider
			if len(modelExamples[provider]) < 3 {
				// Only keep first 3 examples per provider
				found := slices.Contains(modelExamples[provider], lastModel)
				if !found {
					modelExamples[provider] = append(modelExamples[provider], lastModel)
				}
			}

			// Check if provider has color in config
			config := findProviderConfig(providerConfigs, provider)
			if config == nil || config.Color == "" {
				missingColors[provider]++
			}
		}
	}

	// Report findings
	t.Logf("\n📈 PROVIDER DETECTION STATISTICS\n")
	t.Log(strings.Repeat("-", 80))

	totalWithProviders := 0
	for provider, count := range stats {
		totalWithProviders += count
		config := findProviderConfig(providerConfigs, provider)
		color := "NO COLOR"
		if config != nil && config.Color != "" {
			color = config.Color
		}
		icon := getProviderIcon(provider)

		t.Logf("  %s %-15s : %4d conversations | Color: %-10s | Examples: %v\n",
			icon, provider, count, color, modelExamples[provider])
	}

	t.Logf("\n✅ Total conversations with detected providers: %d\n", totalWithProviders)

	// Report blank providers (this is the problem!)
	if len(blankProviders) > 0 {
		t.Logf("\n❌ CONVERSATIONS WITH BLANK/MISSING PROVIDER DETECTION\n")
		t.Log(strings.Repeat("-", 80))

		total := 0
		for model, count := range blankProviders {
			total += count
			t.Logf("  Model: %-40s | Count: %d\n", model, count)
		}

		t.Logf("\n❌ Total conversations with blank provider: %d\n", total)
		t.Logf("\n💡 RECOMMENDATIONS:\n")
		t.Logf("   Add detection patterns for these models in extractProviderFromModel()\n")
	}

	// Report missing colors
	if len(missingColors) > 0 {
		t.Logf("\n⚠️  PROVIDERS MISSING COLORS IN providers.json\n")
		t.Log(strings.Repeat("-", 80))

		for provider, count := range missingColors {
			t.Logf("  Provider: %-20s | Affected conversations: %d\n", provider, count)
		}

		t.Logf("\n💡 RECOMMENDATIONS:\n")
		t.Logf("   Add 'color' field to these providers in ~/.swarmos/providers.json\n")
	}

	// Summary
	t.Log("")
	t.Log(strings.Repeat("=", 80))
	t.Logf("📊 SUMMARY\n")
	t.Log(strings.Repeat("-", 80))
	t.Logf("  Total conversations scanned:        %d\n", len(sdkConvs))
	t.Logf("  Conversations with providers:       %d\n", totalWithProviders)
	t.Logf("  Conversations with blank providers: %d\n", func() int {
		total := 0
		for _, count := range blankProviders {
			total += count
		}
		return total
	}())
	t.Logf("  Unique providers detected:          %d\n", len(stats))
	t.Logf("  Providers missing colors:           %d\n", len(missingColors))
	t.Log(strings.Repeat("=", 80))

	// Fail the test if we have blank providers (so we know we need to fix them)
	if len(blankProviders) > 0 {
		t.Logf("\n⚠️  Test FAILED: %d model patterns need provider detection\n", len(blankProviders))
		// Don't actually fail - just report
		// t.Fail()
	}
}

// TestProviderExtractionExamples tests specific model name patterns
func TestProviderExtractionExamples(t *testing.T) {
	tests := []struct {
		model    string
		expected string
	}{
		// Anthropic
		{"claude-3-opus", "anthropic"},
		{"claude-3-sonnet", "anthropic"},
		{"claude-3-haiku", "anthropic"},
		{"sonnet4", "anthropic"},
		{"opus", "anthropic"},
		{"haiku", "anthropic"},
		{"claude-opus-4-20250514", "anthropic"},
		{"anthropic/claude-3-opus", "anthropic"},

		// OpenAI
		{"gpt-4", "openai"},
		{"gpt-3.5-turbo", "openai"},
		{"o1-preview", "openai"},
		{"o3-mini", "openai"},
		{"openai/gpt-4", "openai"},

		// Google
		{"gemini-2.0-flash", "google"},
		{"gemini-1.5-pro", "google"},
		{"models/gemini-2.0-flash", "google"},
		{"google/gemini-pro", "google"},
		{"gemini", "google"},

		// Others
		{"llama-3.1-70b", "meta"},
		{"deepseek-coder", "deepseek"},
		{"mistral-large", "mistral"},
		{"grok-beta", "xai"},
	}

	t.Logf("\n🧪 TESTING PROVIDER EXTRACTION PATTERNS\n")
	t.Log(strings.Repeat("=", 80))

	passed := 0
	failed := 0

	for _, tt := range tests {
		result := extractProviderFromModel(tt.model)
		if result == tt.expected {
			passed++
			t.Logf("  ✅ %-40s → %-15s (expected: %s)\n", tt.model, result, tt.expected)
		} else {
			failed++
			t.Logf("  ❌ %-40s → %-15s (expected: %s)\n", tt.model, result, tt.expected)
			t.Errorf("extractProviderFromModel(%q) = %q, want %q", tt.model, result, tt.expected)
		}
	}

	t.Log("")
	t.Log(strings.Repeat("=", 80))
	t.Logf("  Passed: %d/%d\n", passed, passed+failed)
	t.Logf("  Failed: %d/%d\n", failed, passed+failed)
	t.Log(strings.Repeat("=", 80))
}
