package effects

import (
	"math"
	"math/rand"
	"sort"

	tte "github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/tteengine"
)

// ── helpers (suffix 5) ────────────────────────────────────────────────────────

func max5(a, b int) int {
	if a > b {
		return a
	}
	return b
}
func min5(a, b int) int {
	if a < b {
		return a
	}
	return b
}

var _ = math.Sqrt
var _ = sort.Ints
var _ = max5
var _ = min5

// ── 1. SlideEffect ────────────────────────────────────────────────────────────

type SlideConfig struct {
	MovementSpeed          float64
	Gap                    int
	FinalGradientStops     []tte.Color
	FinalGradientSteps     []int
	FinalGradientDirection tte.GradientDirection
}

func DefaultSlideConfig() SlideConfig {
	return SlideConfig{
		MovementSpeed: 0.8,
		Gap:           2,
		FinalGradientStops: []tte.Color{
			tte.MustColorFromHex("833ab4"),
			tte.MustColorFromHex("fd1d1d"),
			tte.MustColorFromHex("fcb045"),
		},
		FinalGradientSteps:     []int{12},
		FinalGradientDirection: tte.GradientVertical,
	}
}

type SlideEffect struct {
	Config     SlideConfig
	base       *tte.BaseIterator
	terminal   *tte.Terminal
	rowGroups  [][]*tte.EffectCharacter
	rowIdx     int
	gapCounter int
}

func NewSlideEffect(cfg SlideConfig) *SlideEffect { return &SlideEffect{Config: cfg} }
func (e *SlideEffect) Name() string               { return "slide" }
func (e *SlideEffect) Init(t *tte.Terminal) {
	e.terminal = t
	e.base = tte.NewBaseIterator(t)
	e.rowIdx = 0
	e.gapCounter = 0
	canvas := t.Canvas.Canvas

	grad := tte.NewGradient(e.Config.FinalGradientStops, e.Config.FinalGradientSteps)
	colorMap := grad.BuildCoordinateColorMapping(canvas.TextBottom, canvas.TextTop, canvas.TextLeft, canvas.TextRight, e.Config.FinalGradientDirection)

	rowMap := make(map[int][]*tte.EffectCharacter)
	for _, ch := range t.GetCharacters() {
		finalColor := colorMap[ch.InputCoord()]
		slideScn := ch.Animation.NewScene("slide", false, tte.SyncNone, nil)
		slideGrad := tte.NewGradient([]tte.Color{e.Config.FinalGradientStops[0], finalColor}, []int{8})
		slideScn.ApplyGradientToSymbols([]string{ch.InputSymbol()}, 6, slideGrad, nil)
		slideScn.AddFrame(ch.InputSymbol(), 1, tte.FGOnly(finalColor))

		startCoord := tte.Coord{Column: canvas.Left - 1, Row: ch.InputCoord().Row}
		ch.Motion.SetCoordinate(startCoord)
		path := ch.Motion.NewPath("slide", e.Config.MovementSpeed, tte.EaseInOutQuad, 0, false)
		path.NewWaypoint(ch.InputCoord(), "home")
		ch.EventHandler.RegisterEvent(tte.EventPathActivated, path, tte.ActionActivateScene, slideScn)
		t.SetCharacterVisibility(ch, true)
		ch.Motion.ActivatePath(path, ch.EventHandler)
		rowMap[ch.InputCoord().Row] = append(rowMap[ch.InputCoord().Row], ch)
	}

	rows := make([]int, 0, len(rowMap))
	for r := range rowMap {
		rows = append(rows, r)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(rows))) // top first
	e.rowGroups = make([][]*tte.EffectCharacter, len(rows))
	for i, r := range rows {
		e.rowGroups[i] = rowMap[r]
	}
}
func (e *SlideEffect) Next() (string, bool) {
	if e.gapCounter > 0 {
		e.gapCounter--
	} else if e.rowIdx < len(e.rowGroups) {
		for _, ch := range e.rowGroups[e.rowIdx] {
			e.base.Activate(ch)
		}
		e.rowIdx++
		e.gapCounter = e.Config.Gap
	}
	e.base.Update()
	return e.base.Frame(), !e.base.HasWork() && e.rowIdx >= len(e.rowGroups)
}
func (e *SlideEffect) Reset() { e.rowIdx = 0; e.gapCounter = 0 }

// ── 2. SmokeEffect ────────────────────────────────────────────────────────────

