package termimage

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/ansi/kitty"
)

// decodedRow is what a terminal recovers from one placeholder line.
type decodedRow struct {
	imageRGB     uint32
	placementRGB uint32
	cells        []decodedCell
	sawImageSGR  bool
	sawPlaceSGR  bool
}

type decodedCell struct{ row, column, high int }

// decodeRow reverses PlaceholderLines for one row: it reads the SGR foreground
// (image ID) and underline colour (placement ID) and then every placeholder
// grapheme with its row/column/high-byte diacritic triplet. Decoding proves the
// terminal can reconstruct exactly the identity the manager uploaded; a mismatch
// means the cells point at an image that was never sent.
func decodeRow(t *testing.T, line string) decodedRow {
	t.Helper()
	var out decodedRow
	runes := []rune(line)
	for i := 0; i < len(runes); i++ {
		if runes[i] == 0x1b {
			j := i
			for j < len(runes) && runes[j] != 'm' {
				j++
			}
			if j >= len(runes) {
				t.Fatalf("unterminated SGR in %q", line)
			}
			seq := string(runes[i : j+1])
			var sel, r, g, b int
			if n, _ := fmt.Sscanf(seq, "\x1b[%d;2;%d;%d;%dm", &sel, &r, &g, &b); n == 4 {
				rgb := uint32(r)<<16 | uint32(g)<<8 | uint32(b)
				if sel == 38 {
					out.imageRGB, out.sawImageSGR = rgb, true
				} else if sel == 58 {
					out.placementRGB, out.sawPlaceSGR = rgb, true
				}
			}
			i = j
			continue
		}
		if runes[i] != kitty.Placeholder {
			t.Fatalf("unexpected rune %U outside a placeholder cell in %q", runes[i], line)
		}
		if !out.sawImageSGR || !out.sawPlaceSGR {
			// Without both colours in front of it a cell has no identity; the
			// terminal reserves the row and paints nothing.
			t.Fatalf("placeholder cell emitted before its image/placement colours: %q", line)
		}
		if i+3 >= len(runes) {
			t.Fatalf("placeholder cell missing its diacritic triplet: %q", line)
		}
		out.cells = append(out.cells, decodedCell{
			row:    diacriticIndex(t, runes[i+1]),
			column: diacriticIndex(t, runes[i+2]),
			high:   diacriticIndex(t, runes[i+3]),
		})
		i += 3
	}
	return out
}

func diacriticIndex(t *testing.T, r rune) int {
	t.Helper()
	for i := 0; i < diacriticCount; i++ {
		if kitty.Diacritic(i) == r {
			return i
		}
	}
	t.Fatalf("rune %U is not a Kitty row/column diacritic", r)
	return -1
}

// diacriticCount is the size of the Kitty rowcolumn-diacritics table. Rows and
// columns beyond it cannot be addressed by the protocol at all.
const diacriticCount = maxPlaceholderCells

func TestPlaceholderLinesRoundTripIdentity(t *testing.T) {
	cases := []Placement{
		{ImageID: 0x0240202a, PlacementID: 0x010203, Columns: 3, Rows: 2},
		{ImageID: 1, PlacementID: 1, Columns: 1, Rows: 1},
		{ImageID: 0x00ffffff, PlacementID: 0x00ffffff, Columns: 5, Rows: 4},
		{ImageID: 0x01000000, PlacementID: 0x000001, Columns: 2, Rows: 1},
		{ImageID: 0xffffffff, PlacementID: 0x00ffff00, Columns: 4, Rows: 3},
		{ImageID: 0x12345600, PlacementID: 0x00abcd00, Columns: 7, Rows: 2},
	}
	for _, p := range cases {
		t.Run(fmt.Sprintf("i%08x_p%06x", p.ImageID, p.PlacementID), func(t *testing.T) {
			assertPlaceholderInvariants(t, p, PlaceholderLines(p))
		})
	}
}

// assertPlaceholderInvariants is the shared contract for every placeholder run,
// used by both the table test and the fuzz target.
func assertPlaceholderInvariants(t *testing.T, p Placement, lines []string) {
	t.Helper()
	if p.Columns <= 0 || p.Rows <= 0 || p.ImageID == 0 || p.PlacementID == 0 {
		if lines != nil {
			t.Fatalf("degenerate placement %+v produced %d lines", p, len(lines))
		}
		return
	}
	if p.Columns > diacriticCount || p.Rows > diacriticCount {
		// Beyond the diacritic table the protocol cannot address the cell, so
		// emitting anything would silently mislabel it. Rejection is correct.
		if lines != nil {
			t.Fatalf("placement %dx%d exceeds the diacritic table but produced lines", p.Columns, p.Rows)
		}
		return
	}
	if len(lines) != p.Rows {
		t.Fatalf("lines = %d, want %d", len(lines), p.Rows)
	}
	wantImage := p.ImageID & 0x00ffffff
	wantPlacement := p.PlacementID & 0x00ffffff
	wantHigh := int((p.ImageID >> 24) & 0xff)
	for row, line := range lines {
		if !utf8.ValidString(line) {
			t.Fatalf("row %d is not valid UTF-8", row)
		}
		if got := ansi.StringWidth(line); got != p.Columns {
			t.Fatalf("row %d visual width = %d, want %d", row, got, p.Columns)
		}
		if got := strings.Count(line, string(kitty.Placeholder)); got != p.Columns {
			t.Fatalf("row %d placeholder cells = %d, want %d", row, got, p.Columns)
		}
		decoded := decodeRow(t, line)
		if decoded.imageRGB != wantImage {
			t.Fatalf("row %d image colour = %06x, want %06x", row, decoded.imageRGB, wantImage)
		}
		if decoded.placementRGB != wantPlacement {
			t.Fatalf("row %d placement colour = %06x, want %06x", row, decoded.placementRGB, wantPlacement)
		}
		if len(decoded.cells) != p.Columns {
			t.Fatalf("row %d decoded %d cells, want %d", row, len(decoded.cells), p.Columns)
		}
		for column, cell := range decoded.cells {
			if cell.row != row || cell.column != column || cell.high != wantHigh {
				t.Fatalf("row %d column %d decoded (%d,%d,%d), want (%d,%d,%d)",
					row, column, cell.row, cell.column, cell.high, row, column, wantHigh)
			}
		}
	}
}

func FuzzPlaceholderLines(f *testing.F) {
	f.Add(uint32(0x0240202a), uint32(0x010203), 3, 2)
	f.Add(uint32(1), uint32(1), 1, 1)
	f.Add(uint32(0xffffffff), uint32(0xffffff), 8, 8)
	f.Add(uint32(0), uint32(0), 0, 0)
	f.Add(uint32(0x01000000), uint32(1), 300, 300)
	f.Add(uint32(0x00ff0000), uint32(0x0000ff), 297, 297)

	f.Fuzz(func(t *testing.T, imageID, placementID uint32, columns, rows int) {
		// Bound the requested geometry so the fuzzer spends its budget on
		// encoding edge cases rather than on allocating gigabyte strings.
		if columns < -4 || columns > 400 || rows < -4 || rows > 400 {
			t.Skip()
		}
		p := Placement{ImageID: imageID, PlacementID: placementID, Columns: columns, Rows: rows}
		lines := PlaceholderLines(p)
		assertPlaceholderInvariants(t, p, lines)
	})
}
