// Source: terminaltexteffects/utils/easing.py
// All easing functions take a progress ratio t ∈ [0,1] and return an eased
// ratio ∈ [0,1].  They are pure functions with no side-effects and can be
// stored as EasingFunc values in Paths or Scenes.
package tteengine

import "math"

// EasingFunc is the type for all easing functions.
// t is the normalised progress ratio (0 = start, 1 = end).
type EasingFunc func(t float64) float64

// ── Linear ───────────────────────────────────────────────────────────────────

// EaseLinear returns t unchanged — constant speed.
func EaseLinear(t float64) float64 { return t }

// ── Sine ─────────────────────────────────────────────────────────────────────

// EaseInSine accelerates from zero using a sine curve.
func EaseInSine(t float64) float64 { return 1 - math.Cos(t*math.Pi/2) }

// EaseOutSine decelerates to zero using a sine curve.
func EaseOutSine(t float64) float64 { return math.Sin(t * math.Pi / 2) }

// EaseInOutSine accelerates then decelerates using a sine curve.
func EaseInOutSine(t float64) float64 { return -(math.Cos(math.Pi*t) - 1) / 2 }

// ── Quadratic ────────────────────────────────────────────────────────────────

// EaseInQuad accelerates from zero quadratically.
func EaseInQuad(t float64) float64 { return t * t }

// EaseOutQuad decelerates to zero quadratically.
func EaseOutQuad(t float64) float64 { return t * (2 - t) }

// EaseInOutQuad accelerates then decelerates quadratically.
func EaseInOutQuad(t float64) float64 {
	if t < 0.5 {
		return 2 * t * t
	}
	return -1 + (4-2*t)*t
}

// ── Cubic ─────────────────────────────────────────────────────────────────────

// EaseInCubic accelerates from zero cubically.
func EaseInCubic(t float64) float64 { return t * t * t }

// EaseOutCubic decelerates to zero cubically.
func EaseOutCubic(t float64) float64 { t1 := t - 1; return t1*t1*t1 + 1 }

// EaseInOutCubic accelerates then decelerates cubically.
func EaseInOutCubic(t float64) float64 {
	if t < 0.5 {
		return 4 * t * t * t
	}
	t1 := 2*t - 2
	return 0.5*t1*t1*t1 + 1
}

// ── Quartic ───────────────────────────────────────────────────────────────────

// EaseInQuart accelerates from zero quartically.
func EaseInQuart(t float64) float64 { return t * t * t * t }

// EaseOutQuart decelerates to zero quartically.
func EaseOutQuart(t float64) float64 { t1 := t - 1; return 1 - t1*t1*t1*t1 }

// EaseInOutQuart accelerates then decelerates quartically.
func EaseInOutQuart(t float64) float64 {
	if t < 0.5 {
		return 8 * t * t * t * t
	}
	t1 := t - 1
	return 1 - 8*t1*t1*t1*t1
}

// ── Quintic ───────────────────────────────────────────────────────────────────

// EaseInQuint accelerates from zero quintically.
func EaseInQuint(t float64) float64 { return t * t * t * t * t }

// EaseOutQuint decelerates to zero quintically.
func EaseOutQuint(t float64) float64 { t1 := t - 1; return 1 + t1*t1*t1*t1*t1 }

// EaseInOutQuint accelerates then decelerates quintically.
func EaseInOutQuint(t float64) float64 {
	if t < 0.5 {
		return 16 * t * t * t * t * t
	}
	t1 := 2*t - 2
	return 0.5*t1*t1*t1*t1*t1 + 1
}

// ── Exponential ──────────────────────────────────────────────────────────────

// EaseInExpo accelerates from zero exponentially.
func EaseInExpo(t float64) float64 {
	if t == 0 {
		return 0
	}
	return math.Pow(2, 10*(t-1))
}

// EaseOutExpo decelerates to zero exponentially.
func EaseOutExpo(t float64) float64 {
	if t == 1 {
		return 1
	}
	return 1 - math.Pow(2, -10*t)
}

// EaseInOutExpo accelerates then decelerates exponentially.
func EaseInOutExpo(t float64) float64 {
	switch {
	case t == 0:
		return 0
	case t == 1:
		return 1
	case t < 0.5:
		return math.Pow(2, 20*t-10) / 2
	default:
		return (2 - math.Pow(2, -20*t+10)) / 2
	}
}

// ── Circular ─────────────────────────────────────────────────────────────────

// EaseInCirc accelerates from zero along a circular arc.
func EaseInCirc(t float64) float64 { return 1 - math.Sqrt(1-t*t) }

// EaseOutCirc decelerates to zero along a circular arc.
func EaseOutCirc(t float64) float64 { t1 := t - 1; return math.Sqrt(1 - t1*t1) }

// EaseInOutCirc accelerates then decelerates along circular arcs.
func EaseInOutCirc(t float64) float64 {
	if t < 0.5 {
		return (1 - math.Sqrt(1-4*t*t)) / 2
	}
	t1 := -2*t + 2
	return (math.Sqrt(1-t1*t1) + 1) / 2
}

