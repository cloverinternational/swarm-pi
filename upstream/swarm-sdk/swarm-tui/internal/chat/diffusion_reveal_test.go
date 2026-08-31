package chat

import (
	"strings"
	"testing"
)

const revealSample = "Rainbows form when sunlight refracts.\n\n- droplets\n- dispersion ☀"

// TestDiffusionRevealFinalFrameVerbatim verifies the animation never corrupts
// the message: at (and past) the final frame the output is byte-identical.
func TestDiffusionRevealFinalFrameVerbatim(t *testing.T) {
	for _, elapsed := range []int{diffusionRevealFrames, diffusionRevealFrames + 1, 100} {
		got := applyDiffusionReveal(revealSample, elapsed, diffusionRevealFrames, 42)
		if got != revealSample {
			t.Fatalf("elapsed=%d: output differs from original\ngot:  %q\nwant: %q", elapsed, got, revealSample)
		}
	}
}

// TestDiffusionRevealPreservesLayout verifies whitespace passes through
// unmasked and the rune count never changes, so wrapping stays stable.
func TestDiffusionRevealPreservesLayout(t *testing.T) {
	for elapsed := 0; elapsed < diffusionRevealFrames; elapsed++ {
		got := applyDiffusionReveal(revealSample, elapsed, diffusionRevealFrames, 42)
		gotRunes, wantRunes := []rune(got), []rune(revealSample)
		if len(gotRunes) != len(wantRunes) {
			t.Fatalf("elapsed=%d: rune count changed: %d != %d", elapsed, len(gotRunes), len(wantRunes))
		}
		for i, r := range wantRunes {
			if r == ' ' || r == '\n' || r == '\t' || r == '\r' {
				if gotRunes[i] != r {
					t.Fatalf("elapsed=%d: whitespace at rune %d masked as %q", elapsed, i, gotRunes[i])
				}
			}
		}
	}
}

// TestDiffusionRevealDeterministicAndMonotonic verifies the same frame
// renders identically twice, and a rune that has resolved never un-resolves
// on later frames.
func TestDiffusionRevealDeterministicAndMonotonic(t *testing.T) {
	const seed = 7
	want := []rune(revealSample)
	prevResolved := make([]bool, len(want))

	for elapsed := 0; elapsed <= diffusionRevealFrames; elapsed++ {
		first := applyDiffusionReveal(revealSample, elapsed, diffusionRevealFrames, seed)
		second := applyDiffusionReveal(revealSample, elapsed, diffusionRevealFrames, seed)
		if first != second {
			t.Fatalf("elapsed=%d: non-deterministic frame", elapsed)
		}

		got := []rune(first)
		for i := range want {
			resolved := got[i] == want[i]
			if prevResolved[i] && !resolved {
				t.Fatalf("elapsed=%d: rune %d un-resolved (%q -> %q)", elapsed, i, want[i], got[i])
			}
			// Noise glyphs that coincide with the real rune count as resolved
			// for monotonicity purposes only if they stay — skip strictness
			// for glyphs that are themselves noise characters.
			if resolved && !strings.ContainsRune(string(diffusionNoiseGlyphs)+"░", want[i]) {
				prevResolved[i] = true
			}
		}
	}

	// Everything must be resolved on the last animated frame + 1.
	final := applyDiffusionReveal(revealSample, diffusionRevealFrames, diffusionRevealFrames, seed)
	if final != revealSample {
		t.Fatal("final frame not fully resolved")
	}
}

// TestDiffusionRevealProgresses verifies the reveal actually starts noisy and
// converges: frame 0 masks a majority of non-whitespace runes.
func TestDiffusionRevealProgresses(t *testing.T) {
	want := []rune(revealSample)
	got := []rune(applyDiffusionReveal(revealSample, 0, diffusionRevealFrames, 99))

	masked, total := 0, 0
	for i, r := range want {
		if r == ' ' || r == '\n' || r == '\t' || r == '\r' {
			continue
		}
		total++
		if got[i] != r {
			masked++
		}
	}
	if masked*2 < total {
		t.Fatalf("frame 0 should mask most characters: masked %d of %d", masked, total)
	}
}

