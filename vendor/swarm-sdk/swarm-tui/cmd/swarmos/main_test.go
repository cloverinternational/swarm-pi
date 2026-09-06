package main

import (
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/a2a"
	attserver "github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/headless/automation/server"
)

func TestValidatePromptFlagArgs(t *testing.T) {
	newFlagSet := func() *flag.FlagSet {
		fs := flag.NewFlagSet("swarmos-test", flag.ContinueOnError)
		fs.String("p", "", "")
		fs.String("harness", "", "")
		fs.String("P", "", "")
		fs.String("workspace", "", "")
		fs.String("initial-prompt", "", "")
		fs.Bool("debug", false, "")
		fs.Bool("daemon", false, "")
		return fs
	}

	valid := [][]string{
		{"-p", "prompt", "--harness", "."},
		{"--harness", ".", "-p", "prompt"},
		{"-p=--harness"},
		{"--p=-harness"},
		{"-p", "-"},
		{"--debug", "-p", "prompt"},
		{"--workspace", "-p", "-p", "prompt"},
		{"-P=-p", "-p", "prompt"},
		{"--initial-prompt=--daemon"},
	}
	for _, args := range valid {
		if err := validatePromptFlagArgs(newFlagSet(), args); err != nil {
			t.Errorf("validatePromptFlagArgs(%q) = %v, want nil", args, err)
		}
	}

	invalid := [][]string{
		{"-p", "--harness", ".", "prompt"},
		{"--p", "-harness", ".", "prompt"},
		{"-p", "--harness=."},
		{"-p", "--debug"},
		{"-p", "--harnes", "./harness.yaml"},
		{"-p", "-1"},
		{"-p", "--"},
		{"-p"},
		{"-p", "first", "-p", "--harness"},
	}
	for _, args := range invalid {
		err := validatePromptFlagArgs(newFlagSet(), args)
		if err == nil {
			t.Errorf("validatePromptFlagArgs(%q) = nil, want error", args)
			continue
		}
		if !strings.Contains(err.Error(), "-p requires a prompt") {
			t.Errorf("validatePromptFlagArgs(%q) error = %q, want actionable -p message", args, err)
		}
	}
	initialPromptInvalid := [][]string{
		{"--initial-prompt", "--daemon"},
		{"--initial-prompt", "--unknown"},
		{"--initial-prompt", "--"},
		{"--initial-prompt"},
	}
	for _, args := range initialPromptInvalid {
		err := validatePromptFlagArgs(newFlagSet(), args)
		if err == nil || !strings.Contains(err.Error(), "--initial-prompt requires a prompt") {
			t.Errorf("validatePromptFlagArgs(%q) error = %v, want actionable --initial-prompt message", args, err)
		}
	}
}

func TestDaemonBackedRequestedIncludesEnvironment(t *testing.T) {
	if !daemonBackedRequested(false, "1") {
		t.Fatal("expected SWARM_DAEMON to select daemon-backed mode")
	}
	if !daemonBackedRequested(true, "") {
		t.Fatal("expected --daemon to select daemon-backed mode")
	}
	if daemonBackedRequested(false, "") {
		t.Fatal("plain TUI unexpectedly selected daemon-backed mode")
	}
}

