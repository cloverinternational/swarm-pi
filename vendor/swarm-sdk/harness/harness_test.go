package harness

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// compileErr is a small helper returning the Diagnostics from a failed compile.
func compileErr(t *testing.T, raw string, sourcePath string) Diagnostics {
	t.Helper()
	_, err := CompileBytes([]byte(raw), sourcePath)
	if err == nil {
		t.Fatalf("expected compile to fail, got success")
	}
	ds, ok := AsDiagnostics(err)
	if !ok {
		t.Fatalf("expected Diagnostics, got %T: %v", err, err)
	}
	return ds
}

// hasCode reports whether any diagnostic carries the given code.
func hasCode(ds Diagnostics, code string) bool {
	for _, d := range ds {
		if d.Code == code {
			return true
		}
	}
	return false
}

const syntheticYAML = "/synthetic/dir/harness.yaml"

func TestValidMinimalYAML(t *testing.T) {
	path := filepath.Join("testdata", "valid_minimal.yaml")
	p, err := Compile(path)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}
	if p.Name() != "minimal" {
		t.Errorf("name = %q, want minimal", p.Name())
	}
	if p.APIVersion() != APIVersionV1Alpha1 {
		t.Errorf("apiVersion = %q", p.APIVersion())
	}
	if p.ProviderID() != "anthropic" || p.Model() != "claude-x" {
		t.Errorf("provider = %q/%q", p.ProviderID(), p.Model())
	}
	if got := p.Tools(); len(got) != 2 || got[0] != "forge.read" || got[1] != "forge.apply_patch" {
		t.Errorf("tools = %v", got)
	}
	if p.SystemPromptSource() != "inline" {
		t.Errorf("prompt source = %q", p.SystemPromptSource())
	}
	if !strings.HasPrefix(p.SystemPromptHash(), "sha256:") {
		t.Errorf("prompt hash = %q", p.SystemPromptHash())
	}
	if p.Digest() == "" {
		t.Errorf("digest is empty")
	}
	// Workspace defaults to the manifest directory.
	absTestdata, _ := filepath.Abs("testdata")
	if p.Workspace() != absTestdata {
		t.Errorf("workspace = %q, want %q", p.Workspace(), absTestdata)
	}
}

func TestJSONEquivalence(t *testing.T) {
	py, err := Compile(filepath.Join("testdata", "valid_minimal.yaml"))
	if err != nil {
		t.Fatalf("yaml compile: %v", err)
	}
	pj, err := Compile(filepath.Join("testdata", "valid_minimal.json"))
	if err != nil {
		t.Fatalf("json compile: %v", err)
	}
	if py.Digest() != pj.Digest() {
		t.Errorf("digest mismatch: yaml=%s json=%s", py.Digest(), pj.Digest())
	}
}

func TestUnknownFieldAtEachLevel(t *testing.T) {
	base := `{
  "apiVersion": "swarm.ai/v1alpha1", "kind": "Harness",
  "metadata": {"name": "m"},
  "provider": {"id": "p", "model": "x"},
  "agent": {"systemPrompt": {"inline": "hi"}, "tools": []}
}`
	_ = base
	cases := map[string]string{
		"top":          `{"apiVersion":"swarm.ai/v1alpha1","kind":"Harness","bogus":1,"metadata":{"name":"m"},"provider":{"id":"p","model":"x"},"agent":{"systemPrompt":{"inline":"hi"},"tools":[]}}`,
		"metadata":     `{"apiVersion":"swarm.ai/v1alpha1","kind":"Harness","metadata":{"name":"m","bogus":1},"provider":{"id":"p","model":"x"},"agent":{"systemPrompt":{"inline":"hi"},"tools":[]}}`,
		"provider":     `{"apiVersion":"swarm.ai/v1alpha1","kind":"Harness","metadata":{"name":"m"},"provider":{"id":"p","model":"x","bogus":1},"agent":{"systemPrompt":{"inline":"hi"},"tools":[]}}`,
		"agent":        `{"apiVersion":"swarm.ai/v1alpha1","kind":"Harness","metadata":{"name":"m"},"provider":{"id":"p","model":"x"},"agent":{"systemPrompt":{"inline":"hi"},"tools":[],"bogus":1}}`,
		"systemPrompt": `{"apiVersion":"swarm.ai/v1alpha1","kind":"Harness","metadata":{"name":"m"},"provider":{"id":"p","model":"x"},"agent":{"systemPrompt":{"inline":"hi","bogus":1},"tools":[]}}`,
	}
	for name, doc := range cases {
		t.Run(name, func(t *testing.T) {
			ds := compileErr(t, doc, "/x/harness.json")
			if !hasCode(ds, "harness.decode.unknownField") {
				t.Errorf("expected unknownField diagnostic, got: %v", ds)
			}
		})
	}
}

