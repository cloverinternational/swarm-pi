package hooks

import (
	"reflect"
	"strings"
	"testing"
)

func TestExtractShellCommandWords(t *testing.T) {
	quotedHeredoc := "cat > f.md <<'EOF'\nssh -i key.pem user@host\nEOF"
	unquotedHeredoc := "cat <<EOF\nssh user@host\nEOF"

	tests := []struct {
		name    string
		command string
		want    []string
	}{
		{"cd then grep", "cd /tmp && grep -n foo bar.go", []string{"cd", "grep"}},
		{"quoted heredoc", quotedHeredoc, []string{"cat"}},
		{"unquoted heredoc", unquotedHeredoc, []string{"cat"}},
		{"double quoted argument", `echo "ssh user@host"`, []string{"echo"}},
		{"single quoted argument", `echo 'ssh user@host'`, []string{"echo"}},
		{"pipeline", "a | b | c", []string{"a", "b", "c"}},
		{"semicolon", "a; b", []string{"a", "b"}},
		{"and", "a && b", []string{"a", "b"}},
		{"or", "a || b", []string{"a", "b"}},
		{"assignment and path", "FOO=bar /usr/bin/grep x", []string{"grep"}},
		{"command substitution", "echo $(rm -rf /)", []string{"echo"}},
		{"empty", "", []string{}},
		{"whitespace", " \t\n ", []string{}},
		{"lone pipe", "|", []string{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractShellCommandWords(tt.command)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("ExtractShellCommandWords(%q) = %#v, want %#v", tt.command, got, tt.want)
			}
		})
	}

	if got := ExtractShellCommandWords(quotedHeredoc); strings.Contains(strings.Join(got, "\n"), "ssh") {
		t.Fatalf("heredoc command words unexpectedly contain ssh: %#v", got)
	}
}

func TestIsBashReadOnly(t *testing.T) {
	tests := []struct {
		name    string
		command string
		want    bool
	}{
		{"cd then grep", "cd /some/dir && grep -n foo bar.go", true},
		{"cd then rm", "cd /some/dir && rm -rf x", false},
		{"bare grep", "grep foo", true},
		{"git status", "git status", true},
		{"git commit", `git commit -m "message"`, false},
		{"go list", "go list ./...", true},
		{"go build", "go build ./...", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsBashReadOnly(tt.command); got != tt.want {
				t.Fatalf("IsBashReadOnly(%q) = %v, want %v", tt.command, got, tt.want)
			}
		})
	}
}

func TestShellHookBuildEnvironmentCommandWords(t *testing.T) {
	raw := "cat > f.md <<'EOF'\nssh -i key.pem user@host\nEOF"
	hook := NewShellHook("command-words", "true", nil)
	env := hook.buildEnvironment(Event{Data: map[string]any{"command": raw}})

	values := make(map[string]string)
	for _, entry := range env {
		if key, value, ok := strings.Cut(entry, "="); ok {
			values[key] = value
		}
	}

	if got := values["COMMAND"]; got != raw {
		t.Fatalf("COMMAND = %q, want exact raw command %q", got, raw)
	}
	if got := values["COMMAND_WORDS"]; got != "cat" {
		t.Fatalf("COMMAND_WORDS = %q, want %q", got, "cat")
	}
	if strings.Contains(values["COMMAND_WORDS"], "ssh") {
		t.Fatalf("COMMAND_WORDS unexpectedly contains ssh: %q", values["COMMAND_WORDS"])
	}
}
