package harness

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// baseHookManifest returns a minimal valid manifest with the given raw hooks
// block spliced in verbatim (the block already includes the `hooks:` key).
func baseHookManifest(hooksBlock string) string {
	return "apiVersion: swarm.ai/v1alpha1\n" +
		"kind: Harness\n" +
		"metadata:\n  name: hooktest\n" +
		"provider:\n  id: anthropic\n  model: claude-x\n" +
		"agent:\n  systemPrompt:\n    inline: \"hi\"\n  tools: []\n" +
		hooksBlock
}

// writeHookScriptFixture writes dir/hooks/<name> with the given body and
// returns the manifest-relative path to it.
func writeHookScriptFixture(t *testing.T, dir, name, body string) string {
	t.Helper()
	hd := filepath.Join(dir, "hooks")
	if err := os.MkdirAll(hd, 0o755); err != nil {
		t.Fatalf("mkdir hooks dir: %v", err)
	}
	p := filepath.Join(hd, name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("write hook script %q: %v", name, err)
	}
	return filepath.ToSlash(filepath.Join("hooks", name))
}

// TestHooksOmittedAndEmptyValid: omitted hooks and empty `hooks: []` are both
// valid, yield no hooks, and preserve the same digest as each other (and, by
// construction, as a pre-Phase-6a compile of the same manifest since neither
// block contributes anything to the redacted surface).
func TestHooksOmittedAndEmptyValid(t *testing.T) {
	omitted := baseHookManifest("")
	p, err := CompileBytes([]byte(omitted), syntheticYAML)
	if err != nil {
		t.Fatalf("omitted hooks should compile: %v", err)
	}
	if len(p.Hooks()) != 0 {
		t.Errorf("omitted: Hooks() = %v, want empty", p.Hooks())
	}
	digestNoHooks := p.Digest()

	empty := baseHookManifest("hooks: []\n")
	p2, err := CompileBytes([]byte(empty), syntheticYAML)
	if err != nil {
		t.Fatalf("empty hooks should compile: %v", err)
	}
	if len(p2.Hooks()) != 0 {
		t.Errorf("empty: Hooks() = %v, want empty", p2.Hooks())
	}
	if p2.Digest() != digestNoHooks {
		t.Errorf("empty vs omitted digests differ: %q vs %q", p2.Digest(), digestNoHooks)
	}
}

// TestHooksMissingIDRejected: an entry without an id is rejected.
func TestHooksMissingIDRejected(t *testing.T) {
	manifest := baseHookManifest(
		"hooks:\n  - event: tool.before_execute\n    scope: global\n    type: command\n    command: echo hi\n")
	ds := compileErr(t, manifest, syntheticYAML)
	if !hasCode(ds, "harness.hooks.id.missing") {
		t.Fatalf("expected missing-id diagnostic, got: %v", ds)
	}
}

// TestHooksDuplicateID: duplicate ids across the list are rejected.
func TestHooksDuplicateID(t *testing.T) {
	manifest := baseHookManifest(
		"hooks:\n" +
			"  - id: dup\n    event: e\n    scope: global\n    type: command\n    command: echo hi\n" +
			"  - id: dup\n    event: e\n    scope: global\n    type: command\n    command: echo bye\n")
	ds := compileErr(t, manifest, syntheticYAML)
	if !hasCode(ds, "harness.hooks.id.duplicate") {
		t.Fatalf("expected duplicate id diagnostic, got: %v", ds)
	}
}

// TestHooksUnknownFieldRejected: an unknown key under a hooks entry is rejected.
func TestHooksUnknownFieldRejected(t *testing.T) {
	manifest := baseHookManifest(
		"hooks:\n  - id: x\n    event: e\n    scope: global\n    type: command\n    command: echo hi\n    bogus: y\n")
	ds := compileErr(t, manifest, syntheticYAML)
	if !hasCode(ds, "harness.decode.unknownField") {
		t.Fatalf("expected unknown-field rejection, got: %v", ds)
	}
}

