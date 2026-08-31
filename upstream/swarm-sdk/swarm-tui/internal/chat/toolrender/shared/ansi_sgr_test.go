package shared

import "testing"

// SGR color codes must survive sanitization (regression: the SGR-preserve
// branch previously wrote an empty slice because the cursor index was advanced
// before the write, silently stripping ALL colors from bash/terminal output).
func TestSanitizeANSI_PreservesSGRColors(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"basic green", "\x1b[32mgreen\x1b[0m text", "\x1b[32mgreen\x1b[0m text"},
		{"bold red", "\x1b[1;31mred\x1b[0m", "\x1b[1;31mred\x1b[0m"},
		{"256 color", "\x1b[38;5;196mX\x1b[0m", "\x1b[38;5;196mX\x1b[0m"},
		{"truecolor", "\x1b[38;2;255;0;0mX\x1b[0m", "\x1b[38;2;255;0;0mX\x1b[0m"},
		{"reset short", "a\x1b[mb", "a\x1b[mb"},
		{"no ansi", "plain text", "plain text"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := SanitizeANSI(c.in); got != c.want {
				t.Errorf("SanitizeANSI(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// Non-SGR CSI sequences (cursor movement / erase) must still be stripped.
func TestSanitizeANSI_StripsNonSGR(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"cursor up", "a\x1b[1Ab", "ab"},
		{"erase line", "a\x1b[0Kb", "ab"},
		{"cursor pos", "\x1b[2J\x1b[Hhome", "home"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := SanitizeANSI(c.in); got != c.want {
				t.Errorf("SanitizeANSI(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
