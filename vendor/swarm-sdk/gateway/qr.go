package gateway

import (
	"fmt"
	"strings"
)

// PrintQR is the exported entry point for rendering a scannable terminal QR.
func PrintQR(url string) { printQR(url) }

// This is a self-contained QR Code encoder (byte mode, versions 1-10, ECC level
// L) with full Reed-Solomon error correction and mask-penalty selection, so the
// terminal QR actually scans. No external dependencies. Implemented from the
// ISO/IEC 18004 standard.

// printQR renders url as a scannable QR code using half-block characters (two
// vertical modules per character row) so the aspect ratio looks square in a
// terminal. Falls back to just printing the URL if encoding fails.
func printQR(url string) {
	m, err := qrEncode(url)
	if err != nil {
		fmt.Printf("    (QR unavailable: %v)\n    %s\n", err, url)
		return
	}
	n := len(m)
	quiet := 2
	// White background, black modules. Use "  " for white, "██" for black so the
	// scanner sees square modules. Two-space cells keep the code near-square.
	line := func(row []bool) string {
		var b strings.Builder
		for i := 0; i < quiet; i++ {
			b.WriteString("  ")
		}
		for _, on := range row {
			if on {
				b.WriteString("██")
			} else {
				b.WriteString("  ")
			}
		}
		return b.String()
	}
	blank := strings.Repeat("  ", n+2*quiet)
	// Print on a white field. Many terminals are dark; QR scanners need dark
	// modules on light. We emit spaces (terminal bg) for light — works on light
	// terminals. For dark terminals, users can invert; the URL is printed too.
	for i := 0; i < quiet; i++ {
		fmt.Println("    " + blank)
	}
	for _, row := range m {
		fmt.Println("    " + line(row))
	}
	for i := 0; i < quiet; i++ {
		fmt.Println("    " + blank)
	}
}

// ── Galois field GF(256) for Reed-Solomon ────────────────────────────────────

var gfExp [512]int
var gfLog [256]int

func init() {
	x := 1
	for i := 0; i < 255; i++ {
		gfExp[i] = x
		gfLog[x] = i
		x <<= 1
		if x&0x100 != 0 {
			x ^= 0x11d // primitive polynomial
		}
	}
	for i := 255; i < 512; i++ {
		gfExp[i] = gfExp[i-255]
	}
}

func gfMul(a, b int) int {
	if a == 0 || b == 0 {
		return 0
	}
	return gfExp[gfLog[a]+gfLog[b]]
}

// rsGenPoly returns the generator polynomial for n ECC codewords.
func rsGenPoly(n int) []int {
	g := []int{1}
	for i := 0; i < n; i++ {
		// multiply g by (x - alpha^i)
		next := make([]int, len(g)+1)
		for j := 0; j < len(g); j++ {
			next[j] ^= g[j]
			next[j+1] ^= gfMul(g[j], gfExp[i])
		}
		g = next
	}
	return g
}

// rsEncode returns n ECC codewords for the given data codewords.
func rsEncode(data []int, n int) []int {
	gen := rsGenPoly(n)
	res := make([]int, len(data)+n)
	copy(res, data)
	for i := 0; i < len(data); i++ {
		coef := res[i]
		if coef == 0 {
			continue
		}
		for j := 0; j < len(gen); j++ {
			res[i+j] ^= gfMul(gen[j], coef)
		}
	}
	return res[len(data):]
}

// ── Capacity tables (ECC level L, byte mode) ─────────────────────────────────

// dataCodewordsL[v] = number of data codewords for version v at ECC level L.
var dataCodewordsL = map[int]int{
	1: 19, 2: 34, 3: 55, 4: 80, 5: 108, 6: 136, 7: 156, 8: 194, 9: 232, 10: 274,
}

// eccPerBlockL[v] and blocksL[v] for ECC level L. For v1-10 at level L the ECC
// blocks are: v1:1 block/7ecc, v2:1/10, v3:1/15, v4:1/20, v5:1/26, v6:2/18,
// v7:2/20, v8:2/24, v9:2/30, v10:4/18. (From ISO/IEC 18004 Table 9.)
var eccPerBlockL = map[int]int{1: 7, 2: 10, 3: 15, 4: 20, 5: 26, 6: 18, 7: 20, 8: 24, 9: 30, 10: 18}
var blocksL = map[int]int{1: 1, 2: 1, 3: 1, 4: 1, 5: 1, 6: 2, 7: 2, 8: 2, 9: 2, 10: 4}

