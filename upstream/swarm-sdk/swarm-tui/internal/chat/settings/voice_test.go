package settings

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/voice"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

func newTestVoiceSettings(t *testing.T) *VoiceSettings {
	t.Helper()
	return NewVoiceSettingsWithConfigPath(filepath.Join(t.TempDir(), "voice_config.json"))
}

func TestVoiceSectionIsDiscoverableInIntegrations(t *testing.T) {
	var found *SectionInfo
	for i := range Sections {
		if Sections[i].ID == SectionVoice {
			found = &Sections[i]
			break
		}
	}
	if found == nil {
		t.Fatal("voice settings section is not registered")
	}
	if found.Name != "Transcription" || found.Group != "Tools & Integrations" {
		t.Fatalf("voice section metadata = %#v", *found)
	}
}

func TestVoiceDefaultViewIsProgressiveAndMinimal(t *testing.T) {
	v := newTestVoiceSettings(t)
	view := v.View(72, 30, true)

	for _, want := range []string{
		"Transcription",
		"Local NVIDIA NeMo (managed)",
		"Status: Not configured",
		"Action:",
		"Set up local runtime",
		"Microphone:",
		"no microphones detected",
		"Advanced ▸",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("default view missing %q:\n%s", want, view)
		}
	}
	for _, hidden := range []string{"Server URL:", "API key:", "Runtime port:", "Language:"} {
		if strings.Contains(view, hidden) {
			t.Fatalf("default view unexpectedly exposes %q:\n%s", hidden, view)
		}
	}
}

func TestVoiceProviderPickerContainsRequiredChoices(t *testing.T) {
	v := newTestVoiceSettings(t)
	v.Update("enter") // provider is the first row
	view := v.View(80, 40, true)

	for _, want := range []string{
		"Local NVIDIA NeMo (managed)",
		"OpenAI-compatible / Custom",
		"Groq (Whisper)",
		"OpenAI (Whisper)",
		"OpenRouter",
		"Anthropic (streaming)",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("provider picker missing %q", want)
		}
	}
}

func TestVoiceProviderFieldsUseProgressiveDisclosure(t *testing.T) {
	v := newTestVoiceSettings(t)

	if cmd := v.selectOption(transcriptionOptions[1]); cmd != nil {
		v.HandleMsg(cmd())
	}
	custom := v.View(80, 30, true)
	for _, want := range []string{"Server URL:", "Model:", "API key (optional):"} {
		if !strings.Contains(custom, want) {
			t.Errorf("custom provider missing %q:\n%s", want, custom)
		}
	}

	if cmd := v.selectOption(transcriptionOptions[2]); cmd != nil {
		v.HandleMsg(cmd())
	}
	hosted := v.View(80, 30, true)
	if !strings.Contains(hosted, "API key:") || !strings.Contains(hosted, "not set") {
		t.Fatalf("hosted provider did not show required key:\n%s", hosted)
	}
	for _, hidden := range []string{"Server URL:", "Model:"} {
		if strings.Contains(hosted, hidden) {
			t.Fatalf("hosted provider unexpectedly exposed %q:\n%s", hidden, hosted)
		}
	}
}

func TestVoiceNavigationEditingMasksSecret(t *testing.T) {
	v := newTestVoiceSettings(t)
	v.SetSelectedProvider(voice.ProviderOpenAI)
	v.SetAPIKey(voice.ProviderOpenAI, "sk-super-secret-1234")

	v.Update("down") // primary action
	v.Update("down") // API key
	v.Update("enter")
	view := v.View(70, 24, true)

	if strings.Contains(view, "sk-super-secret-1234") || strings.Contains(view, "secret") {
		t.Fatalf("secret leaked while editing:\n%s", view)
	}
	if !strings.Contains(view, "••••") {
		t.Fatalf("editing view did not mask secret:\n%s", view)
	}
	v.Update("escape")
	if v.editing != editNone {
		t.Fatal("escape did not leave edit mode")
	}
}

func TestVoiceRuntimeUpdateAndPrimaryActionArePureState(t *testing.T) {
	v := newTestVoiceSettings(t)
	caps := VoiceRuntimeCapabilities{
		CanCheck: true, CanDownload: true, CanStart: true, ManagedRuntime: true,
	}
	v.ApplyRuntimeUpdate(VoiceRuntimeUpdate{
		Phase: VoicePhaseDownloading, Detail: "NeMo image", Progress: 0.42,
		ProgressLabel: "1.2 GB", Capabilities: caps,
	})

	if got := v.RuntimePhase(); got != VoicePhaseDownloading {
		t.Fatalf("RuntimePhase() = %v", got)
	}
	if got := v.RuntimeCapabilities(); got != caps {
		t.Fatalf("RuntimeCapabilities() = %#v", got)
	}
	label, enabled := v.PrimaryAction()
	if label != "Downloading model…" || enabled {
		t.Fatalf("PrimaryAction() = %q, %v", label, enabled)
	}
	view := v.View(64, 24, true)
	for _, want := range []string{"Downloading — NeMo image", "42%", "1.2 GB"} {
		if !strings.Contains(view, want) {
			t.Fatalf("runtime view missing %q:\n%s", want, view)
		}
	}
}

