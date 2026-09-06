// Source: terminaltexteffects/engine/animation.py
// Covers: CharacterVisual, Frame, Scene, Animation
//
// The animation system controls what a character *looks like* each tick.
// A character has an Animation which manages named Scenes.
// A Scene is an ordered list of Frames; each Frame is a visual + duration.
// Only one Scene is active at a time; each call to StepAnimation advances it.
package tteengine

import (
	"fmt"
	"strings"
)

// ---------------------------------------------------------------------------
// CharacterVisual
// Source: animation.py — CharacterVisual
// ---------------------------------------------------------------------------

// CharacterVisual is the fully-formatted representation of a character for
// a single tick: its symbol plus any ANSI colour/mode decorations.
type CharacterVisual struct {
	Symbol    string // the rune(s) to display
	Colors    ColorPair
	Bold      bool
	Dim       bool
	Italic    bool
	Underline bool
	Blink     bool
	Reverse   bool
	Strike    bool
}

// Formatted returns the symbol wrapped in ANSI escape sequences for all active
// modes and colours.  If nothing is active the bare symbol is returned.
// Source: CharacterVisual.format_symbol
func (cv CharacterVisual) Formatted() string {
	var sb strings.Builder
	if cv.Bold {
		sb.WriteString("\033[1m")
	}
	if cv.Dim {
		sb.WriteString("\033[2m")
	}
	if cv.Italic {
		sb.WriteString("\033[3m")
	}
	if cv.Underline {
		sb.WriteString("\033[4m")
	}
	if cv.Blink {
		sb.WriteString("\033[5m")
	}
	if cv.Reverse {
		sb.WriteString("\033[7m")
	}
	if cv.Strike {
		sb.WriteString("\033[9m")
	}
	if cv.Colors.FG != nil {
		sb.WriteString(fmt.Sprintf("\033[38;2;%d;%d;%dm", cv.Colors.FG.R, cv.Colors.FG.G, cv.Colors.FG.B))
	}
	if cv.Colors.BG != nil {
		sb.WriteString(fmt.Sprintf("\033[48;2;%d;%d;%dm", cv.Colors.BG.R, cv.Colors.BG.G, cv.Colors.BG.B))
	}
	if sb.Len() == 0 {
		return cv.Symbol
	}
	return sb.String() + cv.Symbol + "\033[0m"
}

// ---------------------------------------------------------------------------
// Frame
// Source: animation.py — Frame
// ---------------------------------------------------------------------------

// Frame is a CharacterVisual displayed for a fixed number of ticks.
type Frame struct {
	Visual       CharacterVisual
	Duration     int // ticks this frame is shown
	TicksElapsed int
}

// ---------------------------------------------------------------------------
// Scene
// Source: animation.py — Scene
// ---------------------------------------------------------------------------

// SyncMetric controls whether a Scene's frame advancement is coupled to
// a Path's progress rather than advancing every tick.
type SyncMetric int

const (
	SyncNone     SyncMetric = iota
	SyncStep                // sync to path current_step / max_steps
	SyncDistance            // sync to path distance traveled ratio
)

// Scene is an ordered sequence of Frames that can be played once or looped.
// It is analogous to a named "state" for the character's appearance.
type Scene struct {
	id        int // unique integer for EventHandler keying
	ID        string
	IsLooping bool
	Sync      SyncMetric
	Ease      EasingFunc // optional easing applied to frame selection
	// frame queues — mirrors Python's frames / played_frames split
	frames       []*Frame
	playedFrames []*Frame
	// total ticks across all frames, used for easing
	totalTicks  int
	currentTick int
}

// newScene creates a new empty Scene.  Called by Animation.NewScene.
func newScene(id string, isLooping bool, sync SyncMetric, ease EasingFunc) *Scene {
	return &Scene{
		id:        nextID(),
		ID:        id,
		IsLooping: isLooping,
		Sync:      sync,
		Ease:      ease,
	}
}

// AddFrame appends a frame to the Scene.
// Source: Scene.add_frame
func (s *Scene) AddFrame(symbol string, duration int, colors ColorPair, opts ...FrameOption) {
	if duration < 1 {
		duration = 1
	}
	cv := CharacterVisual{Symbol: symbol, Colors: colors}
	for _, o := range opts {
		o(&cv)
	}
	s.frames = append(s.frames, &Frame{Visual: cv, Duration: duration})
	s.totalTicks += duration
}

// FrameOption allows optional ANSI mode flags to be set on a Frame.
type FrameOption func(*CharacterVisual)

func WithBold() FrameOption      { return func(cv *CharacterVisual) { cv.Bold = true } }
func WithItalic() FrameOption    { return func(cv *CharacterVisual) { cv.Italic = true } }
func WithUnderline() FrameOption { return func(cv *CharacterVisual) { cv.Underline = true } }
func WithBlink() FrameOption     { return func(cv *CharacterVisual) { cv.Blink = true } }
func WithReverse() FrameOption   { return func(cv *CharacterVisual) { cv.Reverse = true } }
func WithStrike() FrameOption    { return func(cv *CharacterVisual) { cv.Strike = true } }
func WithDim() FrameOption       { return func(cv *CharacterVisual) { cv.Dim = true } }