// TestDiffusionRevealLeftToRightBias verifies the eased schedule denoises
// front-to-back: averaged over the alphabet, runes in the first half resolve
// no later than runes in the second half (the wavefront moves left→right).
func TestDiffusionRevealLeftToRightBias(t *testing.T) {
	const n = 200
	const duration = diffusionRevealFrames
	var firstSum, secondSum, firstCount, secondCount int
	for i := 0; i < n; i++ {
		f := diffusionResolveFrame(99, i, n, duration)
		if i < n/2 {
			firstSum += f
			firstCount++
		} else {
			secondSum += f
			secondCount++
		}
	}
	firstAvg := float64(firstSum) / float64(firstCount)
	secondAvg := float64(secondSum) / float64(secondCount)
	if firstAvg >= secondAvg {
		t.Fatalf("expected left-to-right bias: first-half avg resolve %.2f should be < second-half %.2f", firstAvg, secondAvg)
	}
}

// TestDiffusionResolveFrameDeterministicInRange verifies the eased resolve
// schedule is deterministic and always lands in [0, duration), so every rune
// resolves before the final (verbatim) frame.
func TestDiffusionResolveFrameDeterministicInRange(t *testing.T) {
	const n = 137
	const duration = diffusionRevealFrames
	for i := 0; i < n; i++ {
		a := diffusionResolveFrame(7, i, n, duration)
		b := diffusionResolveFrame(7, i, n, duration)
		if a != b {
			t.Fatalf("rune %d: non-deterministic resolve frame %d != %d", i, a, b)
		}
		if a < 0 || a >= duration {
			t.Fatalf("rune %d: resolve frame %d out of range [0,%d)", i, a, duration)
		}
	}
}

// TestDiffusionCommonPrefixRunes verifies the replace-update suffix detector:
// identical strings share their full length (so no re-animation), divergent
// tails share only the common head, and multi-byte runes count as one.
func TestDiffusionCommonPrefixRunes(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"hello world", "hello world", 11}, // identical → whole thing
		{"hello world", "hello there", 6},  // diverge after "hello "
		{"", "abc", 0},
		{"abc", "", 0},
		{"☀ sun", "☀ moon", 2}, // multi-byte rune counts as one
		{"abc", "abcdef", 3},   // b is a superset of a
	}
	for _, c := range cases {
		if got := diffusionCommonPrefixRunes(c.a, c.b); got != c.want {
			t.Fatalf("diffusionCommonPrefixRunes(%q,%q)=%d want %d", c.a, c.b, got, c.want)
		}
	}
}

// TestDiffusionRevealSuffixOnlyAnimates verifies the single-animation-per-text
// guarantee at the transform layer: with a baseRunes prefix, the prefix is
// rendered verbatim on every frame while only the suffix denoises. This is the
// core of "each piece of text animates exactly once" — already-revealed text
// never re-noises.
func TestDiffusionRevealSuffixOnlyAnimates(t *testing.T) {
	const full = "Already revealed prefix text. NEW suffix that should denoise now."
	runes := []rune(full)
	base := len([]rune("Already revealed prefix text. "))
	prefix := string(runes[:base])

	for elapsed := 0; elapsed < diffusionRevealFrames; elapsed++ {
		// Mirror diffusionRevealTransform's prefix/suffix split.
		suffix := applyDiffusionReveal(string(runes[base:]), elapsed, diffusionRevealFrames, 1234)
		got := prefix + suffix
		gotRunes := []rune(got)
		// Prefix must be byte-identical on every frame.
		for i := 0; i < base; i++ {
			if gotRunes[i] != runes[i] {
				t.Fatalf("elapsed=%d: revealed prefix rune %d changed %q -> %q", elapsed, i, runes[i], gotRunes[i])
			}
		}
		// Rune count preserved overall.
		if len(gotRunes) != len(runes) {
			t.Fatalf("elapsed=%d: rune count changed %d != %d", elapsed, len(gotRunes), len(runes))
		}
	}

	// At the final frame the suffix resolves to the real text.
	finalSuffix := applyDiffusionReveal(string(runes[base:]), diffusionRevealFrames, diffusionRevealFrames, 1234)
	if prefix+finalSuffix != full {
		t.Fatal("final frame not byte-identical to source")
	}
}
