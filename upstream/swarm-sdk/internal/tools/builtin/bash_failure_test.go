package builtin

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	sdkerr "github.com/Swarm-Code/mono/swarm-sdk/internal/sdkerr"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

func TestBashFailureCarriesOutputInBatchAndStreaming(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell commands are Unix-only")
	}

	tests := []struct {
		name string
		run  func(*BashTool, BashParams) (*tools.ToolResult, error)
	}{
		{
			name: "batch",
			run: func(tool *BashTool, params BashParams) (*tools.ToolResult, error) {
				return tool.Run(context.Background(), params)
			},
		},
		{
			name: "streaming",
			run: func(tool *BashTool, params BashParams) (*tools.ToolResult, error) {
				return tool.RunStreaming(context.Background(), params, nil)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := tt.run(newBashForTest(), BashParams{
				Command: "printf 'stdout-marker\\n'; printf 'stderr-marker\\n' >&2; exit 3",
			})
			if err == nil {
				t.Fatal("expected non-zero command to return an error")
			}
			if got := sdkerr.GetCode(err); got != "bash.command_failed" {
				t.Fatalf("error code = %q, want bash.command_failed", got)
			}

			errText := err.Error()
			for _, want := range []string{"exited with code 3", "stdout-marker", "stderr-marker"} {
				if !strings.Contains(errText, want) {
					t.Errorf("error %q does not contain %q", errText, want)
				}
			}
			if strings.Contains(errText, "exited with code 1") {
				t.Errorf("error reports the wrong exit code: %q", errText)
			}

			if result == nil {
				t.Fatal("failure did not return a ToolResult")
			}
			for _, want := range []string{
				`exit_code="3"`,
				"<stdout><![CDATA[stdout-marker\n]]></stdout>",
				"<stderr><![CDATA[stderr-marker\n]]></stderr>",
			} {
				if !strings.Contains(result.Output, want) {
					t.Errorf("result %q does not contain %q", result.Output, want)
				}
			}
		})
	}
}

func TestBashFailureErrorIsBounded(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell commands are Unix-only")
	}

	result, err := newBashForTest().Run(context.Background(), BashParams{
		Command: "printf 'recognisable-marker\\n' >&2; i=0; while [ $i -lt 10000 ]; do printf 'diagnostic-line-%05d-xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx\\n' \"$i\" >&2; i=$((i+1)); done; exit 7",
	})
	if err == nil {
		t.Fatal("expected non-zero command to return an error")
	}
	if result == nil {
		t.Fatal("failure did not return a ToolResult")
	}
	if !strings.Contains(err.Error(), "recognisable-marker") {
		t.Fatalf("bounded error lost recognisable output marker: %q", err)
	}
	const maxErrorBytes = 100 << 10
	if got := len(err.Error()); got > maxErrorBytes {
		t.Fatalf("error length = %d bytes, want <= %d", got, maxErrorBytes)
	}
}

func TestBashSuccessResultShapeUnchanged(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell commands are Unix-only")
	}

	result, err := newBashForTest().Run(context.Background(), BashParams{
		Command: "printf 'success-marker\\n'",
	})
	if err != nil {
		t.Fatalf("successful command returned error: %v", err)
	}
	if result == nil {
		t.Fatal("successful command returned a nil ToolResult")
	}
	const wantStdout = "<stdout><![CDATA[success-marker\n]]></stdout>"
	if !strings.Contains(result.Output, wantStdout) {
		t.Fatalf("result %q does not contain exact stdout field %q", result.Output, wantStdout)
	}
}

func TestBashRejectsMissingCwdClearly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell commands are Unix-only")
	}

	missing := filepath.Join(t.TempDir(), "missing")
	result, err := newBashForTest().Run(context.Background(), BashParams{
		Command: "printf 'must-not-run\\n'",
		Cwd:     missing,
	})
	if err == nil {
		t.Fatal("expected missing cwd to return an error")
	}
	if result != nil {
		t.Fatalf("missing cwd returned unexpected result: %#v", result)
	}
	if !strings.Contains(err.Error(), "cwd does not exist") || !strings.Contains(err.Error(), missing) {
		t.Fatalf("error does not clearly name missing cwd %q: %v", missing, err)
	}
	if strings.Contains(strings.ToLower(err.Error()), "stdbuf") {
		t.Fatalf("missing cwd error incorrectly blames stdbuf: %v", err)
	}
}

func TestBashRejectsFileCwdClearly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell commands are Unix-only")
	}

	file := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(file, []byte("fixture"), 0o600); err != nil {
		t.Fatalf("create cwd file fixture: %v", err)
	}

	result, err := newBashForTest().Run(context.Background(), BashParams{
		Command: "printf 'must-not-run\\n'",
		Cwd:     file,
	})
	if err == nil {
		t.Fatal("expected file cwd to return an error")
	}
	if result != nil {
		t.Fatalf("file cwd returned unexpected result: %#v", result)
	}
	if !strings.Contains(err.Error(), "cwd is not a directory") || !strings.Contains(err.Error(), file) {
		t.Fatalf("error does not clearly identify file cwd %q: %v", file, err)
	}
	if strings.Contains(err.Error(), "cwd does not exist") {
		t.Fatalf("file cwd was misclassified as missing: %v", err)
	}
}