type SmokeConfig struct {
	StartingColor          tte.Color
	SmokeSymbols           []string
	SmokeGradientStops     []tte.Color
	FinalGradientStops     []tte.Color
	FinalGradientSteps     []int
	FinalGradientDirection tte.GradientDirection
}

func DefaultSmokeConfig() SmokeConfig {
	return SmokeConfig{
		StartingColor: tte.MustColorFromHex("7A7A7A"),
		SmokeSymbols:  []string{"░", "▒", "▓", "▒", "░"},
		SmokeGradientStops: []tte.Color{
			tte.MustColorFromHex("242424"),
			tte.MustColorFromHex("FFFFFF"),
		},
		FinalGradientStops: []tte.Color{
			tte.MustColorFromHex("8A008A"),
			tte.MustColorFromHex("00D1FF"),
			tte.MustColorFromHex("FFFFFF"),
		},
		FinalGradientSteps:     []int{12},
		FinalGradientDirection: tte.GradientVertical,
	}
}

type SmokeEffect struct {
	Config       SmokeConfig
	base         *tte.BaseIterator
	terminal     *tte.Terminal
	orderedChars []*tte.EffectCharacter
	batchSize    int
	batchIdx     int
	tick         int
}

func NewSmokeEffect(cfg SmokeConfig) *SmokeEffect { return &SmokeEffect{Config: cfg} }
func (e *SmokeEffect) Name() string               { return "smoke" }
func (e *SmokeEffect) Init(t *tte.Terminal) {
	e.terminal = t
	e.base = tte.NewBaseIterator(t)
	e.tick = 0
	e.batchIdx = 0
	canvas := t.Canvas.Canvas

	grad := tte.NewGradient(e.Config.FinalGradientStops, e.Config.FinalGradientSteps)
	colorMap := grad.BuildCoordinateColorMapping(canvas.TextBottom, canvas.TextTop, canvas.TextLeft, canvas.TextRight, e.Config.FinalGradientDirection)
	smokeGrad := tte.NewGradient(e.Config.SmokeGradientStops, []int{5})

	chars := t.GetCharacters()
	startCoord := canvas.RandomCoord(false, true)

	// sort by distance from startCoord (BFS approximation)
	type cdist struct {
		ch   *tte.EffectCharacter
		dist float64
	}
	items := make([]cdist, len(chars))
	for i, ch := range chars {
		items[i] = cdist{ch, distSq2(ch.InputCoord(), startCoord)}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].dist < items[j].dist })

	e.orderedChars = make([]*tte.EffectCharacter, len(items))
	for i, it := range items {
		ch := it.ch
		e.orderedChars[i] = ch
		finalColor := colorMap[ch.InputCoord()]

		t.SetCharacterVisibility(ch, true)
		ch.Animation.SetAppearance(ch.InputSymbol(), tte.FGOnly(e.Config.StartingColor))

		smokeScn := ch.Animation.NewScene("smoke", false, tte.SyncNone, nil)
		smokeScn.ApplyGradientToSymbols(e.Config.SmokeSymbols, 3, smokeGrad, nil)

		paintScn := ch.Animation.NewScene("paint", false, tte.SyncNone, nil)
		paintGrad := tte.NewGradient([]tte.Color{e.Config.FinalGradientStops[0], finalColor}, []int{5})
		paintScn.ApplyGradientToSymbols([]string{ch.InputSymbol()}, 3, paintGrad, nil)
		paintScn.AddFrame(ch.InputSymbol(), 1, tte.FGOnly(finalColor))

		ch.EventHandler.RegisterEvent(tte.EventSceneComplete, smokeScn, tte.ActionActivateScene, paintScn)
	}
	e.batchSize = max5(1, len(chars)/20)
}
func (e *SmokeEffect) Next() (string, bool) {
	e.tick++
	if e.tick%3 == 0 && e.batchIdx < len(e.orderedChars) {
		end := min5(e.batchIdx+e.batchSize, len(e.orderedChars))
		for _, ch := range e.orderedChars[e.batchIdx:end] {
			smokeScn := ch.Animation.QueryScene("smoke")
			if smokeScn != nil {
				ch.Animation.ActivateScene(smokeScn)
			}
			e.base.ActiveCharacters[ch.ID()] = ch
		}
		e.batchIdx = end
	}
	e.base.Update()
	done := e.batchIdx >= len(e.orderedChars) && !e.base.HasWork()
	return e.base.Frame(), done
}
func (e *SmokeEffect) Reset() { e.batchIdx = 0; e.tick = 0 }