func TestVoicePrimaryActionEmitsIntegrationMessage(t *testing.T) {
	v := newTestVoiceSettings(t)
	v.cursor = 1
	cmd := v.Update("enter")
	if cmd == nil {
		t.Fatal("managed runtime action returned no command")
	}
	msg, ok := cmd().(VoiceRuntimeActionMsg)
	if !ok {
		t.Fatalf("primary action message type = %T", cmd())
	}
	if msg.Action != VoiceActionStartRuntime || !msg.Managed || msg.Provider != voice.ProviderOpenAICompatible {
		t.Fatalf("unexpected action message: %#v", msg)
	}
}

func TestVoiceInitHonorsManagedRuntimeAutoStart(t *testing.T) {
	v := newTestVoiceSettings(t)
	v.settings.Runtime.AutoStart = true
	batch, ok := v.Init()().(tea.BatchMsg)
	if !ok || len(batch) != 2 {
		t.Fatalf("Init() message = %T (%v), want device discovery plus auto-start batch", v.Init()(), batch)
	}
}

func TestVoiceRuntimePortUpdatesManagedEndpoint(t *testing.T) {
	v := newTestVoiceSettings(t)
	v.editing = editPort
	v.input = "9100"
	_ = v.commitEdit()
	if v.settings.Runtime.Port != 9100 || v.providerSettings().BaseURL != "http://127.0.0.1:9100" {
		t.Fatalf("port edit did not update runtime endpoint: runtime=%+v provider=%+v", v.settings.Runtime, v.providerSettings())
	}
}

func TestVoiceLegacyConfigMigrationAndSecureSave(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "voice_config.json")
	legacy := `{
  "selected_provider": "groq",
  "api_keys": {"groq": "legacy-secret"},
  "custom_urls": {"groq": "https://legacy.invalid"},
  "selected_device": "mic-7",
  "future_field": {"keep": true}
}`
	if err := os.WriteFile(path, []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}

	v := NewVoiceSettingsWithConfigPath(path)
	if v.SelectedProvider() != voice.ProviderGroq || v.APIKey(voice.ProviderGroq) != "legacy-secret" {
		t.Fatalf("legacy provider/key not migrated: provider=%q key=%q", v.SelectedProvider(), v.APIKey(voice.ProviderGroq))
	}
	if v.CustomURL(voice.ProviderGroq) != "https://legacy.invalid" || v.SelectedDevice() != "mic-7" {
		t.Fatal("legacy URL/device not migrated")
	}

	cmd := v.saveConfiguration()
	if cmd == nil {
		t.Fatal("saveConfiguration returned nil")
	}
	v.HandleMsg(cmd())
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("saved config mode = %o, want 600", got)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{`"version": 2`, `"providers"`, `"future_field"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("canonical save missing %s:\n%s", want, text)
		}
	}
	if strings.Contains(text, `"api_keys"`) || strings.Contains(text, `"custom_urls"`) {
		t.Fatalf("canonical save retained legacy keys:\n%s", text)
	}
}

func TestVoiceNarrowViewStaysWithinWidth(t *testing.T) {
	v := newTestVoiceSettings(t)
	v.Update("enter")
	view := v.View(20, 30, true)
	if strings.TrimSpace(view) == "" {
		t.Fatal("narrow view is empty")
	}
	for i, line := range strings.Split(view, "\n") {
		if got := lipgloss.Width(line); got > 20 {
			t.Fatalf("line %d width = %d, want <= 20: %q", i, got, line)
		}
	}
}

func TestVoiceAdvancedNavigationClampsCursor(t *testing.T) {
	v := newTestVoiceSettings(t)
	v.advancedOpen = true
	v.cursor = len(v.visibleRows()) - 1
	v.advancedOpen = false
	v.clampCursor()
	if v.cursor != len(v.visibleRows())-1 {
		t.Fatalf("cursor not clamped: cursor=%d rows=%d", v.cursor, len(v.visibleRows()))
	}
	for range 20 {
		v.Update("down")
	}
	if v.cursor != len(v.visibleRows())-1 {
		t.Fatalf("down navigation escaped rows: cursor=%d rows=%d", v.cursor, len(v.visibleRows()))
	}
}
