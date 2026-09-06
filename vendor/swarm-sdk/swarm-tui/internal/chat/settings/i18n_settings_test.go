package settings

import (
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/commands"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

func TestGeneralLanguageSwitchesImmediatelyAndPersists(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	i18n.SetLanguage("en")
	t.Cleanup(func() { i18n.SetLanguage("en") })

	cm, err := commands.NewConfigManager()
	if err != nil {
		t.Fatalf("NewConfigManager: %v", err)
	}
	general := NewGeneralSettings(cm)
	general.SetLanguage("es")

	if got := i18n.CurrentLanguage(); got != i18n.LanguageSpanish {
		t.Fatalf("current language = %q, want es", got)
	}
	if got := general.GetLanguage(); got != i18n.LanguageSpanish {
		t.Fatalf("general language = %q, want es", got)
	}

	reloaded := NewGeneralSettings(cm)
	if got := reloaded.GetLanguage(); got != i18n.LanguageSpanish {
		t.Fatalf("reloaded language = %q, want persisted es", got)
	}
}

func TestGeneralSettingsRendersSpanishImmediately(t *testing.T) {
	i18n.SetLanguage("en")
	t.Cleanup(func() { i18n.SetLanguage("en") })

	general := NewGeneralSettings(nil)
	state := NewState()
	state.Focus = FocusContent

	english := general.Render(100, 48, state, testSettingsTheme())
	if !strings.Contains(english, "General Settings") || !strings.Contains(english, "Language") {
		t.Fatalf("English render missing expected labels:\n%s", english)
	}

	general.SetLanguage("es")
	spanish := general.Render(100, 48, state, testSettingsTheme())
	for _, want := range []string{"Configuración general", "Idioma", "Panel lateral"} {
		if !strings.Contains(spanish, want) {
			t.Fatalf("Spanish render missing %q:\n%s", want, spanish)
		}
	}
	if strings.Contains(spanish, "General Settings") {
		t.Fatalf("Spanish render retained English title:\n%s", spanish)
	}
}

func testSettingsTheme() Theme {
	return Theme{
		Primary:   "#ffffff",
		Accent:    "#ffffff",
		Text:      "#ffffff",
		TextDim:   "#aaaaaa",
		TextMuted: "#888888",
		BG:        "#000000",
		BGLight:   "#111111",
		BGLighter: "#222222",
		Border:    "#333333",
		Success:   "#00aa00",
		Warning:   "#aaaa00",
		Error:     "#aa0000",
	}
}
