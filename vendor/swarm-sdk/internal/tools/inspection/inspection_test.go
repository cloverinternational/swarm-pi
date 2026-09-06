package inspection

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sdkerr "github.com/Swarm-Code/mono/swarm-sdk/internal/sdkerr"
)

func TestToolListSearchAndReadAreBounded(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "pkg", "alpha.go"), "package pkg\n\nconst Alpha = \"needle\"\n")
	mustWrite(t, filepath.Join(root, "pkg", "beta.go"), "package pkg\n\nconst Beta = \"needle\"\n")

	tool, err := New(root)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	list := executeJSON(t, tool, map[string]any{
		"operation": "list",
		"path":      "pkg",
		"limit":     1,
	})
	if list["truncated"] != true {
		t.Fatalf("list truncated = %v, want true", list["truncated"])
	}
	if got := len(list["entries"].([]any)); got != 1 {
		t.Fatalf("list entries = %d, want 1", got)
	}

	search := executeJSON(t, tool, map[string]any{
		"operation": "search",
		"path":      "pkg",
		"query":     "needle",
		"limit":     1,
	})
	if search["truncated"] != true {
		t.Fatalf("search truncated = %v, want true", search["truncated"])
	}
	if got := len(search["matches"].([]any)); got != 1 {
		t.Fatalf("search matches = %d, want 1", got)
	}

	read := executeJSON(t, tool, map[string]any{
		"operation": "read",
		"path":      "pkg/alpha.go",
		"line":      2,
		"limit":     1,
	})
	if got := read["content"]; got != "\n" {
		t.Fatalf("read content = %#v, want one blank line", got)
	}
	if read["truncated"] != true {
		t.Fatalf("read truncated = %v, want true", read["truncated"])
	}
}

func TestToolRejectsWorkspaceEscapeIncludingSymlinks(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	mustWrite(t, filepath.Join(outside, "secret.txt"), "secret\n")

	tool, err := New(root)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	for name, path := range map[string]string{
		"parent traversal": "../" + filepath.Base(outside) + "/secret.txt",
		"absolute path":    filepath.Join(outside, "secret.txt"),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := tool.Execute(context.Background(), map[string]any{
				"operation": "read",
				"path":      path,
			})
			assertErrorCode(t, err, "repository_inspect.path_outside_workspace")
		})
	}

	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	_, err = tool.Execute(context.Background(), map[string]any{
		"operation": "read",
		"path":      "escape/secret.txt",
	})
	assertErrorCode(t, err, "repository_inspect.path_outside_workspace")
}

func TestToolBlocksSymlinkSwapAfterPathValidation(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	victim := filepath.Join(root, "victim.txt")
	mustWrite(t, victim, "safe\n")
	mustWrite(t, filepath.Join(outside, "secret.txt"), "secret\n")

	tool, err := New(root)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	tool.afterResolve = func(path string) {
		if err := os.Rename(victim, victim+".safe"); err != nil {
			t.Fatalf("rename validated file: %v", err)
		}
		if err := os.Symlink(filepath.Join(outside, "secret.txt"), victim); err != nil {
			t.Skipf("symlink unsupported: %v", err)
		}
	}

	result, err := tool.Execute(context.Background(), map[string]any{
		"operation": "read",
		"path":      "victim.txt",
	})
	if err == nil {
		t.Fatalf("read succeeded after symlink swap: %q", result.Output)
	}
	if result != nil {
		t.Fatalf("blocked read returned a result: %q", result.Output)
	}
}

func TestToolRejectsBinaryAndOversizedInputs(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "binary.dat"), "text\x00binary")

	tool, err := New(root)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = tool.Execute(context.Background(), map[string]any{
		"operation": "read",
		"path":      "binary.dat",
	})
	assertErrorCode(t, err, "repository_inspect.not_text")

	_, err = tool.Execute(context.Background(), map[string]any{
		"operation": "list",
		"limit":     1001,
	})
	assertErrorCode(t, err, "repository_inspect.invalid_limit")
}

