// Source: terminaltexteffects/effects/effect_rain.py
//
// Rain characters fall from the top of the canvas and reveal the underlying
// text as they pass over it.  Each character gets a raindrop scene (random
// symbol in a rain colour) followed by a fade scene (raindrop colour →
// final gradient colour).
//
// This file is the canonical reference for how to implement an effect.
// Read it top-to-bottom to understand the pattern before writing a new one.
package effects

import (
	"math/rand"

	tte "github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/tteengine"
)

// ---------------------------------------------------------------------------
// Config
// ---------------------------------------------------------------------------

// RainConfig holds all tunable parameters for the Rain effect.
type RainConfig struct {
	// RainColors is the palette of colours for falling drops.
	RainColors []tte.Color
	// RainSymbols are the runes used to represent each drop.
	RainSymbols []string
	// MovementSpeed is the number of canvas rows the drop travels per tick.
	MovementSpeed [2]float64 // [min, max]
	// MovementEasing controls the drop fall curve.
	MovementEasing tte.EasingFunc
	// FinalGradientStops are the colours for the text after the rain reveals it.
	FinalGradientStops []tte.Color
	// FinalGradientSteps is the number of interpolation steps in the gradient.
	FinalGradientSteps []int
	// FinalGradientDirection controls how the gradient is mapped across the text.
	FinalGradientDirection tte.GradientDirection
}

// DefaultRainConfig returns the Rain effect's defaults, matching the Python engine.
func DefaultRainConfig() RainConfig {
	return RainConfig{
		RainColors: []tte.Color{
			tte.MustColorFromHex("00315C"),
			tte.MustColorFromHex("004C8F"),
			tte.MustColorFromHex("0075DB"),
			tte.MustColorFromHex("3F91D9"),
			tte.MustColorFromHex("78B9F2"),
			tte.MustColorFromHex("9AC8F5"),
			tte.MustColorFromHex("B8D8F8"),
			tte.MustColorFromHex("E3EFFC"),
		},
		RainSymbols:    []string{"o", ".", ",", "*", "|"},
		MovementSpeed:  [2]float64{0.33, 0.57},
		MovementEasing: tte.EaseInQuart,
		FinalGradientStops: []tte.Color{
			tte.MustColorFromHex("488bff"),
			tte.MustColorFromHex("b2e7de"),
			tte.MustColorFromHex("57eaf7"),
		},
		FinalGradientSteps:     []int{12},
		FinalGradientDirection: tte.GradientDiagonal,
	}
}

// ---------------------------------------------------------------------------
// Effect
// ---------------------------------------------------------------------------

// RainEffect implements tteengine.Effect.
type RainEffect struct {
	Config RainConfig

	base    *tte.BaseIterator
	grouped map[int][]*tte.EffectCharacter // characters grouped by row
	// final colour per character — built in Init
	finalColorMap map[int]tte.Color // keyed by character id
}

// NewRain constructs a Rain effect with the given config.
func NewRain(cfg RainConfig) *RainEffect {
	return &RainEffect{Config: cfg}
}

// Name satisfies tteengine.Effect.
func (r *RainEffect) Name() string { return "rain" }

