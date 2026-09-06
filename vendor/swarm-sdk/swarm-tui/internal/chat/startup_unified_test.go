package chat

import (
	"math"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	tte "github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/tteengine"
)

func TestAsyncStartupBrandIsSelectedOnce(t *testing.T) {
	app := NewAsyncAppWithOptions(AppOptions{})
	selection := app.appOptions.startupBrand
	if selection == nil {
		t.Fatal("async startup did not preserve its brand selection in AppOptions")
	}
	if app.autovacMode != selection.autovac ||
		app.autovacLogoText != selection.logo ||
		app.autovacBrandName != selection.brand ||
		app.currentSplashText != selection.splash {
		t.Fatal("bootstrap shell branding does not match the selection passed to runtime construction")
	}
}

func TestBootstrapIntroTickAdvancesAnimation(t *testing.T) {
	t.Setenv(introSkipEnvVar, "0")
	app := NewAsyncAppWithOptions(AppOptions{})
	_ = app.startIntro()
	if app.introAnim == nil {
		t.Fatal("bootstrap did not initialize a TTE effect")
	}
	_, cmd := app.Update(introTickMsg{})
	if cmd == nil {
		t.Fatal("bootstrap intro tick did not schedule its successor")
	}
	if app.introFrame == "" {
		t.Fatal("bootstrap intro tick did not produce a frame")
	}
}

type completedIntroEffect struct{}

func (completedIntroEffect) Name() string         { return "completed-test-effect" }
func (completedIntroEffect) Init(*tte.Terminal)   {}
func (completedIntroEffect) Next() (string, bool) { return "done", true }
func (completedIntroEffect) Reset()               {}

func TestBootstrapIntroLoopsUntilRuntimeReady(t *testing.T) {
	app := NewAsyncAppWithOptions(AppOptions{})
	previous := &introAnimState{
		effect:   completedIntroEffect{},
		terminal: tte.NewTerminal(app.introLogoForWidth(app.width), tte.TerminalConfig{}),
	}
	app.introAnim = previous
	app.introComplete = false
	app.introDoneHold = finalHoldTicks - 1

	_, cmd := app.handleIntroTick()
	if cmd == nil {
		t.Fatal("completed bootstrap effect did not start another effect")
	}
	if app.introAnim == previous {
		t.Fatal("completed bootstrap effect was not replaced")
	}
	if app.introGlowing {
		t.Fatal("bootstrap entered settled breathing before runtime readiness")
	}
}

func TestBootstrapAnimationOnlyLayoutFitsTerminal(t *testing.T) {
	app := NewAsyncAppWithOptions(AppOptions{})
	app.width = 36
	app.height = 12

	content := app.View().Content
	lines := strings.Split(content, "\n")
	if len(lines) != app.height {
		t.Fatalf("rendered %d lines, want %d", len(lines), app.height)
	}
	for i, line := range lines {
		if got := lipgloss.Width(line); got > app.width {
			t.Fatalf("line %d width = %d, want <= %d: %q", i, got, app.width, line)
		}
	}
	for _, forbidden := range []string{
		"Runtime starting",
		"Resolving workspace",
		"Home   Chat",
		"Type now",
	} {
		if strings.Contains(content, forbidden) {
			t.Fatalf("animation-only bootstrap rendered %q: %q", forbidden, content)
		}
	}
	if app.textInput != nil && app.textInput.Value() != "" {
		t.Fatalf("bootstrap unexpectedly captured input: %q", app.textInput.Value())
	}

	_, _ = app.Update(tea.KeyPressMsg{Code: 'h', Text: "h"})
	if app.textInput != nil && app.textInput.Value() != "" {
		t.Fatalf("hidden bootstrap input captured a key: %q", app.textInput.Value())
	}
}

func TestIntroColorInterpolation(t *testing.T) {
	tests := []struct {
		name   string
		from   string
		to     string
		amount float64
		want   string
	}{
		{name: "start", from: "#000000", to: "#FFFFFF", amount: 0, want: "#000000"},
		{name: "middle", from: "#000000", to: "#FFFFFF", amount: 0.5, want: "#808080"},
		{name: "end", from: "#000000", to: "#FFFFFF", amount: 1, want: "#FFFFFF"},
		{name: "clamp low", from: "#123456", to: "#ABCDEF", amount: -1, want: "#123456"},
		{name: "clamp high", from: "#123456", to: "#ABCDEF", amount: 2, want: "#ABCDEF"},
		{name: "fallbacks", from: "", to: "not-a-color", amount: 0, want: "#4A457F"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := interpolateIntroColor(tt.from, tt.to, tt.amount); got != tt.want {
				t.Fatalf("interpolateIntroColor() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestIntroLogoUsesSelectedBrandAndFitsNarrowViewport(t *testing.T) {
	app := &App{
		autovacMode:      true,
		autovacLogoText:  autovacLogoText,
		autovacBrandName: "Multivac",
	}
	if got := app.introLogoForWidth(80); !strings.Contains(got, "___") || strings.Contains(got, "████") {
		t.Fatalf("wide AutoVac logo was not preserved: %q", got)
	}
	if got := app.introLogoForWidth(8); got != "MULTIVAC" {
		t.Fatalf("narrow AutoVac fallback = %q, want MULTIVAC", got)
	}
	if got := app.introLogoForWidth(4); lipgloss.Width(got) > 4 {
		t.Fatalf("tiny fallback width = %d, want <= 4: %q", lipgloss.Width(got), got)
	}
}

func TestSettledIntroFitsNarrowViewport(t *testing.T) {
	app := &App{
		theme:             AdaptThemeForTerminal(DefaultTheme, true),
		autovacLogoText:   introLogoText,
		autovacBrandName:  "Swarm",
		currentSplashText: strings.Repeat("long splash ", 20),
		introGlowing:      true,
	}
	const width, height = 20, 12
	content := app.renderGlowContent(width, height)
	lines := strings.Split(content, "\n")
	if len(lines) != height {
		t.Fatalf("rendered %d lines, want %d", len(lines), height)
	}
	for i, line := range lines {
		if got := lipgloss.Width(line); got > width {
			t.Fatalf("line %d width = %d, want <= %d: %q", i, got, width, line)
		}
	}
}

func TestSettledIntroUsesSlowBreathingTick(t *testing.T) {
	app := &App{
		screen:         ScreenChat,
		introGlowing:   true,
		introGlowPhase: 0,
	}
	model, cmd := app.handleGlowTick()
	if model != app {
		t.Fatal("handleGlowTick returned a different model")
	}
	if cmd == nil {
		t.Fatal("settled intro did not schedule its next low-frequency tick")
	}
	if math.Abs(app.introGlowPhase-glowPhaseStep) > 1e-9 {
		t.Fatalf("phase = %f, want %f", app.introGlowPhase, glowPhaseStep)
	}
	if !app.viewNeedsRefresh || !app.viewportContentDirty {
		t.Fatal("settled intro tick did not invalidate the empty chat frame")
	}
}

func TestSkipIntroEntersSettledState(t *testing.T) {
	t.Setenv(introSkipEnvVar, "1")
	app := &App{
		screen:        ScreenChat,
		introFrame:    introLogoText,
		introComplete: false,
	}
	cmd := app.startIntro()
	if cmd == nil {
		t.Fatal("skip-intro path did not start the settled breathing tick")
	}
	if app.introComplete {
		t.Fatal("skip-intro path hid the settled logo")
	}
	if !app.introGlowing {
		t.Fatal("skip-intro path did not enter the settled logo state")
	}
}
