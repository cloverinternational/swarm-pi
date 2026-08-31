package harness

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// baseMcpManifest returns a minimal valid manifest with the given raw mcp
// block spliced in verbatim (the block already includes the `mcp:` key).
func baseMcpManifest(mcpBlock string) string {
	return "apiVersion: swarm.ai/v1alpha1\n" +
		"kind: Harness\n" +
		"metadata:\n  name: mcptest\n" +
		"provider:\n  id: anthropic\n  model: claude-x\n" +
		"agent:\n  systemPrompt:\n    inline: \"hi\"\n  tools: []\n" +
		mcpBlock
}

// TestMcpOmittedAndEmptyValid: omitted mcp and empty `mcp: []` are both valid,
// yield no servers, and preserve the same digest as each other (neither block
// contributes anything to the redacted surface).
func TestMcpOmittedAndEmptyValid(t *testing.T) {
	omitted := baseMcpManifest("")
	p, err := CompileBytes([]byte(omitted), syntheticYAML)
	if err != nil {
		t.Fatalf("omitted mcp should compile: %v", err)
	}
	if len(p.MCPServers()) != 0 {
		t.Errorf("omitted: MCPServers() = %v, want empty", p.MCPServers())
	}
	digestNoMcp := p.Digest()

	empty := baseMcpManifest("mcp: []\n")
	p2, err := CompileBytes([]byte(empty), syntheticYAML)
	if err != nil {
		t.Fatalf("empty mcp should compile: %v", err)
	}
	if len(p2.MCPServers()) != 0 {
		t.Errorf("empty: MCPServers() = %v, want empty", p2.MCPServers())
	}
	if p2.Digest() != digestNoMcp {
		t.Errorf("empty vs omitted digests differ: %q vs %q", p2.Digest(), digestNoMcp)
	}
}

// TestMcpMissingIDRejected: an entry without an id is rejected.
func TestMcpMissingIDRejected(t *testing.T) {
	manifest := baseMcpManifest(
		"mcp:\n  - type: stdio\n    command: /usr/bin/mcp-server\n")
	ds := compileErr(t, manifest, syntheticYAML)
	if !hasCode(ds, "harness.mcp.id.missing") {
		t.Fatalf("expected missing-id diagnostic, got: %v", ds)
	}
}

// TestMcpDuplicateID: duplicate ids across the list are rejected.
func TestMcpDuplicateID(t *testing.T) {
	manifest := baseMcpManifest(
		"mcp:\n" +
			"  - id: dup\n    type: stdio\n    command: /usr/bin/mcp-server\n" +
			"  - id: dup\n    type: stdio\n    command: /usr/bin/mcp-server2\n")
	ds := compileErr(t, manifest, syntheticYAML)
	if !hasCode(ds, "harness.mcp.id.duplicate") {
		t.Fatalf("expected duplicate id diagnostic, got: %v", ds)
	}
}

// TestMcpUnknownFieldRejected: an unknown key under an mcp entry is rejected.
func TestMcpUnknownFieldRejected(t *testing.T) {
	manifest := baseMcpManifest(
		"mcp:\n  - id: x\n    type: stdio\n    command: /usr/bin/mcp-server\n    bogus: y\n")
	ds := compileErr(t, manifest, syntheticYAML)
	if !hasCode(ds, "harness.decode.unknownField") {
		t.Fatalf("expected unknown-field rejection, got: %v", ds)
	}
}

// TestMcpTypeEnumValidated: type must be one of stdio/http/sse; oauth is
// explicitly rejected with a dedicated diagnostic (not silently dropped or
// lumped in with the generic "invalid" message); an unrecognized value gets
// the generic invalid diagnostic.
func TestMcpTypeEnumValidated(t *testing.T) {
	oauth := baseMcpManifest(
		"mcp:\n  - id: x\n    type: oauth\n    url: https://example.com/mcp\n")
	ds := compileErr(t, oauth, syntheticYAML)
	if !hasCode(ds, "harness.mcp.type.oauthUnsupported") {
		t.Fatalf("expected oauthUnsupported diagnostic, got: %v", ds)
	}

	bogus := baseMcpManifest(
		"mcp:\n  - id: x\n    type: bogus\n")
	ds2 := compileErr(t, bogus, syntheticYAML)
	if !hasCode(ds2, "harness.mcp.type.invalid") {
		t.Fatalf("expected type.invalid diagnostic, got: %v", ds2)
	}

	missing := baseMcpManifest(
		"mcp:\n  - id: x\n")
	ds3 := compileErr(t, missing, syntheticYAML)
	if !hasCode(ds3, "harness.mcp.type.missing") {
		t.Fatalf("expected type.missing diagnostic, got: %v", ds3)
	}
}

