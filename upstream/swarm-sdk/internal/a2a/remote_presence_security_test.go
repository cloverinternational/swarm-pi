package a2a_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sdka2a "github.com/Swarm-Code/mono/swarm-sdk/a2a"
	internala2a "github.com/Swarm-Code/mono/swarm-sdk/internal/a2a"
)

func TestPublicSDKRemotePresenceIsCapabilityFree(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SWARM_HOME", root)

	const (
		swarm      = "public-remote-security"
		handle     = "00112233445566778899aabbccddeeff~0123456789abcdef0123456789abcdef"
		card       = "https://card.invalid/public-sdk-sentinel"
		token      = "public-sdk-token-sentinel"
		serve      = "https://serve.invalid/public-sdk-sentinel"
		endpoint   = "https://endpoint.invalid/public-sdk-sentinel"
		socket     = "/tmp/public-sdk-control-sentinel.sock"
		process    = "public-sdk-process-sentinel"
		executable = "/tmp/public-sdk-executable-sentinel"
		addedBy    = "public-sdk-added-by-host-sentinel"
		version    = "version-public-sdk-sentinel"
		vcsRev     = "public-sdk-vcs-revision-sentinel"
	)
	peer := sdka2a.PeerPresence{
		Handle:        handle,
		Name:          "Public Remote",
		PID:           987654321,
		EndpointURL:   endpoint,
		CardURL:       card,
		Workspace:     "/display/workspace/public-sdk-sentinel",
		Model:         "model-public-sdk-sentinel",
		Status:        "active",
		CurrentTask:   "task-public-sdk-sentinel",
		StartedAt:     time.Now().Add(-time.Minute).UTC(),
		Type:          sdka2a.PeerTypeRemote,
		AddedBy:       addedBy,
		ControlSocket: socket,
		ServeURL:      serve,
		Version:       version,
		BinaryModTime: time.Now().Add(-time.Hour).UTC(),
		InstanceToken: token,
		ProcessStart:  process,
		Executable:    executable,
		SchemaVersion: internala2a.CurrentPresenceSchemaVersion,
		BuildProvenance: internala2a.BuildProvenance{
			Available:   true,
			GoVersion:   "go1.26.2",
			MainVersion: "v0.0.0-public-sdk-sentinel",
			VCSRevision: vcsRev,
		},
	}
	if err := sdka2a.JoinSwarm(swarm, peer); err != nil {
		t.Fatalf("public JoinSwarm: %v", err)
	}

	path := filepath.Join(root, "swarms", swarm, "peers", handle+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read public SDK persisted peer: %v", err)
	}
	for _, value := range []string{
		card, token, serve, endpoint, socket, process, executable, addedBy, version,
		"Public Remote", "/display/workspace/public-sdk-sentinel",
		"model-public-sdk-sentinel", "task-public-sdk-sentinel", "987654321",
		vcsRev, "v0.0.0-public-sdk-sentinel",
	} {
		if strings.Contains(string(data), value) {
			t.Fatalf("public SDK persisted forbidden sentinel %q: %s", value, data)
		}
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("decode public SDK persisted keys: %v", err)
	}
	for _, key := range []string{
		"pid", "card_url", "instance_token", "serve_url", "endpoint_url",
		"control_socket", "binary_mtime", "process_start", "executable",
		"added_by", "version", "name", "workspace", "model", "current_task",
		"started_at", "added_at",
		"schema_version", "build_provenance",
	} {
		if _, ok := raw[key]; ok {
			t.Fatalf("public SDK persisted forbidden key %q: %s", key, data)
		}
	}

	got, err := sdka2a.GetPeer(swarm, handle)
	if err != nil {
		t.Fatalf("public GetPeer: %v", err)
	}
	if got == nil {
		t.Fatal("public GetPeer returned nil remote peer")
	}
	assertPublicRemoteCapabilityFree(t, *got)

	peers, err := sdka2a.ListPeers(swarm)
	if err != nil {
		t.Fatalf("public ListPeers: %v", err)
	}
	if len(peers) != 1 {
		t.Fatalf("public ListPeers returned %d peers, want 1", len(peers))
	}
	assertPublicRemoteCapabilityFree(t, peers[0])
	if peers[0].Status != peer.Status {
		t.Fatalf("public remote projection lost opaque liveness status: %+v", peers[0])
	}
}