// TestHooksScopeEnumValidated: scope must be one of the four enum values.
func TestHooksScopeEnumValidated(t *testing.T) {
	manifest := baseHookManifest(
		"hooks:\n  - id: x\n    event: e\n    scope: bogus\n    type: command\n    command: echo hi\n")
	ds := compileErr(t, manifest, syntheticYAML)
	if !hasCode(ds, "harness.hooks.scope.invalid") {
		t.Fatalf("expected scope.invalid diagnostic, got: %v", ds)
	}
}

// TestHooksMatcherRequiredForNonGlobalScope: a non-global scope without a
// matcher is rejected.
func TestHooksMatcherRequiredForNonGlobalScope(t *testing.T) {
	manifest := baseHookManifest(
		"hooks:\n  - id: x\n    event: e\n    scope: tool\n    type: command\n    command: echo hi\n")
	ds := compileErr(t, manifest, syntheticYAML)
	if !hasCode(ds, "harness.hooks.matcher.missing") {
		t.Fatalf("expected matcher.missing diagnostic, got: %v", ds)
	}
}

// TestHooksMatcherForbiddenForGlobalScope: a global scope with a matcher set
// is explicitly rejected (not silently dropped).
func TestHooksMatcherForbiddenForGlobalScope(t *testing.T) {
	manifest := baseHookManifest(
		"hooks:\n  - id: x\n    event: e\n    scope: global\n    matcher: Bash\n    type: command\n    command: echo hi\n")
	ds := compileErr(t, manifest, syntheticYAML)
	if !hasCode(ds, "harness.hooks.matcher.forbidden") {
		t.Fatalf("expected matcher.forbidden diagnostic, got: %v", ds)
	}
}

// TestHooksSourceOneOfNoneSet: none of command/path/url set is rejected.
func TestHooksSourceOneOfNoneSet(t *testing.T) {
	manifest := baseHookManifest(
		"hooks:\n  - id: x\n    event: e\n    scope: global\n    type: command\n")
	ds := compileErr(t, manifest, syntheticYAML)
	if !hasCode(ds, "harness.hooks.source.oneOf") {
		t.Fatalf("expected source.oneOf diagnostic, got: %v", ds)
	}
}

// TestHooksSourceOneOfTwoSet: two of command/path/url set is rejected.
func TestHooksSourceOneOfTwoSet(t *testing.T) {
	manifest := baseHookManifest(
		"hooks:\n  - id: x\n    event: e\n    scope: global\n    type: command\n    command: echo hi\n    url: https://example.com/hook\n")
	ds := compileErr(t, manifest, syntheticYAML)
	if !hasCode(ds, "harness.hooks.source.oneOf") {
		t.Fatalf("expected source.oneOf diagnostic, got: %v", ds)
	}
}

// TestHooksTypeMismatch: type declared as script but command set (mismatch).
func TestHooksTypeMismatch(t *testing.T) {
	manifest := baseHookManifest(
		"hooks:\n  - id: x\n    event: e\n    scope: global\n    type: script\n    command: echo hi\n")
	ds := compileErr(t, manifest, syntheticYAML)
	if !hasCode(ds, "harness.hooks.source.typeMismatch") {
		t.Fatalf("expected source.typeMismatch diagnostic, got: %v", ds)
	}
}

