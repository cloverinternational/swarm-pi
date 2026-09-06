package forge

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

// TestBlankRepairNeverCorruptsContent is a property test for the invariant
// that matters: stripping the blank lines out of a patch's context must either
// produce the SAME file the fully-specified patch produces, or fail outright.
// It must never produce a third, different file.
//
// This is the guard against the blank-tolerant alignment quietly mis-anchoring
// a hunk and writing plausible-looking but wrong output.
func TestBlankRepairNeverCorruptsContent(t *testing.T) {
	rng := rand.New(rand.NewSource(20260811))
	var exercised, refused, skipped int

	for iteration := 0; iteration < 500; iteration++ {
		// Build a file of unique tokens with random blank-line runs, so any
		// successful match is unambiguous by construction.
		var lines []string
		tokenCount := 6 + rng.Intn(10)
		for i := 0; i < tokenCount; i++ {
			lines = append(lines, fmt.Sprintf("tok%02d", i))
			for blanks := rng.Intn(3); blanks > 0; blanks-- {
				lines = append(lines, "")
			}
		}
		original := strings.Join(lines, "\n") + "\n"

		// Pick a non-blank line to replace.
		var candidates []int
		for i, l := range lines {
			if l != "" {
				candidates = append(candidates, i)
			}
		}
		target := candidates[rng.Intn(len(candidates))]

		// Full context: the target plus its true neighbours, blanks included.
		lo := target - 2
		if lo < 0 {
			lo = 0
		}
		hi := target + 2
		if hi > len(lines)-1 {
			hi = len(lines) - 1
		}

		buildPatch := func(keepBlanks bool) string {
			var b strings.Builder
			b.WriteString("*** Begin Patch\n*** Update File: f.txt\n@@\n")
			for i := lo; i <= hi; i++ {
				if lines[i] == "" {
					if keepBlanks {
						b.WriteString(" \n")
					}
					continue
				}
				if i == target {
					b.WriteString("-" + lines[i] + "\n")
					b.WriteString("+" + lines[i] + "_EDITED\n")
				} else {
					b.WriteString(" " + lines[i] + "\n")
				}
			}
			b.WriteString("*** End Patch")
			return b.String()
		}

		// Reference result from the fully-specified patch.
		want, wantErr := applyPatchText(t, original, buildPatch(true))
		if wantErr != nil {
			skipped++
			continue // fixture not expressible; skip
		}

		got, gotErr := applyPatchText(t, original, buildPatch(false))
		if gotErr != nil {
			refused++
			continue // refusing is always an acceptable outcome
		}
		exercised++
		if got != want {
			t.Fatalf("iteration %d: blank-free context produced DIFFERENT output\noriginal: %q\n got: %q\nwant: %q",
				iteration, original, got, want)
		}
	}

	// A property test that silently skipped every case would pass while
	// proving nothing, so assert that the repair path was actually taken.
	t.Logf("blank-repair property: exercised=%d refused=%d skipped=%d", exercised, refused, skipped)
	if exercised == 0 {
		t.Fatal("no iteration exercised the blank-tolerant repair path; the property was never tested")
	}
	if exercised < 100 {
		t.Errorf("only %d/500 iterations exercised the repair path; coverage too thin to trust", exercised)
	}
}
