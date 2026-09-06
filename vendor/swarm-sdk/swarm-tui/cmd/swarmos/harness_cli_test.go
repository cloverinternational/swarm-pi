package main

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/client"
	"github.com/Swarm-Code/mono/swarm-sdk/harness"
)

func TestHarnessDiscoveryExplicitPathWins(t *testing.T) {
	const explicit = "/tmp/operator-selected-harness.yaml"
	path, found := discoverHarnessPath(explicit)
	if !found {
		t.Fatal("explicit harness path was not found")
	}
	if path != explicit {
		t.Fatalf("path = %q, want %q", path, explicit)
	}
}

func TestHarnessDiscoveryExactLocal(t *testing.T) {
	dir := t.TempDir()
	writeHarnessFile(t, dir, "harness.yaml", minimalHarnessManifest("HARNESS_DISCOVERY_KEY", "interactive"))
	withWorkingDirectory(t, dir)

	path, found := discoverHarnessPath("")
	if !found {
		t.Fatal("local harness.yaml was not discovered")
	}
	if path != harness.DefaultManifestName {
		t.Fatalf("path = %q, want %q", path, harness.DefaultManifestName)
	}

	path, found = discoverHarnessPath(".")
	if !found || path != harness.DefaultManifestName {
		t.Fatalf("explicit dot discovery = (%q, %v), want (%q, true)", path, found, harness.DefaultManifestName)
	}
}

func TestHarnessDiscoveryDoesNotWalkParents(t *testing.T) {
	parent := t.TempDir()
	writeHarnessFile(t, parent, "harness.yaml", minimalHarnessManifest("HARNESS_PARENT_KEY", "interactive"))
	child := filepath.Join(parent, "child")
	if err := os.Mkdir(child, 0o755); err != nil {
		t.Fatal(err)
	}
	withWorkingDirectory(t, child)

	if path, found := discoverHarnessPath(""); found {
		t.Fatalf("discovered parent harness unexpectedly: %q", path)
	}
	if path, found := discoverHarnessPath("."); found {
		t.Fatalf("explicit dot walked to parent unexpectedly: %q", path)
	}
}

func TestHarnessValidateMalformedReturnsStructuredDiagnostics(t *testing.T) {
	dir := t.TempDir()
	path := writeHarnessFile(t, dir, "malformed.yaml", "apiVersion: [")

	var stdout, stderr bytes.Buffer
	err := runHarnessSubcommandIO([]string{"validate", path}, &stdout, &stderr)
	if err == nil {
		t.Fatal("validate malformed file succeeded")
	}
	if stdout.Len() != 0 {
		t.Fatalf("unexpected stdout: %q", stdout.String())
	}
	output := stderr.String()
	if !strings.Contains(output, `"ok": false`) || !strings.Contains(output, `"diagnostics"`) {
		t.Fatalf("stderr is not structured diagnostics: %s", output)
	}
}

func TestHarnessMissingCredentialEnvFailsValidateAndExplain(t *testing.T) {
	const envName = "SWARM_HARNESS_TEST_MISSING_CREDENTIAL"
	t.Setenv(envName, "")
	if err := os.Unsetenv(envName); err != nil {
		t.Fatal(err)
	}
	path := writeHarnessFile(t, t.TempDir(), "harness.yaml", minimalHarnessManifest(envName, "interactive"))

	for _, command := range []string{"validate", "explain"} {
		t.Run(command, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			err := runHarnessSubcommandIO([]string{command, path}, &stdout, &stderr)
			if err == nil {
				t.Fatalf("%s with missing credential succeeded", command)
			}
			if stdout.Len() != 0 {
				t.Fatalf("%s wrote unexpected stdout: %q", command, stdout.String())
			}
			output := stderr.String()
			if !strings.Contains(output, "harness.ref.envMissing") || !strings.Contains(output, envName) {
				t.Fatalf("%s error did not identify missing env safely: %s", command, output)
			}
		})
	}
}

func TestHarnessConstructionFailsClosedWithoutCredential(t *testing.T) {
	path := writeHarnessFile(t, t.TempDir(), "harness.yaml", `apiVersion: swarm.ai/v1alpha1
kind: Harness
metadata:
  name: no-credential
provider:
  id: anthropic
  model: claude-x
agent:
  systemPrompt:
    inline: "closed"
  tools: []
permissions:
  approvalMode: interactive
`)
	plan, err := harness.Compile(path)
	if err != nil {
		t.Fatal(err)
	}
	options, err := buildHarnessClientOptions(plan, false)
	if err != nil {
		t.Fatal(err)
	}
	sdk, err := client.New(options...)
	if err == nil {
		_ = sdk.Close()
		t.Fatal("client.New succeeded without a plan credential")
	}
	if sdk != nil {
		t.Fatal("client.New returned a partial client on credential failure")
	}
	if !strings.Contains(err.Error(), "empty credential") {
		t.Fatalf("unexpected construction error: %v", err)
	}
}