func TestWrongAndFutureVersion(t *testing.T) {
	mk := func(apiVersion, kind string) string {
		return `{"apiVersion":"` + apiVersion + `","kind":"` + kind + `","metadata":{"name":"m"},"provider":{"id":"p","model":"x"},"agent":{"systemPrompt":{"inline":"hi"},"tools":[]}}`
	}
	t.Run("unknownGroup", func(t *testing.T) {
		ds := compileErr(t, mk("other.io/v1alpha1", "Harness"), "/x/harness.json")
		if !hasCode(ds, "harness.version.unknownGroup") {
			t.Errorf("got %v", ds)
		}
	})
	t.Run("futureMajor", func(t *testing.T) {
		ds := compileErr(t, mk("swarm.ai/v2", "Harness"), "/x/harness.json")
		if !hasCode(ds, "harness.version.futureMajor") {
			t.Errorf("got %v", ds)
		}
	})
	t.Run("wrongKind", func(t *testing.T) {
		ds := compileErr(t, mk("swarm.ai/v1alpha1", "Widget"), "/x/harness.json")
		if !hasCode(ds, "harness.kind.unknown") {
			t.Errorf("got %v", ds)
		}
	})
}

func TestDuplicateToolIDs(t *testing.T) {
	doc := `{"apiVersion":"swarm.ai/v1alpha1","kind":"Harness","metadata":{"name":"m"},"provider":{"id":"p","model":"x"},"agent":{"systemPrompt":{"inline":"hi"},"tools":["forge.read","forge.read"]}}`
	ds := compileErr(t, doc, "/x/harness.json")
	if !hasCode(ds, "harness.agent.tools.duplicate") {
		t.Errorf("expected duplicate diagnostic, got %v", ds)
	}
}

func TestMissingToolsField(t *testing.T) {
	doc := `{"apiVersion":"swarm.ai/v1alpha1","kind":"Harness","metadata":{"name":"m"},"provider":{"id":"p","model":"x"},"agent":{"systemPrompt":{"inline":"hi"}}}`
	ds := compileErr(t, doc, "/x/harness.json")
	if !hasCode(ds, "harness.agent.tools.required") {
		t.Errorf("expected required diagnostic, got %v", ds)
	}
}

func TestEmptyToolsValid(t *testing.T) {
	p, err := Compile(filepath.Join("testdata", "empty_tools.yaml"))
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}
	if len(p.Tools()) != 0 {
		t.Errorf("expected zero tools, got %v", p.Tools())
	}
}

func TestUnknownToolID(t *testing.T) {
	doc := `{"apiVersion":"swarm.ai/v1alpha1","kind":"Harness","metadata":{"name":"m"},"provider":{"id":"p","model":"x"},"agent":{"systemPrompt":{"inline":"hi"},"tools":["forge.read","made.up"]}}`
	ds := compileErr(t, doc, "/x/harness.json")
	if !hasCode(ds, "harness.agent.tools.unknown") {
		t.Errorf("expected unknown-tool diagnostic, got %v", ds)
	}
}

func TestPromptExclusivity(t *testing.T) {
	both := `{"apiVersion":"swarm.ai/v1alpha1","kind":"Harness","metadata":{"name":"m"},"provider":{"id":"p","model":"x"},"agent":{"systemPrompt":{"inline":"hi","file":"p.md"},"tools":[]}}`
	ds := compileErr(t, both, "/x/harness.json")
	if !hasCode(ds, "harness.agent.systemPrompt.exclusive") {
		t.Errorf("expected exclusive diagnostic, got %v", ds)
	}
	neither := `{"apiVersion":"swarm.ai/v1alpha1","kind":"Harness","metadata":{"name":"m"},"provider":{"id":"p","model":"x"},"agent":{"systemPrompt":{},"tools":[]}}`
	ds = compileErr(t, neither, "/x/harness.json")
	if !hasCode(ds, "harness.agent.systemPrompt.empty") {
		t.Errorf("expected empty prompt diagnostic, got %v", ds)
	}
	missing := `{"apiVersion":"swarm.ai/v1alpha1","kind":"Harness","metadata":{"name":"m"},"provider":{"id":"p","model":"x"},"agent":{"tools":[]}}`
	ds = compileErr(t, missing, "/x/harness.json")
	if !hasCode(ds, "harness.agent.systemPrompt.missing") {
		t.Errorf("expected missing prompt diagnostic, got %v", ds)
	}
}

