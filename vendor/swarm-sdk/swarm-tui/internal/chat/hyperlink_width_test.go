package chat

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// TestOSC8IsZeroWidth is the gate for the entire hyperlink feature.
//
// A terminal hyperlink is an out-of-band escape: the terminal consumes the
// sequence and paints only the text between the open and close. Every layout
// decision in this package — column budgets, table fitting, wrap points, the
// raw-to-wrapped mapping that image placement depends on — is computed from
// lipgloss.Width or ansi.StringWidth. If either of them counted the URL bytes,
// adding a link would silently shift every downstream measurement and links
// could not be introduced without breaking layout.
//
// This test exists so that assumption is proven rather than believed.
func TestOSC8IsZeroWidth(t *testing.T) {
	const (
		st   = "\x1b\\"
		text = "click here"
	)

	cases := []struct {
		name string
		link string
	}{
		{
			name: "st_terminated",
			link: "\x1b]8;;https://example.com/a/very/long/path?with=query" + st + text + "\x1b]8;;" + st,
		},
		{
			name: "with_id_param",
			link: "\x1b]8;id=42;https://example.com" + st + text + "\x1b]8;;" + st,
		},
		{
			name: "bel_terminated",
			link: "\x1b]8;;https://example.com\x07" + text + "\x1b]8;;\x07",
		},
		{
			name: "file_url_with_fragment",
			link: "\x1b]8;;file://host/home/user/project/main.go#123" + st + text + "\x1b]8;;" + st,
		},
	}

	want := len(text) // "click here" is plain ASCII: 10 columns

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ansi.StringWidth(tc.link); got != want {
				t.Errorf("ansi.StringWidth = %d, want %d — the URL bytes are being counted as visible columns", got, want)
			}
			if got := lipgloss.Width(tc.link); got != want {
				t.Errorf("lipgloss.Width = %d, want %d — the URL bytes are being counted as visible columns", got, want)
			}
			if got := PrintableWidth(tc.link); got != want {
				t.Errorf("PrintableWidth = %d, want %d — the URL bytes are being counted as visible columns", got, want)
			}
			if got := StripANSI(tc.link); got != text {
				t.Errorf("StripANSI = %q, want %q — link markup leaks into plain text", got, text)
			}
		})
	}
}

// TestOSC8SurvivesLongURL guards the case that matters most in practice: a very
// long URL behind short link text must not make the line "wide".
func TestOSC8SurvivesLongURL(t *testing.T) {
	url := "https://example.com/" + strings.Repeat("segment/", 60)
	link := "\x1b]8;;" + url + "\x1b\\" + "docs" + "\x1b]8;;\x1b\\"

	if got := ansi.StringWidth(link); got != 4 {
		t.Fatalf("a %d-byte URL measured as %d columns behind 4 characters of text", len(url), got)
	}
}