func TestToolValidationAndRegexSearch(t *testing.T) {
	if _, err := New(""); err == nil {
		t.Fatal("New(\"\") succeeded, want missing workspace error")
	}
	if _, err := New(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("New(missing) succeeded, want invalid workspace error")
	}

	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "source.go"), "package main\nvar Alpha42 = true\n")
	tool, err := New(root)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if tool.Name() != "repository_inspect" || !tool.IsIdempotent() || !tool.IsSafeRepositoryInspector() {
		t.Fatal("tool capability metadata is inconsistent")
	}
	if tool.Description() == "" || tool.Parameters() == nil {
		t.Fatal("tool description/schema must be populated")
	}

	result := executeJSON(t, tool, map[string]any{
		"operation": "search",
		"path":      "source.go",
		"query":     `Alpha\d+`,
		"regex":     true,
	})
	if got := len(result["matches"].([]any)); got != 1 {
		t.Fatalf("regex matches = %d, want 1", got)
	}

	for name, params := range map[string]map[string]any{
		"missing query": {"operation": "search"},
		"invalid regex": {"operation": "search", "query": "[", "regex": true},
		"invalid line":  {"operation": "read", "path": "source.go", "line": 0},
		"invalid op":    {"operation": "write", "path": "source.go"},
		"missing path":  {"operation": "read", "path": "missing.go"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := tool.Execute(context.Background(), params); err == nil {
				t.Fatalf("Execute(%v) succeeded, want error", params)
			}
		})
	}
}

func TestToolSearchHasAggregateScanAndResponseBudgets(t *testing.T) {
	root := t.TempDir()
	largeLine := strings.Repeat("x", 256<<10) + " needle\n"
	for index := 0; index < 100; index++ {
		mustWrite(t, filepath.Join(root, "many", fmt.Sprintf("%03d.txt", index)), largeLine)
	}
	tool, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	result, err := tool.Execute(context.Background(), map[string]any{
		"operation": "search",
		"path":      "many",
		"query":     "needle",
		"limit":     maxMatchLimit,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Output) > maxSearchResponseBytes {
		t.Fatalf("response bytes = %d, maximum %d", len(result.Output), maxSearchResponseBytes)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(result.Output), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["truncated"] != true {
		t.Fatalf("truncated = %v, want true", decoded["truncated"])
	}
	if scanned := int(decoded["scanned_files"].(float64)); scanned > maxSearchFiles {
		t.Fatalf("scanned_files = %d, maximum %d", scanned, maxSearchFiles)
	}
	if scanned := int64(decoded["scanned_bytes"].(float64)); scanned > maxSearchScanBytes {
		t.Fatalf("scanned_bytes = %d, maximum %d", scanned, maxSearchScanBytes)
	}
	for _, raw := range decoded["matches"].([]any) {
		match := raw.(map[string]any)
		if text := match["text"].(string); len(text) > maxMatchTextBytes {
			t.Fatalf("match text bytes = %d, maximum %d", len(text), maxMatchTextBytes)
		}
	}
}

func executeJSON(t *testing.T, tool *Tool, params map[string]any) map[string]any {
	t.Helper()
	result, err := tool.Execute(context.Background(), params)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(result.Output), &decoded); err != nil {
		t.Fatalf("unmarshal output %q: %v", result.Output, err)
	}
	return decoded
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func assertErrorCode(t *testing.T, err error, code string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error code %q", code)
	}
	var sdkError *sdkerr.Error
	if errors.As(err, &sdkError) {
		if sdkError.Code != code {
			t.Fatalf("error code = %q, want %q", sdkError.Code, code)
		}
		return
	}
	if !strings.Contains(err.Error(), code) {
		t.Fatalf("error %q does not contain machine-readable code %q", err, code)
	}
}