// TestHooksScriptPathResolves: a type: script entry resolves manifest-relative,
// is hashed, and the hash (not content) is carried on the plan + shown in Explain.
func TestHooksScriptPathResolves(t *testing.T) {
	dir := t.TempDir()
	rel := writeHookScriptFixture(t, dir, "pre-commit.sh", "SCRIPT_BODY_SENTINEL\n")
	manifest := baseHookManifest(
		"hooks:\n  - id: pre-commit-guard\n    event: tool.before_execute\n    scope: tool\n    matcher: Bash\n    type: script\n    path: " + rel + "\n")
	path := writeManifest(t, dir, manifest)

	p, err := Compile(path)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}
	hk := p.Hooks()
	if len(hk) != 1 {
		t.Fatalf("Hooks() len = %d, want 1 (%+v)", len(hk), hk)
	}
	h := hk[0]
	if !strings.HasPrefix(h.ContentHash, "sha256:") {
		t.Errorf("script hook contentHash = %q, want sha256: prefix", h.ContentHash)
	}
	if filepath.IsAbs(h.Path) {
		t.Errorf("script hook path %q is absolute; want manifest-relative", h.Path)
	}
	if h.TimeoutSeconds != defaultHookTimeoutSeconds {
		t.Errorf("script hook timeoutSeconds = %d, want default %d", h.TimeoutSeconds, defaultHookTimeoutSeconds)
	}
	if !h.Enabled {
		t.Errorf("script hook enabled = false, want default true")
	}

	js, err := p.ExplainJSON()
	if err != nil {
		t.Fatalf("ExplainJSON: %v", err)
	}
	if strings.Contains(string(js), "SCRIPT_BODY_SENTINEL") {
		t.Errorf("Explain JSON leaked hook script body:\n%s", js)
	}
	if !strings.Contains(string(js), h.ContentHash) {
		t.Errorf("Explain JSON missing hook content hash %q", h.ContentHash)
	}
}

// TestHooksScriptPathMissingFailsClosed: a script path that does not exist
// fails compilation with a source-cited diagnostic.
func TestHooksScriptPathMissingFailsClosed(t *testing.T) {
	dir := t.TempDir()
	manifest := baseHookManifest(
		"hooks:\n  - id: ghost\n    event: e\n    scope: global\n    type: script\n    path: hooks/does-not-exist.sh\n")
	path := writeManifest(t, dir, manifest)

	_, err := Compile(path)
	if err == nil {
		t.Fatalf("expected compile to fail closed on missing hook script path")
	}
	ds, ok := AsDiagnostics(err)
	if !ok {
		t.Fatalf("expected Diagnostics, got %T: %v", err, err)
	}
	if !hasCode(ds, "harness.path.missing") {
		t.Fatalf("expected harness.path.missing, got: %v", ds)
	}
}

// TestHooksScriptPathDirRejected: a script path pointing at a directory (not a
// file) is rejected — unlike skills there is no SKILL.md convention here.
func TestHooksScriptPathDirRejected(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "hooks", "adir"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	manifest := baseHookManifest(
		"hooks:\n  - id: x\n    event: e\n    scope: global\n    type: script\n    path: hooks/adir\n")
	path := writeManifest(t, dir, manifest)

	_, err := Compile(path)
	if err == nil {
		t.Fatalf("expected compile to fail closed on directory hook path")
	}
	ds, ok := AsDiagnostics(err)
	if !ok {
		t.Fatalf("expected Diagnostics, got %T: %v", err, err)
	}
	if !hasCode(ds, "harness.hooks.pathIsDir") {
		t.Fatalf("expected harness.hooks.pathIsDir, got: %v", ds)
	}
}

// TestHooksScriptPathTraversalRejected: a script path escaping the manifest
// directory via "../" is rejected (reusing resolve.go's containment policy).
func TestHooksScriptPathTraversalRejected(t *testing.T) {
	dir := t.TempDir()
	manifest := baseHookManifest(
		"hooks:\n  - id: x\n    event: e\n    scope: global\n    type: script\n    path: ../outside.sh\n")
	path := writeManifest(t, dir, manifest)

	_, err := Compile(path)
	if err == nil {
		t.Fatalf("expected compile to fail closed on traversal hook path")
	}
	ds, ok := AsDiagnostics(err)
	if !ok {
		t.Fatalf("expected Diagnostics, got %T: %v", err, err)
	}
	if !hasCode(ds, "harness.path.traversal") {
		t.Fatalf("expected harness.path.traversal, got: %v", ds)
	}
}