func TestValidateInitialPromptMode(t *testing.T) {
	tests := []struct {
		name          string
		initialPrompt string
		headless      string
		resume        string
		conversation  string
		harness       string
		debugRender   bool
		debugFixture  bool
		daemon        bool
		want          string
	}{
		{name: "plain interactive", initialPrompt: "start work"},
		{name: "blank is absent", initialPrompt: "   ", headless: "headless"},
		{name: "headless", initialPrompt: "start", headless: "headless", want: "cannot be combined with -p"},
		{name: "resume", initialPrompt: "start", resume: "conv-1", want: "cannot be combined with -r"},
		{name: "conversation id", initialPrompt: "start", conversation: "conv-1", want: "cannot be combined with --conversation-id"},
		{name: "harness", initialPrompt: "start", harness: ".", want: "cannot be combined with --harness"},
		{name: "debug render", initialPrompt: "start", debugRender: true, want: "cannot be combined with --debug-render"},
		{name: "debug fixture", initialPrompt: "start", debugFixture: true, want: "cannot be combined with --debug-lineage-fixture"},
		{name: "daemon", initialPrompt: "start", daemon: true, want: "cannot be combined with --daemon"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateInitialPromptMode(
				tt.initialPrompt,
				tt.headless,
				tt.resume,
				tt.conversation,
				tt.harness,
				tt.debugRender,
				tt.debugFixture,
				tt.daemon,
			)
			if tt.want == "" {
				if err != nil {
					t.Fatalf("validateInitialPromptMode() = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("validateInitialPromptMode() = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestResolveHeadlessIsolationCleanAgentDisablesAllAmbientBehavior(t *testing.T) {
	got := resolveHeadlessIsolation(true, false, false, false, false)
	if !got.noHooks || !got.noGlobalMemory || !got.noProjectMemory || !got.noSkills {
		t.Fatalf("clean isolation = %#v, want every ambient source disabled", got)
	}
}

func TestResolveHeadlessIsolationKeepsGranularFlagsIndependent(t *testing.T) {
	got := resolveHeadlessIsolation(false, false, false, false, true)
	if got.noHooks || got.noGlobalMemory || got.noProjectMemory || !got.noSkills {
		t.Fatalf("granular isolation = %#v, want only skills disabled", got)
	}
}

func TestResolveEndpointOverrideRequiresExplicitProtocol(t *testing.T) {
	_, err := resolveEndpointOverride("", "secret", "", "https://example.test/v1", "model")
	if err == nil || !strings.Contains(err.Error(), "require --api-type") {
		t.Fatalf("expected explicit protocol error, got %v", err)
	}
}

func TestResolveEndpointOverrideRequiresExplicitRunScopedKey(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "persisted-environment-key")
	_, err := resolveEndpointOverride("openai", "", "", "https://example.test/v1", "model")
	if err == nil || !strings.Contains(err.Error(), "requires --api-key or --api-key-env") {
		t.Fatalf("expected explicit run-scoped key error, got %v", err)
	}
	if strings.Contains(err.Error(), "persisted-environment-key") {
		t.Fatal("error leaked an ambient credential")
	}
}

func TestResolveEndpointOverrideValidatesProtocolModelAndURL(t *testing.T) {
	tests := []struct {
		name    string
		apiType string
		baseURL string
		model   string
		want    string
	}{
		{name: "protocol", apiType: "gpt", model: "m", want: "expected openai or anthropic"},
		{name: "model", apiType: "openai", want: "requires an explicit --model"},
		{name: "url", apiType: "anthropic", baseURL: "example.test", model: "m", want: "must be a valid"},
		{name: "missing host", apiType: "openai", baseURL: "http://", model: "m", want: "must be a valid"},
		{name: "userinfo", apiType: "openai", baseURL: "https://secret@example.test/v1", model: "m", want: "must be a valid"},
		{name: "query", apiType: "openai", baseURL: "https://example.test/v1?key=secret", model: "m", want: "must be a valid"},
		{name: "fragment", apiType: "anthropic", baseURL: "https://example.test/api#messages", model: "m", want: "must be a valid"},
		{name: "port", apiType: "openai", baseURL: "https://example.test:99999/v1", model: "m", want: "invalid port"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := resolveEndpointOverride(tt.apiType, "", "", tt.baseURL, tt.model)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestResolveEndpointOverrideBuildsRunScopedContract(t *testing.T) {
	got, err := resolveEndpointOverride(" OpenAI ", " secret ", "", "https://example.test/v1/", "model")
	if err != nil {
		t.Fatalf("resolveEndpointOverride: %v", err)
	}
	if got.APIType != "openai" || got.APIKey != "secret" || got.BaseURL != "https://example.test/v1" {
		t.Fatalf("override = %#v", got)
	}
}

func TestResolveEndpointOverrideReadsRunScopedKeyFromEnvironment(t *testing.T) {
	t.Setenv("TEST_SWARM_ENDPOINT_KEY", " env-secret ")

	got, err := resolveEndpointOverride("anthropic", "", "TEST_SWARM_ENDPOINT_KEY", "https://example.test", "model")
	if err != nil {
		t.Fatalf("resolveEndpointOverride: %v", err)
	}
	if got.APIKey != "env-secret" {
		t.Fatalf("API key = %q, want value read from environment", got.APIKey)
	}
}

func TestResolveEndpointOverrideRejectsUnsafeOrMissingKeySources(t *testing.T) {
	t.Setenv("TEST_SWARM_EMPTY_ENDPOINT_KEY", "")
	tests := []struct {
		name      string
		apiKey    string
		apiKeyEnv string
		want      string
	}{
		{name: "conflicting sources", apiKey: "inline", apiKeyEnv: "TEST_SWARM_ENDPOINT_KEY", want: "mutually exclusive"},
		{name: "missing env", apiKeyEnv: "TEST_SWARM_MISSING_ENDPOINT_KEY", want: "unset or empty"},
		{name: "empty env", apiKeyEnv: "TEST_SWARM_EMPTY_ENDPOINT_KEY", want: "unset or empty"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := resolveEndpointOverride("openai", tt.apiKey, tt.apiKeyEnv, "", "model")
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestValidateEndpointFlagsForModeRejectsTUIUsage(t *testing.T) {
	if err := validateEndpointFlagsForMode(true, "openai", "", "", ""); err != nil {
		t.Fatalf("headless endpoint flags rejected: %v", err)
	}
	if err := validateEndpointFlagsForMode(false, "", "", "", ""); err != nil {
		t.Fatalf("plain TUI invocation rejected: %v", err)
	}
	if err := validateEndpointFlagsForMode(false, "", "", "API_KEY_NAME", ""); err == nil ||
		!strings.Contains(err.Error(), "only supported with -p") {
		t.Fatalf("expected clear TUI-mode rejection, got %v", err)
	}
}

func TestCurrentAppOptionsIncludesA2AFlags(t *testing.T) {
	prevA2A := *a2aFlag
	prevHandle := *a2aHandleFlag
	prevListen := *a2aListenFlag
	prevDebug := *debugMode
	prevNewUI := *newUIFlag
	prevFixture := *debugLineageFixture
	prevInitialPrompt := *initialPromptFlag
	t.Cleanup(func() {
		*a2aFlag = prevA2A
		*a2aHandleFlag = prevHandle
		*a2aListenFlag = prevListen
		*debugMode = prevDebug
		*newUIFlag = prevNewUI
		*debugLineageFixture = prevFixture
		*initialPromptFlag = prevInitialPrompt
	})

	*a2aFlag = true
	*a2aHandleFlag = "beta"
	*a2aListenFlag = "127.0.0.1:4444"
	*debugMode = true
	*newUIFlag = true
	*debugLineageFixture = true
	*initialPromptFlag = "  start from jefe  "

	// Handle resolution now lives in computePeerHandle() (flag > hostname); the
	// resolved handle is passed to currentAppOptions as a parameter. Exercise
	// that real path so the test verifies --a2a-handle flows into app options.
	if h := computePeerHandle(); h != "beta" {
		t.Fatalf("computePeerHandle should honor --a2a-handle, got %q", h)
	}
	opts := currentAppOptions(nil, computePeerHandle())
	if !opts.A2AEnabled {
		t.Fatal("expected A2A to be enabled in app options")
	}
	if opts.A2AHandle != "beta" {
		t.Fatalf("expected A2A handle beta, got %q", opts.A2AHandle)
	}
	if opts.A2AListenAddress != "127.0.0.1:4444" {
		t.Fatalf("expected custom A2A listen address, got %q", opts.A2AListenAddress)
	}
	if !opts.DebugMode || !opts.UseNewUI || !opts.DebugLineageFixture {
		t.Fatalf("expected other app options to still be preserved: %#v", opts)
	}
	if opts.WorkspaceRoot == "" || opts.ProjectRoot == "" {
		t.Fatalf("expected execution and project roots: %#v", opts)
	}
	if !opts.TrackWorkspaceSession {
		t.Fatal("expected real TUI sessions to register a workspace lease")
	}
	if opts.InitialPrompt != "start from jefe" {
		t.Fatalf("expected trimmed initial prompt, got %q", opts.InitialPrompt)
	}
}

type failingControlSocketListener struct{}

func (failingControlSocketListener) Listen() error { return errors.New("bind failed") }

func TestListenAndPublishControlSocketDoesNotPublishBindFailure(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	published := false
	err := listenAndPublishControlSocket("bind-failure-test", failingControlSocketListener{}, func(tx *a2a.PeerHandleTransaction) error {
		published = true
		return nil
	})
	if err == nil {
		t.Fatal("expected bind error")
	}
	if published {
		t.Fatal("publisher called after bind failure")
	}
}

func TestListenAndPublishControlSocketPublishesDialableSocket(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	sockPath := filepath.Join(t.TempDir(), "peer.ctrl")
	handle := "control-publication-test"
	server := attserver.NewAttached(nil, nil, 80, 24, nil, sockPath)
	t.Cleanup(server.Stop)
	t.Cleanup(func() { _ = a2a.LeaveSwarm(a2a.DefaultSwarmName, handle) })

	if err := listenAndPublishControlSocket(handle, server, func(tx *a2a.PeerHandleTransaction) error {
		return patchControlSocket(tx, handle, sockPath)
	}); err != nil {
		t.Fatalf("listen and publish: %v", err)
	}

	peer, err := a2a.GetPeer(a2a.DefaultSwarmName, handle)
	if err != nil {
		t.Fatalf("get published peer: %v", err)
	}
	if peer == nil || peer.ControlSocket != sockPath {
		t.Fatalf("published control socket: peer=%#v want %q", peer, sockPath)
	}
	conn, err := net.Dial("unix", peer.ControlSocket)
	if err != nil {
		t.Fatalf("published socket was not immediately dialable: %v", err)
	}
	_ = conn.Close()
}

// TestListenAndPublishControlSocketRollsBackSocketOnPublicationFailure proves
// the identity-checked Stop-before-release contract: when publish fails
// AFTER a real, successful bind, listenAndPublishControlSocket removes the
// just-bound socket (via the production AttachedServer.Stop, not a fake)
// before returning — so a caller never sees "success" but also never leaks a
// bound-and-abandoned socket for a later same-handle attempt to trip over.
// The failure here is genuine: a forged presence file whose internal Handle
// field does not match the filename it is stored under, so tx.Publish()
// legitimately rejects it (Publish's own handle-mismatch guard) — no
// production behavior is faked to produce this error.
func TestListenAndPublishControlSocketRollsBackSocketOnPublicationFailure(t *testing.T) {
	withTempHome(t)
	const handle = "rollback-publication-test"
	peersDir := filepath.Join(a2a.SwarmPath(a2a.DefaultSwarmName), "peers")
	if err := os.MkdirAll(peersDir, 0o700); err != nil {
		t.Fatalf("mkdir peers dir: %v", err)
	}
	forged := filepath.Join(peersDir, handle+".json")
	if err := os.WriteFile(forged, []byte(`{"handle":"someone-else","pid":1,"status":"idle"}`), 0o600); err != nil {
		t.Fatalf("forge mismatched presence: %v", err)
	}
	sockPath := filepath.Join(peersDir, handle+".ctrl")
	server := attserver.NewAttached(nil, nil, 80, 24, nil, sockPath)
	t.Cleanup(server.Stop)

	err := listenAndPublishControlSocket(handle, server, func(tx *a2a.PeerHandleTransaction) error {
		existing, gerr := tx.Get()
		if gerr != nil {
			return gerr
		}
		if existing == nil {
			return fmt.Errorf("expected forged presence to be readable")
		}
		existing.ControlSocket = sockPath
		return tx.Publish(*existing)
	})
	if err == nil || !strings.Contains(err.Error(), "does not match locked handle") {
		t.Fatalf("listenAndPublishControlSocket() error = %v, want handle-mismatch publication failure", err)
	}
	if _, serr := os.Lstat(sockPath); !errors.Is(serr, os.ErrNotExist) {
		t.Fatalf("bound socket was not rolled back after publication failure: %v", serr)
	}
	data, rerr := os.ReadFile(forged)
	if rerr != nil || !strings.Contains(string(data), "someone-else") {
		t.Fatalf("forged presence file mutated despite rejected publish: data=%q err=%v", data, rerr)
	}
}

func TestPatchControlSocketRejectsForeignPresence(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	handle := "foreign-owner-test"
	peer := a2a.PeerPresence{Handle: handle, PID: os.Getpid() + 1, Status: "active", ControlSocket: "/original"}
	if err := a2a.JoinSwarm(a2a.DefaultSwarmName, peer); err != nil {
		t.Fatalf("join foreign peer: %v", err)
	}
	t.Cleanup(func() { _ = a2a.LeaveSwarm(a2a.DefaultSwarmName, handle) })

	err := a2a.WithPeerHandleTransaction(a2a.DefaultSwarmName, handle, func(tx *a2a.PeerHandleTransaction) error {
		return patchControlSocket(tx, handle, "/replacement")
	})
	if err == nil {
		t.Fatal("patchControlSocket accepted a foreign-owned presence")
	}
	remaining, err := a2a.GetPeer(a2a.DefaultSwarmName, handle)
	if err != nil || remaining == nil || remaining.ControlSocket != peer.ControlSocket {
		t.Fatalf("foreign presence changed: %#v, %v", remaining, err)
	}
}

func TestLeaveOwnedPeerPreservesReplacementPresence(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	handle := "replacement-owner-test"
	peer := a2a.PeerPresence{Handle: handle, PID: os.Getpid() + 1, Status: "active"}
	if err := a2a.JoinSwarm(a2a.DefaultSwarmName, peer); err != nil {
		t.Fatalf("join replacement peer: %v", err)
	}
	t.Cleanup(func() { _ = a2a.LeaveSwarm(a2a.DefaultSwarmName, handle) })

	if leaveOwnedPeer(handle) {
		t.Fatal("leaveOwnedPeer removed a presence owned by another process")
	}
	remaining, err := a2a.GetPeer(a2a.DefaultSwarmName, handle)
	if err != nil || remaining == nil || remaining.PID != peer.PID {
		t.Fatalf("replacement presence = %#v, %v", remaining, err)
	}
}
