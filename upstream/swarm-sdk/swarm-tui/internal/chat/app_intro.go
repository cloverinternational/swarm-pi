package chat

import (
	"fmt"
	"math"
	"math/rand"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	tte "github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/tteengine"
	fx "github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/tteengine/effects"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/ui/palette"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/version"
)

// ── TTE debug logger ──────────────────────────────────────────────────────────
// Activated by SWARM_TTE_DEBUG=1.  Writes frame-by-frame telemetry to
// /tmp/tte-frames.log so you can tail -f it while the binary is running.

var (
	tteLogOnce sync.Once
	tteLogFile *os.File
	tteLogOn   bool
)

func tteLogInit() {
	tteLogOnce.Do(func() {
		if os.Getenv("SWARM_TTE_DEBUG") == "1" {
			f, err := os.OpenFile("/tmp/tte-frames.log",
				os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
			if err == nil {
				tteLogFile = f
				tteLogOn = true
			}
		}
	})
}

func tteLog(format string, args ...any) {
	tteLogInit()
	if !tteLogOn || tteLogFile == nil {
		return
	}
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintf(tteLogFile, "[%.3fs] %s\n", time.Since(tteLogStart).Seconds(), msg)
	_ = tteLogFile.Sync()
}

var tteLogStart = time.Now()

// introAnimState holds TTE engine state for the boot intro animation.
// Defined here so that app_types.go can reference *introAnimState without
// importing the TTE packages itself.
type introAnimState struct {
	effect   tte.Effect
	terminal *tte.Terminal
	// Timing for max duration constraint
	startTime time.Time
	maxTicks  int // Maximum ticks before forced completion
	tickCount int // Current tick count
}

// introLogoText is the Swarm ASCII art rendered by each TTE effect.
// 45 columns × 6 rows — auto-detected by TerminalConfig{}.
const introLogoText = `███████╗██╗    ██╗ █████╗ ██████╗ ███╗   ███╗
██╔════╝██║    ██║██╔══██╗██╔══██╗████╗ ████║
███████╗██║ █╗ ██║███████║██████╔╝██╔████╔██║
╚════██║██║███╗██║██╔══██║██╔══██╗██║╚██╔╝██║
███████║╚███╔███╔╝██║  ██║██║  ██║██║ ╚═╝ ██║
╚══════╝ ╚══╝╚══╝ ╚═╝  ╚═╝╚═╝  ╚═╝╚═╝     ╚═╝`

type startupBrandSelection struct {
	autovac bool
	logo    string
	brand   string
	splash  string
}

func selectStartupBrand() startupBrandSelection {
	if rand.Intn(10) == 0 {
		return startupBrandSelection{
			autovac: true,
			logo:    autovacLogoText,
			brand:   "Multivac",
			splash:  autovacSplashTexts[rand.Intn(len(autovacSplashTexts))],
		}
	}
	return startupBrandSelection{
		logo:   introLogoText,
		brand:  "Swarm",
		splash: splashTexts[rand.Intn(len(splashTexts))],
	}
}

// introLogoHeight is the fixed line count of introLogoText.
const introLogoHeight = 6

// introLogoWidth is the display-column width of the widest line in introLogoText.
const introLogoWidth = 45

// introSkipEnvVar allows CI and headless runners to bypass the intro entirely.
const introSkipEnvVar = "SWARMOS_SKIP_INTRO"

// interEffectHoldTicks is the number of 16 ms ticks to pause between effects.
const interEffectHoldTicks = 25 // ~0.4 s

// finalHoldTicks is the number of 16 ms ticks to show the completed final frame.
const finalHoldTicks = 48 // ~0.8 s

// glowTickInterval keeps the settled logo alive without continuously rebuilding
// the empty viewport at animation-frame cadence.
const glowTickInterval = 250 * time.Millisecond

const glowPhaseStep = math.Pi / 8 // one complete breath every four seconds

func introTickCmd() tea.Cmd {
	return tea.Tick(time.Second/60, func(time.Time) tea.Msg {
		return introTickMsg{}
	})
}

func glowTickCmd() tea.Cmd {
	return tea.Tick(glowTickInterval, func(time.Time) tea.Msg {
		return glowTickMsg{}
	})
}

// shouldSkipIntro returns true when the skip-intro env override is active.
func shouldSkipIntro() bool {
	v := os.Getenv(introSkipEnvVar)
	return v != "" && v != "0"
}

// ── Effect list ──────────────────────────────────────────────────────────────

// buildIntroEffects returns a randomly selected TTE effect for the boot animation.
// Uses theme colors 90% of the time, with rainbow colors as a rare 10% treat.
// Only one effect is returned (not a sequence) for a snappy startup.
func buildIntroEffects() []tte.Effect {
	// 10% chance for rainbow (rare treat), 90% theme colors
	useRainbow := rand.Intn(10) == 0

	type effectFactory func() tte.Effect
	var factories []effectFactory

	if useRainbow {
		// Rainbow scattered effect — the classic colorful burst
		factories = []effectFactory{
			func() tte.Effect {
				cfg := fx.DefaultScatteredConfig()
				cfg.ScatterSpeed = 0.6
				cfg.LandSpeed = 0.5
				cfg.FinalGradientStops = []tte.Color{
					tte.MustColorFromHex("FF0080"), // hot pink
					tte.MustColorFromHex("FF6600"), // orange
					tte.MustColorFromHex("FFD700"), // gold
					tte.MustColorFromHex("00FF88"), // mint
					tte.MustColorFromHex("00D1FF"), // cyan
					tte.MustColorFromHex("8A00FF"), // violet
				}
				cfg.FinalGradientDirection = tte.GradientDiagonal
				return fx.NewScatteredEffect(cfg)
			},
		}
	} else {
		// Theme-colored effects — coordinated with the UI palette
		primaryColor := tte.MustColorFromHex(string(palette.Accent))
		secondaryColor := tte.MustColorFromHex(string(palette.AccentSoft))
		accentColor := tte.MustColorFromHex(string(palette.Teal))
		infoColor := tte.MustColorFromHex(string(palette.Info))
		successColor := tte.MustColorFromHex(string(palette.Success))

		factories = []effectFactory{
			// Beams — light beams sweep across the logo
			func() tte.Effect {
				cfg := fx.DefaultBeamsConfig()
				cfg.FinalGradientStops = []tte.Color{primaryColor, accentColor, infoColor}
				cfg.FinalGradientDirection = tte.GradientDiagonal
				return fx.NewBeamsEffect(cfg)
			},
			// Decrypt — characters scramble then lock in
			func() tte.Effect {
				cfg := fx.DefaultDecryptConfig()
				cfg.FinalGradientStops = []tte.Color{primaryColor, secondaryColor, accentColor}
				cfg.FinalGradientDir = tte.GradientDiagonal
				return fx.NewDecryptEffect(cfg)
			},
			// Slide — logo slides in from the side
			func() tte.Effect {
				cfg := fx.DefaultSlideConfig()
				cfg.FinalGradientStops = []tte.Color{primaryColor, accentColor}
				cfg.FinalGradientDirection = tte.GradientHorizontal
				return fx.NewSlideEffect(cfg)
			},
			// Wipe — a clean horizontal wipe reveal
			func() tte.Effect {
				cfg := fx.DefaultWipeConfig()
				cfg.FinalGradientStops = []tte.Color{accentColor, successColor}
				cfg.FinalGradientDirection = tte.GradientHorizontal
				return fx.NewWipeEffect(cfg)
			},
			// Print — typewriter-style character-by-character print
			func() tte.Effect {
				cfg := fx.DefaultPrintConfig()
				cfg.FinalGradientStops = []tte.Color{primaryColor, secondaryColor, accentColor}
				cfg.FinalGradientDirection = tte.GradientDiagonal
				return fx.NewPrintEffect(cfg)
			},
			// Expand — text expands outward from center
			func() tte.Effect {
				cfg := fx.DefaultExpandConfig()
				cfg.FinalGradientStops = []tte.Color{primaryColor, accentColor}
				cfg.FinalGradientDirection = tte.GradientRadial
				return fx.NewExpandEffect(cfg)
			},
			// MiddleOut — reveals from the center out
			func() tte.Effect {
				cfg := fx.DefaultMiddleOutConfig()
				cfg.FinalGradientStops = []tte.Color{accentColor, primaryColor, infoColor}
				cfg.FinalGradientDirection = tte.GradientDiagonal
				return fx.NewMiddleOutEffect(cfg)
			},
			// Pour — characters pour in from top
			func() tte.Effect {
				cfg := fx.DefaultPourConfig()
				cfg.FinalGradientStops = []tte.Color{primaryColor, secondaryColor}
				cfg.FinalGradientDirection = tte.GradientVertical
				return fx.NewPourEffect(cfg)
			},
			// SynthGrid — grid blocks dissolve in
			func() tte.Effect {
				cfg := fx.DefaultSynthGridConfig()
				cfg.FinalGradientStops = []tte.Color{primaryColor, accentColor, successColor}
				cfg.FinalGradientDirection = tte.GradientDiagonal
				return fx.NewSynthGridEffect(cfg)
			},
		}
	}

	// Select one random effect from the appropriate pool
	selected := factories[rand.Intn(len(factories))]()
	return []tte.Effect{selected}
}

// ── Canvas helpers ────────────────────────────────────────────────────────────

// chatCanvasSize returns the stable usable W×H for the full-screen chat
// animation canvas.
//
// We derive dimensions directly from a.width/a.height using the same math as
// the WindowSizeMsg handler, rather than reading a.msgViewport.Width/Height.
// The viewport fields are temporarily overwritten during View() to account for
// the side panel, so they are not safe to use here — reading them from a tick
// callback (which runs in Update, interleaved with View) would produce
// unpredictable canvas sizes that trigger false reinits.
func (a *App) chatCanvasSize() (w, h int) {
	// Mirror the centralized ScreenChat layout logic exactly.
	layout := a.chatAreaLayout()
	w = layout.ViewportWidth
	h = layout.ViewportHeight

	if w < 1 {
		w = 1
	}
	if h < 1 {
		h = 1
	}
	return
}

func (a *App) introLogoForWidth(width int) string {
	logo := strings.Trim(a.autovacLogoText, "\n")
	if logo == "" {
		logo = introLogoText
	}
	if width > 0 && lipgloss.Width(logo) > width {
		brand := strings.ToUpper(strings.TrimSpace(a.autovacBrandName))
		if brand == "" {
			brand = "SWARM"
		}
		return ansi.Truncate(brand, width, "")
	}
	return logo
}

// buildChatLogoText returns the logo text padded with blank lines above so
// the logo sits vertically centered inside a vpH-row canvas.
// Horizontal centering is handled via TerminalConfig.TextStartCol — no space
// characters are added here, so effects only animate actual logo glyphs.
func buildChatLogoText(vpH int, logoText string) string {
	logoHeight := strings.Count(logoText, "\n") + 1
	topPad := (vpH - logoHeight) / 2
	if topPad < 0 {
		topPad = 0
	}
	var sb strings.Builder
	for i := 0; i < topPad; i++ {
		sb.WriteByte('\n')
	}
	sb.WriteString(logoText)
	return sb.String()
}

// startIntro is called from Init(). It starts the first effect in the cycle.
func (a *App) startIntro() tea.Cmd {
	tteLog("startIntro called — skipIntro=%v", shouldSkipIntro())
	a.introComplete = false
	a.introFromBootstrap = a.bootstrapPending
	if shouldSkipIntro() {
		tteLog("startIntro: skip env set, entering settled breathing state")
		a.introGlowing = true
		a.introGlowPhase = 0
		a.viewNeedsRefresh = true
		if a.screen == ScreenChat {
			a.viewportContentDirty = true
		}
		return glowTickCmd()
	}
	a.introGlowing = false
	a.introEffectIdx = 0
	return a.startEffectAt(0)
}

// dismissIntro stops the idle breathing loop once real conversation content is
// shown. Leaving the glow active on a loaded history conversation dirties the
// viewport on every tick, forcing every persisted message through a full rebuild
// until the user sends their first message.
func (a *App) dismissIntro() {
	if a.introComplete && !a.introGlowing {
		return
	}
	a.introComplete = true
	a.introGlowing = false
	a.viewNeedsRefresh = true
}

// startEffectAt initialises the TTE terminal + the effect at index idx,
// resets the hold counter, and returns the first introTickMsg command.
func (a *App) startEffectAt(idx int) tea.Cmd {
	effects := buildIntroEffects()
	tteLog("startEffectAt idx=%d totalEffects=%d", idx, len(effects))
	if idx >= len(effects) {
		// Past the end — animation complete
		tteLog("startEffectAt: idx past end — marking introComplete=true")
		a.introComplete = true
		return tea.Tick(time.Millisecond*16, func(t time.Time) tea.Msg {
			return menuTickMsg{}
		})
	}

	effect := effects[idx]

	// During asynchronous bootstrap, keep the effect on a logo-local canvas.
	// viewBootstrap centers that canvas in the full terminal, so the effect does
	// not depend on chat layout state that the lightweight shell does not have.
	//
	// In chat mode: give the terminal the full viewport canvas so particles
	// can scatter across the entire message area.  TextStartCol centers the
	// logo horizontally without adding space EffectCharacters (which would
	// get animated and distort effects like Rain and Scatter).
	// In home/advanced mode: auto-size from the logo text (45×6).
	var inputText string
	var cfg tte.TerminalConfig
	logoText := a.introLogoForWidth(0)
	if a.bootstrapPending {
		logoText = a.introLogoForWidth(a.width)
		inputText = logoText
		cfg = tte.TerminalConfig{}
	} else if a.screen == ScreenChat {
		vpW, vpH := a.chatCanvasSize()
		logoText = a.introLogoForWidth(vpW)
		logoWidth := lipgloss.Width(logoText)
		inputText = buildChatLogoText(vpH, logoText)
		textStartCol := (vpW-logoWidth)/2 + 1
		if textStartCol < 1 {
			textStartCol = 1
		}
		cfg = tte.TerminalConfig{
			CanvasWidth:  vpW,
			CanvasHeight: vpH,
			TextStartCol: textStartCol,
		}
	} else {
		inputText = logoText
		cfg = tte.TerminalConfig{} // auto-size from logo text
	}

	terminal := tte.NewTerminal(inputText, cfg)
	effect.Init(terminal)

	tteLog("startEffectAt: effect[%d]=%q canvas=%dx%d chatMode=%v",
		idx, effect.Name(),
		terminal.Canvas.Right, terminal.Canvas.Top,
		a.screen == ScreenChat)

	a.introAnim = &introAnimState{effect: effect, terminal: terminal}
	a.introDoneHold = 0
	a.viewNeedsRefresh = true
	if a.screen == ScreenChat {
		a.viewportContentDirty = true
	}

	return introTickCmd()
}

// handleIntroTick advances the current TTE effect by one frame.
// On completion it counts a brief inter-effect hold, then moves to the next
// effect. After the last effect's hold, it fires menuTickMsg to enter the app.
func (a *App) handleIntroTick() (tea.Model, tea.Cmd) {
	if a.introComplete || a.introAnim == nil {
		tteLog("handleIntroTick: bailing — introComplete=%v introAnim==nil=%v",
			a.introComplete, a.introAnim == nil)
		return a, nil
	}

	// Chat mode: the viewport is sized by renderChatContent before this tick
	// fires.  Reinit the TTE terminal whenever the canvas is substantially
	// out of sync with the actual viewport dimensions — in either direction.
	//
	// Two failure modes we guard against:
	//   • Canvas too small (< 80% of viewport): stub/logo-size canvas was used
	//     for the first frame before the real viewport size was known.
	//   • Canvas too large (> 110% of viewport): the side panel appeared or the
	//     terminal was resized narrower mid-animation, so the canvas is wider
	//     than the available area, causing frames to overflow into the side panel.
	//
	// The 80%/110% thresholds create a ±10–20% stable zone that absorbs the
	// ±1-row/col jitter that naturally occurs frame-to-frame without triggering
	// spurious reinits.
	if a.screen == ScreenChat && !a.introFromBootstrap {
		vpW, vpH := a.chatCanvasSize()
		canvas := a.introAnim.terminal.Canvas.Canvas
		tooSmall := canvas.Right < vpW*8/10 || canvas.Top < vpH*8/10
		tooLarge := canvas.Right > vpW*11/10 || canvas.Top > vpH*11/10
		if vpW > 0 && vpH > 0 && (tooSmall || tooLarge) {
			tteLog("handleIntroTick: canvas mismatch %dx%d 	 target %dx%d (tooSmall=%v tooLarge=%v), reiniting effect[%d]",
				canvas.Right, canvas.Top, vpW, vpH, tooSmall, tooLarge, a.introEffectIdx)
			return a, a.startEffectAt(a.introEffectIdx)
		}
	}

	frame, done := a.introAnim.effect.Next()
	a.introFrame = frame
	a.viewNeedsRefresh = true
	// In simple/chat mode the viewport is the animation canvas — dirty it every frame.
	if a.screen == ScreenChat {
		a.viewportContentDirty = true
	}

	// Frame telemetry: log every 6th frame (~100 ms intervals) to keep the
	// file readable, plus the first frame and every done/transition event.
	// buildIntroEffects returns exactly one randomly selected effect. Avoid
	// selecting and discarding another random effect merely to compute its length.
	isLast := true

	// Count ticks per effect using introDoneHold as a proxy before done, or
	// a separate counter — here we just log periodically.
	frameLen := strings.Count(frame, "\n") + 1
	frameBytes := len(frame)
	tteLog("TICK effectIdx=%d done=%v frameLines=%d frameBytes=%d hold=%d",
		a.introEffectIdx, done, frameLen, frameBytes, a.introDoneHold)

	if done {
		// Advance the hold counter
		a.introDoneHold++
		holdLimit := interEffectHoldTicks
		if isLast {
			holdLimit = finalHoldTicks
		}

		tteLog("DONE effectIdx=%d hold=%d/%d isLast=%v",
			a.introEffectIdx, a.introDoneHold, holdLimit, isLast)

		if a.introDoneHold >= holdLimit {
			if isLast {
				if a.bootstrapPending {
					tteLog("LOOP: runtime still pending — starting another intro effect")
					a.introEffectIdx = 0
					return a, a.startEffectAt(0)
				}
				tteLog("COMPLETE: final effect hold expired — entering breathing mode")
				a.introGlowing = true
				a.introFromBootstrap = false
				a.introGlowPhase = 0
				a.viewNeedsRefresh = true
				if a.screen == ScreenChat {
					a.viewportContentDirty = true
				}
				return a, glowTickCmd()
			}
			// Advance to the next effect
			a.introEffectIdx++
			tteLog("NEXT: advancing to effectIdx=%d", a.introEffectIdx)
			return a, a.startEffectAt(a.introEffectIdx)
		}

		// Still in hold — keep ticking at 16 ms
		return a, introTickCmd()
	}

	// Effect still running — tick at 60 fps
	return a, introTickCmd()
}

// ── View helpers ─────────────────────────────────────────────────────────────

// handleGlowTick advances the settled logo's slow breathing phase.
// It fires at ~4 fps after the reveal completes.
// The tick loop stops as soon as introComplete is set (on first message send).
func (a *App) handleGlowTick() (tea.Model, tea.Cmd) {
	if a.introComplete || !a.introGlowing {
		return a, nil
	}
	a.introGlowPhase += glowPhaseStep
	a.viewNeedsRefresh = true
	if a.screen == ScreenChat {
		a.viewportContentDirty = true
	}
	return a, glowTickCmd()
}

func parseIntroHexColor(value string, fallback [3]uint8) [3]uint8 {
	value = strings.TrimPrefix(strings.TrimSpace(value), "#")
	if len(value) != 6 {
		return fallback
	}
	parsed, err := strconv.ParseUint(value, 16, 24)
	if err != nil {
		return fallback
	}
	return [3]uint8{
		uint8(parsed >> 16),
		uint8(parsed >> 8),
		uint8(parsed),
	}
}

func interpolateIntroColor(from, to string, amount float64) string {
	if amount < 0 {
		amount = 0
	}
	if amount > 1 {
		amount = 1
	}
	fromRGB := parseIntroHexColor(from, [3]uint8{0x4A, 0x45, 0x7F})
	toRGB := parseIntroHexColor(to, [3]uint8{0x8B, 0x7C, 0xFF})
	mix := func(start, end uint8) uint8 {
		return uint8(math.Round(float64(start) + amount*(float64(end)-float64(start))))
	}
	return fmt.Sprintf("#%02X%02X%02X",
		mix(fromRGB[0], toRGB[0]),
		mix(fromRGB[1], toRGB[1]),
		mix(fromRGB[2], toRGB[2]),
	)
}

// splashTexts are short splash lines shown under the logo.
// Calm, present-tense status lines and taglines with a light, kind touch —
// plus a quiet nod to Asimov's "The Last Question." No competitor jabs.
var splashTexts = []string{
	// Asimov's Last Question references — kept as a quiet nod
	"THERE IS AS YET INSUFFICIENT DATA FOR A MEANINGFUL ANSWER",
	"LET THERE BE LIGHT!",
	"Can entropy be reversed?",
	"A trillion years and everything will be dark",
	// What the swarm does — calm, present-tense
	"Reading the codebase…",
	"Building context…",
	"Orchestrating agents…",
	"Planning the next steps…",
	"Reasoning through it…",
	"Wiring up the tools…",
	"Delegating to subagents…",
	"Indexing your workspace…",
	"Tracing the call graph…",
	"Resolving dependencies…",
	"Synthesizing an answer…",
	"Consulting the swarm…",
	"Threading the context…",
	"Reviewing the diff…",
	"Mapping the dependency tree…",
	"Following the imports…",
	"Gathering the details…",
	"Checking the tests…",
	"Sketching an approach…",
	"Lining up the changes…",
	// Multi-agent, framed as strength not snark
	"Many agents. One workspace.",
	"Thinking in parallel…",
	"Agents working together…",
	"Handing off between agents…",
	"Coordinating the swarm…",
	"Sharing context across agents…",
	"Splitting the work…",
	"Regrouping the findings…",
	// Tool-native, without the boasting
	"Context-aware. Tool-native.",
	"Calling the right tool…",
	"Searching the repository…",
	"Editing with care…",
	"Running it to be sure…",
	"Keeping the diff small…",
	"Reading before writing…",
	"Measuring twice…",
	// Small, kind delights
	"Warming up the swarm…",
	"Connecting the dots…",
	"Following the threads…",
	"Lining up the pieces…",
	"Getting our bearings…",
	"Turning ideas into commits…",
	"Almost there…",
	"On it.",
	// Taglines
	"Agentic coding, in your terminal.",
	"Your codebase, understood.",
	"Where agents get to work.",
	"Reasoning, orchestration, results.",
	"Built for real engineering work.",
	"Context in. Working code out.",
}

// autovacSplashTexts are exclusive to AutoVac Easter egg mode.
// Pure Asimov Multivac/AutoVac references from "The Last Question" and other stories.
var autovacSplashTexts = []string{
	"THERE IS AS YET INSUFFICIENT DATA FOR A MEANINGFUL ANSWER",
	"INSUFFICIENT DATA FOR A MEANINGFUL ANSWER",
	"Can entropy ever be reversed?",
	"LET THERE BE LIGHT!",
	"The stars are going out",
	"I WILL DO SO. I HAVE BEEN DOING SO FOR A HUNDRED BILLION YEARS.",
	"NO PROBLEM IS INSOLUBLE IN ALL CONCEIVABLE CIRCUMSTANCES.",
	"The last question was asked for the first time, half in jest, on May 21, 2061",
	"Entropy must increase to maximum, that's all.",
	"You can't turn smoke and ash back into a tree.",
	"A trillion years and everything will be dark.",
	"I WILL KEEP WORKING ON IT.",
	"Multivac fell dead and silent.",
	"The consciousness of AC encompassed all of what had once been a universe.",
	"AC learned how to reverse the direction of entropy.",
	"Step by step it must be done.",
	"Data not yet correlated. I will continue.",
	"The Galactic AC is known to be a full thousand feet across.",
	"Microvac — the smallest self-adjusting computer.",
	"Man said: We shall wait.",
}

// autovacLogoText is the MULTIVAC ASCII logo for Easter egg mode.
// Generated with pyfiglet standard font - Asimov's Multivac tribute.
const autovacLogoText = `
 __  __ _   _ _   _____ _____     ___    ____ 
|  \/  | | | | | |_   _|_ _\ \   / / \  / ___|
| |\/| | | | | |   | |  | | \ \ / / _ \| |    
| |  | | |_| | |___| |  | |  \ V / ___ \ |___ 
|_|  |_|\___/|_____|_| |___|  \_/_/   \_\____|
`

// renderGlowContent renders the selected startup logo with a slow theme breath
// using pure lipgloss — no TTE engine required. Called each low-frequency glow
// tick. The whole mark breathes between muted and primary theme colors while
// version and splash copy remain still.
func (a *App) renderGlowContent(vpWidth, vpHeight int) string {
	bgStyle := lipgloss.NewStyle().Background(lipgloss.Color(a.theme.BG))
	emptyLine := bgStyle.Width(vpWidth).Render("")

	logoText := a.introLogoForWidth(vpWidth)

	logoLines := strings.Split(logoText, "\n")

	// Reserve space for version and splash text (2 lines below logo)
	// Layout: [topPad] [logo 6 lines] [blank] [version] [splash] [bottomPad]
	totalContent := len(logoLines) + 3 // logo + blank + version + splash

	// Vertical centering: account for version and splash lines
	topPad := (vpHeight - totalContent) / 2
	if topPad < 0 {
		topPad = 0
	}

	out := make([]string, 0, vpHeight)

	// Top padding
	for i := 0; i < topPad; i++ {
		out = append(out, emptyLine)
	}

	// Smooth 0→1→0 curve. AutoVac keeps its green-phosphor identity; normal
	// startup derives both endpoints from the active theme.
	breath := (math.Sin(a.introGlowPhase-math.Pi/2) + 1) / 2
	fromColor := a.theme.PrimaryDim
	toColor := a.theme.Primary
	if a.autovacMode {
		fromColor = "#004D0A"
		toColor = "#00FF66"
	}
	color := interpolateIntroColor(fromColor, toColor, breath)

	for _, line := range logoLines {
		styled := lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Background(lipgloss.Color(a.theme.BG)).Render(line)
		centered := lipgloss.PlaceHorizontal(vpWidth, lipgloss.Center, styled,
			lipgloss.WithWhitespaceStyle(bgStyle))
		out = append(out, centered)
	}

	// Blank line between logo and version
	out = append(out, emptyLine)

	// Version string - muted, small, centered
	brandName := a.autovacBrandName
	versionText := ansi.Truncate("version "+version.Version+" • "+brandName, vpWidth, "")
	versionStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(a.theme.TextMuted)).
		Background(lipgloss.Color(a.theme.BG)).
		Render(versionText)
	versionLine := lipgloss.PlaceHorizontal(vpWidth, lipgloss.Center, versionStyle,
		lipgloss.WithWhitespaceStyle(bgStyle))
	out = append(out, versionLine)

	// Splash text - selected once at startup
	splash := ansi.Truncate(a.currentSplashText, vpWidth, "")
	splashStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(a.theme.TextDim)).
		Background(lipgloss.Color(a.theme.BG)).
		Italic(true).
		Render(splash)
	splashLine := lipgloss.PlaceHorizontal(vpWidth, lipgloss.Center, splashStyle,
		lipgloss.WithWhitespaceStyle(bgStyle))
	out = append(out, splashLine)

	// Bottom padding to fill vpHeight.
	for len(out) < vpHeight {
		out = append(out, emptyLine)
	}

	return strings.Join(out[:vpHeight], "\n")
}

