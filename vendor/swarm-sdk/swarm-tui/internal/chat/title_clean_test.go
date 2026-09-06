package chat

import "testing"

func TestCleanGeneratedTitle(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "Fix chat title generation", "Fix chat title generation"},
		{"double_quoted", "\"Meta ads analysis\"", "Meta ads analysis"},
		{"single_quoted", "'Meta ads analysis'", "Meta ads analysis"},
		{"backtick", "`Consolidate history menus`", "Consolidate history menus"},
		{"smart_quotes", "“Refactor naming agent”", "Refactor naming agent"},
		{"title_prefix", "Title: Fix the build", "Fix the build"},
		{"chat_title_prefix", "Chat title: Improve UX", "Improve UX"},
		{"trailing_period", "Add summary panel.", "Add summary panel"},
		{"multiline", "Consolidate menus\nThis captures the goal.", "Consolidate menus"},
		{"leading_blank_line", "\n\nDebug renderer", "Debug renderer"},
		{"quoted_with_prefix", "Title: \"Ship the feature\"", "Ship the feature"},
		{"empty", "", ""},
		{"whitespace_only", "   \n  ", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := cleanGeneratedTitle(c.in); got != c.want {
				t.Errorf("cleanGeneratedTitle(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestHasExistingRecap(t *testing.T) {
	if hasExistingRecap(nil) {
		t.Error("nil conv should have no recap")
	}
}
