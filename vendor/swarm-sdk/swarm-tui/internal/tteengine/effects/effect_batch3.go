package effects

import (
	"math"
	"math/rand"
	"sort"

	tte "github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/tteengine"
)

// ── helpers (suffix 3) ────────────────────────────────────────────────────────

func max3(a, b int) int {
	if a > b {
		return a
	}
	return b
}
func min3(a, b int) int {
	if a < b {
		return a
	}
	return b
}
func abs3(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

var _ = sort.Ints
var _ = math.Sqrt
var _ = max3
var _ = min3
var _ = abs3

// ── 1. HighlightEffect ───────────────────────────────────────────────────────

type HighlightConfig struct {
	FinalGradientStops     []tte.Color
	FinalGradientSteps     []int
	FinalGradientDirection tte.GradientDirection
}

func DefaultHighlightConfig() HighlightConfig {
	return HighlightConfig{
		FinalGradientStops: []tte.Color{
			tte.MustColorFromHex("8A008A"),
			tte.MustColorFromHex("00D1FF"),
			tte.MustColorFromHex("FFFFFF"),
		},
		FinalGradientSteps:     []int{12},
		FinalGradientDirection: tte.GradientDiagonal,
	}
}

type HighlightEffect struct {
	Config       HighlightConfig
	base         *tte.BaseIterator
	terminal     *tte.Terminal
	diagGroups   [][]*tte.EffectCharacter
	diagIdx      int
	hlScenes     map[int]*tte.Scene
	staticScenes map[int]*tte.Scene
}

func NewHighlightEffect(cfg HighlightConfig) *HighlightEffect { return &HighlightEffect{Config: cfg} }
func (e *HighlightEffect) Name() string                       { return "highlight" }
func (e *HighlightEffect) Init(t *tte.Terminal) {
	e.terminal = t
	e.base = tte.NewBaseIterator(t)
	e.hlScenes = make(map[int]*tte.Scene)
	e.staticScenes = make(map[int]*tte.Scene)
	e.diagIdx = 0

	canvas := t.Canvas.Canvas
	grad := tte.NewGradient(e.Config.FinalGradientStops, e.Config.FinalGradientSteps)
	colorMap := grad.BuildCoordinateColorMapping(canvas.TextBottom, canvas.TextTop, canvas.TextLeft, canvas.TextRight, e.Config.FinalGradientDirection)

	diagMap := make(map[int][]*tte.EffectCharacter)
	for _, ch := range t.GetCharacters() {
		finalColor := colorMap[ch.InputCoord()]
		bright := tte.AdjustColorBrightness(finalColor, 1.8)

		baseScn := ch.Animation.NewScene("base", false, tte.SyncNone, nil)
		baseScn.AddFrame(ch.InputSymbol(), 1, tte.FGOnly(finalColor))

		hlScn := ch.Animation.NewScene("highlight", false, tte.SyncNone, nil)
		hlGrad := tte.NewGradient([]tte.Color{finalColor, bright, bright, finalColor}, []int{3, 8, 3})
		hlScn.ApplyGradientToSymbols([]string{ch.InputSymbol()}, 3, hlGrad, nil)

		staticScn := ch.Animation.NewScene("static", true, tte.SyncNone, nil)
		staticScn.AddFrame(ch.InputSymbol(), 1, tte.FGOnly(finalColor))

		ch.EventHandler.RegisterEvent(tte.EventSceneComplete, hlScn, tte.ActionActivateScene, staticScn)
		t.SetCharacterVisibility(ch, true)
		ch.Animation.ActivateScene(baseScn)

		e.hlScenes[ch.ID()] = hlScn
		e.staticScenes[ch.ID()] = staticScn

		diagKey := ch.InputCoord().Column + ch.InputCoord().Row
		diagMap[diagKey] = append(diagMap[diagKey], ch)
	}

	keys := make([]int, 0, len(diagMap))
	for k := range diagMap {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	e.diagGroups = make([][]*tte.EffectCharacter, len(keys))
	for i, k := range keys {
		e.diagGroups[i] = diagMap[k]
	}
}
func (e *HighlightEffect) Next() (string, bool) {
	if e.diagIdx < len(e.diagGroups) {
		for _, ch := range e.diagGroups[e.diagIdx] {
			ch.Animation.ActivateScene(e.hlScenes[ch.ID()])
			e.base.Activate(ch)
		}
		e.diagIdx++
	}
	e.base.Update()
	done := e.diagIdx >= len(e.diagGroups) && !e.base.HasWork()
	return e.base.Frame(), done
}
func (e *HighlightEffect) Reset() { e.diagIdx = 0 }

// ── 2. LaserEtchEffect ───────────────────────────────────────────────────────

type LaserEtchConfig struct {
	LaserColor             tte.Color
	EtchSpeed              int
	FinalGradientStops     []tte.Color
	FinalGradientSteps     []int
	FinalGradientDirection tte.GradientDirection
}

func DefaultLaserEtchConfig() LaserEtchConfig {
	return LaserEtchConfig{
		LaserColor: tte.MustColorFromHex("ff0000"),
		EtchSpeed:  1,
		FinalGradientStops: []tte.Color{
			tte.MustColorFromHex("8A008A"),
			tte.MustColorFromHex("00D1FF"),
			tte.MustColorFromHex("FFFFFF"),
		},
		FinalGradientSteps:     []int{12},
		FinalGradientDirection: tte.GradientDiagonal,
	}
}

type LaserEtchEffect struct {
	Config      LaserEtchConfig
	base        *tte.BaseIterator
	terminal    *tte.Terminal
	laserScenes map[int]*tte.Scene
	colGroups   [][]*tte.EffectCharacter
	colIdx      int
}

func NewLaserEtchEffect(cfg LaserEtchConfig) *LaserEtchEffect { return &LaserEtchEffect{Config: cfg} }
func (e *LaserEtchEffect) Name() string                       { return "laseretch" }
func (e *LaserEtchEffect) Init(t *tte.Terminal) {
	e.terminal = t
	e.base = tte.NewBaseIterator(t)
	e.laserScenes = make(map[int]*tte.Scene)
	e.colIdx = 0

	canvas := t.Canvas.Canvas
	grad := tte.NewGradient(e.Config.FinalGradientStops, e.Config.FinalGradientSteps)
	colorMap := grad.BuildCoordinateColorMapping(canvas.TextBottom, canvas.TextTop, canvas.TextLeft, canvas.TextRight, e.Config.FinalGradientDirection)

	orange := tte.MustColorFromHex("ff8800")
	laserGrad := tte.NewGradient([]tte.Color{e.Config.LaserColor, orange}, []int{4})

	colMap := make(map[int][]*tte.EffectCharacter)
	for _, ch := range t.GetCharacters() {
		finalColor := colorMap[ch.InputCoord()]

		laserScn := ch.Animation.NewScene("laser", false, tte.SyncNone, nil)
		for _, sym := range []string{"█", "▓", "▒", "░"} {
			laserScn.AddFrame(sym, 3, tte.FGOnly(tte.AdjustColorBrightness(e.Config.LaserColor, 1.0)))
		}
		_ = laserGrad

		etchScn := ch.Animation.NewScene("etch", true, tte.SyncNone, nil)
		etchScn.AddFrame(ch.InputSymbol(), 1, tte.FGOnly(finalColor))

		ch.EventHandler.RegisterEvent(tte.EventSceneComplete, laserScn, tte.ActionActivateScene, etchScn)
		e.laserScenes[ch.ID()] = laserScn
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
func (e *LaserEtchEffect) Next() (string, bool) {
	for step := 0; step < e.Config.EtchSpeed && e.colIdx < len(e.colGroups); step++ {
		for _, ch := range e.colGroups[e.colIdx] {
			ch.Animation.ActivateScene(e.laserScenes[ch.ID()])
			e.base.Activate(ch)
			t := e.terminal
			t.SetCharacterVisibility(ch, true)
		}
		e.colIdx++
	}
	e.base.Update()
	done := e.colIdx >= len(e.colGroups) && !e.base.HasWork()
	return e.base.Frame(), done
}
func (e *LaserEtchEffect) Reset() { e.colIdx = 0 }

// ── 3. MatrixEffect ──────────────────────────────────────────────────────────

type MatrixConfig struct {
	RainColor              tte.Color
	FinalGradientStops     []tte.Color
	FinalGradientSteps     []int
	FinalGradientDirection tte.GradientDirection
	RainDuration           int
}

func DefaultMatrixConfig() MatrixConfig {
	return MatrixConfig{
		RainColor: tte.MustColorFromHex("00ff00"),
		FinalGradientStops: []tte.Color{
			tte.MustColorFromHex("003300"),
			tte.MustColorFromHex("00FF41"),
			tte.MustColorFromHex("ffffff"),
		},
		FinalGradientSteps:     []int{12},
		FinalGradientDirection: tte.GradientVertical,
		RainDuration:           200,
	}
}

var matrixSymbols3 = []string{
	"ｦ", "ｧ", "ｨ", "ｩ", "ｪ", "ｫ", "ｬ", "ｭ", "ｮ", "ｯ",
	"0", "1", "2", "3", "4", "5", "6", "7", "8", "9",
}

type MatrixEffect struct {
	Config    MatrixConfig
	base      *tte.BaseIterator
	terminal  *tte.Terminal
	tick      int
	phase     string
	colorMap  map[tte.Coord]tte.Color
	revealScn map[int]*tte.Scene
}

func NewMatrixEffect(cfg MatrixConfig) *MatrixEffect { return &MatrixEffect{Config: cfg} }
func (e *MatrixEffect) Name() string                 { return "matrix" }
func (e *MatrixEffect) Init(t *tte.Terminal) {
	e.terminal = t
	e.base = tte.NewBaseIterator(t)
	e.tick = 0
	e.phase = "rain"
	e.revealScn = make(map[int]*tte.Scene)

	canvas := t.Canvas.Canvas
	grad := tte.NewGradient(e.Config.FinalGradientStops, e.Config.FinalGradientSteps)
	e.colorMap = grad.BuildCoordinateColorMapping(canvas.TextBottom, canvas.TextTop, canvas.TextLeft, canvas.TextRight, e.Config.FinalGradientDirection)

	green := e.Config.RainColor
	for _, ch := range t.GetCharacters() {
		finalColor := e.colorMap[ch.InputCoord()]
		t.SetCharacterVisibility(ch, true)

		rainScn := ch.Animation.NewScene("rain", true, tte.SyncNone, nil)
		for range 8 {
			sym := matrixSymbols3[rand.Intn(len(matrixSymbols3))]
			rainScn.AddFrame(sym, 2, tte.FGOnly(green))
		}
		ch.Animation.ActivateScene(rainScn)

		revealScn := ch.Animation.NewScene("reveal", false, tte.SyncNone, nil)
		revealGrad := tte.NewGradient([]tte.Color{green, finalColor}, []int{6})
		revealScn.ApplyGradientToSymbols([]string{ch.InputSymbol()}, 3, revealGrad, nil)
		revealScn.AddFrame(ch.InputSymbol(), 1, tte.FGOnly(finalColor))
		e.revealScn[ch.ID()] = revealScn

		e.base.ActiveCharacters[ch.ID()] = ch
	}
}
func (e *MatrixEffect) Next() (string, bool) {
	e.tick++
	if e.phase == "rain" {
		// randomly update some char appearances
		for _, ch := range e.base.ActiveCharacters {
			if rand.Float64() < 0.2 {
				sym := matrixSymbols3[rand.Intn(len(matrixSymbols3))]
				ch.Animation.SetAppearance(sym, tte.FGOnly(e.Config.RainColor))
			}
		}
		if e.tick >= e.Config.RainDuration {
			e.phase = "resolve"
			for _, ch := range e.base.ActiveCharacters {
				ch.Animation.ActivateScene(e.revealScn[ch.ID()])
			}
		}
	}
	e.base.Update()
	done := e.phase == "resolve" && !e.base.HasWork()
	return e.base.Frame(), done
}
func (e *MatrixEffect) Reset() { e.tick = 0; e.phase = "rain" }

// ── 4. MiddleOutEffect ───────────────────────────────────────────────────────

type MiddleOutConfig struct {
	ExpandSpeed            float64
	FinalGradientStops     []tte.Color
	FinalGradientSteps     []int
	FinalGradientDirection tte.GradientDirection
}

func DefaultMiddleOutConfig() MiddleOutConfig {
	return MiddleOutConfig{
		ExpandSpeed: 0.7,
		FinalGradientStops: []tte.Color{
			tte.MustColorFromHex("8A008A"),
			tte.MustColorFromHex("00D1FF"),
			tte.MustColorFromHex("FFFFFF"),
		},
		FinalGradientSteps:     []int{12},
		FinalGradientDirection: tte.GradientVertical,
	}
}

type MiddleOutEffect struct {
	Config   MiddleOutConfig
	base     *tte.BaseIterator
	terminal *tte.Terminal
}

func NewMiddleOutEffect(cfg MiddleOutConfig) *MiddleOutEffect { return &MiddleOutEffect{Config: cfg} }
func (e *MiddleOutEffect) Name() string                       { return "middleout" }
func (e *MiddleOutEffect) Init(t *tte.Terminal) {
	e.terminal = t
	e.base = tte.NewBaseIterator(t)
	canvas := t.Canvas.Canvas
	grad := tte.NewGradient(e.Config.FinalGradientStops, e.Config.FinalGradientSteps)
	colorMap := grad.BuildCoordinateColorMapping(canvas.TextBottom, canvas.TextTop, canvas.TextLeft, canvas.TextRight, e.Config.FinalGradientDirection)
	center := canvas.TextCenter

	chars := t.GetCharacters()
	// sort by abs distance from center column, ascending
	sort.Slice(chars, func(i, j int) bool {
		di := abs3(chars[i].InputCoord().Column - center.Column)
		dj := abs3(chars[j].InputCoord().Column - center.Column)
		return di < dj
	})

	for _, ch := range chars {
		finalColor := colorMap[ch.InputCoord()]
		expandScn := ch.Animation.NewScene("expand", false, tte.SyncNone, nil)
		expandScn.ApplyGradientToSymbols([]string{ch.InputSymbol()}, 4,
			tte.NewGradient([]tte.Color{e.Config.FinalGradientStops[0], finalColor}, []int{8}), nil)
		expandScn.AddFrame(ch.InputSymbol(), 1, tte.FGOnly(finalColor))

		startCoord := tte.Coord{Column: center.Column, Row: ch.InputCoord().Row}
		ch.Motion.SetCoordinate(startCoord)
		path := ch.Motion.NewPath("expand", e.Config.ExpandSpeed, tte.EaseOutCubic, 0, false)
		path.NewWaypoint(ch.InputCoord(), "home")
		ch.EventHandler.RegisterEvent(tte.EventPathActivated, path, tte.ActionActivateScene, expandScn)
		t.SetCharacterVisibility(ch, true)
		ch.Motion.ActivatePath(path, ch.EventHandler)
		e.base.PendingCharacters = append(e.base.PendingCharacters, ch)
	}
}
func (e *MiddleOutEffect) Next() (string, bool) {
	for i := 0; i < 3 && len(e.base.PendingCharacters) > 0; i++ {
		ch := e.base.PendingCharacters[0]
		e.base.PendingCharacters = e.base.PendingCharacters[1:]
		e.base.Activate(ch)
	}
	e.base.Update()
	return e.base.Frame(), !e.base.HasWork()
}
func (e *MiddleOutEffect) Reset() {}

// ── 5. OrbittingVolleyEffect ─────────────────────────────────────────────────

type OrbittingVolleyConfig struct {
	LaunchSpeed            float64
	FinalGradientStops     []tte.Color
	FinalGradientSteps     []int
	FinalGradientDirection tte.GradientDirection
}

func DefaultOrbittingVolleyConfig() OrbittingVolleyConfig {
	return OrbittingVolleyConfig{
		LaunchSpeed: 1.0,
		FinalGradientStops: []tte.Color{
			tte.MustColorFromHex("8A008A"),
			tte.MustColorFromHex("00D1FF"),
			tte.MustColorFromHex("FFFFFF"),
		},
		FinalGradientSteps:     []int{12},
		FinalGradientDirection: tte.GradientRadial,
	}
}

type OrbittingVolleyEffect struct {
	Config   OrbittingVolleyConfig
	base     *tte.BaseIterator
	terminal *tte.Terminal
}

func NewOrbittingVolleyEffect(cfg OrbittingVolleyConfig) *OrbittingVolleyEffect {
	return &OrbittingVolleyEffect{Config: cfg}
}
func (e *OrbittingVolleyEffect) Name() string { return "orbittingvolley" }
func (e *OrbittingVolleyEffect) Init(t *tte.Terminal) {
	e.terminal = t
	e.base = tte.NewBaseIterator(t)
	canvas := t.Canvas.Canvas
	grad := tte.NewGradient(e.Config.FinalGradientStops, e.Config.FinalGradientSteps)
	colorMap := grad.BuildCoordinateColorMapping(canvas.TextBottom, canvas.TextTop, canvas.TextLeft, canvas.TextRight, e.Config.FinalGradientDirection)

	launcherColors := []tte.Color{
		tte.MustColorFromHex("FFA500"),
		tte.MustColorFromHex("00FFFF"),
		tte.MustColorFromHex("FF00FF"),
		tte.MustColorFromHex("00FF00"),
	}
	launcherPositions := []tte.Coord{
		{Column: canvas.Left, Row: canvas.Top},
		{Column: canvas.Right, Row: canvas.Top},
		{Column: canvas.Left, Row: canvas.Bottom},
		{Column: canvas.Right, Row: canvas.Bottom},
	}

	center := canvas.TextCenter
	chars := t.GetCharacters()
	// sort by distance from center, nearest first
	sort.Slice(chars, func(i, j int) bool {
		di := distSq2(chars[i].InputCoord(), center)
		dj := distSq2(chars[j].InputCoord(), center)
		return di < dj
	})

	for i, ch := range chars {
		launcherIdx := i % 4
		launcherColor := launcherColors[launcherIdx]
		launcherPos := launcherPositions[launcherIdx]
		finalColor := colorMap[ch.InputCoord()]

		travelScn := ch.Animation.NewScene("travel", true, tte.SyncNone, nil)
		travelScn.AddFrame("*", 2, tte.FGOnly(launcherColor))

		landScn := ch.Animation.NewScene("land", false, tte.SyncNone, nil)
		landGrad := tte.NewGradient([]tte.Color{launcherColor, finalColor}, []int{6})
		landScn.ApplyGradientToSymbols([]string{ch.InputSymbol()}, 3, landGrad, nil)
		landScn.AddFrame(ch.InputSymbol(), 1, tte.FGOnly(finalColor))

		ch.Motion.SetCoordinate(launcherPos)
		path := ch.Motion.NewPath("launch", e.Config.LaunchSpeed, tte.EaseInOutCubic, 0, false)
		path.NewWaypoint(ch.InputCoord(), "home")
		eh := ch.EventHandler
		eh.RegisterEvent(tte.EventPathActivated, path, tte.ActionActivateScene, travelScn)
		eh.RegisterEvent(tte.EventPathComplete, path, tte.ActionActivateScene, landScn)
		t.SetCharacterVisibility(ch, true)
		ch.Motion.ActivatePath(path, eh)
		e.base.PendingCharacters = append(e.base.PendingCharacters, ch)
	}
	rand.Shuffle(len(e.base.PendingCharacters), func(i, j int) {
		e.base.PendingCharacters[i], e.base.PendingCharacters[j] = e.base.PendingCharacters[j], e.base.PendingCharacters[i]
	})
}
func (e *OrbittingVolleyEffect) Next() (string, bool) {
	for i := 0; i < 4 && len(e.base.PendingCharacters) > 0; i++ {
		ch := e.base.PendingCharacters[0]
		e.base.PendingCharacters = e.base.PendingCharacters[1:]
		e.base.Activate(ch)
	}
	e.base.Update()
	return e.base.Frame(), !e.base.HasWork()
}
func (e *OrbittingVolleyEffect) Reset() {}

// ── 6. OverflowEffect ────────────────────────────────────────────────────────

type OverflowConfig struct {
	OverflowSpeed          float64
	FinalGradientStops     []tte.Color
	FinalGradientSteps     []int
	FinalGradientDirection tte.GradientDirection
}

func DefaultOverflowConfig() OverflowConfig {
	return OverflowConfig{
		OverflowSpeed: 0.5,
		FinalGradientStops: []tte.Color{
			tte.MustColorFromHex("8A008A"),
			tte.MustColorFromHex("00D1FF"),
			tte.MustColorFromHex("FFFFFF"),
		},
		FinalGradientSteps:     []int{12},
		FinalGradientDirection: tte.GradientVertical,
	}
}

type OverflowEffect struct {
	Config      OverflowConfig
	base        *tte.BaseIterator
	terminal    *tte.Terminal
	rowGroups   [][]*tte.EffectCharacter
	rowGroupIdx int
}

func NewOverflowEffect(cfg OverflowConfig) *OverflowEffect { return &OverflowEffect{Config: cfg} }
func (e *OverflowEffect) Name() string                     { return "overflow" }
func (e *OverflowEffect) Init(t *tte.Terminal) {
	e.terminal = t
	e.base = tte.NewBaseIterator(t)
	e.rowGroupIdx = 0
	canvas := t.Canvas.Canvas
	grad := tte.NewGradient(e.Config.FinalGradientStops, e.Config.FinalGradientSteps)
	colorMap := grad.BuildCoordinateColorMapping(canvas.TextBottom, canvas.TextTop, canvas.TextLeft, canvas.TextRight, e.Config.FinalGradientDirection)

	rowMap := make(map[int][]*tte.EffectCharacter)
	for _, ch := range t.GetCharacters() {
		finalColor := colorMap[ch.InputCoord()]
		scrollScn := ch.Animation.NewScene("scroll", false, tte.SyncNone, nil)
		scrollGrad := tte.NewGradient([]tte.Color{e.Config.FinalGradientStops[0], finalColor}, []int{6})
		scrollScn.ApplyGradientToSymbols([]string{ch.InputSymbol()}, 3, scrollGrad, nil)
		scrollScn.AddFrame(ch.InputSymbol(), 1, tte.FGOnly(finalColor))

		startRow := canvas.Top + 1 + rand.Intn(5)
		startCoord := tte.Coord{Column: ch.InputCoord().Column, Row: startRow}
		ch.Motion.SetCoordinate(startCoord)
		path := ch.Motion.NewPath("scroll", e.Config.OverflowSpeed, tte.EaseOutCubic, 0, false)
		path.NewWaypoint(ch.InputCoord(), "home")
		ch.EventHandler.RegisterEvent(tte.EventPathActivated, path, tte.ActionActivateScene, scrollScn)
		t.SetCharacterVisibility(ch, true)
		ch.Motion.ActivatePath(path, ch.EventHandler)
		rowMap[ch.InputCoord().Row] = append(rowMap[ch.InputCoord().Row], ch)
	}

	rows := make([]int, 0, len(rowMap))
	for r := range rowMap {
		rows = append(rows, r)
	}
	// sort descending (top rows first = highest row number first)
	sort.Sort(sort.Reverse(sort.IntSlice(rows)))
	e.rowGroups = make([][]*tte.EffectCharacter, len(rows))
	for i, r := range rows {
		e.rowGroups[i] = rowMap[r]
	}
	if len(e.rowGroups) > 0 {
		e.base.PendingCharacters = append(e.base.PendingCharacters, e.rowGroups[0]...)
		e.rowGroupIdx = 1
	}
}
func (e *OverflowEffect) Next() (string, bool) {
	for i := 0; i < 2 && len(e.base.PendingCharacters) > 0; i++ {
		ch := e.base.PendingCharacters[0]
		e.base.PendingCharacters = e.base.PendingCharacters[1:]
		e.base.Activate(ch)
	}
	if len(e.base.PendingCharacters) == 0 && e.rowGroupIdx < len(e.rowGroups) {
		e.base.PendingCharacters = append(e.base.PendingCharacters, e.rowGroups[e.rowGroupIdx]...)
		e.rowGroupIdx++
	}
	e.base.Update()
	return e.base.Frame(), !e.base.HasWork() && e.rowGroupIdx >= len(e.rowGroups)
}
func (e *OverflowEffect) Reset() { e.rowGroupIdx = 0 }