// ── Back ──────────────────────────────────────────────────────────────────────

const backC1 = 1.70158
const backC2 = backC1 * 1.525
const backC3 = backC1 + 1

// EaseInBack overshoots slightly before accelerating forward.
func EaseInBack(t float64) float64 {
	return backC3*t*t*t - backC1*t*t
}

// EaseOutBack decelerates and overshoots slightly past the end.
func EaseOutBack(t float64) float64 {
	t1 := t - 1
	return 1 + backC3*t1*t1*t1 + backC1*t1*t1
}

// EaseInOutBack overshoots both ends.
func EaseInOutBack(t float64) float64 {
	if t < 0.5 {
		return (2 * t) * (2 * t) * (((backC2 + 1) * 2 * t) - backC2) / 2
	}
	t1 := 2*t - 2
	return (t1*t1*(((backC2+1)*t1)+backC2) + 2) / 2
}

// ── Elastic ───────────────────────────────────────────────────────────────────

const elasticC4 = (2 * math.Pi) / 3
const elasticC5 = (2 * math.Pi) / 4.5

// EaseInElastic springs back before accelerating forward like a rubber band.
func EaseInElastic(t float64) float64 {
	if t == 0 || t == 1 {
		return t
	}
	return -math.Pow(2, 10*t-10) * math.Sin((t*10-10.75)*elasticC4)
}

// EaseOutElastic overshoots and oscillates before settling.
func EaseOutElastic(t float64) float64 {
	if t == 0 || t == 1 {
		return t
	}
	return math.Pow(2, -10*t)*math.Sin((t*10-0.75)*elasticC4) + 1
}

// EaseInOutElastic elastic oscillation at both ends.
func EaseInOutElastic(t float64) float64 {
	switch {
	case t == 0 || t == 1:
		return t
	case t < 0.5:
		return -(math.Pow(2, 20*t-10) * math.Sin((20*t-11.125)*elasticC5)) / 2
	default:
		return math.Pow(2, -20*t+10)*math.Sin((20*t-11.125)*elasticC5)/2 + 1
	}
}

// ── Bounce ────────────────────────────────────────────────────────────────────

// EaseOutBounce simulates a bouncing ball landing.
func EaseOutBounce(t float64) float64 {
	const n1 = 7.5625
	const d1 = 2.75
	switch {
	case t < 1/d1:
		return n1 * t * t
	case t < 2/d1:
		t -= 1.5 / d1
		return n1*t*t + 0.75
	case t < 2.5/d1:
		t -= 2.25 / d1
		return n1*t*t + 0.9375
	default:
		t -= 2.625 / d1
		return n1*t*t + 0.984375
	}
}

// EaseInBounce reverses EaseOutBounce.
func EaseInBounce(t float64) float64 { return 1 - EaseOutBounce(1-t) }

// EaseInOutBounce bounces at both ends.
func EaseInOutBounce(t float64) float64 {
	if t < 0.5 {
		return (1 - EaseOutBounce(1-2*t)) / 2
	}
	return (1 + EaseOutBounce(2*t-1)) / 2
}

// ── Named map — for config/CLI lookup ────────────────────────────────────────

// EasingFunctions maps canonical names (matching the Python engine's naming
// convention) to their EasingFunc implementations.  Effects can use this to
// expose string-configurable easing.
var EasingFunctions = map[string]EasingFunc{
	"LINEAR":         EaseLinear,
	"IN_SINE":        EaseInSine,
	"OUT_SINE":       EaseOutSine,
	"IN_OUT_SINE":    EaseInOutSine,
	"IN_QUAD":        EaseInQuad,
	"OUT_QUAD":       EaseOutQuad,
	"IN_OUT_QUAD":    EaseInOutQuad,
	"IN_CUBIC":       EaseInCubic,
	"OUT_CUBIC":      EaseOutCubic,
	"IN_OUT_CUBIC":   EaseInOutCubic,
	"IN_QUART":       EaseInQuart,
	"OUT_QUART":      EaseOutQuart,
	"IN_OUT_QUART":   EaseInOutQuart,
	"IN_QUINT":       EaseInQuint,
	"OUT_QUINT":      EaseOutQuint,
	"IN_OUT_QUINT":   EaseInOutQuint,
	"IN_EXPO":        EaseInExpo,
	"OUT_EXPO":       EaseOutExpo,
	"IN_OUT_EXPO":    EaseInOutExpo,
	"IN_CIRC":        EaseInCirc,
	"OUT_CIRC":       EaseOutCirc,
	"IN_OUT_CIRC":    EaseInOutCirc,
	"IN_BACK":        EaseInBack,
	"OUT_BACK":       EaseOutBack,
	"IN_OUT_BACK":    EaseInOutBack,
	"IN_ELASTIC":     EaseInElastic,
	"OUT_ELASTIC":    EaseOutElastic,
	"IN_OUT_ELASTIC": EaseInOutElastic,
	"IN_BOUNCE":      EaseInBounce,
	"OUT_BOUNCE":     EaseOutBounce,
	"IN_OUT_BOUNCE":  EaseInOutBounce,
}
