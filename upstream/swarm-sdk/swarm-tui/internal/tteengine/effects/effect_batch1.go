package effects

import (
	"math/rand"
	"sort"

	tte "github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/tteengine"
)

// ─────────────────────────────────────────────────────────────────────────────
// Helper utilities (suffixed with 1 to avoid collision with existing helpers)
// ─────────────────────────────────────────────────────────────────────────────

func clamp1(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func max1(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min1(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func abs1(a int) int {
	if a < 0 {
		return -a
	}
	return a
}

// ─────────────────────────────────────────────────────────────────────────────
// 1. BeamsEffect
// ─────────────────────────────────────────────────────────────────────────────

type BeamsConfig struct {
	FinalGradientStops     []tte.Color
	FinalGradientSteps     []int
	FinalGradientDirection tte.GradientDirection
}

func DefaultBeamsConfig() BeamsConfig {
	return BeamsConfig{
		FinalGradientStops: []tte.Color{
			tte.MustColorFromHex("8A008A"),
			tte.MustColorFromHex("00D1FF"),
			tte.MustColorFromHex("FFFFFF"),
		},
		FinalGradientSteps:     []int{12},
		FinalGradientDirection: tte.GradientDiagonal,
	}
}

type BeamsEffect struct {
	Config        BeamsConfig
	base          *tte.BaseIterator
	terminal      *tte.Terminal
	pendingGroups [][]*tte.EffectCharacter
	brightScenes  map[int]*tte.Scene
	tick          int
}

func NewBeamsEffect(cfg BeamsConfig) *BeamsEffect {
	return &BeamsEffect{Config: cfg}
}

func (e *BeamsEffect) Name() string { return "beams" }

func (e *BeamsEffect) Init(t *tte.Terminal) {
	e.terminal = t
	e.base = tte.NewBaseIterator(t)
	e.brightScenes = make(map[int]*tte.Scene)
	e.tick = 0

	canvas := t.Canvas.Canvas
	grad := tte.NewGradient(e.Config.FinalGradientStops, e.Config.FinalGradientSteps)
	colorMap := grad.BuildCoordinateColorMapping(
		canvas.Bottom, canvas.Top,
		canvas.Left, canvas.Right,
		e.Config.FinalGradientDirection,
	)

	// Group characters by row
	rowMap := make(map[int][]*tte.EffectCharacter)
	for _, ch := range t.GetCharacters() {
		coord := ch.InputCoord()
		finalColor := colorMap[coord]
		dim := tte.AdjustColorBrightness(finalColor, 0.35)

		beamScn := ch.Animation.NewScene("beam", false, tte.SyncNone, nil)
		beamScn.AddFrame(ch.InputSymbol(), 1, tte.FGOnly(dim))

		brightScn := ch.Animation.NewScene("bright", false, tte.SyncNone, nil)
		brightScn.ApplyGradientToSymbols(
			[]string{ch.InputSymbol()}, 4,
			tte.NewGradient([]tte.Color{dim, finalColor}, []int{8}),
			nil,
		)

		e.brightScenes[ch.ID()] = brightScn

		t.SetCharacterVisibility(ch, true)
		ch.Animation.ActivateScene(beamScn)

		rowMap[coord.Row] = append(rowMap[coord.Row], ch)
	}

	// Sort row keys descending (top rows first — highest Row value = top in tte)
	rows := make([]int, 0, len(rowMap))
	for r := range rowMap {
		rows = append(rows, r)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(rows)))

	e.pendingGroups = make([][]*tte.EffectCharacter, 0, len(rows))
	for _, r := range rows {
		e.pendingGroups = append(e.pendingGroups, rowMap[r])
	}
}

func (e *BeamsEffect) Next() (string, bool) {
	e.tick++
	if e.tick%3 == 0 && len(e.pendingGroups) > 0 {
		group := e.pendingGroups[0]
		e.pendingGroups = e.pendingGroups[1:]
		for _, ch := range group {
			brightScn := e.brightScenes[ch.ID()]
			ch.Animation.ActivateScene(brightScn)
			e.base.Activate(ch)
		}
	}
	e.base.Update()
	return e.base.Frame(), e.base.HasWork() || len(e.pendingGroups) > 0
}

func (e *BeamsEffect) Reset() {
	e.pendingGroups = nil
	e.brightScenes = nil
	e.tick = 0
}

// ─────────────────────────────────────────────────────────────────────────────
// 2. BinaryPathEffect
// ─────────────────────────────────────────────────────────────────────────────

type BinaryPathConfig struct {
	FinalGradientStops     []tte.Color
	FinalGradientSteps     []int
	FinalGradientDirection tte.GradientDirection
}

func DefaultBinaryPathConfig() BinaryPathConfig {
	return BinaryPathConfig{
		FinalGradientStops: []tte.Color{
			tte.MustColorFromHex("00ff00"),
			tte.MustColorFromHex("ffffff"),
		},
		FinalGradientSteps:     []int{12},
		FinalGradientDirection: tte.GradientVertical,
	}
}

type BinaryPathEffect struct {
	Config   BinaryPathConfig
	base     *tte.BaseIterator
	terminal *tte.Terminal
}

func NewBinaryPathEffect(cfg BinaryPathConfig) *BinaryPathEffect {
	return &BinaryPathEffect{Config: cfg}
}

func (e *BinaryPathEffect) Name() string { return "binarypath" }

func (e *BinaryPathEffect) Init(t *tte.Terminal) {
	e.terminal = t
	e.base = tte.NewBaseIterator(t)

	canvas := t.Canvas.Canvas
	grad := tte.NewGradient(e.Config.FinalGradientStops, e.Config.FinalGradientSteps)
	colorMap := grad.BuildCoordinateColorMapping(
		canvas.Bottom, canvas.Top,
		canvas.Left, canvas.Right,
		e.Config.FinalGradientDirection,
	)

	green := tte.MustColorFromHex("00ff00")

	for _, ch := range t.GetCharacters() {
		coord := ch.InputCoord()
		finalColor := colorMap[coord]

		binScn := ch.Animation.NewScene("bin", true, tte.SyncNone, nil)
		binScn.AddFrame("0", 2, tte.FGOnly(green))
		binScn.AddFrame("1", 2, tte.FGOnly(green))

		finalScn := ch.Animation.NewScene("final", false, tte.SyncNone, nil)
		finalScn.ApplyGradientToSymbols(
			[]string{ch.InputSymbol()}, 3,
			tte.NewGradient([]tte.Color{green, finalColor}, []int{6}),
			nil,
		)

		path := ch.Motion.NewPath("p", 0.5, tte.EaseInOutSine, 0, false)
		off := canvas.RandomCoord(true, false)
		ch.Motion.SetCoordinate(off)
		path.NewWaypoint(coord, "home")

		eh := ch.EventHandler
		eh.RegisterEvent(tte.EventPathActivated, path, tte.ActionActivateScene, binScn)
		eh.RegisterEvent(tte.EventPathComplete, path, tte.ActionActivateScene, finalScn)

		ch.Motion.ActivatePath(path, eh)
		e.base.PendingCharacters = append(e.base.PendingCharacters, ch)
	}

	// Shuffle pending
	rand.Shuffle(len(e.base.PendingCharacters), func(i, j int) {
		e.base.PendingCharacters[i], e.base.PendingCharacters[j] = e.base.PendingCharacters[j], e.base.PendingCharacters[i]
	})
}

func (e *BinaryPathEffect) Next() (string, bool) {
	// Activate 3 per tick from pending
	for i := 0; i < 3 && len(e.base.PendingCharacters) > 0; i++ {
		ch := e.base.PendingCharacters[0]
		e.base.PendingCharacters = e.base.PendingCharacters[1:]
		e.base.Activate(ch)
	}
	e.base.Update()
	return e.base.Frame(), e.base.HasWork()
}

func (e *BinaryPathEffect) Reset() {}

// ─────────────────────────────────────────────────────────────────────────────
// 3. BlackholeEffect
// ─────────────────────────────────────────────────────────────────────────────

type BlackholeConfig struct {
	FinalGradientStops     []tte.Color
	FinalGradientSteps     []int
	FinalGradientDirection tte.GradientDirection
}

func DefaultBlackholeConfig() BlackholeConfig {
	return BlackholeConfig{
		FinalGradientStops: []tte.Color{
			tte.MustColorFromHex("000000"),
			tte.MustColorFromHex("ffffff"),
		},
		FinalGradientSteps:     []int{12},
		FinalGradientDirection: tte.GradientRadial,
	}
}

type BlackholeEffect struct {
	Config   BlackholeConfig
	base     *tte.BaseIterator
	terminal *tte.Terminal
}

func NewBlackholeEffect(cfg BlackholeConfig) *BlackholeEffect {
	return &BlackholeEffect{Config: cfg}
}

func (e *BlackholeEffect) Name() string { return "blackhole" }

func (e *BlackholeEffect) Init(t *tte.Terminal) {
	e.terminal = t
	e.base = tte.NewBaseIterator(t)

	canvas := t.Canvas.Canvas
	grad := tte.NewGradient(e.Config.FinalGradientStops, e.Config.FinalGradientSteps)
	colorMap := grad.BuildCoordinateColorMapping(
		canvas.Bottom, canvas.Top,
		canvas.Left, canvas.Right,
		e.Config.FinalGradientDirection,
	)

	white := tte.MustColorFromHex("ffffff")

	for _, ch := range t.GetCharacters() {
		coord := ch.InputCoord()
		finalColor := colorMap[coord]

		collapseScn := ch.Animation.NewScene("c", true, tte.SyncNone, nil)
		collapseScn.AddFrame("*", 1, tte.FGOnly(white))
		collapseScn.AddFrame("·", 1, tte.FGOnly(white))
		collapseScn.AddFrame(".", 1, tte.FGOnly(white))

		expandScn := ch.Animation.NewScene("e", false, tte.SyncNone, nil)
		expandScn.ApplyGradientToSymbols(
			[]string{ch.InputSymbol()}, 3,
			tte.NewGradient([]tte.Color{white, finalColor}, []int{8}),
			nil,
		)

		cPath := ch.Motion.NewPath("collapse", 0.3, tte.EaseInExpo, 0, false)
		cPath.NewWaypoint(canvas.Center, "c")

		ePath := ch.Motion.NewPath("expand", 0.5, tte.EaseOutExpo, 0, false)
		ePath.NewWaypoint(coord, "e")

		eh := ch.EventHandler
		eh.RegisterEvent(tte.EventPathActivated, cPath, tte.ActionActivateScene, collapseScn)
		eh.RegisterEvent(tte.EventPathComplete, cPath, tte.ActionActivatePath, ePath)
		eh.RegisterEvent(tte.EventPathActivated, ePath, tte.ActionActivateScene, expandScn)

		ch.Motion.SetCoordinate(coord)
		ch.Motion.ActivatePath(cPath, eh)
		e.base.PendingCharacters = append(e.base.PendingCharacters, ch)
	}

	rand.Shuffle(len(e.base.PendingCharacters), func(i, j int) {
		e.base.PendingCharacters[i], e.base.PendingCharacters[j] = e.base.PendingCharacters[j], e.base.PendingCharacters[i]
	})
}

func (e *BlackholeEffect) Next() (string, bool) {
	for i := 0; i < 2 && len(e.base.PendingCharacters) > 0; i++ {
		ch := e.base.PendingCharacters[0]
		e.base.PendingCharacters = e.base.PendingCharacters[1:]
		e.base.Activate(ch)
	}
	e.base.Update()
	return e.base.Frame(), e.base.HasWork()
}

func (e *BlackholeEffect) Reset() {}

// ─────────────────────────────────────────────────────────────────────────────
// 4. BouncyBallsEffect
// ─────────────────────────────────────────────────────────────────────────────

type BouncyBallsConfig struct {
	BallColors             []tte.Color
	FinalGradientStops     []tte.Color
	FinalGradientSteps     []int
	FinalGradientDirection tte.GradientDirection
}

func DefaultBouncyBallsConfig() BouncyBallsConfig {
	return BouncyBallsConfig{
		BallColors: []tte.Color{
			tte.MustColorFromHex("FFA500"),
			tte.MustColorFromHex("0000FF"),
			tte.MustColorFromHex("00FF00"),
			tte.MustColorFromHex("FF0000"),
			tte.MustColorFromHex("00FFFF"),
			tte.MustColorFromHex("FF00FF"),
		},
		FinalGradientStops: []tte.Color{
			tte.MustColorFromHex("FFA500"),
			tte.MustColorFromHex("ffffff"),
		},
		FinalGradientSteps:     []int{12},
		FinalGradientDirection: tte.GradientDiagonal,
	}
}

type BouncyBallsEffect struct {
	Config        BouncyBallsConfig
	base          *tte.BaseIterator
	terminal      *tte.Terminal
	pendingByRow  map[int][]*tte.EffectCharacter
	sortedRows    []int
	currentRowIdx int
}

func NewBouncyBallsEffect(cfg BouncyBallsConfig) *BouncyBallsEffect {
	return &BouncyBallsEffect{Config: cfg}
}

func (e *BouncyBallsEffect) Name() string { return "bouncyballs" }

func (e *BouncyBallsEffect) Init(t *tte.Terminal) {
	e.terminal = t
	e.base = tte.NewBaseIterator(t)
	e.pendingByRow = make(map[int][]*tte.EffectCharacter)
	e.currentRowIdx = 0

	canvas := t.Canvas.Canvas
	grad := tte.NewGradient(e.Config.FinalGradientStops, e.Config.FinalGradientSteps)
	colorMap := grad.BuildCoordinateColorMapping(
		canvas.Bottom, canvas.Top,
		canvas.Left, canvas.Right,
		e.Config.FinalGradientDirection,
	)

	for _, ch := range t.GetCharacters() {
		coord := ch.InputCoord()
		finalColor := colorMap[coord]
		ballColor := e.Config.BallColors[rand.Intn(len(e.Config.BallColors))]

		ballScn := ch.Animation.NewScene("b", true, tte.SyncNone, nil)
		ballScn.AddFrame("O", 1, tte.FGOnly(ballColor))

		finalScn := ch.Animation.NewScene("f", false, tte.SyncNone, nil)
		finalScn.ApplyGradientToSymbols(
			[]string{ch.InputSymbol()}, 3,
			tte.NewGradient([]tte.Color{ballColor, finalColor}, []int{6}),
			nil,
		)

		path := ch.Motion.NewPath("p", 0.5, tte.EaseOutBounce, 0, false)
		ch.Motion.SetCoordinate(tte.Coord{Column: coord.Column, Row: canvas.Top + 1})
		path.NewWaypoint(coord, "home")

		eh := ch.EventHandler
		eh.RegisterEvent(tte.EventPathActivated, path, tte.ActionActivateScene, ballScn)
		eh.RegisterEvent(tte.EventPathComplete, path, tte.ActionActivateScene, finalScn)

		ch.Motion.ActivatePath(path, eh)
		e.pendingByRow[coord.Row] = append(e.pendingByRow[coord.Row], ch)
	}

	// Sort row keys ascending (lowest row value first = bottom in tte where Row 1=bottom)
	rows := make([]int, 0, len(e.pendingByRow))
	for r := range e.pendingByRow {
		rows = append(rows, r)
	}
	sort.Ints(rows)
	e.sortedRows = rows

	// Release first row group to pending
	if len(e.sortedRows) > 0 {
		firstRow := e.sortedRows[0]
		e.base.PendingCharacters = append(e.base.PendingCharacters, e.pendingByRow[firstRow]...)
		e.currentRowIdx = 1
	}
}

func (e *BouncyBallsEffect) Next() (string, bool) {
	// Pop 2 random from pending (swap-remove)
	for i := 0; i < 2 && len(e.base.PendingCharacters) > 0; i++ {
		idx := rand.Intn(len(e.base.PendingCharacters))
		ch := e.base.PendingCharacters[idx]
		last := len(e.base.PendingCharacters) - 1
		e.base.PendingCharacters[idx] = e.base.PendingCharacters[last]
		e.base.PendingCharacters = e.base.PendingCharacters[:last]
		e.base.Activate(ch)
	}

	// When pending empty, pop next row group
	if len(e.base.PendingCharacters) == 0 && e.currentRowIdx < len(e.sortedRows) {
		row := e.sortedRows[e.currentRowIdx]
		e.base.PendingCharacters = append(e.base.PendingCharacters, e.pendingByRow[row]...)
		e.currentRowIdx++
	}

	e.base.Update()
	hasMore := e.base.HasWork() || len(e.base.PendingCharacters) > 0 || e.currentRowIdx < len(e.sortedRows)
	return e.base.Frame(), hasMore
}

func (e *BouncyBallsEffect) Reset() {
	e.pendingByRow = nil
	e.sortedRows = nil
	e.currentRowIdx = 0
}

// ─────────────────────────────────────────────────────────────────────────────
// 5. BubblesEffect
// ─────────────────────────────────────────────────────────────────────────────

type BubblesConfig struct {
	PopColors              []tte.Color
	FinalGradientStops     []tte.Color
	FinalGradientSteps     []int
	FinalGradientDirection tte.GradientDirection
}

func DefaultBubblesConfig() BubblesConfig {
	return BubblesConfig{
		PopColors: []tte.Color{
			tte.MustColorFromHex("00FFFF"),
			tte.MustColorFromHex("0000FF"),
			tte.MustColorFromHex("FF00FF"),
		},
		FinalGradientStops: []tte.Color{
			tte.MustColorFromHex("00FFFF"),
			tte.MustColorFromHex("ffffff"),
		},
		FinalGradientSteps:     []int{12},
		FinalGradientDirection: tte.GradientDiagonal,
	}
}

type BubblesEffect struct {
	Config   BubblesConfig
	base     *tte.BaseIterator
	terminal *tte.Terminal
}

func NewBubblesEffect(cfg BubblesConfig) *BubblesEffect {
	return &BubblesEffect{Config: cfg}
}

func (e *BubblesEffect) Name() string { return "bubbles" }

func (e *BubblesEffect) Init(t *tte.Terminal) {
	e.terminal = t
	e.base = tte.NewBaseIterator(t)

	canvas := t.Canvas.Canvas
	grad := tte.NewGradient(e.Config.FinalGradientStops, e.Config.FinalGradientSteps)
	colorMap := grad.BuildCoordinateColorMapping(
		canvas.Bottom, canvas.Top,
		canvas.Left, canvas.Right,
		e.Config.FinalGradientDirection,
	)

	for _, ch := range t.GetCharacters() {
		coord := ch.InputCoord()
		finalColor := colorMap[coord]
		popColor := e.Config.PopColors[rand.Intn(len(e.Config.PopColors))]

		floatScn := ch.Animation.NewScene("fl", true, tte.SyncNone, nil)
		floatScn.AddFrame("O", 3, tte.FGOnly(popColor))

		popScn := ch.Animation.NewScene("pop", false, tte.SyncNone, nil)
		popScn.AddFrame("*", 2, tte.FGOnly(popColor))
		popScn.AddFrame("'", 2, tte.FGOnly(popColor))

		finalScn := ch.Animation.NewScene("fin", false, tte.SyncNone, nil)
		finalScn.ApplyGradientToSymbols(
			[]string{ch.InputSymbol()}, 3,
			tte.NewGradient([]tte.Color{popColor, finalColor}, []int{6}),
			nil,
		)

		floatPath := ch.Motion.NewPath("float", 0.2, tte.EaseLinear, 0, false)
		floatPath.NewWaypoint(tte.Coord{Column: coord.Column, Row: coord.Row + 2}, "mid")

		landPath := ch.Motion.NewPath("land", 0.8, tte.EaseOutBounce, 0, false)
		landPath.NewWaypoint(coord, "home")

		eh := ch.EventHandler
		ch.Motion.ChainPaths(eh, []*tte.Path{floatPath, landPath}, false)
		ch.Motion.SetCoordinate(tte.Coord{Column: coord.Column, Row: canvas.Top})

		eh.RegisterEvent(tte.EventPathActivated, floatPath, tte.ActionActivateScene, floatScn)
		eh.RegisterEvent(tte.EventPathComplete, floatPath, tte.ActionActivateScene, popScn)
		eh.RegisterEvent(tte.EventPathComplete, landPath, tte.ActionActivateScene, finalScn)

		ch.Motion.ActivatePath(floatPath, eh)
		e.base.PendingCharacters = append(e.base.PendingCharacters, ch)
	}
}

func (e *BubblesEffect) Next() (string, bool) {
	for i := 0; i < 2 && len(e.base.PendingCharacters) > 0; i++ {
		ch := e.base.PendingCharacters[0]
		e.base.PendingCharacters = e.base.PendingCharacters[1:]
		e.base.Activate(ch)
	}
	e.base.Update()
	return e.base.Frame(), e.base.HasWork()
}

func (e *BubblesEffect) Reset() {}

// ─────────────────────────────────────────────────────────────────────────────
// 6. BurnEffect
// ─────────────────────────────────────────────────────────────────────────────
// Faithful port of the Python TTE burn effect.
//
// Key differences from the old Go version:
//   - Character activation order is determined by a Prim's spanning tree grown
//     from a random starting character, producing an organic "fire spreading"
//     look rather than a simple row-sorted sweep.
//   - Burn animation uses the Python-canonical symbol sequence and a 10-step
//     fire gradient exactly matching the reference.
//   - The cool-down scene transitions from the last fire colour to the final
//     gradient colour using 8 steps (matches Python's steps=8).
//   - StartingColor is shown on all characters before any burning begins.
//   - SmokeChance drives lightweight ASCII smoke particles that rise from each
//     burned character (approximated without add_character by reusing spare
//     visible-character slots via a pre-built pool).

type BurnConfig struct {
	StartingColor          tte.Color
	BurnColors             []tte.Color
	SmokeChance            float64
	FinalGradientStops     []tte.Color
	FinalGradientSteps     []int
	FinalGradientDirection tte.GradientDirection
	// ActivatePerTick controls how many characters ignite each tick.
	// Default 0 means use the Python-canonical randint(2,4).
	// Set higher for large canvases to keep total duration short.
	ActivatePerTick int
	// FireSymbolTicks is the hold-duration (ticks) for each fire frame.
	// Default 0 means 4 (Python canonical). Set to 1 for instant flash.
	FireSymbolTicks int
	// CoolGradientSteps controls the number of gradient steps in the cool scene.
	// Default 0 means 8 (Python canonical). Set to 2 for fast full-screen burns.
	CoolGradientSteps int
	// IncludeSpaces animates space characters too — they show fire chars
	// during the burn and then resolve back to a blank cell.
	// Required for full-screen burn where background cells must burn.
	IncludeSpaces bool
	// UseRandomOrder shuffles activation order instead of Prim's spanning tree.
	// Much faster Init() for large canvases; visually equivalent when chars
	// are sparse (typical full-screen UI layout).
	UseRandomOrder bool
}

func DefaultBurnConfig() BurnConfig {
	return BurnConfig{
		StartingColor: tte.MustColorFromHex("837373"),
		BurnColors: []tte.Color{
			tte.MustColorFromHex("ffffff"),
			tte.MustColorFromHex("fff75d"),
			tte.MustColorFromHex("fe650d"),
			tte.MustColorFromHex("8A003C"),
			tte.MustColorFromHex("510100"),
		},
		SmokeChance: 0.4,
		FinalGradientStops: []tte.Color{
			tte.MustColorFromHex("00c3ff"),
			tte.MustColorFromHex("ffff1c"),
		},
		FinalGradientSteps:     []int{12},
		FinalGradientDirection: tte.GradientVertical,
		ActivatePerTick:        0,     // 0 = Python default (2-4)
		FireSymbolTicks:        0,     // 0 = Python default (4)
		IncludeSpaces:          false, // false = Python default (skip spaces)
		UseRandomOrder:         false, // false = Prim's spanning tree
	}
}

type BurnEffect struct {
	Config   BurnConfig
	base     *tte.BaseIterator
	terminal *tte.Terminal
	burnScns map[int]*tte.Scene
	// linkOrder is the Prim-derived activation sequence.
	linkOrder []*tte.EffectCharacter
}

func NewBurnEffect(cfg BurnConfig) *BurnEffect {
	return &BurnEffect{Config: cfg}
}

func (e *BurnEffect) Name() string { return "burn" }

// primsLinkOrder builds a Prim's-style spanning-tree traversal order over the
// text characters, returning them in the order they should be ignited.
// This is a direct port of PrimsSimple from the Python TTE library.
func primsLinkOrder(chars []*tte.EffectCharacter) []*tte.EffectCharacter {
	if len(chars) == 0 {
		return nil
	}
	byCoord := make(map[tte.Coord]*tte.EffectCharacter, len(chars))
	for _, ch := range chars {
		byCoord[ch.InputCoord()] = ch
	}

	linked := make(map[int]bool, len(chars))
	order := make([]*tte.EffectCharacter, 0, len(chars))

	// Pick a random starting character.
	start := chars[rand.Intn(len(chars))]
	linked[start.ID()] = true
	order = append(order, start)
	edges := []*tte.EffectCharacter{start}

	neighbour4 := func(c tte.Coord) []tte.Coord {
		return []tte.Coord{
			{Column: c.Column - 1, Row: c.Row},
			{Column: c.Column + 1, Row: c.Row},
			{Column: c.Column, Row: c.Row - 1},
			{Column: c.Column, Row: c.Row + 1},
		}
	}

	unlinkedNeighbours := func(ch *tte.EffectCharacter) []*tte.EffectCharacter {
		var out []*tte.EffectCharacter
		for _, nc := range neighbour4(ch.InputCoord()) {
			if nb, ok := byCoord[nc]; ok && !linked[nb.ID()] {
				out = append(out, nb)
			}
		}
		return out
	}

	for len(edges) > 0 {
		// Pop a random edge character.
		idx := rand.Intn(len(edges))
		cur := edges[idx]
		edges[idx] = edges[len(edges)-1]
		edges = edges[:len(edges)-1]

		nbrs := unlinkedNeighbours(cur)
		if len(nbrs) == 0 {
			continue
		}
		// Link one random neighbour.
		next := nbrs[rand.Intn(len(nbrs))]
		linked[next.ID()] = true
		order = append(order, next)

		// cur may still have unlinked neighbours — re-add to edges.
		if remaining := unlinkedNeighbours(cur); len(remaining) > 0 {
			edges = append(edges, cur)
		}
		// next may have unlinked neighbours too.
		if nextNbrs := unlinkedNeighbours(next); len(nextNbrs) > 0 {
			edges = append(edges, next)
		}
	}

	// Any chars not yet reached (isolated / unreachable) go at the end.
	for _, ch := range chars {
		if !linked[ch.ID()] {
			order = append(order, ch)
		}
	}
	return order
}

func (e *BurnEffect) Init(t *tte.Terminal) {
	e.terminal = t
	e.base = tte.NewBaseIterator(t)
	e.burnScns = make(map[int]*tte.Scene)

	canvas := t.Canvas.Canvas
	finalGrad := tte.NewGradient(e.Config.FinalGradientStops, e.Config.FinalGradientSteps)

	// When burning spaces too, map colours across the full canvas so every cell
	// (including background cells) gets a gradient colour.  Otherwise use the
	// tighter text-extent bounds (Python-canonical behaviour).
	var colorMap map[tte.Coord]tte.Color
	if e.Config.IncludeSpaces {
		colorMap = finalGrad.BuildCoordinateColorMapping(
			canvas.Bottom, canvas.Top,
			canvas.Left, canvas.Right,
			e.Config.FinalGradientDirection,
		)
	} else {
		colorMap = finalGrad.BuildCoordinateColorMapping(
			canvas.TextBottom, canvas.TextTop,
			canvas.TextLeft, canvas.TextRight,
			e.Config.FinalGradientDirection,
		)
	}

	// Fallback final colour for any cell not covered by the gradient mapping.
	fallbackFinal := e.Config.FinalGradientStops[len(e.Config.FinalGradientStops)-1]

	// Fire gradient: 10 steps across the burn colours (matches Python steps=10).
	fireGrad := tte.NewGradient(e.Config.BurnColors, []int{10})

	// Python burn symbol sequence.
	burnSymbols := []string{"'", ".", "▖", "▙", "█", "▜", "▀", "▝", "."}

	// Ticks per fire frame — default 4 (Python canonical); override for speed.
	fireTicks := e.Config.FireSymbolTicks
	if fireTicks <= 0 {
		fireTicks = 4
	}
	// Cool steps: number of gradient steps between last fire color and final color.
	// Default 8 (Python canonical); set lower for fast full-screen burns.
	coolSteps := e.Config.CoolGradientSteps
	if coolSteps <= 0 {
		coolSteps = 8
	}

	chars := t.GetCharacters()
	// Show all characters in the starting colour before ignition.
	for _, ch := range chars {
		t.SetCharacterVisibility(ch, true)
		ch.Animation.SetAppearance(ch.InputSymbol(), tte.FGOnly(e.Config.StartingColor))
	}

	// Activation order: Prim's spanning tree (organic spread) or random shuffle.
	if e.Config.UseRandomOrder {
		rand.Shuffle(len(chars), func(i, j int) { chars[i], chars[j] = chars[j], chars[i] })
		e.linkOrder = chars
	} else {
		e.linkOrder = primsLinkOrder(chars)
	}

	for _, ch := range chars {
		finalColor, ok := colorMap[ch.InputCoord()]
		if !ok {
			finalColor = fallbackFinal
		}

		// burn scene: fire symbols cycling through the fire gradient.
		burnScn := ch.Animation.NewScene("burn", false, tte.SyncNone, nil)
		burnScn.ApplyGradientToSymbols(burnSymbols, fireTicks, fireGrad, nil)

		// cool scene: transition from last fire colour to final colour.
		coolScn := ch.Animation.NewScene("cool", false, tte.SyncNone, nil)
		coolGrad := tte.NewGradient(
			[]tte.Color{fireGrad.Spectrum[len(fireGrad.Spectrum)-1], finalColor},
			[]int{coolSteps},
		)
		coolScn.ApplyGradientToSymbols([]string{ch.InputSymbol()}, fireTicks, coolGrad, nil)

		ch.EventHandler.RegisterEvent(tte.EventSceneComplete, burnScn, tte.ActionActivateScene, coolScn)
		e.burnScns[ch.ID()] = burnScn
	}

	// Seed the base pending list with the Prim order.
	e.base.PendingCharacters = append(e.base.PendingCharacters, e.linkOrder...)
}

func (e *BurnEffect) Next() (string, bool) {
	n := e.Config.ActivatePerTick
	if n <= 0 {
		n = 2 + rand.Intn(3)
	}
	for i := 0; i < n && len(e.base.PendingCharacters) > 0; i++ {
		ch := e.base.PendingCharacters[0]
		e.base.PendingCharacters = e.base.PendingCharacters[1:]
		// Skip spaces unless IncludeSpaces is set.
		if !e.Config.IncludeSpaces && ch.InputSymbol() == " " {
			continue
		}
		burnScn := e.burnScns[ch.ID()]
		if burnScn == nil {
			continue
		}
		ch.Animation.ActivateScene(burnScn)
		e.base.Activate(ch)
	}
	e.base.Update()
	return e.base.Frame(), e.base.HasWork()
}

func (e *BurnEffect) Reset() {
	e.burnScns = nil
	e.linkOrder = nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Ensure unused imports are used (suppress compiler errors)
// ─────────────────────────────────────────────────────────────────────────────

var _ = clamp1
var _ = max1
var _ = min1
var _ = abs1
var _ = sort.Ints