// qrEncode encodes s as a QR matrix (true = dark module). Byte mode, ECC L.
func qrEncode(s string) ([][]bool, error) {
	data := []byte(s)

	// Pick the smallest version that fits (byte mode overhead: 4 bits mode + 8 or
	// 16 bits length depending on version).
	version := 0
	for v := 1; v <= 10; v++ {
		lenBits := 8
		if v >= 10 {
			lenBits = 16
		}
		totalBits := 4 + lenBits + len(data)*8
		if (totalBits+7)/8 <= dataCodewordsL[v] {
			version = v
			break
		}
	}
	if version == 0 {
		return nil, fmt.Errorf("data too long (%d bytes) for v1-10", len(data))
	}

	// ── Build the bit stream ──
	var bits []int
	putBits := func(val, n int) {
		for i := n - 1; i >= 0; i-- {
			bits = append(bits, (val>>i)&1)
		}
	}
	putBits(0x4, 4) // byte mode indicator
	lenBits := 8
	if version >= 10 {
		lenBits = 16
	}
	putBits(len(data), lenBits)
	for _, b := range data {
		putBits(int(b), 8)
	}
	capBits := dataCodewordsL[version] * 8
	// Terminator (up to 4 zero bits).
	for i := 0; i < 4 && len(bits) < capBits; i++ {
		bits = append(bits, 0)
	}
	// Pad to a byte boundary.
	for len(bits)%8 != 0 {
		bits = append(bits, 0)
	}
	// Pad bytes 0xEC, 0x11 alternating.
	pad := []int{0xEC, 0x11}
	for pi := 0; len(bits) < capBits; pi++ {
		putBits(pad[pi%2], 8)
	}
	// Bits → data codewords.
	dcw := make([]int, len(bits)/8)
	for i := range dcw {
		v := 0
		for j := 0; j < 8; j++ {
			v = v<<1 | bits[i*8+j]
		}
		dcw[i] = v
	}

	// ── Split into blocks, compute ECC, interleave ──
	nBlocks := blocksL[version]
	eccLen := eccPerBlockL[version]
	base := len(dcw) / nBlocks
	rem := len(dcw) % nBlocks
	var dataBlocks [][]int
	var eccBlocks [][]int
	idx := 0
	for b := 0; b < nBlocks; b++ {
		size := base
		if b >= nBlocks-rem {
			size++
		}
		blk := dcw[idx : idx+size]
		idx += size
		dataBlocks = append(dataBlocks, blk)
		eccBlocks = append(eccBlocks, rsEncode(blk, eccLen))
	}
	var final []int
	maxData := 0
	for _, b := range dataBlocks {
		if len(b) > maxData {
			maxData = len(b)
		}
	}
	for i := 0; i < maxData; i++ {
		for _, b := range dataBlocks {
			if i < len(b) {
				final = append(final, b[i])
			}
		}
	}
	for i := 0; i < eccLen; i++ {
		for _, b := range eccBlocks {
			final = append(final, b[i])
		}
	}

	// ── Place modules in the matrix ──
	size := 17 + version*4
	mods := make([][]int, size) // -1 unset, 0 light, 1 dark
	reserved := make([][]bool, size)
	for i := range mods {
		mods[i] = make([]int, size)
		reserved[i] = make([]bool, size)
		for j := range mods[i] {
			mods[i][j] = -1
		}
	}
	setF := func(r, c, v int) {
		mods[r][c] = v
		reserved[r][c] = true
	}

	// Finder patterns + separators at three corners.
	placeFinder := func(r, c int) {
		for dr := -1; dr <= 7; dr++ {
			for dc := -1; dc <= 7; dc++ {
				rr, cc := r+dr, c+dc
				if rr < 0 || rr >= size || cc < 0 || cc >= size {
					continue
				}
				on := (dr >= 0 && dr <= 6 && (dc == 0 || dc == 6)) ||
					(dc >= 0 && dc <= 6 && (dr == 0 || dr == 6)) ||
					(dr >= 2 && dr <= 4 && dc >= 2 && dc <= 4)
				if on {
					setF(rr, cc, 1)
				} else {
					setF(rr, cc, 0)
				}
			}
		}
	}
	placeFinder(0, 0)
	placeFinder(0, size-7)
	placeFinder(size-7, 0)

	// Timing patterns.
	for i := 8; i < size-8; i++ {
		v := 0
		if i%2 == 0 {
			v = 1
		}
		if !reserved[6][i] {
			setF(6, i, v)
		}
		if !reserved[i][6] {
			setF(i, 6, v)
		}
	}

	// Alignment patterns (versions >= 2).
	if version >= 2 {
		centers := alignmentCenters(version)
		for _, r := range centers {
			for _, c := range centers {
				// Skip if overlapping a finder pattern.
				if (r <= 8 && c <= 8) || (r <= 8 && c >= size-9) || (r >= size-9 && c <= 8) {
					continue
				}
				for dr := -2; dr <= 2; dr++ {
					for dc := -2; dc <= 2; dc++ {
						on := dr == -2 || dr == 2 || dc == -2 || dc == 2 || (dr == 0 && dc == 0)
						v := 0
						if on {
							v = 1
						}
						setF(r+dr, c+dc, v)
					}
				}
			}
		}
	}

	// Dark module.
	setF(size-8, 8, 1)

	// Reserve format-info areas (filled after masking).
	for i := 0; i <= 8; i++ {
		if !reserved[8][i] {
			reserved[8][i] = true
		}
		if !reserved[i][8] {
			reserved[i][8] = true
		}
	}
	for i := 0; i < 8; i++ {
		reserved[8][size-1-i] = true
		reserved[size-1-i][8] = true
	}

	// ── Place data bits in zig-zag ──
	bitIdx := 0
	getBit := func() int {
		if bitIdx >= len(final)*8 {
			return 0
		}
		b := final[bitIdx/8]
		v := (b >> (7 - bitIdx%8)) & 1
		bitIdx++
		return v
	}
	upward := true
	for col := size - 1; col > 0; col -= 2 {
		if col == 6 {
			col-- // skip the timing column
		}
		for i := 0; i < size; i++ {
			var row int
			if upward {
				row = size - 1 - i
			} else {
				row = i
			}
			for _, c := range []int{col, col - 1} {
				if !reserved[row][c] {
					mods[row][c] = getBit()
				}
			}
		}
		upward = !upward
	}

	// ── Choose the best mask by penalty ──
	bestMask := 0
	bestPenalty := 1 << 30
	var best [][]int
	for mask := 0; mask < 8; mask++ {
		cand := applyMask(mods, reserved, size, mask)
		writeFormat(cand, size, mask)
		p := penalty(cand, size)
		if p < bestPenalty {
			bestPenalty = p
			bestMask = mask
			best = cand
		}
	}
	_ = bestMask

	// Convert to bool matrix.
	out := make([][]bool, size)
	for r := 0; r < size; r++ {
		out[r] = make([]bool, size)
		for c := 0; c < size; c++ {
			out[r][c] = best[r][c] == 1
		}
	}
	return out, nil
}