// Init satisfies tteengine.Effect.  It builds every character's path and
// scene chain exactly as the Python engine does.
// Source: RainIterator.build
func (r *RainEffect) Init(t *tte.Terminal) {
	r.base = tte.NewBaseIterator(t)
	r.grouped = make(map[int][]*tte.EffectCharacter)
	r.finalColorMap = make(map[int]tte.Color)

	canvas := t.Canvas.Canvas
	cfg := r.Config

	// ── final gradient ────────────────────────────────────────────────────
	finalGrad := tte.NewGradient(cfg.FinalGradientStops, cfg.FinalGradientSteps)
	gradMap := finalGrad.BuildCoordinateColorMapping(
		canvas.TextBottom, canvas.TextTop,
		canvas.TextLeft, canvas.TextRight,
		cfg.FinalGradientDirection,
	)

	for _, ch := range t.GetCharacters() {
		finalColor, ok := gradMap[ch.InputCoord()]
		if !ok {
			finalColor = cfg.FinalGradientStops[len(cfg.FinalGradientStops)-1]
		}
		r.finalColorMap[ch.ID()] = finalColor

		// ── raindrop scene (looping while the character falls) ─────────────
		dropColor := cfg.RainColors[rand.Intn(len(cfg.RainColors))]
		rainScn := ch.Animation.NewScene("rain", true, tte.SyncNone, nil)
		dropSym := cfg.RainSymbols[rand.Intn(len(cfg.RainSymbols))]
		fg := dropColor
		rainScn.AddFrame(dropSym, 1, tte.FGOnly(fg))

		// ── fade scene (plays once when the drop reaches its home coord) ───
		fadeGrad := tte.NewGradient(
			[]tte.Color{dropColor, finalColor},
			[]int{7},
		)
		fadeScn := ch.Animation.NewScene("fade", false, tte.SyncNone, nil)
		for _, c := range fadeGrad.Spectrum {
			cp := c
			fadeScn.AddFrame(ch.InputSymbol(), 3, tte.FGOnly(cp))
		}

		// ── path: top of canvas → home coord ──────────────────────────────
		speed := cfg.MovementSpeed[0] +
			rand.Float64()*(cfg.MovementSpeed[1]-cfg.MovementSpeed[0])
		path := ch.Motion.NewPath("rain", speed, cfg.MovementEasing, 0, false)

		// teleport to the top of the canvas above the character's column
		ch.Motion.SetCoordinate(tte.Coord{Column: ch.InputCoord().Column, Row: canvas.Top})
		path.NewWaypoint(ch.InputCoord(), "home")

		// wire: PATH_ACTIVATED → activate rain scene
		ch.EventHandler.RegisterEvent(tte.EventPathActivated, path, tte.ActionActivateScene, rainScn)
		// wire: PATH_COMPLETE → activate fade scene
		ch.EventHandler.RegisterEvent(tte.EventPathComplete, path, tte.ActionActivateScene, fadeScn)

		ch.Motion.ActivatePath(path, ch.EventHandler)

		// group by row so we release rows from top→bottom
		row := ch.InputCoord().Row
		r.grouped[row] = append(r.grouped[row], ch)
	}

	// seed pending with the topmost row
	r.releaseNextRow()
}

// releaseNextRow moves the highest-numbered remaining row into pending.
func (r *RainEffect) releaseNextRow() {
	if len(r.grouped) == 0 {
		return
	}
	// find max row (closest to top)
	maxRow := -1
	for row := range r.grouped {
		if row > maxRow {
			maxRow = row
		}
	}
	r.base.PendingCharacters = append(r.base.PendingCharacters, r.grouped[maxRow]...)
	delete(r.grouped, maxRow)
}

// Next satisfies tteengine.Effect.
// Source: RainIterator.__next__
func (r *RainEffect) Next() (string, bool) {
	if !r.base.HasWork() && len(r.grouped) == 0 {
		return r.base.Frame(), true
	}

	// Release a new row if pending runs dry
	if len(r.base.PendingCharacters) == 0 && len(r.grouped) > 0 {
		r.releaseNextRow()
	}

	// Drip-feed 1-2 characters from pending each tick
	if len(r.base.PendingCharacters) > 0 {
		n := 1 + rand.Intn(2)
		for i := 0; i < n && len(r.base.PendingCharacters) > 0; i++ {
			idx := rand.Intn(len(r.base.PendingCharacters))
			ch := r.base.PendingCharacters[idx]
			r.base.PendingCharacters = append(
				r.base.PendingCharacters[:idx],
				r.base.PendingCharacters[idx+1:]...,
			)
			r.base.Activate(ch)
		}
	}

	r.base.Update()
	return r.base.Frame(), false
}

// Reset satisfies tteengine.Effect.
func (r *RainEffect) Reset() {
	r.base = nil
	r.grouped = nil
	r.finalColorMap = nil
}
