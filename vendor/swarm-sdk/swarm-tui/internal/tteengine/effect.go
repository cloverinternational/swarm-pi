// Source: terminaltexteffects/engine/base_effect.py
// Covers: Effect interface, BaseEffectIterator, bubbletea Model wrapper
//
// Every effect in this engine implements the Effect interface.
// BaseEffectIterator handles the tick loop, active/pending character sets,
// and produces one string frame per call to Next().
//
// The bubbletea integration is a thin Model wrapper: it ticks at ~60 fps via
// a time.Ticker and calls Next() each frame to produce the view string.
// Drop it into any bubbletea component with:
//
//	model := tteengine.NewEffectModel(MyEffect{...}, terminalConfig)
//	p := tea.NewProgram(model)
package tteengine

import (
	"time"

	tea "charm.land/bubbletea/v2"
)

// ---------------------------------------------------------------------------
// Effect interface
// Source: base_effect.py — BaseEffect / BaseEffectIterator
// ---------------------------------------------------------------------------

// Effect is the interface every TTE effect must satisfy.
//
// Implementing a new effect:
//  1. Define a Config struct with your parameters.
//  2. Embed *BaseIterator in your iterator type.
//  3. Implement Build() to set up all characters' paths, scenes, and events.
//  4. Implement Next() → call BaseIterator.Next() after your per-frame logic.
//  5. Expose a constructor that returns Effect.
type Effect interface {
	// Name returns the display name of the effect.
	Name() string

	// Init receives the parsed Terminal.  Called once before any Next() call.
	// Effects must call their Build() logic here.
	Init(t *Terminal)

	// Next advances the effect by one tick and returns the rendered frame string.
	// Returns ("", false) when the animation is complete.
	Next() (frame string, done bool)

	// Reset restores the effect to its pre-Init state so it can be replayed.
	Reset()
}

// ---------------------------------------------------------------------------
// BaseIterator — shared tick logic
// Source: base_effect.py — BaseEffectIterator
// ---------------------------------------------------------------------------

// BaseIterator provides the common frame-loop mechanics that every effect needs:
//   - a reference to the Terminal and its Config
//   - active/pending character queues
//   - Update() which ticks all active characters and removes finished ones
//   - Frame() which calls Terminal.GetFormattedOutputString()
//
// Effects embed *BaseIterator and call its methods from their own Next().
type BaseIterator struct {
	Terminal          *Terminal
	ActiveCharacters  map[int]*EffectCharacter // keyed by character id
	PendingCharacters []*EffectCharacter
}

// NewBaseIterator creates a BaseIterator backed by the given Terminal.
func NewBaseIterator(t *Terminal) *BaseIterator {
	return &BaseIterator{
		Terminal:         t,
		ActiveCharacters: make(map[int]*EffectCharacter),
	}
}

// Update ticks every active character and removes those that have finished.
// Source: BaseEffectIterator.update
func (b *BaseIterator) Update() {
	for id, ch := range b.ActiveCharacters {
		ch.Tick()
		if !ch.IsActive() {
			delete(b.ActiveCharacters, id)
		}
	}
}

// Frame returns the current rendered frame string.
// Source: BaseEffectIterator.frame (property)
func (b *BaseIterator) Frame() string {
	return b.Terminal.GetFormattedOutputString()
}

// HasWork reports whether there are pending or active characters.
func (b *BaseIterator) HasWork() bool {
	return len(b.PendingCharacters) > 0 || len(b.ActiveCharacters) > 0
}

// Activate makes the next pending character visible and moves it to active.
func (b *BaseIterator) Activate(ch *EffectCharacter) {
	b.Terminal.SetCharacterVisibility(ch, true)
	b.ActiveCharacters[ch.id] = ch
}

// ---------------------------------------------------------------------------
// bubbletea integration
// ---------------------------------------------------------------------------

const defaultFPS = 60

// TickMsg is sent by the internal ticker every frame.
type TickMsg time.Time

// EffectModel is a bubbletea Model that drives any Effect at a fixed frame rate.
// Embed it or use it directly as the root model.
//
// Usage:
//
//	m := tteengine.NewEffectModel("Hello Swarm", effect, tteengine.DefaultTerminalConfig())
//	tea.NewProgram(m).Run()
type EffectModel struct {
	InputText string
	Effect    Effect
	Config    TerminalConfig
	FPS       int

	terminal *Terminal
	done     bool
	frame    string
}

// NewEffectModel creates an EffectModel ready to be passed to tea.NewProgram.
func NewEffectModel(input string, effect Effect, cfg TerminalConfig) EffectModel {
	fps := defaultFPS
	return EffectModel{
		InputText: input,
		Effect:    effect,
		Config:    cfg,
		FPS:       fps,
	}
}

// Init satisfies tea.Model.  It builds the Terminal, calls Effect.Init, and
// starts the ticker.
func (m EffectModel) Init() tea.Cmd {
	m.terminal = NewTerminal(m.InputText, m.Config)
	m.Effect.Init(m.terminal)
	return tickCmd(m.FPS)
}

// Update satisfies tea.Model.
func (m EffectModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg.(type) {
	case TickMsg:
		if m.done {
			return m, nil
		}
		if m.terminal == nil {
			m.terminal = NewTerminal(m.InputText, m.Config)
			m.Effect.Init(m.terminal)
		}
		frame, done := m.Effect.Next()
		m.frame = frame
		m.done = done
		if done {
			return m, nil
		}
		return m, tickCmd(m.FPS)

	case tea.KeyPressMsg:
		return m, tea.Quit
	}
	return m, nil
}

// View satisfies tea.Model — returns the current rendered frame.
func (m EffectModel) View() tea.View {
	return tea.NewView(m.frame)
}

// tickCmd returns a Cmd that fires a TickMsg after one frame interval.
func tickCmd(fps int) tea.Cmd {
	if fps <= 0 {
		fps = defaultFPS
	}
	interval := time.Second / time.Duration(fps)
	return tea.Tick(interval, func(t time.Time) tea.Msg {
		return TickMsg(t)
	})
}