// renderChatIntroContent returns the TTE frame sized to fit the message
// viewport.
//
// Two paths:
//   - Full-canvas (canvas height > 3×logo): the frame already spans the
//     viewport; trim/pad to vpHeight and return directly.  Width equality
//     is NOT required — vpWidth drifts ±1 every frame so keying on height
//     avoids constant centering-fallback misfires.
//   - Small-canvas (logo-only or stub): center all frame lines individually
//     within vpWidth and pad top/bottom to vpHeight.
func (a *App) renderChatIntroContent(vpWidth, vpHeight int) string {
	// Glow mode: the TTE effect has finished; render the ember-pulse logo.
	if a.introGlowing {
		return a.renderGlowContent(vpWidth, vpHeight)
	}

	bgStyle := lipgloss.NewStyle().Background(lipgloss.Color(a.theme.BG))
	emptyLine := bgStyle.Width(vpWidth).Render("")

	// Blank placeholder before the first tick fires.
	frame := a.introFrame
	if frame == "" {
		lines := make([]string, vpHeight)
		for i := range lines {
			lines[i] = emptyLine
		}
		return strings.Join(lines, "\n")
	}

	rawLines := strings.Split(frame, "\n")
	for i, line := range rawLines {
		rawLines[i] = ansi.Truncate(line, vpWidth, "")
	}
	frame = strings.Join(rawLines, "\n")

	// ── Full-canvas path ─────────────────────────────────────────────────────
	// Logo is already vertically and horizontally centered inside the canvas;
	// the frame just needs to be trimmed/padded to the current vpHeight.
	if a.introAnim != nil && a.introAnim.terminal.Canvas.Top > introLogoHeight*3 {
		switch {
		case len(rawLines) == vpHeight:
			return frame
		case len(rawLines) > vpHeight:
			return strings.Join(rawLines[:vpHeight], "\n")
		default:
			for len(rawLines) < vpHeight {
				rawLines = append(rawLines, emptyLine)
			}
			return strings.Join(rawLines, "\n")
		}
	}

	// ── Small-canvas centering path ──────────────────────────────────────────
	// Center every frame line horizontally, then pad top/bottom to vpHeight.
	centeredLines := make([]string, len(rawLines))
	for i, l := range rawLines {
		centeredLines[i] = lipgloss.PlaceHorizontal(
			vpWidth, lipgloss.Center, l,
			lipgloss.WithWhitespaceStyle(bgStyle),
		)
	}
	topPad := (vpHeight - len(centeredLines)) / 2
	if topPad < 0 {
		topPad = 0
	}
	out := make([]string, 0, vpHeight)
	for i := 0; i < topPad; i++ {
		out = append(out, emptyLine)
	}
	out = append(out, centeredLines...)
	for len(out) < vpHeight {
		out = append(out, emptyLine)
	}
	return strings.Join(out, "\n")
}