// ── 3. SpotlightsEffect ───────────────────────────────────────────────────────
// Faithful port of the Python TTE spotlights effect.
//
// Key improvements over the old Go version:
//   - Spotlights follow smooth bezier-curved paths (via SpotlightMover) between
//     points at minimum canvas.Right/4 distance apart, looping during search.
//   - BeamWidthRatio drives radius the same way Python does.
//   - BeamFalloff dims chars at the edge of the beam proportionally.
//   - FindCoordsInCircle (ellipse formula) used for accurate illuminate set.
//   - Distance uses double_row_diff aspect-ratio compensation.
//   - Converge phase uses smooth motion to center; then beam expands to fill canvas.

type SpotlightsConfig struct {
	BeamWidthRatio         float64 // radius = max(1, min(smallest/ratio, smallest))
	BeamFalloff            float64 // 0..1: edge fraction where brightness falls off
	SearchDuration         int     // frames before spotlights converge
	SearchSpeedMin         float64
	SearchSpeedMax         float64
	SpotlightCount         int
	FinalGradientStops     []tte.Color
	FinalGradientSteps     []int
	FinalGradientDirection tte.GradientDirection
}

func DefaultSpotlightsConfig() SpotlightsConfig {
	return SpotlightsConfig{
		BeamWidthRatio: 2.0,
		BeamFalloff:    0.3,
		SearchDuration: 550,
		SearchSpeedMin: 0.35,
		SearchSpeedMax: 0.75,
		SpotlightCount: 3,
		FinalGradientStops: []tte.Color{
			tte.MustColorFromHex("ab48ff"),
			tte.MustColorFromHex("e7b2b2"),
			tte.MustColorFromHex("fffebd"),
		},
		FinalGradientSteps:     []int{12},
		FinalGradientDirection: tte.GradientVertical,
	}
}

type SpotlightsEffect struct {
	Config          SpotlightsConfig
	terminal        *tte.Terminal
	tick            int
	phase           string // "search" | "converge" | "expand" | "done"
	illuminateRange int
	movers          []*tte.SpotlightMover
	brightMap       map[tte.Coord]tte.Color
	dimMap          map[tte.Coord]tte.Color
	doneTick        int
}

func NewSpotlightsEffect(cfg SpotlightsConfig) *SpotlightsEffect {
	return &SpotlightsEffect{Config: cfg}
}
func (e *SpotlightsEffect) Name() string { return "spotlights" }

// spotlightMinDistCoord returns a canvas coord at least minDist from origin.
func spotlightMinDistCoord(canvas tte.Canvas, origin tte.Coord, minDist int) tte.Coord {
	for range 1000 {
		c := canvas.RandomCoord(false, false)
		dc := float64(c.Column - origin.Column)
		dr := float64(c.Row-origin.Row) * 2 // double_row_diff
		if math.Sqrt(dc*dc+dr*dr) >= float64(minDist) {
			return c
		}
	}
	return canvas.RandomCoord(false, false)
}

func (e *SpotlightsEffect) Init(t *tte.Terminal) {
	e.terminal = t
	e.tick = 0
	e.phase = "search"
	e.doneTick = 0

	canvas := t.Canvas.Canvas

	// Gradient color maps.
	grad := tte.NewGradient(e.Config.FinalGradientStops, e.Config.FinalGradientSteps)
	colorMap := grad.BuildCoordinateColorMapping(
		canvas.TextBottom, canvas.TextTop,
		canvas.TextLeft, canvas.TextRight,
		e.Config.FinalGradientDirection,
	)
	e.brightMap = colorMap
	e.dimMap = make(map[tte.Coord]tte.Color)
	for _, ch := range t.GetCharacters() {
		coord := ch.InputCoord()
		bright := colorMap[coord]
		dim := tte.AdjustColorBrightness(bright, 0.2)
		e.dimMap[coord] = dim
		t.SetCharacterVisibility(ch, true)
		ch.Animation.SetAppearance(ch.InputSymbol(), tte.FGOnly(dim))
	}

	// Illuminate radius: max(1, min(smallest/ratio, smallest)).
	smallest := canvas.Right - canvas.Left + 1
	if h := canvas.Top - canvas.Bottom + 1; h < smallest {
		smallest = h
	}
	ratio := e.Config.BeamWidthRatio
	if ratio < 1 {
		ratio = 1
	}
	e.illuminateRange = max5(1, min5(int(float64(smallest)/ratio), smallest))

	minDist := max5(1, (canvas.Right-canvas.Left+1)/4)

	// Build SpotlightMovers with bezier search paths.
	e.movers = make([]*tte.SpotlightMover, e.Config.SpotlightCount)
	for i := range e.movers {
		m := tte.NewSpotlightMover(canvas.RandomCoord(true, false))

		// 10 waypoints, each bezier-curved through an outside-scope control point.
		last := canvas.RandomCoord(false, false)
		for range 10 {
			dest := spotlightMinDistCoord(canvas, last, minDist)
			ctrl := canvas.RandomCoord(true, false)
			speed := e.Config.SearchSpeedMin + rand.Float64()*(e.Config.SearchSpeedMax-e.Config.SearchSpeedMin)
			m.AddSearchPath(dest, ctrl, speed)
			last = dest
		}
		m.SetCenterPath(canvas.TextCenter)
		m.StartSearch()
		e.movers[i] = m
	}
}

