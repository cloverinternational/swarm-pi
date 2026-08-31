package effects

import (
	"math"
	"math/rand"
	"sort"

	tte "github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/tteengine"
)

// ── helpers (suffix 4) ────────────────────────────────────────────────────────

func max4(a, b int) int {
	if a > b {
		return a
	}
	return b
}
func min4(a, b int) int {
	if a < b {
		return a
	}
	return b
}
func abs4(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

var _ = math.Sqrt
var _ = sort.Ints
var _ = max4
var _ = min4
var _ = abs4

// ── 1. PourEffect ─────────────────────────────────────────────────────────────

type PourConfig struct {
	PourDirection          string // "down","up","left","right"
	PourSpeed              int
	MovementSpeed          float64
	FinalGradientStops     []tte.Color
	FinalGradientSteps     []int
	FinalGradientDirection tte.GradientDirection
}

func DefaultPourConfig() PourConfig {
	return PourConfig{
		PourDirection: "down",
		PourSpeed:     2,
		MovementSpeed: 0.5,
		FinalGradientStops: []tte.Color{
			tte.MustColorFromHex("8A008A"),
			tte.MustColorFromHex("00D1FF"),
			tte.MustColorFromHex("FFFFFF"),
		},
		FinalGradientSteps:     []int{12},
		FinalGradientDirection: tte.GradientVertical,
	}
}

type PourEffect struct {
	Config        PourConfig
	base          *tte.BaseIterator
	terminal      *tte.Terminal
	pendingGroups [][]*tte.EffectCharacter
	groupIdx      int
}

func NewPourEffect(cfg PourConfig) *PourEffect { return &PourEffect{Config: cfg} }
func (e *PourEffect) Name() string             { return "pour" }
func (e *PourEffect) Init(t *tte.Terminal) {
	e.terminal = t
	e.base = tte.NewBaseIterator(t)
	e.groupIdx = 0
	canvas := t.Canvas.Canvas

	grad := tte.NewGradient(e.Config.FinalGradientStops, e.Config.FinalGradientSteps)
	colorMap := grad.BuildCoordinateColorMapping(canvas.TextBottom, canvas.TextTop, canvas.TextLeft, canvas.TextRight, e.Config.FinalGradientDirection)

	groupMap := make(map[int][]*tte.EffectCharacter)
	for _, ch := range t.GetCharacters() {
		finalColor := colorMap[ch.InputCoord()]
		pourScn := ch.Animation.NewScene("pour", false, tte.SyncNone, nil)
		pourGrad := tte.NewGradient([]tte.Color{tte.MustColorFromHex("ffffff"), finalColor}, []int{8})
		pourScn.ApplyGradientToSymbols([]string{ch.InputSymbol()}, 5, pourGrad, nil)
		pourScn.AddFrame(ch.InputSymbol(), 1, tte.FGOnly(finalColor))

		var startCoord tte.Coord
		switch e.Config.PourDirection {
		case "up":
			startCoord = tte.Coord{Column: ch.InputCoord().Column, Row: canvas.Bottom - 1}
		case "left":
			startCoord = tte.Coord{Column: canvas.Right + 1, Row: ch.InputCoord().Row}
		case "right":
			startCoord = tte.Coord{Column: canvas.Left - 1, Row: ch.InputCoord().Row}
		default: // "down"
			startCoord = tte.Coord{Column: ch.InputCoord().Column, Row: canvas.Top + 1}
		}
		ch.Motion.SetCoordinate(startCoord)
		path := ch.Motion.NewPath("pour", e.Config.MovementSpeed, tte.EaseInQuad, 0, false)
		path.NewWaypoint(ch.InputCoord(), "home")
		ch.EventHandler.RegisterEvent(tte.EventPathActivated, path, tte.ActionActivateScene, pourScn)
		t.SetCharacterVisibility(ch, true)
		ch.Motion.ActivatePath(path, ch.EventHandler)

		var key int
		switch e.Config.PourDirection {
		case "left", "right":
			key = ch.InputCoord().Column
		default:
			key = ch.InputCoord().Row
		}
		groupMap[key] = append(groupMap[key], ch)
	}

	keys := make([]int, 0, len(groupMap))
	for k := range groupMap {
		keys = append(keys, k)
	}
	switch e.Config.PourDirection {
	case "up", "right":
		sort.Ints(keys) // ascending
	default: // down, left: descending
		sort.Sort(sort.Reverse(sort.IntSlice(keys)))
	}
	e.pendingGroups = make([][]*tte.EffectCharacter, len(keys))
	for i, k := range keys {
		e.pendingGroups[i] = groupMap[k]
	}
	if len(e.pendingGroups) > 0 {
		e.base.PendingCharacters = append(e.base.PendingCharacters, e.pendingGroups[0]...)
		e.groupIdx = 1
	}
}
func (e *PourEffect) Next() (string, bool) {
	for i := 0; i < e.Config.PourSpeed && len(e.base.PendingCharacters) > 0; i++ {
		ch := e.base.PendingCharacters[0]
		e.base.PendingCharacters = e.base.PendingCharacters[1:]
		e.base.Activate(ch)
	}
	if len(e.base.PendingCharacters) == 0 && e.groupIdx < len(e.pendingGroups) {
		e.base.PendingCharacters = append(e.base.PendingCharacters, e.pendingGroups[e.groupIdx]...)
		e.groupIdx++
	}
	e.base.Update()
	return e.base.Frame(), !e.base.HasWork() && e.groupIdx >= len(e.pendingGroups)
}
func (e *PourEffect) Reset() { e.groupIdx = 0 }

// ── 2. PrintEffect ────────────────────────────────────────────────────────────

type PrintConfig struct {
	PrintSpeed             int
	FinalGradientStops     []tte.Color
	FinalGradientSteps     []int
	FinalGradientDirection tte.GradientDirection
}

func DefaultPrintConfig() PrintConfig {
	return PrintConfig{
		PrintSpeed: 2,
		FinalGradientStops: []tte.Color{
			tte.MustColorFromHex("02b8bd"),
			tte.MustColorFromHex("c1f0e3"),
			tte.MustColorFromHex("00ffa0"),
		},
		FinalGradientSteps:     []int{12},
		FinalGradientDirection: tte.GradientDiagonal,
	}
}

type PrintEffect struct {
	Config     PrintConfig
	base       *tte.BaseIterator
	terminal   *tte.Terminal
	rows       [][]*tte.EffectCharacter // top→bottom order
	rowIdx     int
	typeScenes map[int]*tte.Scene
}

func NewPrintEffect(cfg PrintConfig) *PrintEffect { return &PrintEffect{Config: cfg} }
func (e *PrintEffect) Name() string               { return "print" }
func (e *PrintEffect) Init(t *tte.Terminal) {
	e.terminal = t
	e.base = tte.NewBaseIterator(t)
	e.typeScenes = make(map[int]*tte.Scene)
	e.rowIdx = 0
	canvas := t.Canvas.Canvas

	grad := tte.NewGradient(e.Config.FinalGradientStops, e.Config.FinalGradientSteps)
	colorMap := grad.BuildCoordinateColorMapping(canvas.TextBottom, canvas.TextTop, canvas.TextLeft, canvas.TextRight, e.Config.FinalGradientDirection)

	rowMap := make(map[int][]*tte.EffectCharacter)
	for _, ch := range t.GetCharacters() {
		finalColor := colorMap[ch.InputCoord()]
		typeScn := ch.Animation.NewScene("type", false, tte.SyncNone, nil)
		typeGrad := tte.NewGradient([]tte.Color{tte.MustColorFromHex("ffffff"), finalColor}, []int{4})
		typeScn.ApplyGradientToSymbols([]string{"█", "▓", "▒", "░", ch.InputSymbol()}, 3, typeGrad, nil)
		typeScn.AddFrame(ch.InputSymbol(), 1, tte.FGOnly(finalColor))
		e.typeScenes[ch.ID()] = typeScn
		// start each char at row 1
		ch.Motion.SetCoordinate(tte.Coord{Column: ch.InputCoord().Column, Row: 1})
		rowMap[ch.InputCoord().Row] = append(rowMap[ch.InputCoord().Row], ch)
	}

	// sort rows top→bottom (descending row number = top first in tte)
	rows := make([]int, 0, len(rowMap))
	for r := range rowMap {
		rows = append(rows, r)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(rows)))
	e.rows = make([][]*tte.EffectCharacter, len(rows))
	for i, r := range rows {
		e.rows[i] = rowMap[r]
	}
	// release first row
	if len(e.rows) > 0 {
		e.base.PendingCharacters = append(e.base.PendingCharacters, e.rows[0]...)
		e.rowIdx = 1
	}
}
func (e *PrintEffect) Next() (string, bool) {
	// type PrintSpeed chars from pending
	for i := 0; i < e.Config.PrintSpeed && len(e.base.PendingCharacters) > 0; i++ {
		ch := e.base.PendingCharacters[0]
		e.base.PendingCharacters = e.base.PendingCharacters[1:]
		ch.Animation.ActivateScene(e.typeScenes[ch.ID()])
		e.base.Activate(ch)
		t := e.terminal
		t.SetCharacterVisibility(ch, true)
	}
	// when current row is done and no pending, advance row
	if len(e.base.PendingCharacters) == 0 && e.rowIdx < len(e.rows) {
		// shift all typed chars up by 1 row
		for _, ch := range e.base.ActiveCharacters {
			cur := ch.Motion.CurrentCoord.ToCoord()
			ch.Motion.SetCoordinate(tte.Coord{Column: cur.Column, Row: cur.Row + 1})
		}
		e.base.PendingCharacters = append(e.base.PendingCharacters, e.rows[e.rowIdx]...)
		e.rowIdx++
	}
	e.base.Update()
	return e.base.Frame(), !e.base.HasWork() && e.rowIdx >= len(e.rows)
}
func (e *PrintEffect) Reset() { e.rowIdx = 0 }

