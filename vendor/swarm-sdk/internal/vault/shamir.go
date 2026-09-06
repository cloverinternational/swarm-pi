package vault

// Shamir Secret Sharing over GF(2^8).
//
// This is a small, self-contained, audited implementation used to enforce
// Two-Person Integrity: a data key is split into N shares with a threshold of
// K, so that no fewer than K shares can reconstruct it. Sensitive credentials
// use K=2, guaranteeing that a single machine (even a fully compromised one)
// cannot reconstruct the secret alone.
//
// Design notes / provenance:
//   - Field is GF(2^8) with the AES/Rijndael reduction polynomial 0x11b.
//   - Multiplication uses log/exp tables built from generator 0x03 (constant
//     lookups, no data-dependent branches in the hot path).
//   - Share layout follows the well-known HashiCorp Vault convention: each
//     share is the polynomial evaluation bytes with the 1-byte x-coordinate
//     appended as the final byte. Shares are therefore len(secret)+1 bytes.
//
// SECURITY: callers MUST use crypto/rand for coefficients (shamirSplit does).
// The x-coordinates are 1..N (never 0, which is the secret itself).

import (
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"fmt"
)

// gfExp and gfLog are the exp/log tables for GF(2^8) with generator 0x03.
var gfExp [256]byte
var gfLog [256]byte

func init() {
	// Build the tables. 0x03 is a generator of the multiplicative group.
	x := byte(1)
	for i := 0; i < 255; i++ {
		gfExp[i] = x
		gfLog[x] = byte(i)
		// x = x * 0x03 in GF(2^8)
		x = gfMulNoTable(x, 0x03)
	}
	// gfExp has period 255 (exp[255]==exp[0]==1). gfMul reduces sums >=255 and
	// gfDiv wraps negative differences, so table indices stay within [0,254].
	gfExp[255] = gfExp[0]
}

// gfMulNoTable multiplies two field elements using the Russian-peasant method.
// Used only to bootstrap the tables at init; the hot path uses gfMul.
func gfMulNoTable(a, b byte) byte {
	var p byte
	for i := 0; i < 8; i++ {
		if b&1 == 1 {
			p ^= a
		}
		hi := a & 0x80
		a <<= 1
		if hi != 0 {
			a ^= 0x1b // reduction by x^8 + x^4 + x^3 + x + 1 (0x11b)
		}
		b >>= 1
	}
	return p
}

// gfAdd adds two field elements (XOR).
func gfAdd(a, b byte) byte { return a ^ b }

// gfMul multiplies two field elements via log/exp tables.
func gfMul(a, b byte) byte {
	if a == 0 || b == 0 {
		return 0
	}
	sum := int(gfLog[a]) + int(gfLog[b])
	if sum >= 255 {
		sum -= 255
	}
	return gfExp[sum]
}

// gfDiv divides a by b (b != 0) via log/exp tables.
func gfDiv(a, b byte) (byte, error) {
	if b == 0 {
		return 0, errors.New("shamir: division by zero")
	}
	if a == 0 {
		return 0, nil
	}
	diff := int(gfLog[a]) - int(gfLog[b])
	if diff < 0 {
		diff += 255
	}
	return gfExp[diff], nil
}

// evalPoly evaluates the polynomial (given by coeffs, constant term first) at x
// using Horner's method in GF(2^8).
func evalPoly(coeffs []byte, x byte) byte {
	if x == 0 {
		// Guard: x==0 would reveal the secret (constant term). Never used.
		return coeffs[0]
	}
	// Horner from the highest-degree coefficient down.
	out := coeffs[len(coeffs)-1]
	for i := len(coeffs) - 2; i >= 0; i-- {
		out = gfAdd(gfMul(out, x), coeffs[i])
	}
	return out
}

