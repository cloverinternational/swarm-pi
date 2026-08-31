package chat

// Diffusion reveal animation.
//
// Text-diffusion models (DiffusionGemma & co.) served through vLLM commit the
// whole completion as one large chunk — there is no token-by-token stream and
// the server exposes no intermediate denoising steps (probed June 2026:
// single-chunk SSE, no diffusion params in the OpenAPI schema, vllm_xargs
// history flags ignored). So instead of popping the full reply in at once,
// the TUI animates a client-side rendition of the denoising process: every
// glyph starts as flickering noise and resolves to its real character at a
// deterministic pseudo-random frame.
//
// The transform is pure and frame-indexed: rendering the same frame twice
// yields identical output, and the final frame is byte-identical to the real
// message content (whitespace is never masked, so wrapping stays stable).
//
// Re-animation guard: an agentic diffusion turn delivers several chunks, and
// replace-mode updates re-send the same block. To keep each piece of text
// animating exactly once, a reveal records the block it animates (by sequence)
// and the rune offset already shown verbatim (baseRunes); only the freshly
// arrived suffix denoises, and identical / shorter / already-revealed updates
// do not restart it.

const (
	// diffusionRevealMinChars is the minimum delta size that triggers a
	// reveal. Tiny deltas (greeting-sized or token-ish) animate poorly and
	// suggest the provider streamed normally after all.
	diffusionRevealMinChars = 40

	// diffusionRevealFrames is the reveal duration in animation-clock frames.
	// The reveal pins the clock to its streaming rate (AnimationFPS = 10 FPS)
	// while active, so 9 frames is ~0.9s — snappy but legible.
	diffusionRevealFrames = 9
)

// diffusionNoiseGlyphs are the unresolved-character glyphs. All single-width
// so masked text wraps the same as the real text.
var diffusionNoiseGlyphs = []rune("▓▒░█▚▞▖▗▘▝∙·")

// diffusionRevealState tracks the one in-flight reveal. Only the newest
// diffusion chunk animates; a fresh chunk that adds text restarts the reveal
// for just the new suffix.
type diffusionRevealState struct {
	active     bool
	subscribed bool // holds an animationClock subscription while active
	msgIndex   int  // index into a.messages
	blockSeq   int  // Sequence of the content block being revealed (-1 = any)
	baseRunes  int  // runes already shown verbatim before this reveal's suffix
	totalRunes int  // total runes in the block at reveal start (for guard)
	startFrame int
	seed       uint64
}

// startDiffusionReveal begins the denoising reveal for the content block at
// (msgIndex, blockSeq) whose current rune length is totalRunes. baseRunes is
// the rune count already revealed for that block (the prefix that stays
// verbatim). It is a no-op when the update adds no new runes beyond what is
// already revealed, so identical / shorter replace-updates never re-noise
// already-resolved text. Safe to call repeatedly; the clock subscription is
// held at most once.
func (a *App) startDiffusionReveal(msgIndex, blockSeq, baseRunes, totalRunes int) {
	// No genuinely new text to animate — leave any in-flight reveal alone.
	if totalRunes <= baseRunes {
		return
	}
	// Same block already animating the same (or a superset of the) suffix:
	// don't restart, or already-revealed runes would re-noise every chunk.
	if a.diffusionReveal.active &&
		a.diffusionReveal.msgIndex == msgIndex &&
		a.diffusionReveal.blockSeq == blockSeq &&
		baseRunes >= a.diffusionReveal.baseRunes &&
		totalRunes <= a.diffusionReveal.totalRunes {
		return
	}
	if !a.diffusionReveal.subscribed {
		// Reveals start while a stream is in flight, so the clock is already
		// running and Subscribe just bumps the refcount (returns nil cmd).
		_ = a.animationClock.Subscribe()
		a.diffusionReveal.subscribed = true
	}
	// Pin the clock to its faster streaming rate so the reveal stays smooth
	// even after the stream itself completes (idle rate is visibly choppy).
	a.animationClock.SetStreaming(true)
	a.diffusionReveal.active = true
	a.diffusionReveal.msgIndex = msgIndex
	a.diffusionReveal.blockSeq = blockSeq
	a.diffusionReveal.baseRunes = baseRunes
	a.diffusionReveal.totalRunes = totalRunes
	a.diffusionReveal.startFrame = a.animationClock.Frame()
	a.diffusionReveal.seed = uint64(msgIndex)*0x100000001B3 +
		uint64(blockSeq)*0x9E3779B1 + uint64(baseRunes) + 0xCBF29CE484222325
}