// ── 3. RandomSequenceEffect ───────────────────────────────────────────────────

type RandomSequenceConfig struct {
	Speed                  float64
	StartingColor          tte.Color
	FinalGradientStops     []tte.Color
	FinalGradientSteps     []int
	FinalGradientDirection tte.GradientDirection
}

func DefaultRandomSequenceConfig() RandomSequenceConfig {
	return RandomSequenceConfig{
		Speed:         0.007,
		StartingColor: tte.MustColorFromHex("000000"),
		FinalGradientStops: []tte.Color{
			tte.MustColorFromHex("8A008A"),
			tte.MustColorFromHex("00D1FF"),
			tte.MustColorFromHex("FFFFFF"),
		},
		FinalGradientSteps:     []int{12},
		FinalGradientDirection: tte.GradientVertical,
	}
}

type RandomSequenceEffect struct {
	Config       RandomSequenceConfig
	base         *tte.BaseIterator
	terminal     *tte.Terminal
	charsPerTick int
}

func NewRandomSequenceEffect(cfg RandomSequenceConfig) *RandomSequenceEffect {
	return &RandomSequenceEffect{Config: cfg}
}
func (e *RandomSequenceEffect) Name() string { return "randomsequence" }
func (e *RandomSequenceEffect) Init(t *tte.Terminal) {
	e.terminal = t
	e.base = tte.NewBaseIterator(t)
	canvas := t.Canvas.Canvas
	grad := tte.NewGradient(e.Config.FinalGradientStops, e.Config.FinalGradientSteps)
	colorMap := grad.BuildCoordinateColorMapping(canvas.TextBottom, canvas.TextTop, canvas.TextLeft, canvas.TextRight, e.Config.FinalGradientDirection)

	chars := t.GetCharacters()
	e.charsPerTick = max4(1, int(e.Config.Speed*float64(len(chars))))

	for _, ch := range chars {
		finalColor := colorMap[ch.InputCoord()]
		revealScn := ch.Animation.NewScene("reveal", false, tte.SyncNone, nil)
		revealGrad := tte.NewGradient([]tte.Color{e.Config.StartingColor, finalColor}, []int{7})
		revealScn.ApplyGradientToSymbols([]string{ch.InputSymbol()}, 8, revealGrad, nil)
		revealScn.AddFrame(ch.InputSymbol(), 1, tte.FGOnly(finalColor))
	}
	rand.Shuffle(len(chars), func(i, j int) { chars[i], chars[j] = chars[j], chars[i] })
	e.base.PendingCharacters = chars
}
func (e *RandomSequenceEffect) Next() (string, bool) {
	for i := 0; i < e.charsPerTick && len(e.base.PendingCharacters) > 0; i++ {
		ch := e.base.PendingCharacters[len(e.base.PendingCharacters)-1]
		e.base.PendingCharacters = e.base.PendingCharacters[:len(e.base.PendingCharacters)-1]
		revealScn := ch.Animation.QueryScene("reveal")
		if revealScn != nil {
			ch.Animation.ActivateScene(revealScn)
		}
		e.base.Activate(ch)
	}
	e.base.Update()
	return e.base.Frame(), !e.base.HasWork()
}
func (e *RandomSequenceEffect) Reset() {}