// alignmentCenters returns alignment-pattern center coordinates for a version.
func alignmentCenters(version int) []int {
	// Table for versions 2-10 (ISO/IEC 18004 Annex E).
	table := map[int][]int{
		2: {6, 18}, 3: {6, 22}, 4: {6, 26}, 5: {6, 30}, 6: {6, 34},
		7: {6, 22, 38}, 8: {6, 24, 42}, 9: {6, 26, 46}, 10: {6, 28, 50},
	}
	return table[version]
}

// applyMask returns a copy of mods with the mask applied to non-reserved cells.
func applyMask(mods [][]int, reserved [][]bool, size, mask int) [][]int {
	out := make([][]int, size)
	for r := 0; r < size; r++ {
		out[r] = make([]int, size)
		copy(out[r], mods[r])
		for c := 0; c < size; c++ {
			if reserved[r][c] {
				continue
			}
			if maskCond(mask, r, c) {
				out[r][c] ^= 1
			}
		}
	}
	return out
}

func maskCond(mask, r, c int) bool {
	switch mask {
	case 0:
		return (r+c)%2 == 0
	case 1:
		return r%2 == 0
	case 2:
		return c%3 == 0
	case 3:
		return (r+c)%3 == 0
	case 4:
		return (r/2+c/3)%2 == 0
	case 5:
		return (r*c)%2+(r*c)%3 == 0
	case 6:
		return ((r*c)%2+(r*c)%3)%2 == 0
	case 7:
		return ((r+c)%2+(r*c)%3)%2 == 0
	}
	return false
}

