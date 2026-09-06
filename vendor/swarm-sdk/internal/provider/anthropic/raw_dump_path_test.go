package anthropic

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRawDumpPathFromEnv(t *testing.T) {
	t.Parallel()

	explicit := filepath.Join(t.TempDir(), "provider.log")
	tests := []struct {
		name  string
		value string
		want  string
	}{
		{name: "unset", value: "", want: ""},
		{name: "disabled zero", value: "0", want: ""},
		{name: "disabled false", value: "false", want: ""},
		{name: "legacy one", value: "1", want: defaultRawDumpPath},
		{name: "legacy true", value: " TRUE ", want: defaultRawDumpPath},
		{name: "explicit path", value: explicit, want: explicit},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := rawDumpPathFromEnv(tt.value); got != tt.want {
				t.Fatalf("rawDumpPathFromEnv(%q) = %q, want %q", tt.value, got, tt.want)
			}
		})
	}
}

func TestOpenRawDumpWriterUsesPrivatePermissions(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "provider.log")
	f := openRawDumpWriter(path)
	if f == nil {
		t.Fatal("openRawDumpWriter() returned nil")
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close dump writer: %v", err)
	}

	fileInfo, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat dump file: %v", err)
	}
	if got, want := fileInfo.Mode().Perm(), os.FileMode(0o600); got != want {
		t.Fatalf("dump permissions = %o, want %o", got, want)
	}
}
