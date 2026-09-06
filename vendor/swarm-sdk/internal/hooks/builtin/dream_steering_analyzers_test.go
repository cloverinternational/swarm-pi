package builtin

import (
	"context"
	"testing"
)

func TestIsDiscoveryNonZero(t *testing.T) {
	cases := []struct {
		name string
		tool string
		cmd  string
		err  string
		want bool
	}{
		{"which miss", "Bash", "which sentry-cli", "", true},
		{"which path miss", "Bash", "/usr/bin/which sentry-cli", "", true},
		{"command -v miss", "Bash", "command -v foo", "", true},
		{"grep no match", "Bash", "grep needle haystack.txt", "", true},
		{"find no match", "Bash", "find . -name foo", "", true},
		{"test missing file", "Bash", "test -f /nope", "", true},
		{"ls", "Bash", "ls /nonexistent", "", true},

		// Real failures: keep them tagged.
		{"permission denied trumps discovery", "Bash", "grep needle /root/secret", "permission denied", false},
		{"segfault trumps discovery", "Bash", "find /proc -name foo", "segmentation fault", false},

		// Non-discovery commands: keep them tagged.
		{"rm fail", "Bash", "rm /nope", "", false},
		{"go build fail", "Bash", "go build ./...", "", false},
		{"git push fail", "Bash", "git push origin main", "", false},

		// Non-bash tool: never suppress.
		{"non-bash tool", "Edit", "which foo", "", false},

		// Empty command: never suppress.
		{"empty cmd", "Bash", "", "", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			input := map[string]any{"command": tc.cmd}
			output := map[string]any{}
			if tc.err != "" {
				output["error"] = tc.err
			}
			got := isDiscoveryNonZero(tc.tool, input, output)
			if got != tc.want {
				t.Errorf("isDiscoveryNonZero(%s, %q, err=%q) = %v, want %v", tc.tool, tc.cmd, tc.err, got, tc.want)
			}
		})
	}
}

func TestExtractRelationshipEmpty(t *testing.T) {
	a := NewDreamAnalyzer()
	// content with NO recognized extensions → relationship insight should be ""
	got := a.extractRelationship(
		map[string]any{"file_path": "/some/dir"},
		map[string]any{"content": "this is just prose with no file references at all"},
		0.8,
	)
	if got != "" {
		t.Errorf("expected empty relationship insight on prose-only content, got %q", got)
	}

	// content with a Go reference → emits a string
	got = a.extractRelationship(
		map[string]any{"file_path": "/some/dir"},
		map[string]any{"content": "package foo\nimport \"x.go\""},
		0.8,
	)
	if got == "" {
		t.Errorf("expected non-empty relationship insight when extensions are present")
	}
}

func TestDreamAnalyzer_DiscoveryFailureNotTagged(t *testing.T) {
	a := NewDreamAnalyzer()
	ctx := context.Background()

	// `which sentry-cli` returning nothing should NOT be tagged anti-pattern.
	res, err := a.Analyze(ctx, "Bash", map[string]any{
		"command": "which sentry-cli",
	}, map[string]any{
		"success": false,
		"stderr":  "",
		"error":   "",
	})
	if err != nil {
		t.Fatalf("Analyze returned error: %v", err)
	}
	for _, tag := range res.Tags {
		if tag == "anti-pattern" {
			t.Errorf("discovery non-zero command should not be tagged anti-pattern; got tags=%v insights=%v", res.Tags, res.Insights)
		}
	}
}
