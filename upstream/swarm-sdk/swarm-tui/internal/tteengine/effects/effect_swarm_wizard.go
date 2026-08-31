// Source: modelled after terminaltexteffects/effects/effect_swarm.py + custom design.
//
// SwarmWizard — three-phase animation:
//  1. SCATTER  characters launch from random off-canvas positions → cluster rendezvous
//  2. SWARM    characters orbit near the cluster centre while glowing
//  3. LAND     characters fly home to their input coord with a colour fade
//
// This is a good example of a multi-phase effect that chains multiple paths
// and scenes together via the EventHandler.
package effects

import (
	"math/rand"

	tte "github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/tteengine"
)

// ---------------------------------------------------------------------------
// Config
// ---------------------------------------------------------------------------

// SwarmWizardConfig holds all tunable parameters for the SwarmWizard effect.
type SwarmWizardConfig struct {
	// SwarmColors is the colour palette; each cluster gets one colour round-robin.
	SwarmColors []tte.Color
	// SwarmCount is the number of independent swarm clusters.
	SwarmCount int
	// SwarmSize is the fraction [0,1] of characters assigned to each cluster.
	SwarmSize float64
	// ScatterSpeed controls how fast characters fly to their cluster.
	ScatterSpeed float64
	// SwarmSpeed controls how fast characters orbit inside the cluster.
	SwarmSpeed float64
	// LandSpeed controls how fast characters fly to their final position.
	LandSpeed float64
	// ScatterEasing is the easing for the scatter phase.
	ScatterEasing tte.EasingFunc
	// SwarmEasing is the easing for the orbit phase.
	SwarmEasing tte.EasingFunc
	// LandEasing is the easing for the landing phase.
	LandEasing tte.EasingFunc
	// FinalGradientStops colours the text at rest.
	FinalGradientStops []tte.Color
	// FinalGradientSteps sets gradient smoothness.
	FinalGradientSteps []int
	// FinalGradientDirection controls gradient mapping direction.
	FinalGradientDirection tte.GradientDirection
}

// DefaultSwarmWizardConfig returns sensible defaults.
func DefaultSwarmWizardConfig() SwarmWizardConfig {
	return SwarmWizardConfig{
		SwarmColors: []tte.Color{
			tte.MustColorFromHex("6E64E8"), // Primary (theme)
			tte.MustColorFromHex("39D2C0"), // Accent (theme)
			tte.MustColorFromHex("ff6600"),
			tte.MustColorFromHex("cc00ff"),
			tte.MustColorFromHex("00ccff"),
			tte.MustColorFromHex("ffcc00"),
		},
		SwarmCount:    3,
		SwarmSize:     0.1,
		ScatterSpeed:  0.5,
		SwarmSpeed:    0.3,
		LandSpeed:     0.4,
		ScatterEasing: tte.EaseInOutQuad,
		SwarmEasing:   tte.EaseInOutSine,
		LandEasing:    tte.EaseOutExpo,
		FinalGradientStops: []tte.Color{
			tte.MustColorFromHex("6E64E8"), // Primary (theme)
			tte.MustColorFromHex("39D2C0"), // Accent (theme)
			tte.MustColorFromHex("FFFFFF"),
		},
		FinalGradientSteps:     []int{12},
		FinalGradientDirection: tte.GradientRadial,
	}
}

// ---------------------------------------------------------------------------
// Effect
// ---------------------------------------------------------------------------

// swarmSymbols are displayed during the swarm/orbit phase.
var swarmSymbols = []string{"*", "·", "•", "+", "×", "✦", "✧", "◆", "◇", "○"}

const orbitHops = 4   // waypoints visited while orbiting
const orbitRadius = 4 // column-radius of the orbit jitter

// SwarmWizardEffect implements tteengine.Effect.
type SwarmWizardEffect struct {
	Config SwarmWizardConfig

	base          *tte.BaseIterator
	finalColorMap map[int]tte.Color
}

// NewSwarmWizard constructs a SwarmWizard effect with the given config.
func NewSwarmWizard(cfg SwarmWizardConfig) *SwarmWizardEffect {
	return &SwarmWizardEffect{Config: cfg}
}

// Name satisfies tteengine.Effect.
func (s *SwarmWizardEffect) Name() string { return "swarm-wizard" }

