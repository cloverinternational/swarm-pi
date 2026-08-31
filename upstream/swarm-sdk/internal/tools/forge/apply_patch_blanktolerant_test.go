package forge

import (
	"strings"
	"testing"
)

// applyPatchText is a small helper: parse+apply one Update File patch against
// `original` and return the resulting file text.
func applyPatchText(t *testing.T, original, patch string) (string, error) {
	t.Helper()
	ops, err := parsePatch(patch)
	if err != nil {
		return "", err
	}
	if len(ops) != 1 {
		t.Fatalf("expected exactly 1 op, got %d", len(ops))
	}
	out, _, err := applyHunks(original, ops[0].hunks, ops[0].path)
	return out, err
}

// TestApplyPatchToleratesContextMissingBlankLines is the regression for the
// dominant apply_patch failure mode (#264, #297): a context block that is
// correct except that its blank lines are absent, which is what an agent
// produces when the file content it was shown had empty lines removed.
func TestApplyPatchToleratesContextMissingBlankLines(t *testing.T) {
	original := "package main\n\nimport \"fmt\"\n\nfunc main() {\n\tfmt.Println(\"a\")\n}\n"

	// Context omits both blank lines; everything else is verbatim.
	patch := `*** Begin Patch
*** Update File: main.go
@@
 package main
 import "fmt"
 func main() {
-	fmt.Println("a")
+	fmt.Println("b")
 }
*** End Patch`

	got, err := applyPatchText(t, original, patch)
	if err != nil {
		t.Fatalf("patch with blank-free context must apply, got error: %v", err)
	}

	want := "package main\n\nimport \"fmt\"\n\nfunc main() {\n\tfmt.Println(\"b\")\n}\n"
	if got != want {
		t.Fatalf("blank lines were not preserved.\n got: %q\nwant: %q", got, want)
	}
}

// TestBlankRepairPreservesEveryBlankLine guards the property that matters most:
// the repair must never drop, add, or move a blank line in the output.
func TestBlankRepairPreservesEveryBlankLine(t *testing.T) {
	original := "a\n\n\nb\n\nc\n\n\n\nd\n"
	patch := `*** Begin Patch
*** Update File: f.txt
@@
 a
 b
 c
-d
+D
*** End Patch`

	got, err := applyPatchText(t, original, patch)
	if err != nil {
		t.Fatalf("apply failed: %v", err)
	}
	want := "a\n\n\nb\n\nc\n\n\n\nD\n"
	if got != want {
		t.Fatalf("blank runs not preserved exactly.\n got: %q\nwant: %q", got, want)
	}
}

// TestBlankRepairRefusesAmbiguousMatch is the critical near-miss: the relaxed
// alignment must NOT silently pick one of several candidate sites. Editing the
// wrong copy of repeated code is far worse than refusing the patch.
func TestBlankRepairRefusesAmbiguousMatch(t *testing.T) {
	// The ENTIRE context (x, y, and the deleted "middle") appears twice, each
	// time split by a blank line the patch omits. Both sites align equally
	// well, so there is no basis for choosing one.
	original := "start\nx\n\ny\nmiddle\nend\nx\n\ny\nmiddle\ntail\n"
	patch := `*** Begin Patch
*** Update File: f.txt
@@
 x
 y
-middle
+MIDDLE
*** End Patch`

	_, err := applyPatchText(t, original, patch)
	if err == nil {
		t.Fatal("an ambiguous blank-tolerant alignment must be refused, not applied")
	}
}

// TestBlankRepairDoesNotFireWhenExactMatchExists proves the repair is a last
// resort: a patch that already matches exactly must take the normal path and
// be unaffected by this change.
func TestBlankRepairDoesNotFireWhenExactMatchExists(t *testing.T) {
	original := "a\n\nb\n"
	patch := `*** Begin Patch
*** Update File: f.txt
@@
 a

-b
+B
*** End Patch`

	got, err := applyPatchText(t, original, patch)
	if err != nil {
		t.Fatalf("exact-context patch must still apply: %v", err)
	}
	if got != "a\n\nB\n" {
		t.Fatalf("got %q", got)
	}
}

// TestBlankRepairRejectsGenuinelyWrongContext is the other near-miss: context
// that does not exist must still fail. Blank tolerance must not degrade into
// "match anything".
func TestBlankRepairRejectsGenuinelyWrongContext(t *testing.T) {
	original := "a\n\nb\n\nc\n"
	patch := `*** Begin Patch
*** Update File: f.txt
@@
 a
 NOPE
-c
+C
*** End Patch`

	if _, err := applyPatchText(t, original, patch); err == nil {
		t.Fatal("context that does not exist must be refused")
	} else if !strings.Contains(err.Error(), "context not found") {
		t.Fatalf("expected a context-not-found diagnosis, got: %v", err)
	}
}

// TestAnchorDisambiguatesRepeatedContext is the regression for #260 and #291:
// an explicit @@ anchor must resolve a line that repeats elsewhere in the file,
// instead of being ignored and reported as ambiguous.
func TestAnchorDisambiguatesRepeatedContext(t *testing.T) {
	original := strings.Join([]string{
		"func Alpha() {",
		"\tRequestContext(ctx)",
		"\treturn",
		"}",
		"",
		"func Beta() {",
		"\tRequestContext(ctx)",
		"\treturn",
		"}",
		"",
	}, "\n")

	// Target the occurrence inside Beta specifically.
	patch := `*** Begin Patch
*** Update File: client_test.go
@@ func Beta() {
 	RequestContext(ctx)
-	return
+	return nil
 }
*** End Patch`

	got, err := applyPatchText(t, original, patch)
	if err != nil {
		t.Fatalf("anchored patch must apply despite the repeated line: %v", err)
	}
	if !strings.Contains(got, "func Beta() {\n\tRequestContext(ctx)\n\treturn nil") {
		t.Fatalf("anchor did not target Beta:\n%s", got)
	}
	if !strings.Contains(got, "func Alpha() {\n\tRequestContext(ctx)\n\treturn\n}") {
		t.Fatalf("anchor wrongly modified Alpha:\n%s", got)
	}
}

// TestUnanchoredRepeatedContextStillAmbiguous is the near-miss for the anchor
// change: WITHOUT an anchor, a genuinely ambiguous context must still fail.
func TestUnanchoredRepeatedContextStillAmbiguous(t *testing.T) {
	original := "f()\ng()\nh()\nf()\ng()\nh()\n"
	patch := `*** Begin Patch
*** Update File: f.go
@@
 f()
-g()
+G()
 h()
*** End Patch`

	_, err := applyPatchText(t, original, patch)
	if err == nil {
		t.Fatal("unanchored ambiguous context must still be refused")
	}
	if !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("expected an ambiguity diagnosis, got: %v", err)
	}
}