// ── 4. RingsEffect ────────────────────────────────────────────────────────────

type RingsConfig struct {
	RingColors             []tte.Color
	FinalGradientStops     []tte.Color
	FinalGradientSteps     []int
	FinalGradientDirection tte.GradientDirection
	LandSpeed              float64
}

func DefaultRingsConfig() RingsConfig {
	return RingsConfig{
		RingColors: []tte.Color{
			tte.MustColorFromHex("00FFFF"),
			tte.MustColorFromHex("FF00FF"),
			tte.MustColorFromHex("FFFF00"),
			tte.MustColorFromHex("00FF00"),
		},
		FinalGradientStops: []tte.Color{
			tte.MustColorFromHex("8A008A"),
			tte.MustColorFromHex("00D1FF"),
			tte.MustColorFromHex("FFFFFF"),
		},
		FinalGradientSteps:     []int{12},
		FinalGradientDirection: tte.GradientRadial,
		LandSpeed:              0.8,
	}
}

type RingsEffect struct {
	Config   RingsConfig
	base     *tte.BaseIterator
	terminal *tte.Terminal
}

func NewRingsEffect(cfg RingsConfig) *RingsEffect { return &RingsEffect{Config: cfg} }
func (e *RingsEffect) Name() string               { return "rings" }
func (e *RingsEffect) Init(t *tte.Terminal) {
	e.terminal = t
	e.base = tte.NewBaseIterator(t)
	canvas := t.Canvas.Canvas
	grad := tte.NewGradient(e.Config.FinalGradientStops, e.Config.FinalGradientSteps)
	colorMap := grad.BuildCoordinateColorMapping(canvas.TextBottom, canvas.TextTop, canvas.TextLeft, canvas.TextRight, e.Config.FinalGradientDirection)

	radii := []int{4, 8, 12, 16}
	center := canvas.Center
	chars := t.GetCharacters()

	// sort by distance from center
	sort.Slice(chars, func(i, j int) bool {
		return distSq2(chars[i].InputCoord(), center) < distSq2(chars[j].InputCoord(), center)
	})

	// pre-compute ring points for each radius
	ringPoints := make([][]tte.Coord, len(radii))
	for i, r := range radii {
		pts := tte.FindCoordsOnCircle(center, r, 0, true)
		if len(pts) == 0 {
			pts = []tte.Coord{center}
		}
		ringPoints[i] = pts
	}

	for i, ch := range chars {
		ringIdx := i % len(radii)
		ringColor := e.Config.RingColors[ringIdx%len(e.Config.RingColors)]
		finalColor := colorMap[ch.InputCoord()]

		pts := ringPoints[ringIdx]
		startPt := pts[i%len(pts)]
		// clamp to canvas
		if startPt.Column < canvas.Left {
			startPt.Column = canvas.Left
		}
		if startPt.Column > canvas.Right {
			startPt.Column = canvas.Right
		}
		if startPt.Row < canvas.Bottom {
			startPt.Row = canvas.Bottom
		}
		if startPt.Row > canvas.Top {
			startPt.Row = canvas.Top
		}

		ringScn := ch.Animation.NewScene("ring", true, tte.SyncNone, nil)
		ringScn.AddFrame("*", 2, tte.FGOnly(ringColor))
		ringScn.AddFrame("·", 2, tte.FGOnly(tte.AdjustColorBrightness(ringColor, 0.7)))

		landScn := ch.Animation.NewScene("land", false, tte.SyncNone, nil)
		landGrad := tte.NewGradient([]tte.Color{ringColor, finalColor}, []int{6})
		landScn.ApplyGradientToSymbols([]string{ch.InputSymbol()}, 3, landGrad, nil)
		landScn.AddFrame(ch.InputSymbol(), 1, tte.FGOnly(finalColor))

		ch.Motion.SetCoordinate(startPt)
		path := ch.Motion.NewPath("land", e.Config.LandSpeed, tte.EaseOutCubic, 0, false)
		path.NewWaypoint(ch.InputCoord(), "home")
		eh := ch.EventHandler
		eh.RegisterEvent(tte.EventPathActivated, path, tte.ActionActivateScene, ringScn)
		eh.RegisterEvent(tte.EventPathComplete, path, tte.ActionActivateScene, landScn)
		t.SetCharacterVisibility(ch, true)
		ch.Motion.ActivatePath(path, eh)
		e.base.PendingCharacters = append(e.base.PendingCharacters, ch)
	}
}
func (e *RingsEffect) Next() (string, bool) {
	for i := 0; i < 3 && len(e.base.PendingCharacters) > 0; i++ {
		ch := e.base.PendingCharacters[0]
		e.base.PendingCharacters = e.base.PendingCharacters[1:]
		e.base.Activate(ch)
	}
	e.base.Update()
	return e.base.Frame(), !e.base.HasWork()
}
func (e *RingsEffect) Reset() {}