func TestHarnessYoloRequiresExplicitCLIOptIn(t *testing.T) {
	const envName = "SWARM_HARNESS_TEST_YOLO_KEY"
	t.Setenv(envName, "test-yolo-key")
	path := writeHarnessFile(t, t.TempDir(), "harness.yaml", minimalHarnessManifest(envName, "yolo"))
	plan, err := harness.Compile(path)
	if err != nil {
		t.Fatal(err)
	}

	options, err := buildHarnessClientOptions(plan, false)
	if err != nil {
		t.Fatal(err)
	}
	sdk, err := client.New(options...)
	if err == nil {
		_ = sdk.Close()
		t.Fatal("YAML-only yolo unexpectedly constructed a client")
	}
	if sdk != nil {
		t.Fatal("YAML-only yolo returned a partial client")
	}
	if !strings.Contains(err.Error(), "WithHarnessAllowYolo") {
		t.Fatalf("YAML-only yolo error did not identify explicit posture: %v", err)
	}

	options, err = buildHarnessClientOptions(plan, true)
	if err != nil {
		t.Fatal(err)
	}
	sdk, err = client.New(options...)
	if err != nil {
		t.Fatalf("explicit yolo opt-in failed: %v", err)
	}
	if got := sdk.HarnessSnapshot().ApprovalMode; got != "yolo" {
		t.Errorf("approval mode = %q, want yolo", got)
	}
	if err := sdk.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestHarnessExplainRedactsCredential(t *testing.T) {
	const (
		envName = "SWARM_HARNESS_TEST_EXPLAIN_KEY"
		secret  = "sk-real-secret-must-never-appear"
	)
	t.Setenv(envName, secret)
	path := writeHarnessFile(t, t.TempDir(), "harness.yaml", minimalHarnessManifest(envName, "interactive"))

	var stdout, stderr bytes.Buffer
	if err := runHarnessSubcommandIO([]string{"explain", path}, &stdout, &stderr); err != nil {
		t.Fatalf("explain failed: %v\nstderr: %s", err, stderr.String())
	}
	output := stdout.String()
	if strings.Contains(output, secret) {
		t.Fatal("explain output leaked the credential value")
	}
	for _, want := range []string{`"digest"`, `"provider"`, `"model"`, `"tools"`, `"permissions"`, `"buildIdentity"`, "env:" + envName} {
		if !strings.Contains(output, want) {
			t.Errorf("explain output missing %q: %s", want, output)
		}
	}
}

func TestHarnessCLIAndDirectClientSnapshotsConform(t *testing.T) {
	const envName = "SWARM_HARNESS_TEST_CONFORMANCE_KEY"
	t.Setenv(envName, "test-conformance-key")
	path := writeHarnessFile(t, t.TempDir(), "harness.yaml", minimalHarnessManifest(envName, "readonly"))
	plan, err := harness.Compile(path)
	if err != nil {
		t.Fatal(err)
	}

	cliOptions, err := buildHarnessClientOptions(plan, false)
	if err != nil {
		t.Fatal(err)
	}
	cliClient, err := client.New(cliOptions...)
	if err != nil {
		t.Fatal(err)
	}
	defer cliClient.Close()

	directClient, err := client.New(client.WithHarnessPlan(plan))
	if err != nil {
		t.Fatal(err)
	}
	defer directClient.Close()

	cliSnapshot := cliClient.HarnessSnapshot()
	directSnapshot := directClient.HarnessSnapshot()
	if !reflect.DeepEqual(cliSnapshot, directSnapshot) {
		t.Fatalf("CLI snapshot differs from direct client:\nCLI:    %#v\ndirect: %#v", cliSnapshot, directSnapshot)
	}
	if !cliSnapshot.Harness {
		t.Fatal("CLI client snapshot is not marked as harness constructed")
	}
	if cliSnapshot.Provider != plan.ProviderID() || cliSnapshot.Model != plan.Model() {
		t.Fatalf("provider/model = %s/%s, want %s/%s", cliSnapshot.Provider, cliSnapshot.Model, plan.ProviderID(), plan.Model())
	}
	if cliSnapshot.SystemPromptSHA256 != plan.SystemPromptHash() {
		t.Fatalf("prompt hash = %q, want %q", cliSnapshot.SystemPromptSHA256, plan.SystemPromptHash())
	}
	if len(cliSnapshot.ExposedTools) != 0 || len(cliSnapshot.SelectedCatalogIDs) != 0 {
		t.Fatalf("zero-tool plan widened exposure: %#v", cliSnapshot)
	}
}

func minimalHarnessManifest(envName, approvalMode string) string {
	return `apiVersion: swarm.ai/v1alpha1
kind: Harness
metadata:
  name: cli-test
runtime:
  workspace: .
  storage: .swarm-test
provider:
  id: anthropic
  model: claude-x
  credential:
    env: ` + envName + `
agent:
  systemPrompt:
    inline: "CLI-HARNESS-PROMPT"
  tools: []
permissions:
  approvalMode: ` + approvalMode + `
  workspaceBoundary: true
  allowMutation: false
interfaces:
  default: print
  print:
    format: text
`
}

func writeHarnessFile(t *testing.T, dir, name, contents string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func withWorkingDirectory(t *testing.T, dir string) {
	t.Helper()
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(previous); err != nil {
			t.Errorf("restore working directory: %v", err)
		}
	})
}