// TestHooksScriptPathSymlinkEscapeRejected: a script path that resolves via a
// symlink outside the manifest directory is rejected.
func TestHooksScriptPathSymlinkEscapeRejected(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	outsideFile := filepath.Join(outside, "secret.sh")
	if err := os.WriteFile(outsideFile, []byte("echo outside\n"), 0o644); err != nil {
		t.Fatalf("write outside file: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "hooks"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	link := filepath.Join(dir, "hooks", "linked.sh")
	if err := os.Symlink(outsideFile, link); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}
	manifest := baseHookManifest(
		"hooks:\n  - id: x\n    event: e\n    scope: global\n    type: script\n    path: hooks/linked.sh\n")
	path := writeManifest(t, dir, manifest)

	_, err := Compile(path)
	if err == nil {
		t.Fatalf("expected compile to fail closed on symlink-escaping hook path")
	}
	ds, ok := AsDiagnostics(err)
	if !ok {
		t.Fatalf("expected Diagnostics, got %T: %v", err, err)
	}
	if !hasCode(ds, "harness.path.symlinkEscape") {
		t.Fatalf("expected harness.path.symlinkEscape, got: %v", ds)
	}
}

// TestHooksURLValidated: an empty or malformed url is rejected; a well-formed
// url is accepted and shown plainly in Explain (it is a destination, not a
// secret).
func TestHooksURLValidated(t *testing.T) {
	malformed := baseHookManifest(
		"hooks:\n  - id: x\n    event: e\n    scope: global\n    type: http\n    url: \"not a url\"\n")
	ds := compileErr(t, malformed, syntheticYAML)
	if !hasCode(ds, "harness.hooks.url.invalid") {
		t.Fatalf("expected url.invalid diagnostic for malformed url, got: %v", ds)
	}

	wellFormed := baseHookManifest(
		"hooks:\n  - id: x\n    event: e\n    scope: global\n    type: http\n    url: https://example.com/hook\n")
	p, err := CompileBytes([]byte(wellFormed), syntheticYAML)
	if err != nil {
		t.Fatalf("well-formed url should compile: %v", err)
	}
	hk := p.Hooks()
	if len(hk) != 1 || hk[0].URL != "https://example.com/hook" {
		t.Fatalf("http hook not carried as-is: %+v", hk)
	}
	js, err := p.ExplainJSON()
	if err != nil {
		t.Fatalf("ExplainJSON: %v", err)
	}
	if !strings.Contains(string(js), "https://example.com/hook") {
		t.Errorf("Explain JSON missing plain url:\n%s", js)
	}
}

// TestHooksCommandNeverInExplainButRevealable: an inline command is carried on
// the plan for a trusted later-phase consumer, but never appears in
// Explain/ExplainJSON/Digest-visible text — only its hash. RevealHookCommand
// returns the real value.
func TestHooksCommandNeverInExplainButRevealable(t *testing.T) {
	manifest := baseHookManifest(
		"hooks:\n  - id: pre-commit-guard\n    event: tool.before_execute\n    scope: global\n    type: command\n    command: \"echo SECRET_COMMAND_SENTINEL\"\n")
	p, err := CompileBytes([]byte(manifest), syntheticYAML)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}
	hk := p.Hooks()
	if len(hk) != 1 {
		t.Fatalf("Hooks() len = %d, want 1", len(hk))
	}
	h := hk[0]
	if !strings.HasPrefix(h.CommandHash, "sha256:") {
		t.Errorf("command hook commandHash = %q, want sha256: prefix", h.CommandHash)
	}
	if h.CommandBytes != len("echo SECRET_COMMAND_SENTINEL") {
		t.Errorf("command hook commandBytes = %d, want %d", h.CommandBytes, len("echo SECRET_COMMAND_SENTINEL"))
	}

	js, err := p.ExplainJSON()
	if err != nil {
		t.Fatalf("ExplainJSON: %v", err)
	}
	if strings.Contains(string(js), "SECRET_COMMAND_SENTINEL") {
		t.Errorf("Explain JSON leaked hook command:\n%s", js)
	}
	if !strings.Contains(string(js), h.CommandHash) {
		t.Errorf("Explain JSON missing command hash %q", h.CommandHash)
	}

	revealed, ok := p.RevealHookCommand("pre-commit-guard")
	if !ok || revealed != "echo SECRET_COMMAND_SENTINEL" {
		t.Errorf("RevealHookCommand = (%q, %v), want (%q, true)", revealed, ok, "echo SECRET_COMMAND_SENTINEL")
	}
	if _, ok := p.RevealHookCommand("no-such-id"); ok {
		t.Errorf("RevealHookCommand for unknown id returned ok=true")
	}
}