func TestMissingPromptFile(t *testing.T) {
	dir := t.TempDir()
	manifest := filepath.Join(dir, "harness.yaml")
	writeFileT(t, manifest, `apiVersion: swarm.ai/v1alpha1
kind: Harness
metadata:
  name: m
provider:
  id: p
  model: x
agent:
  systemPrompt:
    file: does-not-exist.md
  tools: []
`)
	_, err := Compile(manifest)
	ds, ok := AsDiagnostics(err)
	if !ok || !hasCode(ds, "harness.path.missing") {
		t.Errorf("expected missing-file diagnostic, got %v", err)
	}
}

func TestManifestRelativeResolutionUnderChangedCWD(t *testing.T) {
	dirA := t.TempDir()
	manifest := filepath.Join(dirA, "harness.yaml")
	writeFileT(t, filepath.Join(dirA, "sys.md"), "PROMPT-FROM-A")
	writeFileT(t, manifest, `apiVersion: swarm.ai/v1alpha1
kind: Harness
metadata:
  name: m
provider:
  id: p
  model: x
agent:
  systemPrompt:
    file: sys.md
  tools: []
`)
	// Change CWD elsewhere; resolution must still bind to dirA.
	dirB := t.TempDir()
	t.Chdir(dirB)

	p, err := Compile(manifest)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}
	if p.RevealSystemPrompt() != "PROMPT-FROM-A" {
		t.Errorf("prompt = %q, want PROMPT-FROM-A", p.RevealSystemPrompt())
	}
	realA, _ := filepath.EvalSymlinks(dirA)
	realWS, _ := filepath.EvalSymlinks(p.Workspace())
	if realWS != realA {
		t.Errorf("workspace = %q, want %q", realWS, realA)
	}
}

func TestSymlinkAndTraversalPolicy(t *testing.T) {
	t.Run("traversal", func(t *testing.T) {
		dir := t.TempDir()
		manifest := filepath.Join(dir, "harness.yaml")
		writeFileT(t, manifest, `apiVersion: swarm.ai/v1alpha1
kind: Harness
metadata:
  name: m
provider:
  id: p
  model: x
agent:
  systemPrompt:
    file: ../escape.md
  tools: []
`)
		_, err := Compile(manifest)
		ds, ok := AsDiagnostics(err)
		if !ok || !hasCode(ds, "harness.path.traversal") {
			t.Errorf("expected traversal diagnostic, got %v", err)
		}
	})

	t.Run("symlinkEscape", func(t *testing.T) {
		dir := t.TempDir()
		outside := t.TempDir() // sibling, NOT under dir
		secret := filepath.Join(outside, "secret.md")
		writeFileT(t, secret, "OUTSIDE-SECRET")
		link := filepath.Join(dir, "link.md")
		if err := os.Symlink(secret, link); err != nil {
			t.Skipf("symlink unsupported: %v", err)
		}
		manifest := filepath.Join(dir, "harness.yaml")
		writeFileT(t, manifest, `apiVersion: swarm.ai/v1alpha1
kind: Harness
metadata:
  name: m
provider:
  id: p
  model: x
agent:
  systemPrompt:
    file: link.md
  tools: []
`)
		_, err := Compile(manifest)
		ds, ok := AsDiagnostics(err)
		if !ok || !hasCode(ds, "harness.path.symlinkEscape") {
			t.Errorf("expected symlinkEscape diagnostic, got %v", err)
		}
	})
}

func TestMissingEnvVar(t *testing.T) {
	os.Unsetenv("HARNESS_TEST_ABSENT_VAR")
	doc := `{"apiVersion":"swarm.ai/v1alpha1","kind":"Harness","metadata":{"name":"m"},"provider":{"id":"p","model":"x","credential":{"env":"HARNESS_TEST_ABSENT_VAR"}},"agent":{"systemPrompt":{"inline":"hi"},"tools":[]}}`
	ds := compileErr(t, doc, "/x/harness.json")
	if !hasCode(ds, "harness.ref.envMissing") {
		t.Errorf("expected envMissing diagnostic, got %v", ds)
	}
}