// illuminateChars updates character colours based on current spotlight positions.
func (e *SpotlightsEffect) illuminateChars() {
	r := e.illuminateRange
	falloff := e.Config.BeamFalloff

	// Collect illuminated coords using FindCoordsInCircle (ellipse formula).
	illuminated := make(map[tte.Coord]bool)
	for _, m := range e.movers {
		spCoord := m.CurrentCoord().ToCoord()
		for _, c := range tte.FindCoordsInCircle(spCoord, r) {
			illuminated[c] = true
		}
	}

	for _, ch := range e.terminal.GetCharacters() {
		coord := ch.InputCoord()
		if e.phase == "done" {
			ch.Animation.SetAppearance(ch.InputSymbol(), tte.FGOnly(e.brightMap[coord]))
			continue
		}
		if !illuminated[coord] {
			ch.Animation.SetAppearance(ch.InputSymbol(), tte.FGOnly(e.dimMap[coord]))
			continue
		}
		// Find minimum distance to any spotlight (double_row_diff).
		minD := math.MaxFloat64
		for _, m := range e.movers {
			spCoord := m.CurrentCoord().ToCoord()
			dc := float64(coord.Column - spCoord.Column)
			dr := float64(coord.Row-spCoord.Row) * 2
			if d := math.Sqrt(dc*dc + dr*dr); d < minD {
				minD = d
			}
		}
		// Apply falloff dimming at beam edge.
		fr := float64(r)
		falloffStart := fr * (1 - falloff)
		var color tte.Color
		if minD > falloffStart && falloff > 0 {
			bf := minD - falloffStart
			bfRange := fr * falloff
			brightness := 1.0 - (bf / bfRange)
			if brightness < 0.2 {
				brightness = 0.2
			}
			color = tte.AdjustColorBrightness(e.brightMap[coord], brightness)
		} else {
			color = e.brightMap[coord]
		}
		ch.Animation.SetAppearance(ch.InputSymbol(), tte.FGOnly(color))
	}
}

func (e *SpotlightsEffect) Next() (string, bool) {
	e.tick++
	canvas := e.terminal.Canvas.Canvas

	switch e.phase {
	case "search":
		for _, m := range e.movers {
			m.Advance()
		}
		if e.tick >= e.Config.SearchDuration {
			for _, m := range e.movers {
				m.StartConverge()
			}
			e.phase = "converge"
		}

	case "converge":
		allDone := true
		for _, m := range e.movers {
			m.Advance()
			if !m.IsConvergeComplete() {
				allDone = false
			}
		}
		if allDone {
			// Keep only first spotlight, expand beam.
			e.movers = e.movers[:1]
			e.phase = "expand"
		}

	case "expand":
		e.illuminateRange++
		maxDim := max5(canvas.Right-canvas.Left+1, canvas.Top-canvas.Bottom+1)
		if e.illuminateRange > maxDim*2/3 {
			e.phase = "done"
		}

	case "done":
		e.illuminateChars()
		e.doneTick++
		return e.terminal.GetFormattedOutputString(), e.doneTick > 30
	}

	e.illuminateChars()
	return e.terminal.GetFormattedOutputString(), false
}