// TestMcpStdioRequiresCommand: type stdio without a command is rejected.
func TestMcpStdioRequiresCommand(t *testing.T) {
	manifest := baseMcpManifest(
		"mcp:\n  - id: x\n    type: stdio\n    unsafeDevMode: true\n")
	ds := compileErr(t, manifest, syntheticYAML)
	if !hasCode(ds, "harness.mcp.stdio.command.missing") {
		t.Fatalf("expected stdio.command.missing diagnostic, got: %v", ds)
	}
}

// TestMcpStdioPinningAcceptAndReject exercises the PINNING RULE: an absolute
// command path is accepted; a pinned package@version arg is accepted; a bare
// floating package (no version, or "@latest") without unsafeDevMode is
// rejected; the same floating command WITH unsafeDevMode: true is accepted.
func TestMcpStdioPinningAcceptAndReject(t *testing.T) {
	absPath := baseMcpManifest(
		"mcp:\n  - id: x\n    type: stdio\n    command: /usr/bin/mcp-server\n")
	p, err := CompileBytes([]byte(absPath), syntheticYAML)
	if err != nil {
		t.Fatalf("absolute command path should compile: %v", err)
	}
	if len(p.MCPServers()) != 1 || !p.MCPServers()[0].Pinned {
		t.Fatalf("absolute command path should be pinned: %+v", p.MCPServers())
	}

	pinnedArg := baseMcpManifest(
		"mcp:\n  - id: x\n    type: stdio\n    command: npx\n    args: [\"-y\", \"@modelcontextprotocol/server-filesystem@1.2.3\", \".\"]\n")
	p2, err := CompileBytes([]byte(pinnedArg), syntheticYAML)
	if err != nil {
		t.Fatalf("pinned package arg should compile: %v", err)
	}
	if len(p2.MCPServers()) != 1 || !p2.MCPServers()[0].Pinned {
		t.Fatalf("pinned package arg should be pinned: %+v", p2.MCPServers())
	}

	unpinnedBare := baseMcpManifest(
		"mcp:\n  - id: x\n    type: stdio\n    command: npx\n    args: [\"-y\", \"@modelcontextprotocol/server-filesystem\"]\n")
	ds := compileErr(t, unpinnedBare, syntheticYAML)
	if !hasCode(ds, "harness.mcp.stdio.unpinned") {
		t.Fatalf("expected stdio.unpinned diagnostic for bare package, got: %v", ds)
	}

	unpinnedLatest := baseMcpManifest(
		"mcp:\n  - id: x\n    type: stdio\n    command: npx\n    args: [\"-y\", \"@modelcontextprotocol/server-filesystem@latest\"]\n")
	ds2 := compileErr(t, unpinnedLatest, syntheticYAML)
	if !hasCode(ds2, "harness.mcp.stdio.unpinned") {
		t.Fatalf("expected stdio.unpinned diagnostic for @latest, got: %v", ds2)
	}

	unsafeDevMode := baseMcpManifest(
		"mcp:\n  - id: x\n    type: stdio\n    command: npx\n    args: [\"-y\", \"@modelcontextprotocol/server-filesystem@latest\"]\n    unsafeDevMode: true\n")
	p3, err := CompileBytes([]byte(unsafeDevMode), syntheticYAML)
	if err != nil {
		t.Fatalf("unsafeDevMode should bypass the pin check: %v", err)
	}
	if len(p3.MCPServers()) != 1 {
		t.Fatalf("expected one server, got: %+v", p3.MCPServers())
	}
	if p3.MCPServers()[0].Pinned {
		t.Errorf("unsafeDevMode entry should report Pinned=false (it bypassed the check, it wasn't actually pinned)")
	}
	if !p3.MCPServers()[0].UnsafeDevMode {
		t.Errorf("expected UnsafeDevMode=true carried on the resolved spec")
	}
}