func TestSecretRedactionAcrossOutputs(t *testing.T) {
	const secretVal = "sk-SUPER-SECRET-VALUE-9x"
	t.Setenv("HARNESS_TEST_SECRET", secretVal)
	doc := `{"apiVersion":"swarm.ai/v1alpha1","kind":"Harness","metadata":{"name":"m"},"provider":{"id":"p","model":"x","credential":{"env":"HARNESS_TEST_SECRET"}},"agent":{"systemPrompt":{"inline":"a prompt that is sensitive"},"tools":[]}}`
	p, err := CompileBytes([]byte(doc), "/x/harness.json")
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

	// The value is retrievable only via the explicit reveal accessor.
	if v, ok := p.Credential().Reveal(); !ok || v != secretVal {
		t.Fatalf("reveal failed: %q ok=%v", v, ok)
	}

	// It must not appear in any redacted output surface.
	explainJSON, _ := p.ExplainJSON()
	credJSON, _ := json.Marshal(p.Credential())
	provJSON, _ := json.Marshal(p.Provenance())
	surfaces := map[string]string{
		"explain":    string(explainJSON),
		"digest":     p.Digest(),
		"credential": string(credJSON),
		"provenance": string(provJSON),
		"credString": p.Credential().String(),
	}
	for name, s := range surfaces {
		if strings.Contains(s, secretVal) {
			t.Errorf("secret leaked in %s: %s", name, s)
		}
	}
	if string(credJSON) != `"`+redactedPlaceholder+`"` {
		t.Errorf("credential JSON = %s, want redacted", credJSON)
	}
	// Provenance records the source label, not the value.
	if !strings.Contains(string(provJSON), "env:HARNESS_TEST_SECRET") {
		t.Errorf("provenance missing credential source label: %s", provJSON)
	}
	// The raw prompt text must not appear in explain output either.
	if strings.Contains(string(explainJSON), "a prompt that is sensitive") {
		t.Errorf("raw prompt leaked into explain: %s", explainJSON)
	}
}

func TestDeterministicDigest(t *testing.T) {
	path := filepath.Join("testdata", "valid_minimal.yaml")
	p1, err := Compile(path)
	if err != nil {
		t.Fatal(err)
	}
	p2, err := Compile(path)
	if err != nil {
		t.Fatal(err)
	}
	if p1.Digest() != p2.Digest() {
		t.Errorf("nondeterministic digest: %s vs %s", p1.Digest(), p2.Digest())
	}
	// Immutability: mutating a returned copy does not affect the plan.
	tools := p1.Tools()
	if len(tools) > 0 {
		tools[0] = "MUTATED"
	}
	if p1.Tools()[0] == "MUTATED" {
		t.Errorf("Tools() returned a mutable reference")
	}
}

func TestTrailingData(t *testing.T) {
	t.Run("jsonTrailing", func(t *testing.T) {
		doc := `{"apiVersion":"swarm.ai/v1alpha1","kind":"Harness","metadata":{"name":"m"},"provider":{"id":"p","model":"x"},"agent":{"systemPrompt":{"inline":"hi"},"tools":[]}}{"extra":true}`
		ds := compileErr(t, doc, "/x/harness.json")
		if !hasCode(ds, "harness.decode.trailingData") {
			t.Errorf("expected trailingData diagnostic, got %v", ds)
		}
	})
	t.Run("yamlMultiDoc", func(t *testing.T) {
		doc := `apiVersion: swarm.ai/v1alpha1
kind: Harness
metadata:
  name: m
provider:
  id: p
  model: x
agent:
  systemPrompt:
    inline: hi
  tools: []
---
apiVersion: swarm.ai/v1alpha1
kind: Harness
`
		ds := compileErr(t, doc, "/x/harness.yaml")
		if !hasCode(ds, "harness.decode.multipleDocuments") {
			t.Errorf("expected multipleDocuments diagnostic, got %v", ds)
		}
	})
}

func TestNoDefaultDiscoveryParentWalk(t *testing.T) {
	// With no explicit path and no ./harness.yaml in an empty CWD, discovery
	// must fail rather than walk upward to a parent's harness.yaml.
	dir := t.TempDir()
	t.Chdir(dir)
	_, _, err := Load("")
	ds, ok := AsDiagnostics(err)
	if !ok || !hasCode(ds, "harness.load.notFound") {
		t.Errorf("expected notFound diagnostic, got %v", err)
	}
}

// writeFileT writes a file for a test, failing on error.
func writeFileT(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