func (e *SpotlightsEffect) Reset() { e.tick = 0; e.phase = "search"; e.doneTick = 0 }

// ── 4. SprayEffect ────────────────────────────────────────────────────────────

type SprayConfig struct {
	SprayOrigin            string // "center","north","south","east","west"
	SprayVolume            float64
	FinalGradientStops     []tte.Color
	FinalGradientSteps     []int
	FinalGradientDirection tte.GradientDirection
}

func DefaultSprayConfig() SprayConfig {
	return SprayConfig{
		SprayOrigin: "center",
		SprayVolume: 0.005,
		FinalGradientStops: []tte.Color{
			tte.MustColorFromHex("8A008A"),
			tte.MustColorFromHex("00D1FF"),
			tte.MustColorFromHex("FFFFFF"),
		},
		FinalGradientSteps:     []int{12},
		FinalGradientDirection: tte.GradientDiagonal,
	}
}

type SprayEffect struct {
	Config       SprayConfig
	base         *tte.BaseIterator
	terminal     *tte.Terminal
	charsPerTick int
}

func NewSprayEffect(cfg SprayConfig) *SprayEffect { return &SprayEffect{Config: cfg} }
func (e *SprayEffect) Name() string               { return "spray" }
func (e *SprayEffect) Init(t *tte.Terminal) {
	e.terminal = t
	e.base = tte.NewBaseIterator(t)
	canvas := t.Canvas.Canvas
	grad := tte.NewGradient(e.Config.FinalGradientStops, e.Config.FinalGradientSteps)
	colorMap := grad.BuildCoordinateColorMapping(canvas.TextBottom, canvas.TextTop, canvas.TextLeft, canvas.TextRight, e.Config.FinalGradientDirection)

	var origin tte.Coord
	switch e.Config.SprayOrigin {
	case "north":
		origin = tte.Coord{Column: canvas.TextCenter.Column, Row: canvas.Top}
	case "south":
		origin = tte.Coord{Column: canvas.TextCenter.Column, Row: canvas.Bottom}
	case "east":
		origin = tte.Coord{Column: canvas.Right, Row: canvas.TextCenter.Row}
	case "west":
		origin = tte.Coord{Column: canvas.Left, Row: canvas.TextCenter.Row}
	default:
		origin = canvas.TextCenter
	}

	chars := t.GetCharacters()
	e.charsPerTick = max5(1, int(e.Config.SprayVolume*float64(len(chars))))
	rand.Shuffle(len(chars), func(i, j int) { chars[i], chars[j] = chars[j], chars[i] })

	for _, ch := range chars {
		finalColor := colorMap[ch.InputCoord()]
		sprayScn := ch.Animation.NewScene("spray", false, tte.SyncNone, nil)
		sprayGrad := tte.NewGradient([]tte.Color{e.Config.FinalGradientStops[0], finalColor}, []int{6})
		sprayScn.ApplyGradientToSymbols([]string{ch.InputSymbol()}, 4, sprayGrad, nil)
		sprayScn.AddFrame(ch.InputSymbol(), 1, tte.FGOnly(finalColor))

		ch.Motion.SetCoordinate(origin)
		path := ch.Motion.NewPath("spray", 0.8, tte.EaseOutCubic, 0, false)
		path.NewWaypoint(ch.InputCoord(), "home")
		ch.EventHandler.RegisterEvent(tte.EventPathActivated, path, tte.ActionActivateScene, sprayScn)
		t.SetCharacterVisibility(ch, true)
		ch.Motion.ActivatePath(path, ch.EventHandler)
	}
	e.base.PendingCharacters = chars
}
func (e *SprayEffect) Next() (string, bool) {
	for i := 0; i < e.charsPerTick && len(e.base.PendingCharacters) > 0; i++ {
		ch := e.base.PendingCharacters[0]
		e.base.PendingCharacters = e.base.PendingCharacters[1:]
		e.base.Activate(ch)
	}
	e.base.Update()
	return e.base.Frame(), !e.base.HasWork()
}
func (e *SprayEffect) Reset() {}

// ── 5. SwarmEffect ────────────────────────────────────────────────────────────

type SwarmConfig struct {
	SwarmCount             int
	FinalGradientStops     []tte.Color
	FinalGradientSteps     []int
	FinalGradientDirection tte.GradientDirection
}