// TestHooksEnvironmentAllowlist: duplicate/empty environment names are
// rejected; a valid allowlist of names (never values) is carried.
func TestHooksEnvironmentAllowlist(t *testing.T) {
	dup := baseHookManifest(
		"hooks:\n  - id: x\n    event: e\n    scope: global\n    type: command\n    command: echo hi\n    environment:\n      - API_TOKEN\n      - API_TOKEN\n")
	ds := compileErr(t, dup, syntheticYAML)
	if !hasCode(ds, "harness.hooks.environment.duplicate") {
		t.Fatalf("expected environment.duplicate diagnostic, got: %v", ds)
	}

	empty := baseHookManifest(
		"hooks:\n  - id: x\n    event: e\n    scope: global\n    type: command\n    command: echo hi\n    environment:\n      - \"\"\n")
	ds2 := compileErr(t, empty, syntheticYAML)
	if !hasCode(ds2, "harness.hooks.environment.empty") {
		t.Fatalf("expected environment.empty diagnostic, got: %v", ds2)
	}

	valid := baseHookManifest(
		"hooks:\n  - id: x\n    event: e\n    scope: global\n    type: command\n    command: echo hi\n    environment:\n      - API_TOKEN\n      - OTHER_VAR\n")
	p, err := CompileBytes([]byte(valid), syntheticYAML)
	if err != nil {
		t.Fatalf("valid environment allowlist should compile: %v", err)
	}
	hk := p.Hooks()
	if len(hk) != 1 || len(hk[0].Environment) != 2 {
		t.Fatalf("environment allowlist not carried: %+v", hk)
	}
}

// TestHooksTimeoutSecondsInvalid: a negative timeoutSeconds is rejected.
func TestHooksTimeoutSecondsInvalid(t *testing.T) {
	manifest := baseHookManifest(
		"hooks:\n  - id: x\n    event: e\n    scope: global\n    type: command\n    command: echo hi\n    timeoutSeconds: -1\n")
	ds := compileErr(t, manifest, syntheticYAML)
	if !hasCode(ds, "harness.hooks.timeoutSeconds.invalid") {
		t.Fatalf("expected timeoutSeconds.invalid diagnostic, got: %v", ds)
	}
}

