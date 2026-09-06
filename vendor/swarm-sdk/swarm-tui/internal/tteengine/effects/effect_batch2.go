package effects

import (
	"math/rand"
	"sort"

	tte "github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/tteengine"
)

// ── helpers (suffix 2) ────────────────────────────────────────────────────────

func clamp2(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
func max2(a, b int) int {
	if a > b {
		return a
	}
	return b
}
func min2(a, b int) int {
	if a < b {
		return a
	}
	return b
}

var _ = clamp2
var _ = min2
var _ = sort.Ints

// ── 1. ColorShiftEffect ───────────────────────────────────────────────────────

type ColorShiftConfig struct {
	GradientStops     []tte.Color
	GradientSteps     []int
	GradientDirection tte.GradientDirection
}

func DefaultColorShiftConfig() ColorShiftConfig {
	return ColorShiftConfig{
		GradientStops: []tte.Color{
			tte.MustColorFromHex("8A008A"),
			tte.MustColorFromHex("00D1FF"),
			tte.MustColorFromHex("FFFFFF"),
		},
		GradientSteps:     []int{12},
		GradientDirection: tte.GradientRadial,
	}
}

type ColorShiftEffect struct {
	Config   ColorShiftConfig
	base     *tte.BaseIterator
	terminal *tte.Terminal
	tick     int
	maxTicks int
}

func NewColorShiftEffect(cfg ColorShiftConfig) *ColorShiftEffect {
	return &ColorShiftEffect{Config: cfg}
}
func (e *ColorShiftEffect) Name() string { return "colorshift" }
func (e *ColorShiftEffect) Init(t *tte.Terminal) {
	e.terminal = t
	e.base = tte.NewBaseIterator(t)
	e.tick = 0
	e.maxTicks = 200

	grad := tte.NewGradient(e.Config.GradientStops, e.Config.GradientSteps)

	for _, ch := range t.GetCharacters() {
		t.SetCharacterVisibility(ch, true)
		shiftScn := ch.Animation.NewScene("shift", true, tte.SyncNone, nil)
		for _, c := range grad.Spectrum {
			col := c
			shiftScn.AddFrame(ch.InputSymbol(), 3, tte.FGOnly(col))
		}
		ch.Animation.ActivateScene(shiftScn)
		e.base.ActiveCharacters[ch.ID()] = ch
	}
}
func (e *ColorShiftEffect) Next() (string, bool) {
	e.base.Update()
	e.tick++
	return e.base.Frame(), e.tick >= e.maxTicks
}
func (e *ColorShiftEffect) Reset() { e.tick = 0 }

// ── 2. CrumbleEffect ─────────────────────────────────────────────────────────

type CrumbleConfig struct {
	FinalGradientStops     []tte.Color
	FinalGradientSteps     []int
	FinalGradientDirection tte.GradientDirection
}

func DefaultCrumbleConfig() CrumbleConfig {
	return CrumbleConfig{
		FinalGradientStops: []tte.Color{
			tte.MustColorFromHex("8A008A"),
			tte.MustColorFromHex("00D1FF"),
			tte.MustColorFromHex("FFFFFF"),
		},
		FinalGradientSteps:     []int{12},
		FinalGradientDirection: tte.GradientDiagonal,
	}
}

type CrumbleEffect struct {
	Config   CrumbleConfig
	base     *tte.BaseIterator
	terminal *tte.Terminal
}

func NewCrumbleEffect(cfg CrumbleConfig) *CrumbleEffect { return &CrumbleEffect{Config: cfg} }
func (e *CrumbleEffect) Name() string                   { return "crumble" }
func (e *CrumbleEffect) Init(t *tte.Terminal) {
	e.terminal = t
	e.base = tte.NewBaseIterator(t)
	canvas := t.Canvas.Canvas
	grad := tte.NewGradient(e.Config.FinalGradientStops, e.Config.FinalGradientSteps)
	colorMap := grad.BuildCoordinateColorMapping(canvas.TextBottom, canvas.TextTop, canvas.TextLeft, canvas.TextRight, e.Config.FinalGradientDirection)
	black := tte.MustColorFromHex("000000")
	crumbleSyms := []string{"▓", "▒", "░", "·", "."}

	for _, ch := range t.GetCharacters() {
		finalColor := colorMap[ch.InputCoord()]
		t.SetCharacterVisibility(ch, true)

		crumbleScn := ch.Animation.NewScene("crumble", false, tte.SyncNone, nil)
		dim := tte.AdjustColorBrightness(finalColor, 0.3)
		for _, sym := range crumbleSyms {
			crumbleScn.AddFrame(sym, 3, tte.FGOnly(dim))
		}
		crumbleScn.AddFrame(".", 3, tte.FGOnly(black))

		reformScn := ch.Animation.NewScene("reform", false, tte.SyncNone, nil)
		reformSyms := []string{".", "·", "░", "▒", "▓", ch.InputSymbol()}
		for i, sym := range reformSyms {
			t2 := float64(i) / float64(max2(1, len(reformSyms)-1))
			c := tte.AdjustColorBrightness(finalColor, t2)
			reformScn.AddFrame(sym, 3, tte.FGOnly(c))
		}
		reformScn.AddFrame(ch.InputSymbol(), 1, tte.FGOnly(finalColor))

		ch.EventHandler.RegisterEvent(tte.EventSceneComplete, crumbleScn, tte.ActionActivateScene, reformScn)
		ch.Animation.ActivateScene(crumbleScn)
		e.base.ActiveCharacters[ch.ID()] = ch
	}
}
func (e *CrumbleEffect) Next() (string, bool) {
	e.base.Update()
	return e.base.Frame(), !e.base.HasWork()
}
func (e *CrumbleEffect) Reset() {}

// ── 3. DecryptEffect ─────────────────────────────────────────────────────────

type DecryptConfig struct {
	DecryptSpeed       int
	FinalGradientStops []tte.Color
	FinalGradientSteps []int
	FinalGradientDir   tte.GradientDirection
}

func DefaultDecryptConfig() DecryptConfig {
	return DecryptConfig{
		DecryptSpeed: 1,
		FinalGradientStops: []tte.Color{
			tte.MustColorFromHex("003300"),
			tte.MustColorFromHex("00FF41"),
		},
		FinalGradientSteps: []int{8},
		FinalGradientDir:   tte.GradientVertical,
	}
}

type DecryptEffect struct {
	Config   DecryptConfig
	base     *tte.BaseIterator
	terminal *tte.Terminal
}

func NewDecryptEffect(cfg DecryptConfig) *DecryptEffect { return &DecryptEffect{Config: cfg} }
func (e *DecryptEffect) Name() string                   { return "decrypt" }
func (e *DecryptEffect) Init(t *tte.Terminal) {
	e.terminal = t
	e.base = tte.NewBaseIterator(t)
	canvas := t.Canvas.Canvas
	grad := tte.NewGradient(e.Config.FinalGradientStops, e.Config.FinalGradientSteps)
	colorMap := grad.BuildCoordinateColorMapping(canvas.TextBottom, canvas.TextTop, canvas.TextLeft, canvas.TextRight, e.Config.FinalGradientDir)

	cipherColor := tte.MustColorFromHex("00FF41")
	cipherSyms := []string{"!", "@", "#", "$", "%", "^", "&", "*", "a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l"}

	chars := t.GetCharacters()
	rand.Shuffle(len(chars), func(i, j int) { chars[i], chars[j] = chars[j], chars[i] })

	for _, ch := range chars {
		finalColor := colorMap[ch.InputCoord()]
		t.SetCharacterVisibility(ch, true)

		decryptScn := ch.Animation.NewScene("decrypt", false, tte.SyncNone, nil)
		for range 8 {
			sym := cipherSyms[rand.Intn(len(cipherSyms))]
			decryptScn.AddFrame(sym, 3, tte.FGOnly(cipherColor))
		}
		decryptScn.AddFrame(ch.InputSymbol(), 1, tte.FGOnly(finalColor))
		ch.Animation.ActivateScene(decryptScn)
	}
	e.base.PendingCharacters = chars
}
func (e *DecryptEffect) Next() (string, bool) {
	for i := 0; i < e.Config.DecryptSpeed && len(e.base.PendingCharacters) > 0; i++ {
		ch := e.base.PendingCharacters[0]
		e.base.PendingCharacters = e.base.PendingCharacters[1:]
		e.base.Activate(ch)
	}
	e.base.Update()
	return e.base.Frame(), !e.base.HasWork()
}
func (e *DecryptEffect) Reset() {}

// ── 4. ErrorCorrectEffect ─────────────────────────────────────────────────────

type ErrorCorrectConfig struct {
	ErrorFraction          float64
	FinalGradientStops     []tte.Color
	FinalGradientSteps     []int
	FinalGradientDirection tte.GradientDirection
}

func DefaultErrorCorrectConfig() ErrorCorrectConfig {
	return ErrorCorrectConfig{
		ErrorFraction: 0.1,
		FinalGradientStops: []tte.Color{
			tte.MustColorFromHex("8A008A"),
			tte.MustColorFromHex("00D1FF"),
			tte.MustColorFromHex("FFFFFF"),
		},
		FinalGradientSteps:     []int{12},
		FinalGradientDirection: tte.GradientDiagonal,
	}
}

type ErrorCorrectEffect struct {
	Config   ErrorCorrectConfig
	base     *tte.BaseIterator
	terminal *tte.Terminal
}

func NewErrorCorrectEffect(cfg ErrorCorrectConfig) *ErrorCorrectEffect {
	return &ErrorCorrectEffect{Config: cfg}
}
func (e *ErrorCorrectEffect) Name() string { return "errorcorrect" }
func (e *ErrorCorrectEffect) Init(t *tte.Terminal) {
	e.terminal = t
	e.base = tte.NewBaseIterator(t)
	canvas := t.Canvas.Canvas
	grad := tte.NewGradient(e.Config.FinalGradientStops, e.Config.FinalGradientSteps)
	colorMap := grad.BuildCoordinateColorMapping(canvas.TextBottom, canvas.TextTop, canvas.TextLeft, canvas.TextRight, e.Config.FinalGradientDirection)
	red := tte.MustColorFromHex("FF0000")

	chars := t.GetCharacters()
	n := len(chars)
	wrongCount := max2(1, int(float64(n)*e.Config.ErrorFraction))

	perm := rand.Perm(n)
	wrongSet := make(map[int]bool, wrongCount)
	for _, i := range perm[:wrongCount] {
		wrongSet[i] = true
	}

	var wrongChars []*tte.EffectCharacter
	for i, ch := range chars {
		finalColor := colorMap[ch.InputCoord()]
		if wrongSet[i] {
			otherIdx := rand.Intn(n)
			for otherIdx == i {
				otherIdx = rand.Intn(n)
			}
			wrongStart := chars[otherIdx].InputCoord()

			wrongScn := ch.Animation.NewScene("wrong", true, tte.SyncNone, nil)
			wrongScn.AddFrame(ch.InputSymbol(), 2, tte.FGOnly(red))

			correctScn := ch.Animation.NewScene("correct", false, tte.SyncNone, nil)
			correctGrad := tte.NewGradient([]tte.Color{red, finalColor}, []int{6})
			correctScn.ApplyGradientToSymbols([]string{ch.InputSymbol()}, 3, correctGrad, nil)
			correctScn.AddFrame(ch.InputSymbol(), 1, tte.FGOnly(finalColor))

			ch.Motion.SetCoordinate(wrongStart)
			path := ch.Motion.NewPath("correct_path", 0.8, tte.EaseInOutQuad, 0, false)
			path.NewWaypoint(ch.InputCoord(), "home")

			eh := ch.EventHandler
			eh.RegisterEvent(tte.EventPathActivated, path, tte.ActionActivateScene, wrongScn)
			eh.RegisterEvent(tte.EventPathComplete, path, tte.ActionActivateScene, correctScn)
			t.SetCharacterVisibility(ch, true)
			ch.Motion.ActivatePath(path, eh)
			wrongChars = append(wrongChars, ch)
		} else {
			ch.Animation.SetAppearance(ch.InputSymbol(), tte.FGOnly(finalColor))
			t.SetCharacterVisibility(ch, true)
		}
	}
	rand.Shuffle(len(wrongChars), func(i, j int) { wrongChars[i], wrongChars[j] = wrongChars[j], wrongChars[i] })
	e.base.PendingCharacters = wrongChars
}
func (e *ErrorCorrectEffect) Next() (string, bool) {
	for i := 0; i < 2 && len(e.base.PendingCharacters) > 0; i++ {
		ch := e.base.PendingCharacters[0]
		e.base.PendingCharacters = e.base.PendingCharacters[1:]
		e.base.Activate(ch)
	}
	e.base.Update()
	return e.base.Frame(), !e.base.HasWork()
}
func (e *ErrorCorrectEffect) Reset() {}

// ── 5. ExpandEffect ──────────────────────────────────────────────────────────

type ExpandConfig struct {
	ExpandSpeed            float64
	FinalGradientStops     []tte.Color
	FinalGradientSteps     []int
	FinalGradientDirection tte.GradientDirection
}

func DefaultExpandConfig() ExpandConfig {
	return ExpandConfig{
		ExpandSpeed: 0.7,
		FinalGradientStops: []tte.Color{
			tte.MustColorFromHex("FFFFFF"),
			tte.MustColorFromHex("88CCFF"),
			tte.MustColorFromHex("0044FF"),
		},
		FinalGradientSteps:     []int{8},
		FinalGradientDirection: tte.GradientRadial,
	}
}

type ExpandEffect struct {
	Config   ExpandConfig
	base     *tte.BaseIterator
	terminal *tte.Terminal
	buckets  [][]*tte.EffectCharacter
	bktIdx   int
}

func NewExpandEffect(cfg ExpandConfig) *ExpandEffect { return &ExpandEffect{Config: cfg} }
func (e *ExpandEffect) Name() string                 { return "expand" }

func distSq2(a, b tte.Coord) float64 {
	dc := float64(a.Column - b.Column)
	dr := float64(a.Row - b.Row)
	return dc*dc + dr*dr
}

func (e *ExpandEffect) Init(t *tte.Terminal) {
	e.terminal = t
	e.base = tte.NewBaseIterator(t)
	canvas := t.Canvas.Canvas
	grad := tte.NewGradient(e.Config.FinalGradientStops, e.Config.FinalGradientSteps)
	colorMap := grad.BuildCoordinateColorMapping(canvas.TextBottom, canvas.TextTop, canvas.TextLeft, canvas.TextRight, e.Config.FinalGradientDirection)
	center := canvas.TextCenter

	type cd struct {
		ch   *tte.EffectCharacter
		dist float64
	}
	chars := t.GetCharacters()
	items := make([]cd, len(chars))
	for i, ch := range chars {
		items[i] = cd{ch, distSq2(ch.InputCoord(), center)}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].dist < items[j].dist })

	for _, item := range items {
		ch := item.ch
		finalColor := colorMap[ch.InputCoord()]
		expandScn := ch.Animation.NewScene("expand", false, tte.SyncNone, nil)
		expandScn.ApplyGradientToSymbols([]string{ch.InputSymbol()}, 5,
			tte.NewGradient([]tte.Color{tte.MustColorFromHex("ffffff"), finalColor}, []int{8}), nil)

		ch.Motion.SetCoordinate(center)
		path := ch.Motion.NewPath("expand", e.Config.ExpandSpeed, tte.EaseOutCubic, 0, false)
		path.NewWaypoint(ch.InputCoord(), "home")
		ch.EventHandler.RegisterEvent(tte.EventPathActivated, path, tte.ActionActivateScene, expandScn)
		t.SetCharacterVisibility(ch, true)
		ch.Motion.ActivatePath(path, ch.EventHandler)
	}

	bktSize := 5
	for i := 0; i < len(items); i += bktSize {
		end := min2(i+bktSize, len(items))
		bkt := make([]*tte.EffectCharacter, end-i)
		for j, it := range items[i:end] {
			bkt[j] = it.ch
		}
		e.buckets = append(e.buckets, bkt)
	}
	e.bktIdx = 0
	if len(e.buckets) > 0 {
		e.base.PendingCharacters = e.buckets[0]
		e.bktIdx = 1
	}
}
func (e *ExpandEffect) Next() (string, bool) {
	for i := 0; i < 5 && len(e.base.PendingCharacters) > 0; i++ {
		ch := e.base.PendingCharacters[0]
		e.base.PendingCharacters = e.base.PendingCharacters[1:]
		e.base.Activate(ch)
	}
	if len(e.base.PendingCharacters) == 0 && e.bktIdx < len(e.buckets) {
		e.base.PendingCharacters = e.buckets[e.bktIdx]
		e.bktIdx++
	}
	e.base.Update()
	return e.base.Frame(), e.base.HasWork() || e.bktIdx < len(e.buckets)
}
func (e *ExpandEffect) Reset() { e.bktIdx = 0 }