func TestPublicSDKLegacyRemoteReadsAreSanitizedWithoutChangingLocalPeers(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SWARM_HOME", root)

	const swarm = "public-legacy-security"
	peersDir := filepath.Join(root, "swarms", swarm, "peers")
	if err := os.MkdirAll(peersDir, 0o700); err != nil {
		t.Fatalf("create peers directory: %v", err)
	}

	legacyRemote := sdka2a.PeerPresence{
		Handle:        "ffeeddccbbaa99887766554433221100~legacy-original-local-handle",
		Name:          "Legacy Remote",
		PID:           987654321,
		EndpointURL:   "https://endpoint.invalid/legacy-read-sentinel",
		CardURL:       "https://card.invalid/legacy-read-sentinel",
		Status:        "active",
		LastSeenAt:    time.Now().UTC(),
		Type:          sdka2a.PeerTypeRemote,
		AddedBy:       "legacy-added-by-host-sentinel",
		ControlSocket: "/tmp/legacy-read-control-sentinel.sock",
		ServeURL:      "https://serve.invalid/legacy-read-sentinel",
		Version:       "legacy-binary-version-sentinel",
		BinaryModTime: time.Now().Add(-time.Hour).UTC(),
		InstanceToken: "legacy-read-token-sentinel",
		ProcessStart:  "legacy-read-process-sentinel",
		Executable:    "/tmp/legacy-read-executable-sentinel",
	}
	data, err := json.Marshal(legacyRemote)
	if err != nil {
		t.Fatalf("marshal legacy remote: %v", err)
	}
	if err := os.WriteFile(filepath.Join(peersDir, legacyRemote.Handle+".json"), data, 0o600); err != nil {
		t.Fatalf("write legacy remote: %v", err)
	}

	got, err := sdka2a.GetPeer(swarm, legacyRemote.Handle)
	if err != nil {
		t.Fatalf("public GetPeer legacy remote: %v", err)
	}
	if got == nil {
		t.Fatal("public GetPeer returned nil legacy remote")
	}
	assertPublicRemoteCapabilityFree(t, *got)
	if got.Handle != "" {
		t.Fatalf("legacy remote read exposed original handle %q", got.Handle)
	}
	peers, err := sdka2a.ListPeers(swarm)
	if err != nil {
		t.Fatalf("public ListPeers legacy remote: %v", err)
	}
	if len(peers) != 1 {
		t.Fatalf("public ListPeers returned %d legacy peers, want 1", len(peers))
	}
	assertPublicRemoteCapabilityFree(t, peers[0])

	local := sdka2a.PeerPresence{
		Handle:        "local-preserved",
		PID:           os.Getpid(),
		EndpointURL:   "http://127.0.0.1:49001/local-endpoint",
		CardURL:       "http://127.0.0.1:49002/local-card",
		Status:        "active",
		Type:          sdka2a.PeerTypeLocal,
		AddedBy:       "local-added-by-preserved",
		ControlSocket: "/tmp/local-preserved.ctrl",
		ServeURL:      "http://127.0.0.1:49003/local-serve",
		Version:       "local-version-preserved",
		InstanceToken: "local-token-preserved",
		ProcessStart:  "local-process-preserved",
		Executable:    "/tmp/local-executable-preserved",
	}
	if err := sdka2a.JoinSwarm(swarm, local); err != nil {
		t.Fatalf("public JoinSwarm local peer: %v", err)
	}
	gotLocal, err := sdka2a.GetPeer(swarm, local.Handle)
	if err != nil {
		t.Fatalf("public GetPeer local peer: %v", err)
	}
	if gotLocal == nil {
		t.Fatal("public GetPeer returned nil local peer")
	}
	if gotLocal.PID != local.PID || gotLocal.EndpointURL != local.EndpointURL ||
		gotLocal.CardURL != local.CardURL || gotLocal.ControlSocket != local.ControlSocket ||
		gotLocal.ServeURL != local.ServeURL || gotLocal.InstanceToken != local.InstanceToken ||
		gotLocal.ProcessStart != local.ProcessStart || gotLocal.Executable != local.Executable ||
		gotLocal.AddedBy != local.AddedBy || gotLocal.Version != local.Version {
		t.Fatalf("local peer capabilities changed: got %+v, want %+v", *gotLocal, local)
	}
}

