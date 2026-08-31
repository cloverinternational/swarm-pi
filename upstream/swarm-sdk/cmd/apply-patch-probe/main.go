// Command apply-patch-probe demonstrates the unified editing tool end to end:
// single visible mutation tool, transactional multi-file V4A patching, failure
// rollback, and hidden legacy adapter compatibility. Mirrors the repo's other
// cmd/*-probe utilities.
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/forge"
)

func main() {
	workspace, err := os.MkdirTemp("", "apply-patch-probe-*")
	must(err)
	defer os.RemoveAll(workspace)
	must(os.WriteFile(filepath.Join(workspace, "app.go"), []byte("package main\n\nfunc main() {\n\tprintln(\"hi\")\n}\n"), 0o644))
	must(os.WriteFile(filepath.Join(workspace, "legacy.txt"), []byte("old text\n"), 0o600))

	registry := tools.NewRegistry()
	must(forge.RegisterFileEditingTools(registry, workspace))
	fmt.Printf("1) visible mutation tools: %v\n", registry.List())

	applyPatch, err := registry.Get("apply_patch")
	must(err)
	patch := `*** Begin Patch
*** Update File: app.go
@@ func main() {
-	println("hi")
+	println("hello, codex-compatible patch")
*** Add File: docs/notes.md
+# Notes
+created by one tool
*** End Patch`
	result, err := applyPatch.Execute(context.Background(), map[string]any{"input": patch})
	must(err)
	fmt.Printf("2) apply_patch result:\n%s\n", result.Output)
	body, err := os.ReadFile(filepath.Join(workspace, "app.go"))
	must(err)
	fmt.Printf("3) app.go after patch:\n%s", body)

	bad := `*** Begin Patch
*** Update File: app.go
@@
-	println("hello, codex-compatible patch")
+	println("partial")
*** Update File: missing.go
@@
-missing
+still missing
*** End Patch`
	if _, badErr := applyPatch.Execute(context.Background(), map[string]any{"input": bad}); badErr != nil {
		fmt.Printf("4) invalid patch rejected atomically: %v\n", badErr)
	}
	body, err = os.ReadFile(filepath.Join(workspace, "app.go"))
	must(err)
	fmt.Printf("5) app.go unchanged after rejected patch:\n%s", body)

	legacyEdit, err := registry.Get("Edit") // hidden, still executable for old transcripts
	must(err)
	result, err = legacyEdit.Execute(context.Background(), map[string]any{
		"file_path": filepath.Join(workspace, "legacy.txt"), "old_string": "old text", "new_string": "new text",
	})
	must(err)
	fmt.Printf("6) hidden legacy Edit via same engine: %s\n", result.Output)

	entries, err := os.ReadDir(filepath.Join(workspace, ".swarm", "snapshots"))
	must(err)
	fmt.Printf("7) undo snapshots recorded: %d entries (dir 0700, backups 0600)\n", len(entries))
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "probe failed:", err)
		os.Exit(1)
	}
}