func DefaultSwarmConfig() SwarmConfig {
	return SwarmConfig{
		SwarmCount: 3,
		FinalGradientStops: []tte.Color{
			tte.MustColorFromHex("8A008A"),
			tte.MustColorFromHex("00D1FF"),
			tte.MustColorFromHex("FFFFFF"),
		},
		FinalGradientSteps:     []int{12},
		FinalGradientDirection: tte.GradientRadial,
	}
}

type SwarmEffect struct {
	Config   SwarmConfig
	base     *tte.BaseIterator
	terminal *tte.Terminal
}

func NewSwarmEffect(cfg SwarmConfig) *SwarmEffect { return &SwarmEffect{Config: cfg} }
func (e *SwarmEffect) Name() string               { return "swarm" }
func (e *SwarmEffect) Init(t *tte.Terminal) {
	e.terminal = t
	e.base = tte.NewBaseIterator(t)
	canvas := t.Canvas.Canvas
	grad := tte.NewGradient(e.Config.FinalGradientStops, e.Config.FinalGradientSteps)
	colorMap := grad.BuildCoordinateColorMapping(canvas.TextBottom, canvas.TextTop, canvas.TextLeft, canvas.TextRight, e.Config.FinalGradientDirection)

	swarmColors := []tte.Color{
		tte.MustColorFromHex("ff6600"),
		tte.MustColorFromHex("cc00ff"),
		tte.MustColorFromHex("00ccff"),
		tte.MustColorFromHex("ffcc00"),
	}

	// build cluster centres
	count := max(e.Config.SwarmCount, 1)
	centres := make([]tte.Coord, count)
	colStep := max5(1, (canvas.TextRight-canvas.TextLeft)/(count+1))
	for i := 0; i < count; i++ {
		col := min(canvas.TextLeft+colStep*(i+1), canvas.TextRight)
		centres[i] = tte.Coord{Column: col, Row: canvas.TextCenter.Row}
	}

	chars := t.GetCharacters()
	rand.Shuffle(len(chars), func(i, j int) { chars[i], chars[j] = chars[j], chars[i] })

	for i, ch := range chars {
		clusterIdx := i % count
		centre := centres[clusterIdx]
		clusterColor := swarmColors[clusterIdx%len(swarmColors)]
		finalColor := colorMap[ch.InputCoord()]

		jitter := tte.Coord{
			Column: clamp(centre.Column+rand.Intn(9)-4, canvas.Left, canvas.Right),
			Row:    clamp(centre.Row+rand.Intn(5)-2, canvas.Bottom, canvas.Top),
		}

		swarmScn := ch.Animation.NewScene("swarm", true, tte.SyncNone, nil)
		for _, sym := range []string{"*", "·", "•"} {
			swarmScn.AddFrame(sym, 2, tte.FGOnly(clusterColor))
		}

		landScn := ch.Animation.NewScene("land", false, tte.SyncNone, nil)
		landGrad := tte.NewGradient([]tte.Color{clusterColor, finalColor}, []int{6})
		landScn.ApplyGradientToSymbols([]string{ch.InputSymbol()}, 3, landGrad, nil)
		landScn.AddFrame(ch.InputSymbol(), 1, tte.FGOnly(finalColor))

		flyPath := ch.Motion.NewPath("fly", 0.5, tte.EaseInOutQuad, 0, false)
		flyPath.NewWaypoint(jitter, "cluster")

		landPath := ch.Motion.NewPath("land", 0.4, tte.EaseOutExpo, 0, false)
		landPath.NewWaypoint(ch.InputCoord(), "home")

		eh := ch.EventHandler
		ch.Motion.ChainPaths(eh, []*tte.Path{flyPath, landPath}, false)
		ch.Motion.SetCoordinate(canvas.RandomCoord(true, false))
		eh.RegisterEvent(tte.EventPathActivated, flyPath, tte.ActionActivateScene, swarmScn)
		eh.RegisterEvent(tte.EventPathComplete, landPath, tte.ActionActivateScene, landScn)
		t.SetCharacterVisibility(ch, true)
		ch.Motion.ActivatePath(flyPath, eh)
		e.base.PendingCharacters = append(e.base.PendingCharacters, ch)
	}
}
func (e *SwarmEffect) Next() (string, bool) {
	for i := 0; i < 2 && len(e.base.PendingCharacters) > 0; i++ {
		ch := e.base.PendingCharacters[0]
		e.base.PendingCharacters = e.base.PendingCharacters[1:]
		e.base.Activate(ch)
	}
	e.base.Update()
	return e.base.Frame(), !e.base.HasWork()
}
func (e *SwarmEffect) Reset() {}

