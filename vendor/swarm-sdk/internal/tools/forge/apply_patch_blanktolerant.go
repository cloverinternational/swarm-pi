package forge

import "fmt"

// ─── Blank-line tolerant context repair ──────────────────────────────────────
//
// A patch context is matched positionally: context[j] must line up with
// fileLines[at+j] for every j. That is correct, and it is also brittle against
// one specific and very common authoring defect — a context block that is
// missing the blank lines the file actually contains.
//
// This is not hypothetical. Rendered file output can omit completely empty
// lines, so an agent that reads a file and then writes a patch from what it
// observed produces context in which every blank line has silently vanished.
// The patch is otherwise perfect: same lines, same order, same indentation.
// All four matching tiers then fail, because tiers only relax how a single
// line is compared, never whether a file line may be absent from the context.
// The reported symptom is `context not found` for a block the author can see
// verbatim in the file, and the usual workaround is to shatter the patch into
// single-line hunks until each one happens to avoid a blank line — see issues
// #260, #264, #291, #297.
//
// The repair below re-aligns such a context against the file, permitting file
// blank lines to be skipped, and then rewrites the hunk so that the blanks are
// present as ordinary context lines. Everything downstream is unchanged and
// still operates on an exact 1:1 span.
//
// Safety properties, in order of importance:
//
//  1. It only ever INSERTS blank context lines that genuinely exist in the
//     file at that position. It never inserts, deletes, or reorders content.
//  2. It preserves the uniqueness guarantee. If the relaxed alignment matches
//     at more than one location the repair is refused, so a fuzzy match can
//     never silently edit the wrong copy of repeated code.
//  3. It is a last resort, attempted only after all exact tiers have failed,
//     so no patch that matches today changes behavior.

// blankRepairAlignment describes how a hunk context aligns against the file
// when file-side blank lines are allowed to be missing from the context.
type blankRepairAlignment struct {
	at   int // file index where the aligned span begins
	span int // number of file lines the span consumes
	// insertAt holds context indices before which a file blank line must be
	// inserted, ascending. A value of len(context) means "append at the end",
	// which never occurs because a span is trimmed to end on a real line.
	insertAt []int
}

// alignAllowingFileBlanks aligns context against fileLines starting exactly at
// `at`, permitting blank lines in the file to be absent from context. It
// reports the alignment and whether the whole context was consumed.
//
// A blank line in the context is matched against a blank line in the file
// normally; skipping is only used to absorb file blanks the context lacks.
func alignAllowingFileBlanks(fileLines, context []string, at, tier int) (blankRepairAlignment, bool) {
	align := blankRepairAlignment{at: at}
	if at < 0 || at >= len(fileLines) {
		return align, false
	}
	f := at
	for c := 0; c < len(context); c++ {
		// Absorb file blank lines that the context does not account for.
		for f < len(fileLines) && fileLines[f] == "" && context[c] != "" {
			// Only absorb when the context line is non-blank; a blank context
			// line must consume the blank file line instead of skipping it.
			align.insertAt = append(align.insertAt, c)
			f++
		}
		if f >= len(fileLines) || !lineEqual(fileLines[f], context[c], tier) {
			return align, false
		}
		f++
	}
	align.span = f - at
	// A repair that absorbed nothing is just an ordinary match; the caller has
	// already tried those, so report failure to avoid duplicate work.
	if len(align.insertAt) == 0 {
		return align, false
	}
	return align, true
}

// findBlankRepair searches for a unique blank-tolerant alignment of context at
// or after cursor. It mirrors findContextUnique's tier ladder and uniqueness
// rule. anchored has the same meaning as in findContextUnique: an explicit @@
// anchor has already scoped the search, so the nearest alignment wins.
func findBlankRepair(fileLines, context []string, cursor int, anchored bool) (blankRepairAlignment, bool) {
	if len(context) == 0 {
		return blankRepairAlignment{}, false
	}
	for tier := 0; tier <= 3; tier++ {
		var found []blankRepairAlignment
		for i := cursor; i < len(fileLines); i++ {
			if align, ok := alignAllowingFileBlanks(fileLines, context, i, tier); ok {
				found = append(found, align)
				if anchored {
					break
				}
			}
		}
		if len(found) == 1 || (anchored && len(found) > 0) {
			return found[0], true
		}
		// More than one alignment: ambiguous at this tier. Fall through and
		// try a stricter... there is no stricter tier, so stop entirely rather
		// than let a looser tier pick arbitrarily.
		if len(found) > 1 {
			return blankRepairAlignment{}, false
		}
	}
	return blankRepairAlignment{}, false
}

// repairHunkForFileBlanks returns a copy of hunk whose context includes the
// file's blank lines, together with the file index at which it now matches
// exactly. Chunk offsets are shifted to stay aligned with the rewritten
// context.
func repairHunkForFileBlanks(fileLines []string, hunk patchHunk, cursor int) (patchHunk, int, bool) {
	align, ok := findBlankRepair(fileLines, hunk.context, cursor, len(hunk.anchors) > 0)
	if !ok {
		return hunk, 0, false
	}

	// Rebuild the context with blank lines restored at the recorded positions.
	repaired := make([]string, 0, len(hunk.context)+len(align.insertAt))
	insert := 0
	for c := 0; c <= len(hunk.context); c++ {
		for insert < len(align.insertAt) && align.insertAt[insert] == c {
			repaired = append(repaired, "")
			insert++
		}
		if c < len(hunk.context) {
			repaired = append(repaired, hunk.context[c])
		}
	}

	// Shift each chunk's context offset by the number of blanks inserted at or
	// before it, so chunk boundaries continue to point at the same real lines.
	shifted := make([]patchChunk, len(hunk.chunks))
	for i, chunk := range hunk.chunks {
		delta := 0
		for _, position := range align.insertAt {
			if position <= chunk.ctxOffset {
				delta++
			}
		}
		shifted[i] = patchChunk{
			ctxOffset: chunk.ctxOffset + delta,
			del:       chunk.del,
			ins:       chunk.ins,
		}
	}

	out := patchHunk{
		anchors: hunk.anchors,
		context: repaired,
		chunks:  shifted,
		eof:     hunk.eof,
	}

	// Verify the repair before handing it back. The rewritten context must
	// match the file exactly at the recorded offset; if it does not, something
	// about the alignment was wrong and we must not edit the file on a guess.
	if !matchAt(fileLines, out.context, align.at, 0) {
		return hunk, 0, false
	}
	if len(out.context) != align.span {
		return hunk, 0, false
	}
	return out, align.at, true
}

// describeBlankRepair renders a short human explanation used in diagnostics.
func describeBlankRepair(count int) string {
	return fmt.Sprintf("re-aligned context across %d blank line(s) missing from the patch", count)
}