// shamirSplit splits secret into parts shares, any threshold of which can
// reconstruct it. Returns parts byte slices, each len(secret)+1 bytes (the
// trailing byte is the x-coordinate). Requires 2 <= threshold <= parts <= 255.
func shamirSplit(secret []byte, parts, threshold int) ([][]byte, error) {
	if parts < threshold {
		return nil, fmt.Errorf("shamir: parts (%d) < threshold (%d)", parts, threshold)
	}
	if threshold < 2 {
		return nil, fmt.Errorf("shamir: threshold must be >= 2, got %d", threshold)
	}
	if parts > 255 {
		return nil, fmt.Errorf("shamir: parts must be <= 255, got %d", parts)
	}
	if len(secret) == 0 {
		return nil, errors.New("shamir: empty secret")
	}

	// Distinct, non-zero x-coordinates for each share: 1..parts.
	xs := make([]byte, parts)
	for i := 0; i < parts; i++ {
		xs[i] = byte(i + 1)
	}

	out := make([][]byte, parts)
	for i := range out {
		out[i] = make([]byte, len(secret)+1)
		out[i][len(secret)] = xs[i]
	}

	// For each secret byte, build a random degree-(threshold-1) polynomial whose
	// constant term is that byte, then evaluate at every share's x-coordinate.
	coeffs := make([]byte, threshold)
	defer zeroize(coeffs) // coeffs[0] transiently holds secret bytes
	for b := 0; b < len(secret); b++ {
		coeffs[0] = secret[b]
		if _, err := rand.Read(coeffs[1:]); err != nil {
			return nil, fmt.Errorf("shamir: rand: %w", err)
		}
		for i := 0; i < parts; i++ {
			out[i][b] = evalPoly(coeffs, xs[i])
		}
	}
	return out, nil
}

// shamirCombine reconstructs the secret from a set of shares (>= threshold of
// them) via Lagrange interpolation at x=0. Shares must be the format produced
// by shamirSplit (value bytes + trailing x-coordinate byte).
func shamirCombine(shares [][]byte) ([]byte, error) {
	if len(shares) < 2 {
		return nil, errors.New("shamir: need at least 2 shares")
	}
	shareLen := len(shares[0])
	if shareLen < 2 {
		return nil, errors.New("shamir: share too short")
	}
	// Validate uniform length and distinct, non-zero x-coordinates.
	xs := make([]byte, len(shares))
	seen := make(map[byte]bool, len(shares))
	for i, s := range shares {
		if len(s) != shareLen {
			return nil, errors.New("shamir: shares have differing lengths")
		}
		x := s[shareLen-1]
		if x == 0 {
			return nil, errors.New("shamir: invalid share (x=0)")
		}
		if seen[x] {
			return nil, errors.New("shamir: duplicate share x-coordinate")
		}
		seen[x] = true
		xs[i] = x
	}

	secretLen := shareLen - 1
	secret := make([]byte, secretLen)

	for b := 0; b < secretLen; b++ {
		var acc byte
		for i := range shares {
			// Lagrange basis l_i(0) = prod_{j!=i} x_j / (x_j - x_i)
			basis := byte(1)
			for j := range shares {
				if i == j {
					continue
				}
				num := xs[j]               // x_j - 0 = x_j
				den := gfAdd(xs[i], xs[j]) // x_i - x_j == x_i + x_j in GF(2^8)
				term, err := gfDiv(num, den)
				if err != nil {
					return nil, err
				}
				basis = gfMul(basis, term)
			}
			acc = gfAdd(acc, gfMul(shares[i][b], basis))
		}
		secret[b] = acc
	}
	return secret, nil
}

// zeroize overwrites a byte slice's contents. Best-effort defense so a
// reconstructed data key does not linger in memory longer than necessary.
func zeroize(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

// constantTimeEqual reports whether a and b are equal without leaking length
// timing beyond the standard subtle guarantees.
func constantTimeEqual(a, b []byte) bool {
	return subtle.ConstantTimeCompare(a, b) == 1
}