// ── 6. SweepEffect ────────────────────────────────────────────────────────────

type SweepConfig struct {
	SweepSpeed             int
	FinalGradientStops     []tte.Color
	FinalGradientSteps     []int
	FinalGradientDirection tte.GradientDirection
}

func DefaultSweepConfig() SweepConfig {
	return SweepConfig{
		SweepSpeed: 1,
		FinalGradientStops: []tte.Color{
			tte.MustColorFromHex("8A008A"),
			tte.MustColorFromHex("00D1FF"),
			tte.MustColorFromHex("FFFFFF"),
		},
		FinalGradientSteps:     []int{12},
		FinalGradientDirection: tte.GradientVertical,
	}
}

type SweepEffect struct {
	Config      SweepConfig
	base        *tte.BaseIterator
	terminal    *tte.Terminal
	colGroups   [][]*tte.EffectCharacter
	colIdx      int
	revealScns  map[int]*tte.Scene
	colorScns   map[int]*tte.Scene
	tickCounter int
}

func NewSweepEffect(cfg SweepConfig) *SweepEffect { return &SweepEffect{Config: cfg} }
func (e *SweepEffect) Name() string               { return "sweep" }
func (e *SweepEffect) Init(t *tte.Terminal) {
	e.terminal = t
	e.base = tte.NewBaseIterator(t)
	e.revealScns = make(map[int]*tte.Scene)
	e.colorScns = make(map[int]*tte.Scene)
	e.colIdx = 0
	e.tickCounter = 0
	canvas := t.Canvas.Canvas

	grad := tte.NewGradient(e.Config.FinalGradientStops, e.Config.FinalGradientSteps)
	colorMap := grad.BuildCoordinateColorMapping(canvas.TextBottom, canvas.TextTop, canvas.TextLeft, canvas.TextRight, e.Config.FinalGradientDirection)

	white := tte.MustColorFromHex("ffffff")
	colMap := make(map[int][]*tte.EffectCharacter)
	for _, ch := range t.GetCharacters() {
		finalColor := colorMap[ch.InputCoord()]

		revealScn := ch.Animation.NewScene("reveal", false, tte.SyncNone, nil)
		for _, sym := range []string{"▉", "▊", "▋", "▌"} {
			revealScn.AddFrame(sym, 2, tte.FGOnly(tte.AdjustColorBrightness(white, 0.8)))
		}
		revealScn.AddFrame(ch.InputSymbol(), 1, tte.FGOnly(white))

		colorScn := ch.Animation.NewScene("color", false, tte.SyncNone, nil)
		colorGrad := tte.NewGradient([]tte.Color{white, finalColor}, []int{6})
		colorScn.ApplyGradientToSymbols([]string{ch.InputSymbol()}, 3, colorGrad, nil)
		colorScn.AddFrame(ch.InputSymbol(), 1, tte.FGOnly(finalColor))

		ch.EventHandler.RegisterEvent(tte.EventSceneComplete, revealScn, tte.ActionActivateScene, colorScn)
		e.revealScns[ch.ID()] = revealScn
		e.colorScns[ch.ID()] = colorScn
		colMap[ch.InputCoord().Column] = append(colMap[ch.InputCoord().Column], ch)
	}

	cols := make([]int, 0, len(colMap))
	for c := range colMap {
		cols = append(cols, c)
	}
	sort.Ints(cols)
	e.colGroups = make([][]*tte.EffectCharacter, len(cols))
	for i, c := range cols {
		e.colGroups[i] = colMap[c]
	}
}
func (e *SweepEffect) Next() (string, bool) {
	e.tickCounter++
	if e.tickCounter >= e.Config.SweepSpeed && e.colIdx < len(e.colGroups) {
		e.tickCounter = 0
		for _, ch := range e.colGroups[e.colIdx] {
			ch.Animation.ActivateScene(e.revealScns[ch.ID()])
			e.base.Activate(ch)
			e.terminal.SetCharacterVisibility(ch, true)
		}
		e.colIdx++
	}
	e.base.Update()
	done := e.colIdx >= len(e.colGroups) && !e.base.HasWork()
	return e.base.Frame(), done
}
func (e *SweepEffect) Reset() { e.colIdx = 0; e.tickCounter = 0 }
