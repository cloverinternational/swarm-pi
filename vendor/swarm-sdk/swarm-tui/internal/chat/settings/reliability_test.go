package settings

import (
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/fallback"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/commands"
)

func testTheme() Theme {
	return Theme{
		Primary:   "#7c3aed",
		Accent:    "#22d3ee",
		Text:      "#e5e7eb",
		TextDim:   "#9ca3af",
		TextMuted: "#6b7280",
		BG:        "#111827",
		BGLight:   "#1f2937",
		BGLighter: "#374151",
		Border:    "#4b5563",
		Success:   "#10b981",
		Warning:   "#f59e0b",
		Error:     "#ef4444",
	}
}

func setTempHome(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	commands.InvalidateConfigCache()
	t.Cleanup(commands.InvalidateConfigCache)
}

func testConfigManager(t *testing.T) *commands.ConfigManager {
	t.Helper()
	setTempHome(t)
	cm, err := commands.NewConfigManager()
	if err != nil {
		t.Fatalf("NewConfigManager failed: %v", err)
	}
	return cm
}

func TestReliabilitySectionReachableAndRendersControls(t *testing.T) {
	setTempHome(t)

	mgr := NewManager(
		"ClaudeCode",
		"claude-opus-4-20250514",
		true, false, false, false, false,
		"dot",
		"chat",
		nil,
	)

	if len(Sections) == 0 {
		t.Fatal("sections should not be empty")
	}

	found := false
	for _, section := range Sections {
		if section.ID == SectionReliability {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("reliability section missing from sidebar metadata")
	}

	mgr.SetFocus(FocusSidebar)
	for i := 0; i < len(Sections); i++ {
		if mgr.GetState().SelectedSection == SectionReliability {
			break
		}
		_ = mgr.HandleKey("down", nil)
	}
	if mgr.GetState().SelectedSection != SectionReliability {
		t.Fatalf("failed to navigate to reliability section, got %v", mgr.GetState().SelectedSection)
	}

	mgr.SetFocus(FocusContent)
	view := mgr.Render(120, 40, testTheme(), 0)
	for _, needle := range []string{"Reliability", "Retry", "Fallback", "Rate Limits"} {
		if !strings.Contains(view, needle) {
			t.Fatalf("rendered reliability view missing %q", needle)
		}
	}
}

func TestReliabilityRetryEditsPersistAndReload(t *testing.T) {
	cm := testConfigManager(t)

	reliability := NewReliabilitySettings(cm)
	initial := reliability.GetRetrySettings()

	reliability.ToggleRetryEnabled()
	reliability.ToggleRotateOnRateLimit()
	reliability.AdjustMaxRetries(2)
	reliability.AdjustRetryAfterFallbackMs(1500)
	if err := reliability.Save(); err != nil {
		t.Fatalf("save reliability settings failed: %v", err)
	}

	reloaded := NewReliabilitySettings(cm)
	updated := reloaded.GetRetrySettings()

	if updated.Enabled == initial.Enabled {
		t.Fatalf("expected retry enabled to toggle from %v", initial.Enabled)
	}
	if updated.RotateOnRateLimit == initial.RotateOnRateLimit {
		t.Fatalf("expected rotate-on-rate-limit to toggle from %v", initial.RotateOnRateLimit)
	}
	if updated.MaxRetriesPerProvider != initial.MaxRetriesPerProvider+2 {
		t.Fatalf("expected max retries %d, got %d", initial.MaxRetriesPerProvider+2, updated.MaxRetriesPerProvider)
	}
	if updated.RetryAfterFallbackMs != initial.RetryAfterFallbackMs+1500 {
		t.Fatalf("expected retry-after-fallback %d, got %d", initial.RetryAfterFallbackMs+1500, updated.RetryAfterFallbackMs)
	}
}

func TestReliabilityFallbackChainPersistsAndReloads(t *testing.T) {
	cm := testConfigManager(t)

	reliability := NewReliabilitySettings(cm)
	chain := fallback.NewChain("OpenAI", "gpt-5.1")
	chain.AddFallback("Anthropic", "claude-sonnet-4-20250514")
	chain.AddFallback("Gemini", "gemini-2.5-flash")

	if err := reliability.SetFallbackChain(chain); err != nil {
		t.Fatalf("failed to update fallback chain: %v", err)
	}
	if err := reliability.Save(); err != nil {
		t.Fatalf("failed to save fallback chain: %v", err)
	}

	reloaded := NewReliabilitySettings(cm)
	loadedChain := reloaded.GetFallbackChain()
	if loadedChain == nil {
		t.Fatal("expected fallback chain to reload")
	}
	if loadedChain.Primary.Provider != "OpenAI" || loadedChain.Primary.Model != "gpt-5.1" {
		t.Fatalf("unexpected primary after reload: %s/%s", loadedChain.Primary.Provider, loadedChain.Primary.Model)
	}
	if len(loadedChain.Fallbacks) != 2 {
		t.Fatalf("expected 2 fallback entries, got %d", len(loadedChain.Fallbacks))
	}
}

func TestReliabilityRateLimitCRUDPersistsAndReloads(t *testing.T) {
	cm := testConfigManager(t)

	reliability := NewReliabilitySettings(cm)

	if err := reliability.AddRateLimit(commands.ModelRateLimitConfig{
		Provider:          "OpenAI",
		Model:             "gpt-5.1",
		RequestsPerMinute: 20,
		Enabled:           true,
		Burst:             2,
	}); err != nil {
		t.Fatalf("add rate limit failed: %v", err)
	}

	if err := reliability.AddRateLimit(commands.ModelRateLimitConfig{
		Provider:          "Anthropic",
		Model:             "claude-sonnet-4-20250514",
		RequestsPerMinute: 12,
		Enabled:           false,
		Burst:             1,
	}); err != nil {
		t.Fatalf("second add rate limit failed: %v", err)
	}

	if err := reliability.UpdateRateLimit(0, commands.ModelRateLimitConfig{
		Provider:          "OpenAI",
		Model:             "gpt-5.1",
		RequestsPerMinute: 25,
		Enabled:           true,
		Burst:             3,
	}); err != nil {
		t.Fatalf("update rate limit failed: %v", err)
	}

	if err := reliability.DeleteRateLimit(1); err != nil {
		t.Fatalf("delete rate limit failed: %v", err)
	}
	if err := reliability.Save(); err != nil {
		t.Fatalf("save rate limits failed: %v", err)
	}

	reloaded := NewReliabilitySettings(cm)
	limits := reloaded.GetModelRateLimits()
	if len(limits) != 1 {
		t.Fatalf("expected one rate limit after reload, got %d", len(limits))
	}
	if limits[0].Provider != "OpenAI" || limits[0].Model != "gpt-5.1" {
		t.Fatalf("unexpected provider/model after reload: %s/%s", limits[0].Provider, limits[0].Model)
	}
	if limits[0].RequestsPerMinute != 25 || limits[0].Burst != 3 {
		t.Fatalf("unexpected numeric values after reload: rpm=%d burst=%d", limits[0].RequestsPerMinute, limits[0].Burst)
	}
}

func TestReliabilityRateLimitEdgeCases(t *testing.T) {
	cm := testConfigManager(t)

	reliability := NewReliabilitySettings(cm)

	if err := reliability.DeleteRateLimit(0); err == nil {
		t.Fatal("expected delete from empty list to return error")
	}

	if err := reliability.AddRateLimit(commands.ModelRateLimitConfig{
		Provider:          "",
		Model:             "gpt-5.1",
		RequestsPerMinute: 10,
	}); err == nil {
		t.Fatal("expected provider validation error")
	}

	if err := reliability.AddRateLimit(commands.ModelRateLimitConfig{
		Provider:          "OpenAI",
		Model:             "",
		RequestsPerMinute: 10,
	}); err == nil {
		t.Fatal("expected model validation error")
	}

	if err := reliability.AddRateLimit(commands.ModelRateLimitConfig{
		Provider:          "OpenAI",
		Model:             "gpt-5.1",
		RequestsPerMinute: 0,
	}); err == nil {
		t.Fatal("expected rpm validation error")
	}

	if err := reliability.AddRateLimit(commands.ModelRateLimitConfig{
		Provider:          "OpenAI",
		Model:             "gpt-5.1",
		RequestsPerMinute: 5,
	}); err != nil {
		t.Fatalf("add valid limit failed: %v", err)
	}

	if err := reliability.DeleteRateLimit(0); err != nil {
		t.Fatalf("delete last item failed: %v", err)
	}
	if err := reliability.Save(); err != nil {
		t.Fatalf("save rate limit deletion failed: %v", err)
	}

	reloaded := NewReliabilitySettings(cm)
	limits := reloaded.GetModelRateLimits()
	if len(limits) != 0 {
		t.Fatalf("expected empty limits after deleting last item, got %d", len(limits))
	}
}