// TestHooksDigestChangesOnFieldEdits: changing a hook's script content hash,
// command hash, priority, timeout, scope+matcher, or environment names each
// changes the plan digest.
func TestHooksDigestChangesOnFieldEdits(t *testing.T) {
	baseline := func() (*Plan, string) {
		p, err := CompileBytes([]byte(baseHookManifest(
			"hooks:\n  - id: x\n    event: e\n    scope: global\n    type: command\n    command: echo hi\n    priority: 1\n    timeoutSeconds: 10\n    environment:\n      - A\n")), syntheticYAML)
		if err != nil {
			t.Fatalf("baseline compile failed: %v", err)
		}
		return p, p.Digest()
	}
	_, baseDigest := baseline()

	variants := map[string]string{
		"command":     "hooks:\n  - id: x\n    event: e\n    scope: global\n    type: command\n    command: echo bye\n    priority: 1\n    timeoutSeconds: 10\n    environment:\n      - A\n",
		"priority":    "hooks:\n  - id: x\n    event: e\n    scope: global\n    type: command\n    command: echo hi\n    priority: 2\n    timeoutSeconds: 10\n    environment:\n      - A\n",
		"timeout":     "hooks:\n  - id: x\n    event: e\n    scope: global\n    type: command\n    command: echo hi\n    priority: 1\n    timeoutSeconds: 20\n    environment:\n      - A\n",
		"scope":       "hooks:\n  - id: x\n    event: e\n    scope: tool\n    matcher: Bash\n    type: command\n    command: echo hi\n    priority: 1\n    timeoutSeconds: 10\n    environment:\n      - A\n",
		"environment": "hooks:\n  - id: x\n    event: e\n    scope: global\n    type: command\n    command: echo hi\n    priority: 1\n    timeoutSeconds: 10\n    environment:\n      - B\n",
	}
	for name, block := range variants {
		p, err := CompileBytes([]byte(baseHookManifest(block)), syntheticYAML)
		if err != nil {
			t.Fatalf("variant %s compile failed: %v", name, err)
		}
		if p.Digest() == baseDigest {
			t.Errorf("variant %s: digest unchanged from baseline", name)
		}
	}
}

// TestHooksScriptContentChangesDigest: changing a script-backed hook's file
// content changes the plan digest (mirrors skills' content-hash coverage).
func TestHooksScriptContentChangesDigest(t *testing.T) {
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "hooks", "guard.sh")
	rel := writeHookScriptFixture(t, dir, "guard.sh", "ORIGINAL_CONTENT\n")
	manifest := baseHookManifest(
		"hooks:\n  - id: x\n    event: e\n    scope: global\n    type: script\n    path: " + rel + "\n")
	path := writeManifest(t, dir, manifest)

	p1, err := Compile(path)
	if err != nil {
		t.Fatalf("first compile: %v", err)
	}
	d1 := p1.Digest()
	h1 := p1.Hooks()[0].ContentHash

	if err := os.WriteFile(scriptPath, []byte("CHANGED_CONTENT\n"), 0o644); err != nil {
		t.Fatalf("rewrite hook script: %v", err)
	}
	p2, err := Compile(path)
	if err != nil {
		t.Fatalf("second compile: %v", err)
	}
	if p2.Hooks()[0].ContentHash == h1 {
		t.Errorf("content hash unchanged after edit: %q", h1)
	}
	if p2.Digest() == d1 {
		t.Errorf("digest unchanged after hook script content edit: %q", d1)
	}
}

// TestHooksPlanHooksDefensiveCopy: Plan.Hooks() returns a defensive copy; a
// caller mutating the returned slice must not affect a subsequent call.
func TestHooksPlanHooksDefensiveCopy(t *testing.T) {
	manifest := baseHookManifest(
		"hooks:\n  - id: x\n    event: e\n    scope: global\n    type: command\n    command: echo hi\n    environment:\n      - A\n")
	p, err := CompileBytes([]byte(manifest), syntheticYAML)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}
	first := p.Hooks()
	if len(first) != 1 {
		t.Fatalf("Hooks() len = %d, want 1", len(first))
	}
	first[0].ID = "mutated"
	first[0].Environment[0] = "MUTATED"

	second := p.Hooks()
	if second[0].ID != "x" {
		t.Errorf("Plan.Hooks() not defensively copied: id = %q, want %q", second[0].ID, "x")
	}
	if second[0].Environment[0] != "A" {
		t.Errorf("Plan.Hooks() environment slice not defensively copied: %q, want %q", second[0].Environment[0], "A")
	}
}