// TestMcpHttpSseRequireValidURL: http/sse require a well-formed url; an
// empty or malformed url is rejected, a well-formed one compiles and is
// carried plainly.
func TestMcpHttpSseRequireValidURL(t *testing.T) {
	missing := baseMcpManifest(
		"mcp:\n  - id: x\n    type: http\n")
	ds := compileErr(t, missing, syntheticYAML)
	if !hasCode(ds, "harness.mcp.url.missing") {
		t.Fatalf("expected url.missing diagnostic, got: %v", ds)
	}

	malformed := baseMcpManifest(
		"mcp:\n  - id: x\n    type: sse\n    url: \"not a url\"\n")
	ds2 := compileErr(t, malformed, syntheticYAML)
	if !hasCode(ds2, "harness.mcp.url.invalid") {
		t.Fatalf("expected url.invalid diagnostic, got: %v", ds2)
	}

	wellFormed := baseMcpManifest(
		"mcp:\n  - id: x\n    type: http\n    url: https://example.com/mcp\n")
	p, err := CompileBytes([]byte(wellFormed), syntheticYAML)
	if err != nil {
		t.Fatalf("well-formed url should compile: %v", err)
	}
	servers := p.MCPServers()
	if len(servers) != 1 || servers[0].URL != "https://example.com/mcp" {
		t.Fatalf("http server not carried as-is: %+v", servers)
	}
	js, err := p.ExplainJSON()
	if err != nil {
		t.Fatalf("ExplainJSON: %v", err)
	}
	if !strings.Contains(string(js), "https://example.com/mcp") {
		t.Errorf("Explain JSON missing plain mcp url:\n%s", js)
	}
}

// TestMcpStdioCommandArgsShownPlainly: unlike a hook's inline command, an mcp
// command/args pair is an operational identifier and is NOT hashed/tainted —
// it appears plainly in Explain/ExplainJSON.
func TestMcpStdioCommandArgsShownPlainly(t *testing.T) {
	manifest := baseMcpManifest(
		"mcp:\n  - id: fs\n    type: stdio\n    command: /usr/bin/mcp-filesystem-server\n    args: [\"--root\", \".\"]\n")
	p, err := CompileBytes([]byte(manifest), syntheticYAML)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}
	servers := p.MCPServers()
	if len(servers) != 1 || servers[0].Command != "/usr/bin/mcp-filesystem-server" {
		t.Fatalf("stdio command not carried plainly: %+v", servers)
	}
	js, err := p.ExplainJSON()
	if err != nil {
		t.Fatalf("ExplainJSON: %v", err)
	}
	if !strings.Contains(string(js), "/usr/bin/mcp-filesystem-server") {
		t.Errorf("Explain JSON missing plain mcp command:\n%s", js)
	}
	if !strings.Contains(string(js), "--root") {
		t.Errorf("Explain JSON missing plain mcp args:\n%s", js)
	}
}

// TestMcpWorkDirResolvesManifestRelative: a workDir resolves relative to the
// manifest directory and is carried as a manifest-relative label.
func TestMcpWorkDirResolvesManifestRelative(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "srv"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	manifest := baseMcpManifest(
		"mcp:\n  - id: x\n    type: stdio\n    command: /usr/bin/mcp-server\n    workDir: srv\n")
	path := writeManifest(t, dir, manifest)

	p, err := Compile(path)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}
	servers := p.MCPServers()
	if len(servers) != 1 {
		t.Fatalf("MCPServers() len = %d, want 1", len(servers))
	}
	if filepath.IsAbs(servers[0].WorkDir) {
		t.Errorf("workDir %q is absolute; want manifest-relative", servers[0].WorkDir)
	}
	if servers[0].WorkDir != "srv" {
		t.Errorf("workDir = %q, want %q", servers[0].WorkDir, "srv")
	}
}

// TestMcpWorkDirMissingFailsClosed: a workDir that does not exist fails
// compilation with a source-cited diagnostic.
func TestMcpWorkDirMissingFailsClosed(t *testing.T) {
	dir := t.TempDir()
	manifest := baseMcpManifest(
		"mcp:\n  - id: x\n    type: stdio\n    command: /usr/bin/mcp-server\n    workDir: does-not-exist\n")
	path := writeManifest(t, dir, manifest)

	_, err := Compile(path)
	if err == nil {
		t.Fatalf("expected compile to fail closed on missing workDir")
	}
	ds, ok := AsDiagnostics(err)
	if !ok {
		t.Fatalf("expected Diagnostics, got %T: %v", err, err)
	}
	if !hasCode(ds, "harness.path.missing") {
		t.Fatalf("expected harness.path.missing, got: %v", ds)
	}
}