// Init satisfies tteengine.Effect.
// Source pattern: see effect_rain.go Init + Python effect_swarm.py build()
func (s *SwarmWizardEffect) Init(t *tte.Terminal) {
	s.base = tte.NewBaseIterator(t)
	s.finalColorMap = make(map[int]tte.Color)
	canvas := t.Canvas.Canvas
	cfg := s.Config

	// ── final gradient ────────────────────────────────────────────────────
	finalGrad := tte.NewGradient(cfg.FinalGradientStops, cfg.FinalGradientSteps)
	gradMap := finalGrad.BuildCoordinateColorMapping(
		canvas.TextBottom, canvas.TextTop,
		canvas.TextLeft, canvas.TextRight,
		cfg.FinalGradientDirection,
	)
	for _, ch := range t.GetCharacters() {
		fc, ok := gradMap[ch.InputCoord()]
		if !ok {
			fc = cfg.FinalGradientStops[len(cfg.FinalGradientStops)-1]
		}
		s.finalColorMap[ch.ID()] = fc
	}

	// ── cluster centres ───────────────────────────────────────────────────
	centres := s.makeClusterCentres(canvas)

	// ── assign characters to clusters ─────────────────────────────────────
	allChars := t.GetCharacters()
	rand.Shuffle(len(allChars), func(i, j int) { allChars[i], allChars[j] = allChars[j], allChars[i] })

	assignments := make([][]*tte.EffectCharacter, len(centres))
	for i, ch := range allChars {
		assignments[i%len(centres)] = append(assignments[i%len(centres)], ch)
	}

	// ── wire each character ────────────────────────────────────────────────
	for ci, centre := range centres {
		clusterColor := cfg.SwarmColors[ci%len(cfg.SwarmColors)]
		for _, ch := range assignments[ci] {
			s.wireCharacter(ch, centre, clusterColor, canvas)
			s.base.PendingCharacters = append(s.base.PendingCharacters, ch)
		}
	}
	// shuffle so characters don't all launch in strict cluster order
	rand.Shuffle(len(s.base.PendingCharacters), func(i, j int) {
		s.base.PendingCharacters[i], s.base.PendingCharacters[j] =
			s.base.PendingCharacters[j], s.base.PendingCharacters[i]
	})
}

// makeClusterCentres distributes N centres evenly across the text area.
func (s *SwarmWizardEffect) makeClusterCentres(canvas tte.Canvas) []tte.Coord {
	count := max(s.Config.SwarmCount, 1)
	centres := make([]tte.Coord, 0, count)
	cols := max(1, intSqrt(count))
	rows := max(1, (count+cols-1)/cols)
	colStep := max(1, (canvas.TextRight-canvas.TextLeft)/(cols+1))
	rowStep := max(1, (canvas.TextTop-canvas.TextBottom)/(rows+1))

	for r := 0; r < rows && len(centres) < count; r++ {
		for c := 0; c < cols && len(centres) < count; c++ {
			col := canvas.TextLeft + colStep*(c+1)
			row := canvas.TextBottom + rowStep*(r+1)
			col = clamp(col, canvas.Left, canvas.Right)
			row = clamp(row, canvas.Bottom, canvas.Top)
			centres = append(centres, tte.Coord{Column: col, Row: row})
		}
	}
	for len(centres) < count {
		centres = append(centres, canvas.RandomCoord(false, true))
	}
	return centres
}