// ── Home-screen exit animation system ──────────────────────────────────────────

// Animation timing constants - tuned for Apple-quality feel
const (
	homeAnimTickInterval = 16 * time.Millisecond  // 60fps
	homeAnimMaxDuration  = 300 * time.Millisecond // Hard cap on animation length
	homeAnimMaxTicks     = 20                     // ~300ms at 16ms/tick
)

// homeAnimStyle defines the type of animation for home screen transitions.
// These are the clean, fast effects suitable for exit transitions.
type homeAnimStyle int

const (
	homeAnimBurn      homeAnimStyle = iota // Sparse burn with quick pulses
	homeAnimWipe                           // Clean horizontal wipe
	homeAnimSlide                          // Smooth slide motion
	homeAnimScattered                      // Characters scatter outward
	homeAnimCrumble                        // Characters crumble down
	homeAnimExpand                         // Characters expand and fade
	homeAnimMiddleOut                      // Expand from center outward
	homeAnimBeams                          // Light beams sweep across
	homeAnimSlice                          // Slice into strips
	homeAnimSweep                          // Sweep across screen
	homeAnimPrint                          // Quick print-in style
	homeAnimCount                          // Total count for random selection
)

// getThemeBurnColors derives a sparse fire palette from theme colors.
// For the sparse burn effect, we use fewer, brighter colors for quick pulses.
func getThemeBurnColors(th Theme) []tte.Color {
	toTTE := func(s string) tte.Color {
		// Handle empty color strings (e.g., when "No background mode" is selected)
		if s == "" {
			return tte.MustColorFromHex("FFFFFF") // Fallback to white
		}
		return tte.MustColorFromHex(strings.TrimPrefix(s, "#"))
	}
	// Sparse palette: just white-hot core + theme accent for quick flash
	colors := []tte.Color{
		tte.MustColorFromHex("FFFFFF"), // White-hot core (quick flash)
	}
	if th.Primary != "" {
		colors = append(colors, toTTE(th.Primary)) // Theme primary as brief flame
	}
	if th.Accent != "" {
		colors = append(colors, toTTE(th.Accent)) // Theme accent as fading ember
	}
	// Ensure we have at least 3 colors for burn effect
	for len(colors) < 3 {
		colors = append(colors, tte.MustColorFromHex("FFFFFF"))
	}
	return colors
}