// TestMcpWorkDirTraversalRejected: a workDir escaping the manifest directory
// via "../" is rejected (reusing resolve.go's containment policy).
func TestMcpWorkDirTraversalRejected(t *testing.T) {
	dir := t.TempDir()
	manifest := baseMcpManifest(
		"mcp:\n  - id: x\n    type: stdio\n    command: /usr/bin/mcp-server\n    workDir: ../outside\n")
	path := writeManifest(t, dir, manifest)

	_, err := Compile(path)
	if err == nil {
		t.Fatalf("expected compile to fail closed on traversal workDir")
	}
	ds, ok := AsDiagnostics(err)
	if !ok {
		t.Fatalf("expected Diagnostics, got %T: %v", err, err)
	}
	if !hasCode(ds, "harness.path.traversal") {
		t.Fatalf("expected harness.path.traversal, got: %v", ds)
	}
}

// TestMcpWorkDirSymlinkEscapeRejected: a workDir that resolves via a symlink
// outside the manifest directory is rejected.
func TestMcpWorkDirSymlinkEscapeRejected(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(outside, "outdir"), 0o755); err != nil {
		t.Fatalf("mkdir outside dir: %v", err)
	}
	link := filepath.Join(dir, "linked")
	if err := os.Symlink(filepath.Join(outside, "outdir"), link); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}
	manifest := baseMcpManifest(
		"mcp:\n  - id: x\n    type: stdio\n    command: /usr/bin/mcp-server\n    workDir: linked\n")
	path := writeManifest(t, dir, manifest)

	_, err := Compile(path)
	if err == nil {
		t.Fatalf("expected compile to fail closed on symlink-escaping workDir")
	}
	ds, ok := AsDiagnostics(err)
	if !ok {
		t.Fatalf("expected Diagnostics, got %T: %v", err, err)
	}
	if !hasCode(ds, "harness.path.symlinkEscape") {
		t.Fatalf("expected harness.path.symlinkEscape, got: %v", ds)
	}
}

// TestMcpWorkDirNotAllowedForNonStdio: workDir set on an http/sse entry is
// rejected.
func TestMcpWorkDirNotAllowedForNonStdio(t *testing.T) {
	manifest := baseMcpManifest(
		"mcp:\n  - id: x\n    type: http\n    url: https://example.com/mcp\n    workDir: .\n")
	ds := compileErr(t, manifest, syntheticYAML)
	if !hasCode(ds, "harness.mcp.workDir.notAllowedForNonStdio") {
		t.Fatalf("expected workDir.notAllowedForNonStdio diagnostic, got: %v", ds)
	}
}

// TestMcpEnvironmentAllowlist: duplicate/empty environment names are
// rejected; a valid allowlist of names (never values) is carried.
func TestMcpEnvironmentAllowlist(t *testing.T) {
	dup := baseMcpManifest(
		"mcp:\n  - id: x\n    type: stdio\n    command: /usr/bin/mcp-server\n    environment:\n      - API_TOKEN\n      - API_TOKEN\n")
	ds := compileErr(t, dup, syntheticYAML)
	if !hasCode(ds, "harness.mcp.environment.duplicate") {
		t.Fatalf("expected environment.duplicate diagnostic, got: %v", ds)
	}

	empty := baseMcpManifest(
		"mcp:\n  - id: x\n    type: stdio\n    command: /usr/bin/mcp-server\n    environment:\n      - \"\"\n")
	ds2 := compileErr(t, empty, syntheticYAML)
	if !hasCode(ds2, "harness.mcp.environment.empty") {
		t.Fatalf("expected environment.empty diagnostic, got: %v", ds2)
	}

	valid := baseMcpManifest(
		"mcp:\n  - id: x\n    type: stdio\n    command: /usr/bin/mcp-server\n    environment:\n      - API_TOKEN\n      - OTHER_VAR\n")
	p, err := CompileBytes([]byte(valid), syntheticYAML)
	if err != nil {
		t.Fatalf("valid environment allowlist should compile: %v", err)
	}
	servers := p.MCPServers()
	if len(servers) != 1 || len(servers[0].Environment) != 2 {
		t.Fatalf("environment allowlist not carried: %+v", servers)
	}
}