// ── 5. ScatteredEffect ────────────────────────────────────────────────────────

type ScatteredConfig struct {
	ScatterSpeed           float64
	LandSpeed              float64
	FinalGradientStops     []tte.Color
	FinalGradientSteps     []int
	FinalGradientDirection tte.GradientDirection
}

func DefaultScatteredConfig() ScatteredConfig {
	return ScatteredConfig{
		ScatterSpeed: 0.5,
		LandSpeed:    0.5,
		FinalGradientStops: []tte.Color{
			tte.MustColorFromHex("6E64E8"), // Primary (theme)
			tte.MustColorFromHex("39D2C0"), // Accent (theme)
			tte.MustColorFromHex("4C9AFF"), // Info Blue (theme)
			tte.MustColorFromHex("FFFFFF"),
		},
		FinalGradientSteps:     []int{12},
		FinalGradientDirection: tte.GradientDiagonal,
	}
}

type ScatteredEffect struct {
	Config   ScatteredConfig
	base     *tte.BaseIterator
	terminal *tte.Terminal
}

func NewScatteredEffect(cfg ScatteredConfig) *ScatteredEffect { return &ScatteredEffect{Config: cfg} }
func (e *ScatteredEffect) Name() string                       { return "scattered" }
func (e *ScatteredEffect) Init(t *tte.Terminal) {
	e.terminal = t
	e.base = tte.NewBaseIterator(t)
	canvas := t.Canvas.Canvas
	grad := tte.NewGradient(e.Config.FinalGradientStops, e.Config.FinalGradientSteps)
	colorMap := grad.BuildCoordinateColorMapping(canvas.TextBottom, canvas.TextTop, canvas.TextLeft, canvas.TextRight, e.Config.FinalGradientDirection)

	for _, ch := range t.GetCharacters() {
		finalColor := colorMap[ch.InputCoord()]
		bright := tte.AdjustColorBrightness(finalColor, 1.5)

		scatterScn := ch.Animation.NewScene("scatter", true, tte.SyncNone, nil)
		scatterScn.AddFrame(ch.InputSymbol(), 2, tte.FGOnly(bright))

		landScn := ch.Animation.NewScene("land", false, tte.SyncNone, nil)
		landGrad := tte.NewGradient([]tte.Color{bright, finalColor}, []int{6})
		landScn.ApplyGradientToSymbols([]string{ch.InputSymbol()}, 3, landGrad, nil)
		landScn.AddFrame(ch.InputSymbol(), 1, tte.FGOnly(finalColor))

		randomPos := canvas.RandomCoord(false, false)

		scatterPath := ch.Motion.NewPath("scatter", e.Config.ScatterSpeed, tte.EaseOutCubic, 0, false)
		scatterPath.NewWaypoint(randomPos, "rand")

		landPath := ch.Motion.NewPath("land", e.Config.LandSpeed, tte.EaseInOutCubic, 0, false)
		landPath.NewWaypoint(ch.InputCoord(), "home")

		eh := ch.EventHandler
		eh.RegisterEvent(tte.EventPathActivated, scatterPath, tte.ActionActivateScene, scatterScn)
		eh.RegisterEvent(tte.EventPathComplete, scatterPath, tte.ActionActivatePath, landPath)
		eh.RegisterEvent(tte.EventPathComplete, landPath, tte.ActionActivateScene, landScn)

		t.SetCharacterVisibility(ch, true)
		ch.Motion.ActivatePath(scatterPath, eh)
		e.base.PendingCharacters = append(e.base.PendingCharacters, ch)
	}
}
func (e *ScatteredEffect) Next() (string, bool) {
	// activate all at once on first tick
	for len(e.base.PendingCharacters) > 0 {
		ch := e.base.PendingCharacters[0]
		e.base.PendingCharacters = e.base.PendingCharacters[1:]
		e.base.Activate(ch)
	}
	e.base.Update()
	return e.base.Frame(), !e.base.HasWork()
}
func (e *ScatteredEffect) Reset() {}