// diffusionRevealActiveFor reports whether msgIndex currently animates.
func (a *App) diffusionRevealActiveFor(msgIndex int) bool {
	return a.diffusionReveal.active && a.diffusionReveal.msgIndex == msgIndex
}

// diffusionRevealAnyBlock is the blockSeq sentinel for callers that render a
// message without per-block sequencing (the non-OrderedBlocks fallback path):
// it matches the active reveal by message alone.
const diffusionRevealAnyBlock = -1

// diffusionRevealActiveBlock reports whether the content block (msgIndex,
// blockSeq) is the one currently denoising. A blockSeq of
// diffusionRevealAnyBlock matches by message regardless of the recorded block.
func (a *App) diffusionRevealActiveBlock(msgIndex, blockSeq int) bool {
	return a.diffusionReveal.active &&
		a.diffusionReveal.msgIndex == msgIndex &&
		(blockSeq == diffusionRevealAnyBlock ||
			a.diffusionReveal.blockSeq < 0 ||
			a.diffusionReveal.blockSeq == blockSeq)
}

// tickDiffusionReveal advances the reveal on an animation tick. It returns
// true while the reveal still animates; on expiry it marks the message dirty
// one final time (so the real text renders) and releases the clock.
func (a *App) tickDiffusionReveal() bool {
	if !a.diffusionReveal.active {
		return false
	}
	if a.animationClock.Frame() >= a.diffusionReveal.startFrame+diffusionRevealFrames {
		a.diffusionReveal.active = false
		if a.diffusionReveal.subscribed {
			a.animationClock.Unsubscribe()
			a.diffusionReveal.subscribed = false
		}
		// Restore the clock rate to the real streaming state. The reveal pinned
		// it fast; once it ends, idle/streaming is governed by the stream again.
		a.animationClock.SetStreaming(a.streamingMessage)
		if a.diffusionReveal.msgIndex < len(a.messages) {
			a.messages[a.diffusionReveal.msgIndex].MarkDirty()
		}
		return false
	}
	// Re-assert the fast tick rate every frame: a stream-completion handler may
	// have flipped the clock to idle mid-reveal, which would render choppily.
	a.animationClock.SetStreaming(true)
	return true
}

// diffusionRevealTransform renders the current reveal frame for the content
// block at (msgIndex, blockSeq). Runes before baseRunes stay verbatim (already
// revealed); only the freshly arrived suffix denoises. Returns content
// unchanged when no reveal is active for this block.
func (a *App) diffusionRevealTransform(msgIndex, blockSeq int, content string) string {
	if !a.diffusionRevealActiveBlock(msgIndex, blockSeq) {
		return content
	}
	elapsed := a.animationClock.Frame() - a.diffusionReveal.startFrame
	runes := []rune(content)
	base := a.diffusionReveal.baseRunes
	if base < 0 {
		base = 0
	}
	if base >= len(runes) {
		return content
	}
	// Keep the already-revealed prefix verbatim; only denoise the suffix.
	suffix := applyDiffusionReveal(string(runes[base:]), elapsed, diffusionRevealFrames, a.diffusionReveal.seed)
	return string(runes[:base]) + suffix
}