// TestMcpHeadersReferenceEnvironment: a header value that does not reference
// a name declared in the same entry's environment allowlist is rejected
// (never a literal inline secret); a valid reference compiles and is carried
// as a name-to-name mapping (never a value).
func TestMcpHeadersReferenceEnvironment(t *testing.T) {
	literal := baseMcpManifest(
		"mcp:\n  - id: x\n    type: http\n    url: https://example.com/mcp\n    headers:\n      Authorization: \"Bearer sk-literal-secret\"\n")
	ds := compileErr(t, literal, syntheticYAML)
	if !hasCode(ds, "harness.mcp.headers.unknownEnvRef") {
		t.Fatalf("expected headers.unknownEnvRef diagnostic for literal header value, got: %v", ds)
	}

	unreferenced := baseMcpManifest(
		"mcp:\n  - id: x\n    type: http\n    url: https://example.com/mcp\n    environment:\n      - OTHER_TOKEN\n    headers:\n      Authorization: API_TOKEN\n")
	ds2 := compileErr(t, unreferenced, syntheticYAML)
	if !hasCode(ds2, "harness.mcp.headers.unknownEnvRef") {
		t.Fatalf("expected headers.unknownEnvRef diagnostic for unreferenced name, got: %v", ds2)
	}

	valid := baseMcpManifest(
		"mcp:\n  - id: x\n    type: http\n    url: https://example.com/mcp\n    environment:\n      - API_TOKEN\n    headers:\n      Authorization: API_TOKEN\n")
	p, err := CompileBytes([]byte(valid), syntheticYAML)
	if err != nil {
		t.Fatalf("valid header reference should compile: %v", err)
	}
	servers := p.MCPServers()
	if len(servers) != 1 || servers[0].Headers["Authorization"] != "API_TOKEN" {
		t.Fatalf("header name reference not carried: %+v", servers)
	}
	js, err := p.ExplainJSON()
	if err != nil {
		t.Fatalf("ExplainJSON: %v", err)
	}
	if strings.Contains(string(js), "sk-literal-secret") {
		t.Errorf("Explain JSON leaked a literal header value:\n%s", js)
	}
}

// TestMcpHeadersNotAllowedForStdio: headers set on a stdio entry is rejected.
func TestMcpHeadersNotAllowedForStdio(t *testing.T) {
	manifest := baseMcpManifest(
		"mcp:\n  - id: x\n    type: stdio\n    command: /usr/bin/mcp-server\n    environment:\n      - A\n    headers:\n      X: A\n")
	ds := compileErr(t, manifest, syntheticYAML)
	if !hasCode(ds, "harness.mcp.headers.notAllowedForStdio") {
		t.Fatalf("expected headers.notAllowedForStdio diagnostic, got: %v", ds)
	}
}

// TestMcpToolsExcludeToolsBothSetRejected: setting both tools and
// excludeTools on the same entry is rejected.
func TestMcpToolsExcludeToolsBothSetRejected(t *testing.T) {
	manifest := baseMcpManifest(
		"mcp:\n  - id: x\n    type: stdio\n    command: /usr/bin/mcp-server\n    tools: [read_file]\n    excludeTools: [dangerous_tool]\n")
	ds := compileErr(t, manifest, syntheticYAML)
	if !hasCode(ds, "harness.mcp.tools.bothSet") {
		t.Fatalf("expected tools.bothSet diagnostic, got: %v", ds)
	}
}

// TestMcpTimeoutSecondsInvalid: a negative timeoutSeconds is rejected; an
// unset one resolves to the documented default.
func TestMcpTimeoutSecondsInvalid(t *testing.T) {
	manifest := baseMcpManifest(
		"mcp:\n  - id: x\n    type: stdio\n    command: /usr/bin/mcp-server\n    timeoutSeconds: -1\n")
	ds := compileErr(t, manifest, syntheticYAML)
	if !hasCode(ds, "harness.mcp.timeoutSeconds.invalid") {
		t.Fatalf("expected timeoutSeconds.invalid diagnostic, got: %v", ds)
	}

	unset := baseMcpManifest(
		"mcp:\n  - id: x\n    type: stdio\n    command: /usr/bin/mcp-server\n")
	p, err := CompileBytes([]byte(unset), syntheticYAML)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}
	servers := p.MCPServers()
	if len(servers) != 1 || servers[0].TimeoutSeconds != defaultMcpTimeoutSeconds {
		t.Fatalf("default timeoutSeconds not applied: %+v", servers)
	}
}

