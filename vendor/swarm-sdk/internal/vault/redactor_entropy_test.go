package vault

import (
	"strings"
	"testing"
)

func TestRedactHighEntropy(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantRedact bool // whether [REDACTED-ENTROPY] must appear
		wantSame   bool // whether output must be unchanged (no false positive)
	}{
		// --- MUST be redacted (real secret-shaped tokens) ---
		{
			name:       "aws access key id",
			input:      "key is AKIAIOSFODNN7EXAMPLE done",
			wantRedact: true,
		},
		{
			name:       "github personal access token",
			input:      "token=ghp_16C7e42F292c6912E7710c838347Ae178B4a done",
			wantRedact: true,
		},
		{
			name:       "slack bot token",
			input:      "xoxb-2345678901-2345678901234-AbCdEfGhIjKlMnOpQrStUvWx",
			wantRedact: true,
		},
		{
			name:       "openai style key",
			input:      "sk-abc123DEF456ghi789JKL012mno345PQR678stu",
			wantRedact: true,
		},
		{
			name:       "jwt token",
			input:      "auth: eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U",
			wantRedact: true,
		},
		{
			name:       "40 char hex sha",
			input:      "digest 5f4dcc3b5aa765d61d8327deb882cf99abcdef01 here",
			wantRedact: true,
		},
		{
			name:       "random base64 blob",
			input:      "data Zm9vYmFyQmF6UXV4MTIzNDU2Nzg5MFhZWlBRUlNU end",
			wantRedact: true,
		},

		// --- MUST NOT be redacted (false-positive guards) ---
		{
			name:     "ordinary english",
			input:    "the quick brown fox jumps over the lazy dog",
			wantSame: true,
		},
		{
			name:     "unix path",
			input:    "/usr/local/bin/foo",
			wantSame: true,
		},
		{
			name:     "github url",
			input:    "https://github.com/user/repo",
			wantSame: true,
		},
		{
			name:     "function name",
			input:    "function handleRequest",
			wantSame: true,
		},
		{
			name:     "sentence with long-ish words",
			input:    "internationalization localization configuration",
			wantSame: true,
		},
		{
			name:     "repeated low-entropy chars",
			input:    "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			wantSame: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := NewOutputRedactor()
			out, count := r.RedactHighEntropy(tt.input)

			if tt.wantRedact {
				if count == 0 {
					t.Fatalf("expected redaction but count=0; input=%q output=%q", tt.input, out)
				}
				if !strings.Contains(out, entropyReplacement) {
					t.Fatalf("expected %q in output, got %q", entropyReplacement, out)
				}
			}
			if tt.wantSame {
				if count != 0 {
					t.Fatalf("false positive: count=%d, input=%q output=%q", count, tt.input, out)
				}
				if out != tt.input {
					t.Fatalf("false positive: output changed\n in=%q\nout=%q", tt.input, out)
				}
			}
		})
	}
}

func TestShannonEntropy(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantMin float64 // entropy must be >= this
		wantMax float64 // entropy must be <= this
	}{
		{name: "empty", input: "", wantMin: 0, wantMax: 0},
		{name: "single repeated char", input: "aaaaaaaa", wantMin: 0, wantMax: 0.001},
		{name: "two equal symbols", input: "abababab", wantMin: 0.99, wantMax: 1.01},
		{name: "random-ish hex", input: "5f4dcc3b5aa765d61d8327deb882cf99", wantMin: 3.0, wantMax: 4.1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shannonEntropy(tt.input)
			if got < tt.wantMin || got > tt.wantMax {
				t.Fatalf("shannonEntropy(%q)=%v, want in [%v,%v]", tt.input, got, tt.wantMin, tt.wantMax)
			}
		})
	}
}

func TestRedactAll(t *testing.T) {
	r := NewOutputRedactor()
	secrets := []string{"my-known-secret-value"}
	input := "known=my-known-secret-value fresh=AKIAIOSFODNN7EXAMPLE plain=hello"

	out, count := r.RedactAll(input, secrets)

	if strings.Contains(out, "my-known-secret-value") {
		t.Fatalf("known secret not redacted: %q", out)
	}
	if strings.Contains(out, "AKIAIOSFODNN7EXAMPLE") {
		t.Fatalf("high-entropy token not redacted: %q", out)
	}
	if !strings.Contains(out, "plain=hello") {
		t.Fatalf("ordinary text was altered: %q", out)
	}
	if count < 2 {
		t.Fatalf("expected >=2 redactions (known + entropy), got %d", count)
	}
}

func TestRedactAllNoRedaction(t *testing.T) {
	r := NewOutputRedactor()
	input := "the quick brown fox at /usr/local/bin/foo"
	out, count := r.RedactAll(input, []string{"unused-secret"})
	if count != 0 {
		t.Fatalf("expected 0 redactions, got %d (out=%q)", count, out)
	}
	if out != input {
		t.Fatalf("output altered: %q", out)
	}
}
