package chat

import (
	"strings"
	"testing"
)

// TestCommitLogTableIsNeverTruncated reproduces the exact shape a user reported
// still showing ellipses: eight columns where seven are narrow and one carries
// paragraph-length prose, rendered in a wide pane.
//
// The failure mode was that the wide column absorbed the budget and every narrow
// column was ellipsed away — "Short SHA" became "Shor…", "Files" became "Fil…".
// Nothing may be truncated at any width.
func TestCommitLogTableIsNeverTruncated(t *testing.T) {
	long1 := "Hardens the release-attestation pipeline: reworked ci/release/attest/index.mjs to pin Sigstore provenance to the private source repository."
	long2 := "Large release-infra commit: introduces the actual attestation generator plus its npm package lockfile and a non-publishing signing smoke job."
	long3 := "The big one: a 606-file production readiness sweep — almost certainly stripping dev-only scaffolding, fixtures and scratch documentation."

	md := strings.Join([]string{
		"| # | Short SHA | Author | Date | Subject | What Actually Changed | Files | Net Lines |",
		"| --- | --- | --- | --- | --- | --- | --- | --- |",
		"| 1 | `c2794621` | Luis Alejandro | 2026-07-30 | fix(tui): bind provenance to private source | " + long1 + " | 7 | +147 |",
		"| 2 | `364f628b` | Luis Alejandro | 2026-07-30 | fix(tui): publish offline Sigstore attestations | " + long2 + " | 10 | +1084 |",
		"| 9 | `cb5d1a20` | rinadelph | 2026-07-29 | chore: prepare production readiness | " + long3 + " | 606 | -102431 |",
	}, "\n")

	// The reported pane was very wide; narrower widths must hold too.
	for _, width := range []int{200, 180, 160, 120, 100, 80, 60, 40} {
		lines := renderMarkdownWithWrapping(md, width, makeTestTheme())
		out := strings.Join(lines, "\n")

		if strings.Contains(out, "…") {
			for i, line := range lines {
				if strings.Contains(line, "…") {
					t.Errorf("width %d: line %d truncated with an ellipsis: %s",
						width, i, StripANSI(line))
				}
			}
			t.Fatalf("width %d: table was truncated instead of wrapped", width)
		}

		for i, line := range lines {
			if got := PrintableWidth(line); got > width {
				t.Fatalf("width %d: line %d overflows by %d: %s",
					width, i, got-width, StripANSI(line))
			}
		}

		// Every header must survive intact. A header may wrap VERTICALLY
		// ("Net" above "Lines"), so it has to be reassembled per column —
		// compacting the whole table would never rejoin those two fragments,
		// and would falsely report loss.
		if headers, ok := headerCellsByColumn(lines); ok {
			want := []string{"#", "ShortSHA", "Author", "Date", "Subject",
				"WhatActuallyChanged", "Files", "NetLines"}
			if len(headers) != len(want) {
				t.Fatalf("width %d: got %d header columns, want %d: %q",
					width, len(headers), len(want), headers)
			}
			for i, w := range want {
				if headers[i] != w {
					t.Errorf("width %d: column %d header is %q, want %q",
						width, i, headers[i], w)
				}
			}
		}
	}
}

// headerCellsByColumn reassembles each header cell from the grid, joining the
// fragments of a vertically wrapped header. It reports ok=false when the output
// is not a grid (the stacked record fallback), which has no header row.
func headerCellsByColumn(lines []string) ([]string, bool) {
	var cells []string
	started := false
	for _, raw := range lines {
		line := StripANSI(raw)
		if strings.HasPrefix(line, "┌") {
			started = true
			continue
		}
		if !started {
			continue
		}
		if strings.HasPrefix(line, "├") || strings.HasPrefix(line, "└") {
			break
		}
		fields := strings.Split(strings.Trim(line, "│"), "│")
		if cells == nil {
			cells = make([]string, len(fields))
		}
		if len(fields) != len(cells) {
			return nil, false
		}
		for i, f := range fields {
			cells[i] += strings.ReplaceAll(strings.TrimSpace(f), " ", "")
		}
	}
	return cells, cells != nil
}