// applyDiffusionReveal renders one frame of the denoising reveal.
//
// Each non-whitespace rune gets a deterministic resolve frame in
// [0, duration); once elapsed reaches it the real rune shows. Two frames
// before resolving the rune shimmers as '░'; before that it flickers through
// noise glyphs (glyph choice varies per frame, resolve order does not).
//
// The resolve schedule is eased rather than uniform: an ease-out curve packs
// more characters into the early frames (so the reply reads quickly) and
// trails the last few off, and the curve is biased left-to-right so the text
// denoises front-to-back like a wavefront rather than as uniform sparkle.
// Randomness is blended in so the wavefront stays organic, not a hard sweep.
func applyDiffusionReveal(text string, elapsed, duration int, seed uint64) string {
	if duration <= 0 || elapsed >= duration {
		return text
	}
	if elapsed < 0 {
		elapsed = 0
	}
	runes := []rune(text)
	n := len(runes)
	out := make([]rune, n)
	for i, r := range runes {
		if r == ' ' || r == '\n' || r == '\t' || r == '\r' {
			out[i] = r
			continue
		}
		resolveAt := diffusionResolveFrame(seed, i, n, duration)
		switch {
		case elapsed >= resolveAt:
			out[i] = r
		case elapsed >= resolveAt-2:
			out[i] = '░'
		default:
			g := diffusionHash(seed, uint64(i)*0x9E3779B1+uint64(elapsed))
			out[i] = diffusionNoiseGlyphs[g%uint64(len(diffusionNoiseGlyphs))]
		}
	}
	return string(out)
}

// diffusionResolveFrame returns the frame at which rune i (of n) resolves,
// in [0, duration). It blends three signals:
//
//   - a left-to-right wavefront: position (i/n) maps onto the timeline so
//     earlier runes resolve earlier;
//   - an ease-out curve (1-(1-t)^2): the wavefront moves fast at first and
//     decelerates, so most text is up early and the tail trails off;
//   - deterministic jitter: a per-rune hash perturbs the position so the
//     front is organic rather than a hard left-to-right sweep.
//
// The result is clamped to [0, duration-1] so every rune still resolves before
// the final frame (which is byte-identical to the source text).
func diffusionResolveFrame(seed uint64, i, n, duration int) int {
	if n <= 1 {
		// Single rune: resolve roughly mid-way through the reveal.
		if duration <= 1 {
			return 0
		}
		return duration / 2
	}
	// Position in [0,1) blended with bounded jitter (±~18% of a slot).
	pos := float64(i) / float64(n)
	jitter := (float64(diffusionHash(seed, uint64(i))%1000)/1000.0 - 0.5) * 0.36
	t := pos + jitter
	if t < 0 {
		t = 0
	} else if t > 0.999 {
		t = 0.999
	}
	// Ease-out: 1-(1-t)^2 front-loads resolution.
	inv := 1 - t
	eased := 1 - inv*inv
	frame := int(eased * float64(duration))
	if frame < 0 {
		frame = 0
	} else if frame >= duration {
		frame = duration - 1
	}
	return frame
}

// diffusionCommonPrefixRunes returns the number of leading runes that a and b
// share. Replace-mode diffusion updates re-send the whole block; the shared
// prefix was already revealed, so only the suffix past it should re-animate.
func diffusionCommonPrefixRunes(a, b string) int {
	ar, br := []rune(a), []rune(b)
	n := len(ar)
	if len(br) < n {
		n = len(br)
	}
	i := 0
	for i < n && ar[i] == br[i] {
		i++
	}
	return i
}

// diffusionHash is a small deterministic mixer (splitmix64-style finalizer).
func diffusionHash(seed, x uint64) uint64 {
	h := seed ^ (x * 0x9E3779B97F4A7C15)
	h ^= h >> 33
	h *= 0xFF51AFD7ED558CCD
	h ^= h >> 33
	h *= 0xC4CEB9FE1A85EC53
	h ^= h >> 33
	return h
}