// ── 6. FireworksEffect ────────────────────────────────────────────────────────

type FireworksConfig struct {
	FireworkColors         []tte.Color
	FinalGradientStops     []tte.Color
	FinalGradientSteps     []int
	FinalGradientDirection tte.GradientDirection
}

func DefaultFireworksConfig() FireworksConfig {
	return FireworksConfig{
		FireworkColors: []tte.Color{
			tte.MustColorFromHex("FF0000"),
			tte.MustColorFromHex("FF8800"),
			tte.MustColorFromHex("FFFF00"),
			tte.MustColorFromHex("00FFFF"),
			tte.MustColorFromHex("FF00FF"),
		},
		FinalGradientStops: []tte.Color{
			tte.MustColorFromHex("FF4400"),
			tte.MustColorFromHex("FFAA00"),
			tte.MustColorFromHex("FFFFFF"),
		},
		FinalGradientSteps:     []int{8},
		FinalGradientDirection: tte.GradientRadial,
	}
}

type FireworksEffect struct {
	Config   FireworksConfig
	base     *tte.BaseIterator
	terminal *tte.Terminal
}

func NewFireworksEffect(cfg FireworksConfig) *FireworksEffect {
	return &FireworksEffect{Config: cfg}
}
func (e *FireworksEffect) Name() string { return "fireworks" }
func (e *FireworksEffect) Init(t *tte.Terminal) {
	e.terminal = t
	e.base = tte.NewBaseIterator(t)
	canvas := t.Canvas.Canvas
	grad := tte.NewGradient(e.Config.FinalGradientStops, e.Config.FinalGradientSteps)
	colorMap := grad.BuildCoordinateColorMapping(canvas.TextBottom, canvas.TextTop, canvas.TextLeft, canvas.TextRight, e.Config.FinalGradientDirection)

	chars := t.GetCharacters()
	rand.Shuffle(len(chars), func(i, j int) { chars[i], chars[j] = chars[j], chars[i] })

	for _, ch := range chars {
		fwColor := e.Config.FireworkColors[rand.Intn(len(e.Config.FireworkColors))]
		finalColor := colorMap[ch.InputCoord()]

		launchScn := ch.Animation.NewScene("launch", true, tte.SyncNone, nil)
		launchScn.AddFrame("*", 2, tte.FGOnly(fwColor))
		launchScn.AddFrame("|", 2, tte.FGOnly(tte.AdjustColorBrightness(fwColor, 0.7)))

		explodeScn := ch.Animation.NewScene("explode", false, tte.SyncNone, nil)
		for _, sym := range []string{"✦", "✧", "*", "·"} {
			explodeScn.AddFrame(sym, 2, tte.FGOnly(fwColor))
		}

		landScn := ch.Animation.NewScene("land", false, tte.SyncNone, nil)
		landGrad := tte.NewGradient([]tte.Color{fwColor, finalColor}, []int{6})
		landScn.ApplyGradientToSymbols([]string{ch.InputSymbol()}, 3, landGrad, nil)
		landScn.AddFrame(ch.InputSymbol(), 1, tte.FGOnly(finalColor))

		bottom := tte.Coord{Column: ch.InputCoord().Column, Row: canvas.Bottom}
		top := tte.Coord{Column: ch.InputCoord().Column, Row: canvas.Top}

		launchPath := ch.Motion.NewPath("launch", 1.5, tte.EaseInExpo, 0, false)
		launchPath.NewWaypoint(top, "top")

		fallPath := ch.Motion.NewPath("fall", 0.6, tte.EaseOutBounce, 0, false)
		fallPath.NewWaypoint(ch.InputCoord(), "home")

		eh := ch.EventHandler
		eh.RegisterEvent(tte.EventPathActivated, launchPath, tte.ActionActivateScene, launchScn)
		eh.RegisterEvent(tte.EventPathComplete, launchPath, tte.ActionActivateScene, explodeScn)
		eh.RegisterEvent(tte.EventPathComplete, fallPath, tte.ActionActivateScene, landScn)

		ch.Motion.SetCoordinate(bottom)
		t.SetCharacterVisibility(ch, true)
		ch.Motion.ChainPaths(eh, []*tte.Path{launchPath, fallPath}, false)
	}
	e.base.PendingCharacters = chars
}
func (e *FireworksEffect) Next() (string, bool) {
	for i := 0; i < 2 && len(e.base.PendingCharacters) > 0; i++ {
		ch := e.base.PendingCharacters[0]
		e.base.PendingCharacters = e.base.PendingCharacters[1:]
		e.base.Activate(ch)
	}
	e.base.Update()
	return e.base.Frame(), !e.base.HasWork()
}
func (e *FireworksEffect) Reset() {}
