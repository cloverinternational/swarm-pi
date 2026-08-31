package forge

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestApplyPatchPreservesBlankLinesAroundAnchors(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "app.go")
	original := "package main\n\nfunc main() {\n\tprintln(\"hi\")\n}\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	patch := "*** Begin Patch\n*** Update File: app.go\n@@ func main() {\n-\tprintln(\"hi\")\n+\tprintln(\"hello\")\n*** End Patch"
	if _, err := NewApplyPatchTool(root).Execute(context.Background(), map[string]any{"input": patch}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "package main\n\nfunc main() {\n\tprintln(\"hello\")\n}\n"
	if string(got) != want {
		t.Fatalf("blank line lost:\ngot  %q\nwant %q", got, want)
	}
}
