package tasks

import (
	"context"
	"strings"
	"sync"
	"testing"
)

// TestMintPromptID_Canonicalisation asserts that the trim + NFC
// normalisation rules collapse visually-identical content variants to the
// same id, while genuine content differences produce different ids.
func TestMintPromptID_Canonicalisation(t *testing.T) {
	convID := "conv-abc"

	// Trim leading/trailing whitespace.
	a := MintPromptID(convID, "  Run the build\n")
	b := MintPromptID(convID, "Run the build")
	if a != b {
		t.Fatalf("expected trim-equivalent prompts to mint same id, got %q vs %q", a, b)
	}

	// NFC: precomposed é (U+00E9) should equal NFD é (e + U+0301).
	precomposed := MintPromptID(convID, "café")
	decomposed := MintPromptID(convID, "cafe\u0301")
	if precomposed != decomposed {
		t.Fatalf("expected NFC-equivalent prompts to mint same id, got %q vs %q", precomposed, decomposed)
	}

	// Different content → different id.
	c := MintPromptID(convID, "Run the build")
	d := MintPromptID(convID, "Run the tests")
	if c == d {
		t.Fatalf("expected distinct prompts to mint distinct ids, both got %q", c)
	}
}

// TestMintPromptID_ConversationScope ensures identical prompt content in
// two different conversations yields different ids — a critical property
// for the audit trail (one prompt per subtree, even if the user types the
// same instruction twice).
func TestMintPromptID_ConversationScope(t *testing.T) {
	a := MintPromptID("conv-1", "fix the bug")
	b := MintPromptID("conv-2", "fix the bug")
	if a == b {
		t.Fatalf("expected conv-scoped ids to differ across conversations, both got %q", a)
	}
	if !strings.HasPrefix(a, "conv-1:") || !strings.HasPrefix(b, "conv-2:") {
		t.Fatalf("expected conv prefix on each id, got a=%q b=%q", a, b)
	}
}

// TestMintPromptID_EmptyContent ensures empty/whitespace-only prompts mint
// the empty string rather than aliasing every other empty prompt.
func TestMintPromptID_EmptyContent(t *testing.T) {
	for _, in := range []string{"", "   ", "\n\t\n"} {
		if got := MintPromptID("conv-x", in); got != "" {
			t.Fatalf("empty prompt should mint empty id, got %q for input %q", got, in)
		}
	}
}

// TestMintPlanID_Uniqueness asserts that consecutive calls return distinct
// ids (we use uuid v7; trivially unique) and that the id is non-empty.
func TestMintPlanID_Uniqueness(t *testing.T) {
	seen := make(map[string]struct{}, 100)
	for i := range 100 {
		id := MintPlanID()
		if id == "" {
			t.Fatalf("MintPlanID returned empty string on iteration %d", i)
		}
		if _, dup := seen[id]; dup {
			t.Fatalf("MintPlanID produced duplicate id %q on iteration %d", id, i)
		}
		seen[id] = struct{}{}
	}
}

// TestMintPlanID_TimeSortable verifies the v7 ids sort lexicographically
// in mint order. This is the property that lets the TUI render
// PlanIDHistory in chronological order without storing timestamps.
func TestMintPlanID_TimeSortable(t *testing.T) {
	const n = 16
	ids := make([]string, n)
	for i := range n {
		ids[i] = MintPlanID()
	}
	for i := 1; i < n; i++ {
		// uuid v7 sorts ascending by mint time. Allow tie at sub-ms boundaries.
		if ids[i] < ids[i-1] {
			t.Fatalf("expected uuid v7 ids to sort by mint time; got ids[%d]=%q < ids[%d]=%q",
				i, ids[i], i-1, ids[i-1])
		}
	}
}

// TestWithDecomposing_RoundTrip asserts the parent id is recovered via
// DecomposingFrom and that an empty parent id is a no-op.
func TestWithDecomposing_RoundTrip(t *testing.T) {
	ctx := context.Background()

	if got := DecomposingFrom(ctx); got != "" {
		t.Fatalf("plain ctx should report no decomposition, got %q", got)
	}

	scoped := WithDecomposing(ctx, "task-42")
	if got := DecomposingFrom(scoped); got != "task-42" {
		t.Fatalf("expected DecomposingFrom == task-42, got %q", got)
	}

	// Empty parent id is a no-op: same ctx pointer (no value attached).
	noop := WithDecomposing(ctx, "")
	if got := DecomposingFrom(noop); got != "" {
		t.Fatalf("WithDecomposing(_, \"\") should not stash a value, got %q", got)
	}
}

// TestWithDecomposing_NilContextSafe ensures a nil context does not panic
// in DecomposingFrom — defensive against tests passing nil.
func TestWithDecomposing_NilContextSafe(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("DecomposingFrom(nil) panicked: %v", r)
		}
	}()
	if got := DecomposingFrom(nil); got != "" {
		t.Fatalf("DecomposingFrom(nil) should return empty string, got %q", got)
	}
}

// TestDecomposerFunc_Adapter ensures the function adapter satisfies the
// interface and forwards arguments correctly.
func TestDecomposerFunc_Adapter(t *testing.T) {
	var got ParentTask
	var calls int
	var mu sync.Mutex

	d := DecomposerFunc(func(_ context.Context, p ParentTask) ([]DecomposedTask, error) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		got = p
		return []DecomposedTask{{Subject: "child"}}, nil
	})

	parent := ParentTask{ID: "p-1", Subject: "parent", Category: "acting"}
	out, err := d.Decompose(context.Background(), parent)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected 1 call, got %d", calls)
	}
	if got != parent {
		t.Fatalf("parent not forwarded: got %+v, want %+v", got, parent)
	}
	if len(out) != 1 || out[0].Subject != "child" {
		t.Fatalf("unexpected children: %+v", out)
	}
}