// ApplyGradientToSymbols adds one Frame per symbol, cycling through the
// gradient colours.  Both fg and bg gradients are optional.
// Source: Scene.apply_gradient_to_symbols
func (s *Scene) ApplyGradientToSymbols(symbols []string, duration int, fg, bg *Gradient) {
	if fg == nil && bg == nil {
		return
	}
	n := len(symbols)
	if n == 0 {
		return
	}
	for i, sym := range symbols {
		t := 0.0
		if n > 1 {
			t = float64(i) / float64(n-1)
		}
		var fgColor, bgColor *Color
		if fg != nil && len(fg.Spectrum) > 0 {
			c := fg.At(t)
			fgColor = &c
		}
		if bg != nil && len(bg.Spectrum) > 0 {
			c := bg.At(t)
			bgColor = &c
		}
		s.AddFrame(sym, duration, ColorPair{FG: fgColor, BG: bgColor})
	}
}

// GetNextVisual advances the Scene by one tick and returns the current visual.
// If the Scene is non-looping and all frames are exhausted, the last visual
// is returned and the Scene is considered complete.
// Source: Scene.get_next_visual
func (s *Scene) GetNextVisual() CharacterVisual {
	if len(s.frames) == 0 {
		if len(s.playedFrames) > 0 {
			return s.playedFrames[len(s.playedFrames)-1].Visual
		}
		return CharacterVisual{Symbol: " "}
	}
	cur := s.frames[0]
	visual := cur.Visual
	cur.TicksElapsed++
	if cur.TicksElapsed >= cur.Duration {
		cur.TicksElapsed = 0
		s.playedFrames = append(s.playedFrames, s.frames[0])
		s.frames = s.frames[1:]
		if s.IsLooping && len(s.frames) == 0 {
			// refill
			s.frames = append(s.frames, s.playedFrames...)
			s.playedFrames = s.playedFrames[:0]
		}
	}
	return visual
}

// IsComplete returns true when the scene has no frames left and is not looping.
// Source: Animation.active_scene_is_complete
func (s *Scene) IsComplete() bool {
	return !s.IsLooping && len(s.frames) == 0
}

// Reset refills the scene's frame queue so it can play again.
func (s *Scene) Reset() {
	s.frames = append(s.frames, s.playedFrames...)
	s.playedFrames = s.playedFrames[:0]
	for _, f := range s.frames {
		f.TicksElapsed = 0
	}
	s.currentTick = 0
}

// Activate returns the first frame's visual without advancing.
// Source: Scene.activate
func (s *Scene) Activate() CharacterVisual {
	if len(s.frames) > 0 {
		return s.frames[0].Visual
	}
	return CharacterVisual{Symbol: " "}
}

// ---------------------------------------------------------------------------
// Animation
// Source: animation.py — Animation
// ---------------------------------------------------------------------------

// Animation manages all Scenes for a single EffectCharacter.
// At most one Scene is active at any time; each Tick() call advances it.
type Animation struct {
	scenes        map[string]*Scene
	ActiveScene   *Scene
	CurrentVisual CharacterVisual

	// Terminal-level colour overrides from TerminalConfig
	NoColor               bool
	UseXTermColors        bool
	ExistingColorHandling string // "ignore" | "always" | "dynamic"
	InputFGColor          *Color
	InputBGColor          *Color

	// back-reference so AdjustColorBrightness is accessible as a method
	// (static in Python, kept here for symmetry)
	character interface{ InputSymbol() string }
}

// NewAnimation creates an Animation for the given character.
func NewAnimation(char interface{ InputSymbol() string }) *Animation {
	return &Animation{
		scenes:                make(map[string]*Scene),
		character:             char,
		ExistingColorHandling: "ignore",
	}
}

// NewScene creates a named Scene and registers it.
// Source: Animation.new_scene
func (a *Animation) NewScene(id string, isLooping bool, sync SyncMetric, ease EasingFunc) *Scene {
	if id == "" {
		id = fmt.Sprintf("scene_%d", len(a.scenes))
	}
	scn := newScene(id, isLooping, sync, ease)
	a.scenes[id] = scn
	return scn
}

// QueryScene returns the Scene with the given id, or nil.
func (a *Animation) QueryScene(id string) *Scene {
	return a.scenes[id]
}

// ActivateScene sets the named scene as the active one.
// Source: Animation.activate_scene
func (a *Animation) ActivateScene(scn *Scene) {
	a.ActiveScene = scn
	a.CurrentVisual = scn.Activate()
}

// DeactivateScene clears the active scene if it matches.
func (a *Animation) DeactivateScene(scn *Scene) {
	if a.ActiveScene == scn {
		a.ActiveScene = nil
	}
}

// ActiveSceneIsComplete reports whether the current scene has finished.
// Source: Animation.active_scene_is_complete
func (a *Animation) ActiveSceneIsComplete() bool {
	return a.ActiveScene == nil || a.ActiveScene.IsComplete()
}

// StepAnimation advances the active scene by one tick and updates
// CurrentVisual.  Called by EffectCharacter.Tick().
// Source: Animation.step_animation
func (a *Animation) StepAnimation() {
	if a.ActiveScene == nil || len(a.ActiveScene.frames) == 0 {
		return
	}
	a.CurrentVisual = a.ActiveScene.GetNextVisual()
}

// SetAppearance sets a one-shot visual without a scene.
// Source: Animation.set_appearance
func (a *Animation) SetAppearance(symbol string, colors ColorPair) {
	if symbol == "" && a.character != nil {
		symbol = a.character.InputSymbol()
	}
	a.CurrentVisual = CharacterVisual{Symbol: symbol, Colors: colors}
}

// AdjustColorBrightness is a static helper matching Python's
// Animation.adjust_color_brightness.
func AdjustColorBrightness(c Color, factor float64) Color {
	return c.AdjustBrightness(factor)
}