// wireCharacter sets up the three-phase path+scene chain for one character.
func (s *SwarmWizardEffect) wireCharacter(
	ch *tte.EffectCharacter,
	centre tte.Coord,
	clusterColor tte.Color,
	canvas tte.Canvas,
) {
	cfg := s.Config
	finalColor := s.finalColorMap[ch.ID()]

	// ── scenes ───────────────────────────────────────────────────────────

	// SCATTER scene: random swarm symbols in cluster colour (looping while flying in)
	scatterScn := ch.Animation.NewScene("scatter", true, tte.SyncNone, nil)
	for _, sym := range randSample(swarmSymbols, 3) {
		scatterScn.AddFrame(sym, 3, tte.FGOnly(clusterColor))
	}

	// SWARM scene: brighter symbols while orbiting (looping)
	swarmScn := ch.Animation.NewScene("swarm", true, tte.SyncNone, nil)
	bright := clusterColor.AdjustBrightness(1.4)
	for _, sym := range swarmSymbols {
		swarmScn.AddFrame(sym, 2, tte.FGOnly(bright))
	}

	// LAND scene: fade cluster colour → final gradient colour → true symbol
	landScn := ch.Animation.NewScene("land", false, tte.SyncNone, nil)
	fadeGrad := tte.NewGradient([]tte.Color{clusterColor, finalColor}, []int{10})
	for _, c := range fadeGrad.Spectrum {
		cp := c
		landScn.AddFrame(ch.InputSymbol(), 3, tte.FGOnly(cp))
	}
	landScn.AddFrame(ch.InputSymbol(), 1, tte.FGOnly(finalColor))

	// ── paths ─────────────────────────────────────────────────────────────

	// Phase 1 — SCATTER: off-canvas → cluster rendezvous (with jitter)
	offCanvas := canvas.RandomCoord(true, false)
	ch.Motion.SetCoordinate(offCanvas)

	jitter := tte.Coord{
		Column: clamp(centre.Column+rand.Intn(orbitRadius*2+1)-orbitRadius, canvas.Left, canvas.Right),
		Row:    clamp(centre.Row+rand.Intn(orbitRadius+1)-orbitRadius/2, canvas.Bottom, canvas.Top),
	}
	scatterPath := ch.Motion.NewPath("scatter", cfg.ScatterSpeed, cfg.ScatterEasing, 0, false)
	scatterPath.NewWaypoint(jitter, "rendezvous")

	// Phase 2 — SWARM: orbit several random points near the cluster centre
	swarmPath := ch.Motion.NewPath("swarm", cfg.SwarmSpeed, cfg.SwarmEasing, 0, false)
	for range orbitHops {
		op := tte.Coord{
			Column: clamp(centre.Column+rand.Intn(orbitRadius*2+1)-orbitRadius, canvas.Left, canvas.Right),
			Row:    clamp(centre.Row+rand.Intn(orbitRadius+1)-orbitRadius/2, canvas.Bottom, canvas.Top),
		}
		swarmPath.NewWaypoint(op, "")
	}

	// Phase 3 — LAND: fly to true input coordinate
	landPath := ch.Motion.NewPath("land", cfg.LandSpeed, cfg.LandEasing, 0, false)
	landPath.NewWaypoint(ch.InputCoord(), "home")

	// ── event wiring ──────────────────────────────────────────────────────
	eh := ch.EventHandler

	// scatter start → scatter scene
	eh.RegisterEvent(tte.EventPathActivated, scatterPath, tte.ActionActivateScene, scatterScn)
	// scatter done → start swarm path + swarm scene
	eh.RegisterEvent(tte.EventPathComplete, scatterPath, tte.ActionActivatePath, swarmPath)
	eh.RegisterEvent(tte.EventPathComplete, scatterPath, tte.ActionActivateScene, swarmScn)
	// swarm done → start land path + land scene
	eh.RegisterEvent(tte.EventPathComplete, swarmPath, tte.ActionActivatePath, landPath)
	eh.RegisterEvent(tte.EventPathComplete, swarmPath, tte.ActionActivateScene, landScn)

	// kick off
	ch.Motion.ActivatePath(scatterPath, eh)
}

// Next satisfies tteengine.Effect.
func (s *SwarmWizardEffect) Next() (string, bool) {
	if !s.base.HasWork() {
		return s.base.Frame(), true
	}
	// staggered launch: release a small random batch each tick
	if len(s.base.PendingCharacters) > 0 {
		batch := 1 + rand.Intn(max(1, len(s.base.PendingCharacters)/8))
		for i := 0; i < batch && len(s.base.PendingCharacters) > 0; i++ {
			ch := s.base.PendingCharacters[0]
			s.base.PendingCharacters = s.base.PendingCharacters[1:]
			s.base.Activate(ch)
		}
	}
	s.base.Update()
	return s.base.Frame(), false
}

// Reset satisfies tteengine.Effect.
func (s *SwarmWizardEffect) Reset() {
	s.base = nil
	s.finalColorMap = nil
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func intSqrt(n int) int {
	r := 1
	for r*r < n {
		r++
	}
	return r
}

// randSample returns n randomly-chosen distinct elements from src.
func randSample(src []string, n int) []string {
	if n >= len(src) {
		return src
	}
	indices := rand.Perm(len(src))[:n]
	out := make([]string, n)
	for i, idx := range indices {
		out[i] = src[idx]
	}
	return out
}