func assertPublicRemoteCapabilityFree(t *testing.T, peer sdka2a.PeerPresence) {
	t.Helper()
	if peer.Name != "" || peer.Workspace != "" || peer.Model != "" ||
		peer.CurrentTask != "" || !peer.StartedAt.IsZero() || !peer.AddedAt.IsZero() ||
		peer.PID != 0 || peer.CardURL != "" || peer.InstanceToken != "" ||
		peer.ServeURL != "" || peer.EndpointURL != "" || peer.ControlSocket != "" ||
		!peer.BinaryModTime.IsZero() || peer.ProcessStart != "" || peer.Executable != "" ||
		peer.AddedBy != "" || peer.Version != "" ||
		!peer.SchemaVersion.IsLegacy() || peer.BuildProvenance != (internala2a.BuildProvenance{}) {
		t.Fatalf("public SDK exposed remote capability or operation authority: %+v", peer)
	}
}

// TestPublicSDKBareLegacyRemoteHandleNeverProjectsOrPersists proves the
// removed bare/legacy-handle fallback in remotePresenceProjection: a
// PeerTypeRemote record whose Handle is not a valid opaqueRemoteHandle
// (strict origin~alias, both fixed-length lowercase hex) must always project
// to an empty handle — even when the record is genuinely liveness-only, with
// every other field left at zero value (no Name, PID, URLs, tokens, or
// capability timestamps). The fixture is written as a raw JSON map directly
// into the peers directory — bypassing both JoinSwarm (which now rejects a
// non-opaque remote handle outright) and PeerPresence's own MarshalJSON
// (which would otherwise sanitize the handle before it ever reached disk) —
// so the on-disk file genuinely carries the bare handle, exactly like a
// legacy/hostile writer that never ran the current code's projection.
func TestPublicSDKBareLegacyRemoteHandleNeverProjectsOrPersists(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SWARM_HOME", root)

	const (
		swarm      = "public-bare-legacy-security"
		bareHandle = "bare-legacy-nonopaque-handle-sentinel-r10b-7f2c9a41"
	)
	peersDir := filepath.Join(root, "swarms", swarm, "peers")
	if err := os.MkdirAll(peersDir, 0o700); err != nil {
		t.Fatalf("create peers directory: %v", err)
	}

	// A genuinely liveness-only legacy record: only handle, type, and the
	// liveness clock are present. Everything else is absent (zero value on
	// decode), which is exactly the case the OLD fallback carved out an
	// exception for.
	raw := map[string]any{
		"handle":       bareHandle,
		"type":         string(sdka2a.PeerTypeRemote),
		"last_seen_at": time.Now().UTC().Format(time.RFC3339Nano),
	}
	data, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("marshal bare legacy remote fixture: %v", err)
	}
	if !strings.Contains(string(data), bareHandle) {
		t.Fatalf("test fixture setup failed to persist bare handle on disk: %s", data)
	}
	path := filepath.Join(peersDir, bareHandle+".json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write bare legacy remote fixture: %v", err)
	}

	got, err := sdka2a.GetPeer(swarm, bareHandle)
	if err != nil {
		t.Fatalf("public GetPeer bare legacy remote: %v", err)
	}
	if got == nil {
		t.Fatal("public GetPeer returned nil bare legacy remote")
	}
	if got.Handle != "" {
		t.Fatalf("public GetPeer exposed non-opaque bare/legacy handle %q", got.Handle)
	}
	assertPublicRemoteCapabilityFree(t, *got)

	peers, err := sdka2a.ListPeers(swarm)
	if err != nil {
		t.Fatalf("public ListPeers bare legacy remote: %v", err)
	}
	if len(peers) != 1 {
		t.Fatalf("public ListPeers returned %d bare legacy peers, want 1", len(peers))
	}
	if peers[0].Handle != "" {
		t.Fatalf("public ListPeers exposed non-opaque bare/legacy handle %q", peers[0].Handle)
	}
	assertPublicRemoteCapabilityFree(t, peers[0])

	// Force a writer (UpdateStatus) to read, project, and rewrite this exact
	// on-disk record. The rewritten file must never persist the bare literal
	// — only the projected (empty) handle.
	if err := sdka2a.UpdateStatus(swarm, bareHandle, "active", "should-be-dropped-for-remote"); err != nil {
		t.Fatalf("public UpdateStatus bare legacy remote: %v", err)
	}
	rewritten, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read rewritten bare legacy remote file: %v", err)
	}
	if strings.Contains(string(rewritten), bareHandle) {
		t.Fatalf("rewritten peer file leaked bare/legacy handle: %s", rewritten)
	}
	if !strings.Contains(string(rewritten), `"handle": ""`) {
		t.Fatalf("rewritten peer file did not persist an empty projected handle: %s", rewritten)
	}
}