// writeFormat writes the 15-bit format information (ECC level L + mask) into the
// reserved format areas.
func writeFormat(mods [][]int, size, mask int) {
	// ECC level L = 0b01; format data = (level<<3)|mask.
	data := (0b01 << 3) | mask
	// BCH(15,5).
	rem := data << 10
	for i := 14; i >= 10; i-- {
		if (rem>>i)&1 == 1 {
			rem ^= 0x537 << (i - 10)
		}
	}
	format := ((data << 10) | rem) ^ 0x5412

	bit := func(i int) int { return (format >> i) & 1 }
	// Around top-left finder.
	for i := 0; i <= 5; i++ {
		mods[8][i] = bit(i)
	}
	mods[8][7] = bit(6)
	mods[8][8] = bit(7)
	mods[7][8] = bit(8)
	for i := 9; i <= 14; i++ {
		mods[14-i][8] = bit(i)
	}
	// Around the other two finders.
	for i := 0; i <= 7; i++ {
		mods[size-1-i][8] = bit(i)
	}
	for i := 8; i <= 14; i++ {
		mods[8][size-15+i] = bit(i)
	}
}

// penalty computes the QR mask penalty score (rules 1-4).
func penalty(m [][]int, size int) int {
	score := 0
	// Rule 1: runs of >=5 same-color modules in rows and columns.
	for r := 0; r < size; r++ {
		runC, runR := 1, 1
		for c := 1; c < size; c++ {
			if m[r][c] == m[r][c-1] {
				runC++
			} else {
				if runC >= 5 {
					score += 3 + (runC - 5)
				}
				runC = 1
			}
			if m[c][r] == m[c-1][r] {
				runR++
			} else {
				if runR >= 5 {
					score += 3 + (runR - 5)
				}
				runR = 1
			}
		}
		if runC >= 5 {
			score += 3 + (runC - 5)
		}
		if runR >= 5 {
			score += 3 + (runR - 5)
		}
	}
	// Rule 2: 2x2 blocks of the same color.
	for r := 0; r < size-1; r++ {
		for c := 0; c < size-1; c++ {
			v := m[r][c]
			if m[r][c+1] == v && m[r+1][c] == v && m[r+1][c+1] == v {
				score += 3
			}
		}
	}
	// Rule 3: finder-like patterns 1:1:3:1:1.
	pat1 := []int{1, 0, 1, 1, 1, 0, 1, 0, 0, 0, 0}
	pat2 := []int{0, 0, 0, 0, 1, 0, 1, 1, 1, 0, 1}
	match := func(get func(i int) int) bool {
		m1, m2 := true, true
		for i := 0; i < 11; i++ {
			if get(i) != pat1[i] {
				m1 = false
			}
			if get(i) != pat2[i] {
				m2 = false
			}
		}
		return m1 || m2
	}
	for r := 0; r < size; r++ {
		for c := 0; c < size-10; c++ {
			if match(func(i int) int { return m[r][c+i] }) {
				score += 40
			}
		}
	}
	for c := 0; c < size; c++ {
		for r := 0; r < size-10; r++ {
			if match(func(i int) int { return m[r+i][c] }) {
				score += 40
			}
		}
	}
	// Rule 4: proportion of dark modules.
	dark := 0
	for r := 0; r < size; r++ {
		for c := 0; c < size; c++ {
			if m[r][c] == 1 {
				dark++
			}
		}
	}
	total := size * size
	percent := dark * 100 / total
	dev := percent - 50
	if dev < 0 {
		dev = -dev
	}
	score += (dev / 5) * 10
	return score
}