// ── 6. SliceEffect ────────────────────────────────────────────────────────────

type SliceConfig struct {
	SliceDirection         string // "vertical","horizontal"
	SliceSpeed             float64
	FinalGradientStops     []tte.Color
	FinalGradientSteps     []int
	FinalGradientDirection tte.GradientDirection
}

func DefaultSliceConfig() SliceConfig {
	return SliceConfig{
		SliceDirection: "vertical",
		SliceSpeed:     0.8,
		FinalGradientStops: []tte.Color{
			tte.MustColorFromHex("8A008A"),
			tte.MustColorFromHex("00D1FF"),
			tte.MustColorFromHex("FFFFFF"),
		},
		FinalGradientSteps:     []int{12},
		FinalGradientDirection: tte.GradientVertical,
	}
}

type SliceEffect struct {
	Config   SliceConfig
	base     *tte.BaseIterator
	terminal *tte.Terminal
}

func NewSliceEffect(cfg SliceConfig) *SliceEffect { return &SliceEffect{Config: cfg} }
func (e *SliceEffect) Name() string               { return "slice" }
func (e *SliceEffect) Init(t *tte.Terminal) {
	e.terminal = t
	e.base = tte.NewBaseIterator(t)
	canvas := t.Canvas.Canvas
	grad := tte.NewGradient(e.Config.FinalGradientStops, e.Config.FinalGradientSteps)
	colorMap := grad.BuildCoordinateColorMapping(canvas.TextBottom, canvas.TextTop, canvas.TextLeft, canvas.TextRight, e.Config.FinalGradientDirection)

	chars := t.GetCharacters()
	// find median split
	midCol := (canvas.TextLeft + canvas.TextRight) / 2
	midRow := (canvas.TextBottom + canvas.TextTop) / 2

	for _, ch := range chars {
		finalColor := colorMap[ch.InputCoord()]
		slideScn := ch.Animation.NewScene("slide", false, tte.SyncNone, nil)
		slideGrad := tte.NewGradient([]tte.Color{e.Config.FinalGradientStops[0], finalColor}, []int{6})
		slideScn.ApplyGradientToSymbols([]string{ch.InputSymbol()}, 4, slideGrad, nil)
		slideScn.AddFrame(ch.InputSymbol(), 1, tte.FGOnly(finalColor))

		var startCoord tte.Coord
		if e.Config.SliceDirection == "horizontal" {
			if ch.InputCoord().Row <= midRow {
				startCoord = tte.Coord{Column: canvas.Left - 1, Row: ch.InputCoord().Row}
			} else {
				startCoord = tte.Coord{Column: canvas.Right + 1, Row: ch.InputCoord().Row}
			}
		} else { // vertical
			if ch.InputCoord().Column <= midCol {
				startCoord = tte.Coord{Column: canvas.Left - 1, Row: ch.InputCoord().Row}
			} else {
				startCoord = tte.Coord{Column: canvas.Right + 1, Row: ch.InputCoord().Row}
			}
		}
		ch.Motion.SetCoordinate(startCoord)
		path := ch.Motion.NewPath("slide", e.Config.SliceSpeed, tte.EaseInOutCubic, 0, false)
		path.NewWaypoint(ch.InputCoord(), "home")
		ch.EventHandler.RegisterEvent(tte.EventPathActivated, path, tte.ActionActivateScene, slideScn)
		t.SetCharacterVisibility(ch, true)
		ch.Motion.ActivatePath(path, ch.EventHandler)
		e.base.PendingCharacters = append(e.base.PendingCharacters, ch)
	}
}
func (e *SliceEffect) Next() (string, bool) {
	for len(e.base.PendingCharacters) > 0 {
		ch := e.base.PendingCharacters[0]
		e.base.PendingCharacters = e.base.PendingCharacters[1:]
		e.base.Activate(ch)
	}
	e.base.Update()
	return e.base.Frame(), !e.base.HasWork()
}
func (e *SliceEffect) Reset() {}