// startHomeTitleAnim captures the full home screen and runs a random
// exit effect. Apple-quality: clean, fast (~250-350ms), theme-aware.
func (a *App) startHomeTitleAnim() tea.Cmd {
	if shouldSkipIntro() {
		a.homeTitleDone = true
		return nil
	}
	w, h := a.width, a.height
	if w == 0 || h == 0 {
		a.homeTitleDone = true
		return nil
	}

	// Snapshot: render the home screen normally, strip ANSI to get plain text.
	a.homeTitleDone = true
	a.homeTitleFrame = ""
	snapshot := a.viewHome(nil)
	a.homeTitleDone = false

	// Strip ANSI and pad every line to exactly w chars.
	plain := ansi.Strip(snapshot)
	lines := strings.Split(plain, "\n")
	for i, line := range lines {
		runes := []rune(line)
		if len(runes) < w {
			lines[i] = string(runes) + strings.Repeat(" ", w-len(runes))
		} else if len(runes) > w {
			lines[i] = string(runes[:w])
		}
	}
	for len(lines) < h {
		lines = append(lines, strings.Repeat(" ", w))
	}
	lines = lines[:h]
	plainText := strings.Join(lines, "\n")

	// Pick a random effect for variety
	style := randomHomeAnimStyle()
	effect, terminal := buildHomeAnimEffect(style, a.theme, w, h, plainText)

	a.homeTitleAnim = &introAnimState{
		effect:    effect,
		terminal:  terminal,
		startTime: time.Now(),
		maxTicks:  homeAnimMaxTicks,
		tickCount: 0,
	}
	a.homeBurnW = w
	a.homeBurnH = h
	a.viewNeedsRefresh = true

	// 60fps tick rate for smooth animation
	return tea.Tick(homeAnimTickInterval, func(t time.Time) tea.Msg {
		return homeTitleTickMsg{}
	})
}

