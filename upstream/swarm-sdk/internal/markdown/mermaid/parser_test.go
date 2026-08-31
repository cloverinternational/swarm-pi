package mermaid

import "testing"

func TestDetectType(t *testing.T) {
	cases := []struct {
		name     string
		lines    []string
		wantType string
	}{
		{"flowchart", []string{"flowchart TD", "A --> B"}, "flowchart"},
		{"sequence", []string{"sequenceDiagram", "Alice->>Bob: Hi"}, "sequenceDiagram"},
		{"pie", []string{"pie title Summary", "\"A\" : 42"}, "pie"},
		{"state v2", []string{"stateDiagram-v2", "[*] --> S1"}, "stateDiagram-v2"},
		{"skip blank", []string{"", "", "graph LR", "A-->B"}, "graph"},
		{"unknown", []string{"weirdDiagram"}, "weirdDiagram"},
		{"empty", []string{}, "unknown"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := DetectType(tc.lines); got != tc.wantType {
				t.Errorf("DetectType(%v) = %q, want %q", tc.lines, got, tc.wantType)
			}
		})
	}
}

func TestParseTitle(t *testing.T) {
	cases := []struct {
		name      string
		lines     []string
		wantTitle string
	}{
		{"inline", []string{`pie title "My Pie"`}, "My Pie"},
		{"standalone", []string{"flowchart TD", `title "Flow Title"`}, "Flow Title"},
		{"none", []string{"flowchart TD", "A-->B"}, ""},
		{"unquoted_inline", []string{"pie title My Pie"}, "My Pie"},
		{"title_after_blanks", []string{"", "", "flowchart TD", `title "Later"`}, "Later"},
		{"no_match_anywhere", []string{"some diagram", "without title"}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ParseTitle(tc.lines); got != tc.wantTitle {
				t.Errorf("ParseTitle(%v) = %q, want %q", tc.lines, got, tc.wantTitle)
			}
		})
	}
}
