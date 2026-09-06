package effects

import (
	"math"
	"math/rand"
	"sort"

	tte "github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/tteengine"
)

// ── helpers (suffix 6) ────────────────────────────────────────────────────────

func max6(a, b int) int {
	if a > b {
		return a
	}
	return b
}
func min6(a, b int) int {
	if a < b {
		return a
	}
	return b
}
func abs6(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

var _ = math.Sqrt
var _ = sort.Ints
var _ = max6
var _ = min6
var _ = abs6

// ── 1. SynthGridEffect ────────────────────────────────────────────────────────

type SynthGridConfig struct {
	GridColor              tte.Color
	TextGenSymbols         []string
	MaxActiveBlocks        float64
	FinalGradientStops     []tte.Color
	FinalGradientSteps     []int
	FinalGradientDirection tte.GradientDirection
}

func DefaultSynthGridConfig() SynthGridConfig {
	return SynthGridConfig{
		GridColor:       tte.MustColorFromHex("CC00CC"),
		TextGenSymbols:  []string{"░", "▒", "▓"},
		MaxActiveBlocks: 0.1,
		FinalGradientStops: []tte.Color{
			tte.MustColorFromHex("8A008A"),
			tte.MustColorFromHex("00D1FF"),
			tte.MustColorFromHex("FFFFFF"),
		},
		FinalGradientSteps:     []int{12},
		FinalGradientDirection: tte.GradientVertical,
	}
}

type SynthGridEffect struct {
	Config          SynthGridConfig
	base            *tte.BaseIterator
	terminal        *tte.Terminal
	phase           string
	gridTick        int
	pendingBlocks   [][]*tte.EffectCharacter
	blockIdx        int
	activeBlocks    int
	maxActiveBlocks int
	dissolveScns    map[int]*tte.Scene
}

func NewSynthGridEffect(cfg SynthGridConfig) *SynthGridEffect { return &SynthGridEffect{Config: cfg} }
func (e *SynthGridEffect) Name() string                       { return "synthgrid" }
func (e *SynthGridEffect) Init(t *tte.Terminal) {
	e.terminal = t
	e.base = tte.NewBaseIterator(t)
	e.phase = "grid"
	e.gridTick = 0
	e.blockIdx = 0
	e.activeBlocks = 0
	e.dissolveScns = make(map[int]*tte.Scene)
	canvas := t.Canvas.Canvas

	grad := tte.NewGradient(e.Config.FinalGradientStops, e.Config.FinalGradientSteps)
	colorMap := grad.BuildCoordinateColorMapping(canvas.TextBottom, canvas.TextTop, canvas.TextLeft, canvas.TextRight, e.Config.FinalGradientDirection)
	gridGrad := tte.NewGradient([]tte.Color{e.Config.GridColor, tte.MustColorFromHex("ffffff")}, []int{6})

	// group chars into 4x4 blocks
	chars := t.GetCharacters()
	blockMap := make(map[[2]int][]*tte.EffectCharacter)
	for _, ch := range chars {
		bk := [2]int{(ch.InputCoord().Column - canvas.TextLeft) / 4, (ch.InputCoord().Row - canvas.TextBottom) / 4}
		blockMap[bk] = append(blockMap[bk], ch)
	}
	blocks := make([][]*tte.EffectCharacter, 0, len(blockMap))
	for _, b := range blockMap {
		blocks = append(blocks, b)
	}
	rand.Shuffle(len(blocks), func(i, j int) { blocks[i], blocks[j] = blocks[j], blocks[i] })
	e.pendingBlocks = blocks

	for _, ch := range chars {
		finalColor := colorMap[ch.InputCoord()]
		dissolveScn := ch.Animation.NewScene("dissolve", false, tte.SyncNone, nil)
		for k := range 8 {
			sym := e.Config.TextGenSymbols[rand.Intn(len(e.Config.TextGenSymbols))]
			dissolveScn.AddFrame(sym, 2, tte.FGOnly(gridGrad.At(float64(k)/7.0)))
		}
		if ch.InputSymbol() == " " {
			dissolveScn.AddFrame(ch.InputSymbol(), 1, tte.ColorPair{})
		} else {
			dissolveScn.AddFrame(ch.InputSymbol(), 1, tte.FGOnly(finalColor))
		}
		eh := ch.EventHandler
		eh.RegisterEvent(tte.EventSceneComplete, dissolveScn, tte.ActionCallback,
			tte.Callback{Fn: func(_ *tte.EffectCharacter) { e.activeBlocks-- }})
		e.dissolveScns[ch.ID()] = dissolveScn
	}
	e.maxActiveBlocks = max6(1, int(e.Config.MaxActiveBlocks*float64(len(blocks))))
}
func (e *SynthGridEffect) Next() (string, bool) {
	switch e.phase {
	case "grid":
		e.gridTick++
		if e.gridTick >= 30 {
			e.phase = "dissolve"
		}
	case "dissolve":
		// release up to maxActiveBlocks new blocks per tick
		released := 0
		for released < e.maxActiveBlocks && e.blockIdx < len(e.pendingBlocks) {
			block := e.pendingBlocks[e.blockIdx]
			e.blockIdx++
			released++
			for _, ch := range block {
				e.terminal.SetCharacterVisibility(ch, true)
				ch.Animation.ActivateScene(e.dissolveScns[ch.ID()])
				e.base.Activate(ch)
			}
		}
		if e.blockIdx >= len(e.pendingBlocks) && !e.base.HasWork() {
			return e.base.Frame(), true
		}
	}
	e.base.Update()
	return e.base.Frame(), false
}
func (e *SynthGridEffect) Reset() {
	e.phase = "grid"
	e.gridTick = 0
	e.blockIdx = 0
	e.activeBlocks = 0
}

// ── 2. ThunderstormEffect ────────────────────────────────────────────────────

type ThunderstormConfig struct {
	RainColor              tte.Color
	FinalGradientStops     []tte.Color
	FinalGradientSteps     []int
	FinalGradientDirection tte.GradientDirection
	RainDuration           int
	LightningDuration      int
}

func DefaultThunderstormConfig() ThunderstormConfig {
	return ThunderstormConfig{
		RainColor: tte.MustColorFromHex("68A3E8"),
		FinalGradientStops: []tte.Color{
			tte.MustColorFromHex("8A008A"),
			tte.MustColorFromHex("00D1FF"),
			tte.MustColorFromHex("FFFFFF"),
		},
		FinalGradientSteps:     []int{12},
		FinalGradientDirection: tte.GradientVertical,
		RainDuration:           100,
		LightningDuration:      20,
	}
}

type ThunderstormEffect struct {
	Config    ThunderstormConfig
	base      *tte.BaseIterator
	terminal  *tte.Terminal
	tick      int
	phase     string
	colorMap  map[tte.Coord]tte.Color
	flashScns map[int]*tte.Scene
	finalScns map[int]*tte.Scene
}

func NewThunderstormEffect(cfg ThunderstormConfig) *ThunderstormEffect {
	return &ThunderstormEffect{Config: cfg}
}
func (e *ThunderstormEffect) Name() string { return "thunderstorm" }
func (e *ThunderstormEffect) Init(t *tte.Terminal) {
	e.terminal = t
	e.base = tte.NewBaseIterator(t)
	e.tick = 0
	e.phase = "rain"
	e.flashScns = make(map[int]*tte.Scene)
	e.finalScns = make(map[int]*tte.Scene)
	canvas := t.Canvas.Canvas

	grad := tte.NewGradient(e.Config.FinalGradientStops, e.Config.FinalGradientSteps)
	e.colorMap = grad.BuildCoordinateColorMapping(canvas.TextBottom, canvas.TextTop, canvas.TextLeft, canvas.TextRight, e.Config.FinalGradientDirection)
	rainColor := e.Config.RainColor
	rainGrad := tte.NewGradient([]tte.Color{rainColor, tte.AdjustColorBrightness(rainColor, 0.5)}, []int{3})

	for _, ch := range t.GetCharacters() {
		finalColor := e.colorMap[ch.InputCoord()]
		dim := tte.AdjustColorBrightness(finalColor, 0.5)

		t.SetCharacterVisibility(ch, true)
		ch.Animation.SetAppearance(ch.InputSymbol(), tte.FGOnly(dim))

		rainScn := ch.Animation.NewScene("rain", true, tte.SyncNone, nil)
		for _, sym := range []string{"\\", ".", ","} {
			rainScn.AddFrame(sym, 3, tte.FGOnly(rainGrad.At(float64(rand.Intn(len(rainGrad.Spectrum)))/float64(len(rainGrad.Spectrum)))))
		}
		ch.Animation.ActivateScene(rainScn)

		flashScn := ch.Animation.NewScene("flash", false, tte.SyncNone, nil)
		flashScn.AddFrame(ch.InputSymbol(), 3, tte.FGOnly(tte.MustColorFromHex("ffffff")))
		e.flashScns[ch.ID()] = flashScn

		finalScn := ch.Animation.NewScene("final", false, tte.SyncNone, nil)
		finalGrad := tte.NewGradient([]tte.Color{tte.MustColorFromHex("ffffff"), finalColor}, []int{6})
		finalScn.ApplyGradientToSymbols([]string{ch.InputSymbol()}, 4, finalGrad, nil)
		finalScn.AddFrame(ch.InputSymbol(), 1, tte.FGOnly(finalColor))
		e.finalScns[ch.ID()] = finalScn

		e.base.ActiveCharacters[ch.ID()] = ch
	}
}
func (e *ThunderstormEffect) Next() (string, bool) {
	e.tick++
	if e.phase == "rain" && e.tick >= e.Config.RainDuration {
		e.phase = "lightning"
		for _, ch := range e.base.ActiveCharacters {
			ch.Animation.ActivateScene(e.flashScns[ch.ID()])
		}
	} else if e.phase == "lightning" && e.tick >= e.Config.RainDuration+e.Config.LightningDuration {
		e.phase = "settle"
		for _, ch := range e.base.ActiveCharacters {
			ch.Animation.ActivateScene(e.finalScns[ch.ID()])
		}
	}
	e.base.Update()
	done := e.phase == "settle" && !e.base.HasWork()
	return e.base.Frame(), done
}
func (e *ThunderstormEffect) Reset() { e.tick = 0; e.phase = "rain" }

// ── 3. UnstableEffect ────────────────────────────────────────────────────────

type UnstableConfig struct {
	RumbleDuration         int
	FinalGradientStops     []tte.Color
	FinalGradientSteps     []int
	FinalGradientDirection tte.GradientDirection
}

func DefaultUnstableConfig() UnstableConfig {
	return UnstableConfig{
		RumbleDuration: 80,
		FinalGradientStops: []tte.Color{
			tte.MustColorFromHex("8A008A"),
			tte.MustColorFromHex("00D1FF"),
			tte.MustColorFromHex("FFFFFF"),
		},
		FinalGradientSteps:     []int{12},
		FinalGradientDirection: tte.GradientVertical,
	}
}

type UnstableEffect struct {
	Config    UnstableConfig
	base      *tte.BaseIterator
	terminal  *tte.Terminal
	tick      int
	phase     string
	colorMap  map[tte.Coord]tte.Color
	landScns  map[int]*tte.Scene
	landPaths map[int]*tte.Path
	rumbleScn map[int]*tte.Scene
}

func NewUnstableEffect(cfg UnstableConfig) *UnstableEffect { return &UnstableEffect{Config: cfg} }
func (e *UnstableEffect) Name() string                     { return "unstable" }
func (e *UnstableEffect) Init(t *tte.Terminal) {
	e.terminal = t
	e.base = tte.NewBaseIterator(t)
	e.tick = 0
	e.phase = "rumble"
	e.landScns = make(map[int]*tte.Scene)
	e.landPaths = make(map[int]*tte.Path)
	e.rumbleScn = make(map[int]*tte.Scene)
	canvas := t.Canvas.Canvas

	grad := tte.NewGradient(e.Config.FinalGradientStops, e.Config.FinalGradientSteps)
	e.colorMap = grad.BuildCoordinateColorMapping(canvas.TextBottom, canvas.TextTop, canvas.TextLeft, canvas.TextRight, e.Config.FinalGradientDirection)
	orange := tte.MustColorFromHex("FFA500")

	for _, ch := range t.GetCharacters() {
		finalColor := e.colorMap[ch.InputCoord()]
		t.SetCharacterVisibility(ch, true)

		rumbleScn := ch.Animation.NewScene("rumble", true, tte.SyncNone, nil)
		rumbleScn.AddFrame(ch.InputSymbol(), 1, tte.FGOnly(orange))
		ch.Animation.ActivateScene(rumbleScn)
		e.rumbleScn[ch.ID()] = rumbleScn

		explodePath := ch.Motion.NewPath("explode", 1.5, tte.EaseOutExpo, 0, false)
		explodePath.NewWaypoint(canvas.RandomCoord(true, false), "edge")

		landPath := ch.Motion.NewPath("land", 0.4, tte.EaseInOutCubic, 0, false)
		landPath.NewWaypoint(ch.InputCoord(), "home")
		e.landPaths[ch.ID()] = landPath

		landScn := ch.Animation.NewScene("land", false, tte.SyncNone, nil)
		landGrad := tte.NewGradient([]tte.Color{orange, finalColor}, []int{6})
		landScn.ApplyGradientToSymbols([]string{ch.InputSymbol()}, 3, landGrad, nil)
		landScn.AddFrame(ch.InputSymbol(), 1, tte.FGOnly(finalColor))
		e.landScns[ch.ID()] = landScn

		eh := ch.EventHandler
		eh.RegisterEvent(tte.EventPathComplete, explodePath, tte.ActionActivatePath, landPath)
		eh.RegisterEvent(tte.EventPathComplete, landPath, tte.ActionActivateScene, landScn)

		e.base.ActiveCharacters[ch.ID()] = ch

		// store explode path reference in motion for later activation
		_ = explodePath
	}
}
func (e *UnstableEffect) Next() (string, bool) {
	e.tick++
	if e.phase == "rumble" {
		// jitter positions
		if e.tick%5 == 0 {
			canvas := e.terminal.Canvas.Canvas
			for _, ch := range e.base.ActiveCharacters {
				cur := ch.InputCoord()
				jc := clamp(cur.Column+rand.Intn(3)-1, canvas.Left, canvas.Right)
				jr := clamp(cur.Row+rand.Intn(3)-1, canvas.Bottom, canvas.Top)
				ch.Motion.SetCoordinate(tte.Coord{Column: jc, Row: jr})
			}
		}
		if e.tick >= e.Config.RumbleDuration {
			e.phase = "explode"
			for _, ch := range e.base.ActiveCharacters {
				// reset to home first
				ch.Motion.SetCoordinate(ch.InputCoord())
				// activate the explode path
				explodePath := ch.Motion.QueryPath("explode")
				if explodePath != nil {
					ch.Motion.ActivatePath(explodePath, ch.EventHandler)
				}
			}
		}
	} else if e.phase == "explode" {
		if !e.base.HasWork() {
			// all exploded and landed
			e.phase = "done"
		}
	}
	e.base.Update()
	return e.base.Frame(), e.phase == "done" && !e.base.HasWork()
}
func (e *UnstableEffect) Reset() { e.tick = 0; e.phase = "rumble" }

// ── 4. VHSTapeEffect ─────────────────────────────────────────────────────────

type VHSTapeConfig struct {
	GlitchDuration         int
	FinalGradientStops     []tte.Color
	FinalGradientSteps     []int
	FinalGradientDirection tte.GradientDirection
}

func DefaultVHSTapeConfig() VHSTapeConfig {
	return VHSTapeConfig{
		GlitchDuration: 100,
		FinalGradientStops: []tte.Color{
			tte.MustColorFromHex("8A008A"),
			tte.MustColorFromHex("00D1FF"),
			tte.MustColorFromHex("FFFFFF"),
		},
		FinalGradientSteps:     []int{12},
		FinalGradientDirection: tte.GradientVertical,
	}
}

type VHSTapeEffect struct {
	Config   VHSTapeConfig
	terminal *tte.Terminal
	tick     int
	phase    string
	doneTick int
	colorMap map[tte.Coord]tte.Color
	rowMap   map[int][]*tte.EffectCharacter
}

func NewVHSTapeEffect(cfg VHSTapeConfig) *VHSTapeEffect { return &VHSTapeEffect{Config: cfg} }
func (e *VHSTapeEffect) Name() string                   { return "vhstape" }
func (e *VHSTapeEffect) Init(t *tte.Terminal) {
	e.terminal = t
	e.tick = 0
	e.phase = "glitch"
	e.doneTick = 0
	e.rowMap = make(map[int][]*tte.EffectCharacter)
	canvas := t.Canvas.Canvas

	grad := tte.NewGradient(e.Config.FinalGradientStops, e.Config.FinalGradientSteps)
	e.colorMap = grad.BuildCoordinateColorMapping(canvas.TextBottom, canvas.TextTop, canvas.TextLeft, canvas.TextRight, e.Config.FinalGradientDirection)

	for _, ch := range t.GetCharacters() {
		finalColor := e.colorMap[ch.InputCoord()]
		t.SetCharacterVisibility(ch, true)
		ch.Animation.SetAppearance(ch.InputSymbol(), tte.FGOnly(finalColor))
		e.rowMap[ch.InputCoord().Row] = append(e.rowMap[ch.InputCoord().Row], ch)
	}
}
func (e *VHSTapeEffect) Next() (string, bool) {
	e.tick++
	canvas := e.terminal.Canvas.Canvas

	if e.phase == "glitch" {
		// random chance to glitch a row
		if rand.Float64() < 0.2 {
			rows := make([]int, 0, len(e.rowMap))
			for r := range e.rowMap {
				rows = append(rows, r)
			}
			if len(rows) > 0 {
				row := rows[rand.Intn(len(rows))]
				offset := rand.Intn(7) - 3
				glitchColor := tte.MustColorFromHex("ff0000")
				for _, ch := range e.rowMap[row] {
					newCol := clamp(ch.InputCoord().Column+offset, canvas.Left, canvas.Right)
					ch.Motion.SetCoordinate(tte.Coord{Column: newCol, Row: ch.InputCoord().Row})
					if rand.Float64() < 0.05 {
						ch.Animation.SetAppearance(ch.InputSymbol(), tte.FGOnly(glitchColor))
					}
				}
			}
		}
		if e.tick >= e.Config.GlitchDuration {
			e.phase = "restore"
			for _, ch := range e.terminal.GetCharacters() {
				finalColor := e.colorMap[ch.InputCoord()]
				ch.Motion.SetCoordinate(ch.InputCoord())
				ch.Animation.SetAppearance(ch.InputSymbol(), tte.FGOnly(finalColor))
			}
		}
	} else if e.phase == "restore" {
		e.doneTick++
	}

	return e.terminal.GetFormattedOutputString(), e.phase == "restore" && e.doneTick >= 5
}
func (e *VHSTapeEffect) Reset() { e.tick = 0; e.phase = "glitch"; e.doneTick = 0 }

// ── 5. WavesEffect ────────────────────────────────────────────────────────────

type WavesConfig struct {
	WaveSymbols            []string
	WaveColor              tte.Color
	FinalGradientStops     []tte.Color
	FinalGradientSteps     []int
	FinalGradientDirection tte.GradientDirection
}

func DefaultWavesConfig() WavesConfig {
	return WavesConfig{
		WaveSymbols: []string{"▁", "▂", "▃", "▄", "▅", "▆", "▇", "█", "▇", "▆", "▅", "▄", "▃", "▂", "▁"},
		WaveColor:   tte.MustColorFromHex("00D1FF"),
		FinalGradientStops: []tte.Color{
			tte.MustColorFromHex("8A008A"),
			tte.MustColorFromHex("00D1FF"),
			tte.MustColorFromHex("FFFFFF"),
		},
		FinalGradientSteps:     []int{12},
		FinalGradientDirection: tte.GradientDiagonal,
	}
}

type WavesEffect struct {
	Config    WavesConfig
	base      *tte.BaseIterator
	terminal  *tte.Terminal
	colGroups [][]*tte.EffectCharacter
	colIdx    int
	waveScns  map[int]*tte.Scene
	finalScns map[int]*tte.Scene
}

func NewWavesEffect(cfg WavesConfig) *WavesEffect { return &WavesEffect{Config: cfg} }
func (e *WavesEffect) Name() string               { return "waves" }
func (e *WavesEffect) Init(t *tte.Terminal) {
	e.terminal = t
	e.base = tte.NewBaseIterator(t)
	e.waveScns = make(map[int]*tte.Scene)
	e.finalScns = make(map[int]*tte.Scene)
	e.colIdx = 0
	canvas := t.Canvas.Canvas

	grad := tte.NewGradient(e.Config.FinalGradientStops, e.Config.FinalGradientSteps)
	colorMap := grad.BuildCoordinateColorMapping(canvas.TextBottom, canvas.TextTop, canvas.TextLeft, canvas.TextRight, e.Config.FinalGradientDirection)
	waveColor := e.Config.WaveColor

	colMap := make(map[int][]*tte.EffectCharacter)
	for _, ch := range t.GetCharacters() {
		finalColor := colorMap[ch.InputCoord()]
		waveGrad := tte.NewGradient([]tte.Color{waveColor, finalColor}, []int{5})

		waveScn := ch.Animation.NewScene("wave", false, tte.SyncNone, nil)
		waveScn.ApplyGradientToSymbols(e.Config.WaveSymbols, 2, waveGrad, nil)
		e.waveScns[ch.ID()] = waveScn

		finalScn := ch.Animation.NewScene("final", true, tte.SyncNone, nil)
		finalScn.AddFrame(ch.InputSymbol(), 1, tte.FGOnly(finalColor))
		e.finalScns[ch.ID()] = finalScn

		ch.EventHandler.RegisterEvent(tte.EventSceneComplete, waveScn, tte.ActionActivateScene, finalScn)
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
func (e *WavesEffect) Next() (string, bool) {
	if e.colIdx < len(e.colGroups) {
		for _, ch := range e.colGroups[e.colIdx] {
			ch.Animation.ActivateScene(e.waveScns[ch.ID()])
			e.base.Activate(ch)
			e.terminal.SetCharacterVisibility(ch, true)
		}
		e.colIdx++
	}
	e.base.Update()
	done := e.colIdx >= len(e.colGroups) && !e.base.HasWork()
	return e.base.Frame(), done
}
func (e *WavesEffect) Reset() { e.colIdx = 0 }

// ── 6. WipeEffect ─────────────────────────────────────────────────────────────

type WipeConfig struct {
	WipeDirection          string // "right","left","up","down"
	WipeSpeed              int
	FinalGradientStops     []tte.Color
	FinalGradientSteps     []int
	FinalGradientDirection tte.GradientDirection
}

func DefaultWipeConfig() WipeConfig {
	return WipeConfig{
		WipeDirection: "right",
		WipeSpeed:     1,
		FinalGradientStops: []tte.Color{
			tte.MustColorFromHex("8A008A"),
			tte.MustColorFromHex("00D1FF"),
			tte.MustColorFromHex("FFFFFF"),
		},
		FinalGradientSteps:     []int{12},
		FinalGradientDirection: tte.GradientVertical,
	}
}

type WipeEffect struct {
	Config      WipeConfig
	base        *tte.BaseIterator
	terminal    *tte.Terminal
	groups      [][]*tte.EffectCharacter
	groupIdx    int
	wipeScns    map[int]*tte.Scene
	tickCounter int
}

func NewWipeEffect(cfg WipeConfig) *WipeEffect { return &WipeEffect{Config: cfg} }
func (e *WipeEffect) Name() string             { return "wipe" }
func (e *WipeEffect) Init(t *tte.Terminal) {
	e.terminal = t
	e.base = tte.NewBaseIterator(t)
	e.wipeScns = make(map[int]*tte.Scene)
	e.groupIdx = 0
	e.tickCounter = 0
	canvas := t.Canvas.Canvas

	grad := tte.NewGradient(e.Config.FinalGradientStops, e.Config.FinalGradientSteps)
	colorMap := grad.BuildCoordinateColorMapping(canvas.TextBottom, canvas.TextTop, canvas.TextLeft, canvas.TextRight, e.Config.FinalGradientDirection)

	groupMap := make(map[int][]*tte.EffectCharacter)
	for _, ch := range t.GetCharacters() {
		finalColor := colorMap[ch.InputCoord()]
		wipeScn := ch.Animation.NewScene("wipe", false, tte.SyncNone, nil)
		wipeGrad := tte.NewGradient([]tte.Color{e.Config.FinalGradientStops[0], finalColor}, []int{6})
		wipeScn.ApplyGradientToSymbols([]string{ch.InputSymbol()}, 4, wipeGrad, nil)
		wipeScn.AddFrame(ch.InputSymbol(), 1, tte.FGOnly(finalColor))
		e.wipeScns[ch.ID()] = wipeScn

		var key int
		switch e.Config.WipeDirection {
		case "up", "down":
			key = ch.InputCoord().Row
		default: // "right", "left"
			key = ch.InputCoord().Column
		}
		groupMap[key] = append(groupMap[key], ch)
	}

	keys := make([]int, 0, len(groupMap))
	for k := range groupMap {
		keys = append(keys, k)
	}
	switch e.Config.WipeDirection {
	case "left", "down":
		sort.Sort(sort.Reverse(sort.IntSlice(keys)))
	default:
		sort.Ints(keys)
	}
	e.groups = make([][]*tte.EffectCharacter, len(keys))
	for i, k := range keys {
		e.groups[i] = groupMap[k]
	}
}
func (e *WipeEffect) Next() (string, bool) {
	e.tickCounter++
	if e.tickCounter >= e.Config.WipeSpeed && e.groupIdx < len(e.groups) {
		e.tickCounter = 0
		for _, ch := range e.groups[e.groupIdx] {
			ch.Animation.ActivateScene(e.wipeScns[ch.ID()])
			e.base.Activate(ch)
			e.terminal.SetCharacterVisibility(ch, true)
		}
		e.groupIdx++
	}
	e.base.Update()
	done := e.groupIdx >= len(e.groups) && !e.base.HasWork()
	return e.base.Frame(), done
}
func (e *WipeEffect) Reset() { e.groupIdx = 0; e.tickCounter = 0 }