// TestMcpDigestChangesOnFieldEdits: changing an mcp entry's args, environment
// names, header name-refs, pin status, or enabled state each changes the plan
// digest; unchanged/omitted mcp preserves the prior digest exactly.
func TestMcpDigestChangesOnFieldEdits(t *testing.T) {
	baseline := baseMcpManifest(
		"mcp:\n  - id: x\n    type: stdio\n    command: /usr/bin/mcp-server\n    args: [\"--flag\"]\n    environment:\n      - A\n    enabled: true\n")
	p, err := CompileBytes([]byte(baseline), syntheticYAML)
	if err != nil {
		t.Fatalf("baseline compile failed: %v", err)
	}
	baseDigest := p.Digest()

	variants := map[string]string{
		"args":        "mcp:\n  - id: x\n    type: stdio\n    command: /usr/bin/mcp-server\n    args: [\"--other\"]\n    environment:\n      - A\n    enabled: true\n",
		"environment": "mcp:\n  - id: x\n    type: stdio\n    command: /usr/bin/mcp-server\n    args: [\"--flag\"]\n    environment:\n      - B\n    enabled: true\n",
		"enabled":     "mcp:\n  - id: x\n    type: stdio\n    command: /usr/bin/mcp-server\n    args: [\"--flag\"]\n    environment:\n      - A\n    enabled: false\n",
		"command":     "mcp:\n  - id: x\n    type: stdio\n    command: /usr/bin/other-server\n    args: [\"--flag\"]\n    environment:\n      - A\n    enabled: true\n",
	}
	for name, block := range variants {
		p2, err := CompileBytes([]byte(baseMcpManifest(block)), syntheticYAML)
		if err != nil {
			t.Fatalf("variant %s compile failed: %v", name, err)
		}
		if p2.Digest() == baseDigest {
			t.Errorf("variant %s: digest unchanged from baseline", name)
		}
	}

	// Header name-ref and pin-status variants need their own base (http vs
	// stdio-with-unsafeDevMode) since they touch fields not present above.
	headerBase := baseMcpManifest(
		"mcp:\n  - id: x\n    type: http\n    url: https://example.com/mcp\n    environment:\n      - A\n      - B\n    headers:\n      X: A\n")
	hp, err := CompileBytes([]byte(headerBase), syntheticYAML)
	if err != nil {
		t.Fatalf("header base compile failed: %v", err)
	}
	headerChanged := baseMcpManifest(
		"mcp:\n  - id: x\n    type: http\n    url: https://example.com/mcp\n    environment:\n      - A\n      - B\n    headers:\n      X: B\n")
	hp2, err := CompileBytes([]byte(headerChanged), syntheticYAML)
	if err != nil {
		t.Fatalf("header changed compile failed: %v", err)
	}
	if hp.Digest() == hp2.Digest() {
		t.Errorf("header name-ref change did not change digest")
	}

	pinBase := baseMcpManifest(
		"mcp:\n  - id: x\n    type: stdio\n    command: npx\n    args: [\"pkg@1.0.0\"]\n")
	pp, err := CompileBytes([]byte(pinBase), syntheticYAML)
	if err != nil {
		t.Fatalf("pin base compile failed: %v", err)
	}
	unsafeVariant := baseMcpManifest(
		"mcp:\n  - id: x\n    type: stdio\n    command: npx\n    args: [\"pkg@latest\"]\n    unsafeDevMode: true\n")
	pp2, err := CompileBytes([]byte(unsafeVariant), syntheticYAML)
	if err != nil {
		t.Fatalf("unsafe variant compile failed: %v", err)
	}
	if pp.Digest() == pp2.Digest() {
		t.Errorf("pin-status/unsafeDevMode change did not change digest")
	}
}

// TestMcpPlanMCPServersDefensiveCopy: Plan.MCPServers() returns a defensive
// copy; a caller mutating the returned slice must not affect a subsequent
// call.
func TestMcpPlanMCPServersDefensiveCopy(t *testing.T) {
	manifest := baseMcpManifest(
		"mcp:\n  - id: x\n    type: stdio\n    command: /usr/bin/mcp-server\n    environment:\n      - A\n")
	p, err := CompileBytes([]byte(manifest), syntheticYAML)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}
	first := p.MCPServers()
	if len(first) != 1 {
		t.Fatalf("MCPServers() len = %d, want 1", len(first))
	}
	first[0].ID = "mutated"
	first[0].Environment[0] = "MUTATED"

	second := p.MCPServers()
	if second[0].ID != "x" {
		t.Errorf("Plan.MCPServers() not defensively copied: id = %q, want %q", second[0].ID, "x")
	}
	if second[0].Environment[0] != "A" {
		t.Errorf("Plan.MCPServers() environment slice not defensively copied: %q, want %q", second[0].Environment[0], "A")
	}
}