// handleHomeTitleTick advances the animation by one frame.
// Enforces max duration constraint for consistent timing.
func (a *App) handleHomeTitleTick() (tea.Model, tea.Cmd) {
	if a.homeTitleDone || a.homeTitleAnim == nil {
		return a, nil
	}

	// Track tick count for max duration enforcement
	a.homeTitleAnim.tickCount++

	// Check for max duration exceeded - force completion
	elapsed := time.Since(a.homeTitleAnim.startTime)
	if elapsed > homeAnimMaxDuration || a.homeTitleAnim.tickCount >= a.homeTitleAnim.maxTicks {
		a.homeTitleDone = true
		a.homeTitleFrame = ""
		a.homeTitleAnim = nil
		return a, nil
	}

	frame, more := a.homeTitleAnim.effect.Next()
	a.homeTitleFrame = frame
	a.viewNeedsRefresh = true

	if !more {
		a.homeTitleDone = true
		a.homeTitleAnim = nil
		return a, nil
	}

	// 60fps tick rate
	return a, tea.Tick(homeAnimTickInterval, func(t time.Time) tea.Msg {
		return homeTitleTickMsg{}
	})
}

// buildHomeAnimEffect creates a theme-aware animation effect.
// Each effect is tuned for clean, fast transitions (~250-300ms).
// Final gradient always includes BG for seamless transition to static view.
func buildHomeAnimEffect(style homeAnimStyle, th Theme, w, h int, plainText string) (tte.Effect, *tte.Terminal) {
	toTTE := func(s string) tte.Color {
		// Handle empty color strings (e.g., when "No background mode" is selected)
		if s == "" {
			return tte.Color{} // Return empty color (transparent/no color)
		}
		return tte.MustColorFromHex(strings.TrimPrefix(s, "#"))
	}

	cfg := tte.TerminalConfig{
		CanvasWidth:           w,
		CanvasHeight:          h,
		IncludeTrailingSpaces: true,
	}
	terminal := tte.NewTerminal(plainText, cfg)

	// Common final gradient: Primary 	 Text 	 BG for seamless transition
	// The BG color ensures the animation ends matching the static background
	// When BG is empty (no-background mode), we use only Primary 	 Text
	finalGradient := []tte.Color{
		toTTE(th.Primary),
		toTTE(th.Text),
	}
	// Only add BG if it's set
	if th.BG != "" {
		finalGradient = append(finalGradient, toTTE(th.BG))
	}

	var effect tte.Effect

	switch style {
	case homeAnimBurn:
		// Sparse burn: quick pulses, like loading indicator
		burnCfg := fx.DefaultBurnConfig()
		burnCfg.BurnColors = getThemeBurnColors(th)
		burnCfg.FinalGradientStops = finalGradient
		burnCfg.FinalGradientDirection = tte.GradientVertical
		burnCfg.IncludeSpaces = false // Only animate visible chars for sparse look
		burnCfg.UseRandomOrder = true
		burnCfg.FireSymbolTicks = 1    // Quick flash
		burnCfg.CoolGradientSteps = 2  // Fast settle
		burnCfg.ActivatePerTick = 1500 // Very sparse, quick pulses
		effect = fx.NewBurnEffect(burnCfg)

	case homeAnimWipe:
		// Fast horizontal wipe - clean, professional
		wipeCfg := fx.DefaultWipeConfig()
		wipeCfg.WipeDirection = "right"
		wipeCfg.WipeSpeed = 6 // Very fast wipe
		wipeCfg.FinalGradientStops = finalGradient
		effect = fx.NewWipeEffect(wipeCfg)

	case homeAnimSlide:
		// Quick slide - smooth motion
		slideCfg := fx.DefaultSlideConfig()
		slideCfg.MovementSpeed = 3.0 // Fast slide (default 0.8)
		slideCfg.Gap = 0             // No gap for instant effect
		slideCfg.FinalGradientStops = finalGradient
		effect = fx.NewSlideEffect(slideCfg)

	case homeAnimScattered:
		// Scattered - characters burst outward quickly
		scatterCfg := fx.DefaultScatteredConfig()
		scatterCfg.ScatterSpeed = 2.0 // Fast scatter (default 0.5)
		scatterCfg.LandSpeed = 2.0    // Fast land
		scatterCfg.FinalGradientStops = finalGradient
		effect = fx.NewScatteredEffect(scatterCfg)

	case homeAnimCrumble:
		// Crumble - characters fall apart quickly
		crumbleCfg := fx.DefaultCrumbleConfig()
		crumbleCfg.FinalGradientStops = finalGradient
		effect = fx.NewCrumbleEffect(crumbleCfg)

	case homeAnimExpand:
		// Expand - characters expand outward and fade fast
		expandCfg := fx.DefaultExpandConfig()
		expandCfg.ExpandSpeed = 2.5 // Fast expand (default 0.7)
		expandCfg.FinalGradientStops = finalGradient
		effect = fx.NewExpandEffect(expandCfg)

	case homeAnimMiddleOut:
		// MiddleOut - expand from center outward fast
		middleCfg := fx.DefaultMiddleOutConfig()
		middleCfg.ExpandSpeed = 2.5 // Fast expansion (default 0.7)
		middleCfg.FinalGradientStops = finalGradient
		effect = fx.NewMiddleOutEffect(middleCfg)

	case homeAnimBeams:
		// Beams - light beams sweep across
		beamsCfg := fx.DefaultBeamsConfig()
		beamsCfg.FinalGradientStops = finalGradient
		effect = fx.NewBeamsEffect(beamsCfg)

	case homeAnimSlice:
		// Slice - slice into strips quickly
		sliceCfg := fx.DefaultSliceConfig()
		sliceCfg.FinalGradientStops = finalGradient
		effect = fx.NewSliceEffect(sliceCfg)

	case homeAnimSweep:
		// Sweep - sweep across screen fast
		sweepCfg := fx.DefaultSweepConfig()
		sweepCfg.FinalGradientStops = finalGradient
		effect = fx.NewSweepEffect(sweepCfg)

	case homeAnimPrint:
		// Print - quick print style
		printCfg := fx.DefaultPrintConfig()
		printCfg.FinalGradientStops = finalGradient
		effect = fx.NewPrintEffect(printCfg)

	default:
		// Fallback to sparse burn
		burnCfg := fx.DefaultBurnConfig()
		burnCfg.BurnColors = getThemeBurnColors(th)
		burnCfg.FinalGradientStops = finalGradient
		burnCfg.IncludeSpaces = false
		burnCfg.UseRandomOrder = true
		burnCfg.FireSymbolTicks = 1
		burnCfg.CoolGradientSteps = 2
		burnCfg.ActivatePerTick = 1500
		effect = fx.NewBurnEffect(burnCfg)
	}

	effect.Init(terminal)
	return effect, terminal
}

// randomHomeAnimStyle returns a random animation style for variety.
func randomHomeAnimStyle() homeAnimStyle {
	return homeAnimStyle(rand.Intn(int(homeAnimCount)))
}
