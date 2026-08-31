package chat

import "testing"

// Toggling the side panel flips the viewport between two widths. A message
// wrapped at both widths must be reusable at either without re-rendering.
func TestUsePreRenderForWidthSwapsBetweenTwoWidths(t *testing.T) {
	m := &Message{}
	wide, narrow := 120, 82

	m.SetPreRenderAt(wide, []string{"wide-a", "wide-b"}, nil)
	m.SetPreRenderAt(narrow, []string{"narrow-a"}, nil)

	// Toggling back to the wide layout must hit the alternate slot.
	if !m.UsePreRenderForWidth(wide) {
		t.Fatal("wide wrap was not reused after switching to narrow")
	}
	if got := m.GetPreRenderLines(); len(got) != 2 || got[0] != "wide-a" {
		t.Fatalf("restored lines = %#v, want the wide wrap", got)
	}

	// And back again to narrow.
	if !m.UsePreRenderForWidth(narrow) {
		t.Fatal("narrow wrap was not reused after switching back to wide")
	}
	if got := m.GetPreRenderLines(); len(got) != 1 || got[0] != "narrow-a" {
		t.Fatalf("restored lines = %#v, want the narrow wrap", got)
	}
}

// A width never rendered at must report a miss so the caller re-renders.
func TestUsePreRenderForWidthMissesUnknownWidth(t *testing.T) {
	m := &Message{}
	m.SetPreRenderAt(100, []string{"x"}, nil)

	if m.UsePreRenderForWidth(64) {
		t.Fatal("reported a hit for a width that was never rendered")
	}
	if m.UsePreRenderForWidth(0) {
		t.Fatal("reported a hit for a zero width")
	}
}

// The correctness guard: once content changes, NEITHER cached width may be
// served. Without this, toggling the side panel would resurrect stale text.
func TestMarkDirtyDropsBothCachedWidths(t *testing.T) {
	m := &Message{}
	wide, narrow := 120, 82

	m.SetPreRenderAt(wide, []string{"old-wide"}, nil)
	m.SetPreRenderAt(narrow, []string{"old-narrow"}, nil)

	m.MarkDirty()

	if m.UsePreRenderForWidth(wide) {
		t.Fatal("stale wide wrap was served after MarkDirty")
	}
	if m.UsePreRenderForWidth(narrow) {
		t.Fatal("stale narrow wrap was served after MarkDirty")
	}
}

// SetPreRender carries no width, so it must not leave an alternate slot that a
// later width swap could resurrect.
func TestSetPreRenderClearsWidthTracking(t *testing.T) {
	m := &Message{}
	m.SetPreRenderAt(120, []string{"tracked"}, nil)
	m.SetPreRender([]string{"untracked"}, nil)

	if m.UsePreRenderForWidth(120) {
		t.Fatal("width-tracked content survived an untracked SetPreRender")
	}
}

// Regression: a wrap that was already stale when it was superseded must never
// be demoted into the alternate slot.
//
// Sequence that used to serve pre-mutation text:
//  1. render at W1            -> primary = old W1 lines
//  2. content changes         -> MarkDirty (clears alt; primary still old)
//  3. next render is at W2    -> SetPreRenderAt(W2) demoted the STALE W1 lines
//     into alt and cleared dirty
//  4. side panel toggled back -> UsePreRenderForWidth(W1) served step 1's text
func TestStaleWrapIsNotDemotedToAltSlot(t *testing.T) {
	m := &Message{}
	wide, narrow := 120, 82

	m.SetPreRenderAt(wide, []string{"BEFORE-EDIT"}, nil)

	// Content changed; the wide wrap is now stale but still sits in primary.
	m.MarkDirty()

	// Re-render happens at the other width first.
	m.SetPreRenderAt(narrow, []string{"AFTER-EDIT-narrow"}, nil)

	// Toggling back must NOT resurrect the pre-edit wide wrap.
	if m.UsePreRenderForWidth(wide) {
		t.Fatalf("stale wrap was served for width %d: %#v", wide, m.GetPreRenderLines())
	}
}

// The valid case must still work: no mutation between the two renders means
// the first width's wrap is genuinely reusable.
func TestCleanWrapIsStillDemotedToAltSlot(t *testing.T) {
	m := &Message{}
	wide, narrow := 120, 82

	m.SetPreRenderAt(wide, []string{"clean-wide"}, nil)
	m.SetPreRenderAt(narrow, []string{"clean-narrow"}, nil)

	if !m.UsePreRenderForWidth(wide) {
		t.Fatal("a clean wrap was not reusable — the staleness guard is too aggressive")
	}
	if got := m.GetPreRenderLines(); len(got) != 1 || got[0] != "clean-wide" {
		t.Fatalf("restored lines = %#v, want clean-wide", got)
	}
}
