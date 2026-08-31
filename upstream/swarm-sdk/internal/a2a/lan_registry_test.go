package a2a

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	testOriginA = "00112233445566778899aabbccddeeff"
	testOriginB = "ffeeddccbbaa99887766554433221100"
)

// newFrame builds a peer-sync frame as if broadcast by another registry.
func newFrame(origin, swarm string, peers ...PeerPresence) []byte {
	wirePeers := append([]PeerPresence(nil), peers...)
	for i := range wirePeers {
		if !validAliasID(wirePeers[i].Handle) {
			wirePeers[i].Handle = fmt.Sprintf("%032x", i+1)
		}
	}
	f := peerSyncFrame{
		Service: peerSyncService,
		Version: peerSyncVersion,
		Swarm:   swarm,
		Origin:  origin,
		Sent:    time.Now().Unix(),
		Peers:   wirePeers,
	}
	b, _ := json.Marshal(f)
	return b
}

func TestMarshalPeerSyncFrame_OmitsCapabilities(t *testing.T) {
	const (
		cardSentinel       = "https://card.invalid/wire-sentinel"
		tokenSentinel      = "wire-instance-token-sentinel"
		serveSentinel      = "http://127.0.0.1:39001/wire-serve"
		endpointSentinel   = "http://127.0.0.1:39002/wire-endpoint"
		socketSentinel     = "/tmp/wire-control-sentinel.sock"
		processSentinel    = "wire-process-start-sentinel"
		executableSentinel = "/tmp/wire-executable-sentinel"
		addedBySentinel    = "wire-added-by-host-sentinel"
		versionSentinel    = "wire-binary-version-sentinel"
		nameSentinel       = "wire-original-handle-and-name-sentinel"
		workspaceSentinel  = "/wire/workspace-sentinel"
		modelSentinel      = "wire-model-sentinel"
		taskSentinel       = "wire-current-task-sentinel"
	)
	startedSentinel := time.Date(2001, 2, 3, 4, 5, 6, 0, time.UTC)
	addedSentinel := time.Date(2007, 8, 9, 10, 11, 12, 0, time.UTC)
	frame := peerSyncFrame{
		Service: peerSyncService,
		Version: peerSyncVersion,
		Swarm:   DefaultSwarmName,
		Origin:  testOriginA,
		Sent:    123,
		Peers: []PeerPresence{{
			Handle:        fmt.Sprintf("%032x", 1),
			Name:          nameSentinel,
			PID:           987654321,
			CardURL:       cardSentinel,
			ServeURL:      serveSentinel,
			EndpointURL:   endpointSentinel,
			ControlSocket: socketSentinel,
			Workspace:     workspaceSentinel,
			Model:         modelSentinel,
			Status:        "active",
			CurrentTask:   taskSentinel,
			StartedAt:     startedSentinel,
			Type:          PeerTypeRemote,
			AddedBy:       addedBySentinel,
			AddedAt:       addedSentinel,
			Version:       versionSentinel,
			InstanceToken: tokenSentinel,
			ProcessStart:  processSentinel,
			Executable:    executableSentinel,
		}},
	}

	data, err := marshalPeerSyncFrame(frame)
	if err != nil {
		t.Fatalf("marshalPeerSyncFrame: %v", err)
	}
	var raw struct {
		Peers []map[string]json.RawMessage `json:"peers"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal raw frame: %v", err)
	}
	if len(raw.Peers) != 1 {
		t.Fatalf("wire peers = %d, want 1", len(raw.Peers))
	}
	for _, key := range []string{
		"pid", "card_url", "serve_url", "endpoint_url", "control_socket",
		"instance_token", "process_start", "executable", "binary_mtime",
		"added_by", "version", "name", "workspace", "model", "current_task",
		"started_at", "added_at",
	} {
		if _, ok := raw.Peers[0][key]; ok {
			t.Errorf("unauthenticated LAN peer contains forbidden key %q: %s", key, data)
		}
	}
	for _, value := range []string{
		cardSentinel, tokenSentinel, serveSentinel, endpointSentinel,
		socketSentinel, processSentinel, executableSentinel, addedBySentinel,
		versionSentinel, nameSentinel, workspaceSentinel, modelSentinel,
		taskSentinel, startedSentinel.Format(time.RFC3339),
		addedSentinel.Format(time.RFC3339), "987654321",
	} {
		if strings.Contains(string(data), value) {
			t.Errorf("unauthenticated LAN frame contains forbidden sentinel %q: %s", value, data)
		}
	}
	if strings.Contains(string(data), `"host"`) {
		t.Errorf("LAN frame retained hostname wire key: %s", data)
	}
	for _, key := range []string{"handle", "status", "type", "last_seen_at"} {
		if _, ok := raw.Peers[0][key]; !ok {
			t.Errorf("LAN peer lost metadata key %q: %s", key, data)
		}
	}
}

// TestIngestFrame_PersistsRemotePeer verifies that a frame from another host is
// ingested, its handle is namespaced, and ListPeers surfaces it as a remote
// peer — the core of cross-device discovery. Per Phase 01 CONTRACT.md R6,
// the unauthenticated LAN ingest boundary must never persist the
// attacker-controllable capability fields — see
// TestIngestFrame_ClearsRemoteCapabilitiesBeforePersistence below for the
// dedicated regression covering that boundary in depth.
func TestIngestFrame_PersistsRemotePeer(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	reg := &lanRegistry{swarm: DefaultSwarmName, origin: testOriginA}
	reg.ingestFrame(newFrame(testOriginB, DefaultSwarmName, PeerPresence{
		Handle:      "daemon-42",
		Name:        "Swarm Desktop",
		EndpointURL: "http://192.168.1.9:9000",
		Status:      "active",
		Type:        PeerTypeDaemon, // will be forced to remote on ingest
	}))

	peers, err := ListPeers(DefaultSwarmName)
	if err != nil {
		t.Fatalf("ListPeers: %v", err)
	}
	if len(peers) != 1 {
		t.Fatalf("expected 1 remote peer, got %d", len(peers))
	}
	got := peers[0]
	if got.Handle != testOriginB+"~"+fmt.Sprintf("%032x", 1) {
		t.Errorf("handle not namespaced: got %q", got.Handle)
	}
	if got.Type != PeerTypeRemote {
		t.Errorf("ingested peer type = %q, want remote", got.Type)
	}
	if got.AddedBy != "" {
		t.Errorf("AddedBy must be cleared, got %q", got.AddedBy)
	}
	// R5: the unauthenticated ingest boundary must clear EndpointURL before
	// persistence, not merely leave it "unmangled" — the persisted/listed
	// record must carry an empty URL, never the inbound value verbatim.
	if got.EndpointURL != "" {
		t.Errorf("EndpointURL must be cleared at the unauthenticated ingest boundary, got %q", got.EndpointURL)
	}
	if got.Name != "" {
		t.Errorf("unauthenticated remote Name must be opaque: got %q", got.Name)
	}
}

// TestIngestFrame_ClearsRemoteCapabilitiesBeforePersistence is the dedicated Phase 01
// CONTRACT.md R6 regression for the unauthenticated LAN ingest boundary: a
// frame arriving over UDP broadcast is, by construction, attacker
// controllable (any host on the LAN broadcast segment can spoof a
// peerSyncService frame naming any handle, name, and URLs it likes). Before
// Phase 02 adds signed/authenticated endpoint identity, ingestFrame must
// never let such a frame cause a client to later dial an attacker-chosen
// capability — so this proves ServeURL, EndpointURL, and ControlSocket are empty in
// the persisted/listed remote record (not merely "attacker value present but
// inert" — actually cleared before the first write), while bounded
// non-endpoint presence metadata (Handle, Name, Status,
// LastSeenAt-derived liveness) required for Phase 01 cross-device display
// survives untouched.
func TestIngestFrame_ClearsRemoteCapabilitiesBeforePersistence(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	const (
		attackerServeURL    = "http://attacker.example:9999/rpc"
		attackerEndpointURL = "http://attacker.example:9999/a2a"
		attackerSocket      = "/tmp/attacker-controlled.sock"
		attackerCardURL     = "https://attacker.example/card-sentinel"
		attackerToken       = "attacker-instance-token-sentinel"
		attackerProcess     = "attacker-process-start-sentinel"
		attackerExecutable  = "/tmp/attacker-executable-sentinel"
		attackerAddedBy     = "attacker-host-sentinel"
		attackerVersion     = "attacker-binary-version-sentinel"
	)

	reg := &lanRegistry{swarm: DefaultSwarmName, origin: testOriginA}
	reg.ingestFrame(newFrame(testOriginB, DefaultSwarmName, PeerPresence{
		Handle:        "daemon-evil",
		Name:          "Totally Legit Desktop",
		PID:           987654321,
		ServeURL:      attackerServeURL,
		EndpointURL:   attackerEndpointURL,
		ControlSocket: attackerSocket,
		CardURL:       attackerCardURL,
		Status:        "active",
		Type:          PeerTypeDaemon,
		AddedBy:       attackerAddedBy,
		Version:       attackerVersion,
		InstanceToken: attackerToken,
		ProcessStart:  attackerProcess,
		Executable:    attackerExecutable,
	}))

	// Check both the immediate persisted file AND the ListPeers-surfaced
	// record, since a consumer could plausibly read either path.
	peersDir := filepath.Join(SwarmPath(DefaultSwarmName), "peers")
	entries, err := os.ReadDir(peersDir)
	if err != nil {
		t.Fatalf("read peers dir: %v", err)
	}
	var found bool
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(peersDir, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		if !strings.Contains(string(data), testOriginB+"~"+fmt.Sprintf("%032x", 1)) {
			continue
		}
		found = true
		for _, value := range []string{
			attackerServeURL, attackerEndpointURL, attackerSocket, attackerCardURL,
			attackerToken, attackerProcess, attackerExecutable, attackerAddedBy,
			attackerVersion, "987654321",
		} {
			if strings.Contains(string(data), value) {
				t.Fatalf("persisted remote-peer file contains forbidden sentinel %q: %s", value, data)
			}
		}
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(data, &raw); err != nil {
			t.Fatalf("decode persisted remote peer keys: %v", err)
		}
		for _, key := range []string{
			"pid", "card_url", "serve_url", "endpoint_url", "control_socket",
			"instance_token", "process_start", "executable", "binary_mtime",
			"added_by", "version",
		} {
			if _, ok := raw[key]; ok {
				t.Fatalf("persisted remote-peer file contains forbidden key %q: %s", key, data)
			}
		}
		var persisted PeerPresence
		if err := json.Unmarshal(data, &persisted); err != nil {
			t.Fatalf("decode persisted remote peer: %v", err)
		}
		if persisted.PID != 0 || persisted.CardURL != "" || persisted.ServeURL != "" ||
			persisted.EndpointURL != "" || persisted.ControlSocket != "" ||
			persisted.InstanceToken != "" || persisted.ProcessStart != "" ||
			persisted.Executable != "" || !persisted.BinaryModTime.IsZero() ||
			persisted.AddedBy != "" || persisted.Version != "" {
			t.Fatalf("persisted capabilities not cleared: %+v", persisted)
		}
	}
	if !found {
		t.Fatalf("expected an on-disk opaque remote peer record, found none among %v", entries)
	}

	peers, err := ListPeers(DefaultSwarmName)
	if err != nil {
		t.Fatalf("ListPeers: %v", err)
	}
	if len(peers) != 1 {
		t.Fatalf("expected 1 remote peer, got %d", len(peers))
	}
	got := peers[0]
	if got.ServeURL != "" {
		t.Errorf("listed ServeURL must be cleared, got %q", got.ServeURL)
	}
	if got.EndpointURL != "" {
		t.Errorf("listed EndpointURL must be cleared, got %q", got.EndpointURL)
	}
	if got.ControlSocket != "" {
		t.Errorf("listed ControlSocket must be cleared, got %q", got.ControlSocket)
	}
	if got.PID != 0 || got.CardURL != "" || got.InstanceToken != "" ||
		got.ProcessStart != "" || got.Executable != "" || !got.BinaryModTime.IsZero() {
		t.Errorf("listed remote peer retained process/authority material: %+v", got)
	}
	if got.Handle != testOriginB+"~"+fmt.Sprintf("%032x", 1) {
		t.Errorf("handle not namespaced: got %q", got.Handle)
	}
	if got.Name != "" {
		t.Errorf("unauthenticated remote Name must be cleared: got %q", got.Name)
	}
	if got.Status != "active" {
		t.Errorf("bounded non-endpoint presence metadata (Status) must survive: got %q", got.Status)
	}
	if got.Type != PeerTypeRemote {
		t.Errorf("ingested peer type = %q, want remote", got.Type)
	}
	if got.AddedBy != "" || got.Version != "" {
		t.Errorf("remote host/binary evidence must be cleared: %+v", got)
	}
	if got.LastSeenAt.IsZero() {
		t.Errorf("LastSeenAt must still be populated for liveness/TTL pruning")
	}
}

func remotePeerWithCapabilitySentinels(handle, sentinel string) PeerPresence {
	if opaqueRemoteHandle(handle) == "" {
		handle = testOriginB + "~" + strings.Repeat("a", aliasIDLen)
	}
	return PeerPresence{
		Handle:        handle,
		Name:          "Remote " + sentinel,
		PID:           987654321,
		EndpointURL:   "https://endpoint.invalid/" + sentinel,
		CardURL:       "https://card.invalid/" + sentinel,
		Workspace:     "/safe/display/workspace/" + sentinel,
		Model:         "model-" + sentinel,
		Status:        "active",
		CurrentTask:   "task-" + sentinel,
		StartedAt:     time.Now().Add(-time.Minute).UTC(),
		LastSeenAt:    time.Now().UTC(),
		Type:          PeerTypeRemote,
		AddedBy:       "host-" + sentinel,
		ControlSocket: "/tmp/control-" + sentinel + ".sock",
		ServeURL:      "https://serve.invalid/" + sentinel,
		Version:       "version-" + sentinel,
		BinaryModTime: time.Now().Add(-time.Hour).UTC(),
		InstanceToken: "token-" + sentinel,
		ProcessStart:  "process-" + sentinel,
		Executable:    "/tmp/executable-" + sentinel,
	}
}

func assertRemotePresenceCapabilityFree(t *testing.T, peer PeerPresence) {
	t.Helper()
	if peer.Name != "" || peer.Workspace != "" || peer.Model != "" ||
		peer.CurrentTask != "" || !peer.StartedAt.IsZero() || !peer.AddedAt.IsZero() ||
		peer.PID != 0 || peer.EndpointURL != "" || peer.CardURL != "" ||
		peer.ControlSocket != "" || peer.ServeURL != "" ||
		!peer.BinaryModTime.IsZero() || peer.InstanceToken != "" ||
		peer.ProcessStart != "" || peer.Executable != "" ||
		peer.AddedBy != "" || peer.Version != "" {
		t.Fatalf("remote presence retained a capability or operation authority: %+v", peer)
	}
}

func assertPersistedRemoteCapabilityFree(t *testing.T, swarm, handle, sentinel string) {
	t.Helper()
	path := filepath.Join(SwarmPath(swarm), "peers", handle+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read persisted remote peer: %v", err)
	}
	for _, value := range []string{
		"https://endpoint.invalid/" + sentinel,
		"https://card.invalid/" + sentinel,
		"https://serve.invalid/" + sentinel,
		"/tmp/control-" + sentinel + ".sock",
		"token-" + sentinel,
		"process-" + sentinel,
		"/tmp/executable-" + sentinel,
		"host-" + sentinel,
		"version-" + sentinel,
		"Remote " + sentinel,
		"/safe/display/workspace/" + sentinel,
		"model-" + sentinel,
		"task-" + sentinel,
		"987654321",
	} {
		if strings.Contains(string(data), value) {
			t.Fatalf("persisted remote peer contains forbidden sentinel %q: %s", value, data)
		}
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("decode persisted remote keys: %v", err)
	}
	for _, key := range []string{
		"pid", "endpoint_url", "card_url", "control_socket", "serve_url",
		"binary_mtime", "instance_token", "process_start", "executable",
		"added_by", "version", "name", "workspace", "model", "current_task",
		"started_at", "added_at",
	} {
		if _, ok := raw[key]; ok {
			t.Fatalf("persisted remote peer contains forbidden key %q: %s", key, data)
		}
	}
	var peer PeerPresence
	if err := json.Unmarshal(data, &peer); err != nil {
		t.Fatalf("decode persisted remote peer: %v", err)
	}
	assertRemotePresenceCapabilityFree(t, peer)
}

func TestRemotePresence_AllPersistenceWritersSanitize(t *testing.T) {
	const swarm = "remote-writer-security"

	t.Run("JoinSwarm", func(t *testing.T) {
		t.Setenv("SWARM_HOME", t.TempDir())
		peer := remotePeerWithCapabilitySentinels("join-remote", "join-sentinel")
		if err := JoinSwarm(swarm, peer); err != nil {
			t.Fatalf("JoinSwarm: %v", err)
		}
		assertPersistedRemoteCapabilityFree(t, swarm, peer.Handle, "join-sentinel")
	})

	t.Run("transaction publication refuses remote", func(t *testing.T) {
		// PeerHandleTransaction only ever holds the per-handle lock, never the
		// retention lock upsertRemotePeer requires — a remote Publish here
		// could create an uncapped record and bypass the opaque-handle and
		// local/daemon replacement checks. Publish must refuse PeerTypeRemote
		// outright and leave no file behind, rather than "sanitize and write".
		t.Setenv("SWARM_HOME", t.TempDir())
		peer := remotePeerWithCapabilitySentinels("transaction-remote", "transaction-sentinel")
		if err := secureDir(filepath.Join(SwarmPath(swarm), "peers")); err != nil {
			t.Fatalf("secure peers directory: %v", err)
		}
		err := WithPeerHandleTransaction(swarm, peer.Handle, func(tx *PeerHandleTransaction) error {
			return tx.Publish(peer)
		})
		if err == nil || !strings.Contains(err.Error(), "refuses PeerTypeRemote") {
			t.Fatalf("transaction publish did not refuse PeerTypeRemote: %v", err)
		}
		path := filepath.Join(SwarmPath(swarm), "peers", peer.Handle+".json")
		if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
			t.Fatalf("rejected transaction publish left a file behind: stat err=%v", statErr)
		}
	})

	t.Run("upsert", func(t *testing.T) {
		t.Setenv("SWARM_HOME", t.TempDir())
		peer := remotePeerWithCapabilitySentinels("upsert-remote", "upsert-sentinel")
		if err := upsertRemotePeer(swarm, peer); err != nil {
			t.Fatalf("upsertRemotePeer: %v", err)
		}
		assertPersistedRemoteCapabilityFree(t, swarm, peer.Handle, "upsert-sentinel")
	})

	t.Run("status rewrite", func(t *testing.T) {
		t.Setenv("SWARM_HOME", t.TempDir())
		peer := remotePeerWithCapabilitySentinels("status-remote", "status-sentinel")
		peersDir := filepath.Join(SwarmPath(swarm), "peers")
		if err := os.MkdirAll(peersDir, dirPerm); err != nil {
			t.Fatalf("create peers directory: %v", err)
		}
		data, err := json.Marshal(peer)
		if err != nil {
			t.Fatalf("marshal legacy remote peer: %v", err)
		}
		if err := os.WriteFile(filepath.Join(peersDir, peer.Handle+".json"), data, filePerm); err != nil {
			t.Fatalf("write legacy remote peer: %v", err)
		}
		if err := UpdateStatus(swarm, peer.Handle, "idle", ""); err != nil {
			t.Fatalf("UpdateStatus: %v", err)
		}
		assertPersistedRemoteCapabilityFree(t, swarm, peer.Handle, "status-sentinel")
	})
}

func TestIngestFrame_RejectsTooManyPeersWithoutFilesystemSideEffects(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	peers := make([]PeerPresence, maxAdvertisedPeers+1)
	for i := range peers {
		peers[i] = PeerPresence{Handle: fmt.Sprintf("peer-%02d", i), Status: "active"}
	}
	reg := &lanRegistry{swarm: DefaultSwarmName, origin: testOriginA}
	reg.ingestFrame(newFrame(testOriginB, DefaultSwarmName, peers...))

	if entries, err := os.ReadDir(home); err != nil {
		t.Fatalf("read HOME: %v", err)
	} else if len(entries) != 0 {
		t.Fatalf("oversized frame caused filesystem side effects under HOME: %v", entries)
	}
}

func TestIngestFrame_AcceptsMaxAdvertisedPeers(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	peers := make([]PeerPresence, maxAdvertisedPeers)
	for i := range peers {
		peers[i] = PeerPresence{
			Handle:        fmt.Sprintf("peer-%02d", i),
			Name:          fmt.Sprintf("Peer %02d", i),
			ServeURL:      "http://attacker.example/rpc",
			EndpointURL:   "http://attacker.example/a2a",
			ControlSocket: "/tmp/attacker.sock",
			Status:        "active",
		}
	}
	reg := &lanRegistry{swarm: DefaultSwarmName, origin: testOriginA}
	reg.ingestFrame(newFrame(testOriginB, DefaultSwarmName, peers...))

	peersDir := filepath.Join(SwarmPath(DefaultSwarmName), "peers")
	entries, err := os.ReadDir(peersDir)
	if err != nil {
		t.Fatalf("read peers dir: %v", err)
	}
	persistedCount := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		persistedCount++
		data, err := os.ReadFile(filepath.Join(peersDir, entry.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", entry.Name(), err)
		}
		var persisted PeerPresence
		if err := json.Unmarshal(data, &persisted); err != nil {
			t.Fatalf("decode %s: %v", entry.Name(), err)
		}
		if persisted.ServeURL != "" || persisted.EndpointURL != "" || persisted.ControlSocket != "" {
			t.Fatalf("%s retained an unauthenticated capability: %+v", entry.Name(), persisted)
		}
		if persisted.Type != PeerTypeRemote || persisted.AddedBy != "" || persisted.Version != "" ||
			!strings.HasPrefix(persisted.Handle, testOriginB+"~") || persisted.Name != "" ||
			persisted.Status != "active" || persisted.LastSeenAt.IsZero() {
			t.Fatalf("%s lost namespacing/metadata/remote typing: %+v", entry.Name(), persisted)
		}
	}
	if persistedCount != maxAdvertisedPeers {
		t.Fatalf("persisted peers = %d, want %d", persistedCount, maxAdvertisedPeers)
	}
}

// TestIngestFrame_SkipsOwnOrigin ensures a registry ignores its own broadcast echoed
// back, so we never create a self-referential remote peer.
func TestIngestFrame_SkipsOwnOrigin(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	reg := &lanRegistry{swarm: DefaultSwarmName, origin: testOriginA}
	reg.ingestFrame(newFrame(testOriginA, DefaultSwarmName, PeerPresence{
		Handle:      "daemon-1",
		EndpointURL: "http://192.168.1.9:9000",
		Type:        PeerTypeDaemon,
	}))

	peers, err := ListPeers(DefaultSwarmName)
	if err != nil {
		t.Fatalf("ListPeers: %v", err)
	}
	if len(peers) != 0 {
		t.Fatalf("own-origin frame should be ignored, got %d peers", len(peers))
	}
}

// TestIngestFrame_WrongSwarmIgnored ensures frames for a different swarm are
// dropped.
func TestIngestFrame_WrongSwarmIgnored(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	reg := &lanRegistry{swarm: DefaultSwarmName, origin: testOriginA}
	reg.ingestFrame(newFrame(testOriginB, "other-swarm", PeerPresence{
		Handle:      "daemon-1",
		EndpointURL: "http://192.168.1.9:9000",
		Type:        PeerTypeDaemon,
	}))

	peers, _ := ListPeers(DefaultSwarmName)
	if len(peers) != 0 {
		t.Fatalf("cross-swarm frame should be ignored, got %d peers", len(peers))
	}
}

func TestIngestFrame_RejectsInvalidOriginWithoutPersistence(t *testing.T) {
	cases := map[string]string{
		"empty":       "",
		"too short":   "abcd",
		"oversized":   strings.Repeat("a", originIDLen+1),
		"non-hex":     strings.Repeat("z", originIDLen),
		"uppercase":   strings.ToUpper(testOriginB),
		"host-shaped": "workstation-hostname",
	}
	for name, origin := range cases {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			reg := &lanRegistry{swarm: DefaultSwarmName, origin: testOriginA}
			reg.ingestFrame(newFrame(origin, DefaultSwarmName, PeerPresence{
				Handle: "must-not-persist",
				Name:   "safe display",
				Status: "active",
			}))
			entries, err := os.ReadDir(home)
			if err != nil {
				t.Fatalf("read HOME: %v", err)
			}
			if len(entries) != 0 {
				t.Fatalf("invalid origin %q caused filesystem side effects: %v", origin, entries)
			}
		})
	}
}

type lifecyclePacketConn struct {
	readStarted   chan struct{}
	readStopping  chan struct{}
	readExited    chan struct{}
	allowReadExit chan struct{}
	closeOnce     sync.Once
	mu            sync.Mutex
	closeCount    int
	closed        chan struct{}
}

func newLifecyclePacketConn(trackRead bool) *lifecyclePacketConn {
	conn := &lifecyclePacketConn{closed: make(chan struct{})}
	if trackRead {
		conn.readStarted = make(chan struct{})
		conn.readStopping = make(chan struct{})
		conn.readExited = make(chan struct{})
		conn.allowReadExit = make(chan struct{})
	}
	return conn
}

func (c *lifecyclePacketConn) WriteTo(p []byte, _ net.Addr) (int, error) {
	return len(p), nil
}

func (c *lifecyclePacketConn) ReadFrom([]byte) (int, net.Addr, error) {
	if c.readStarted != nil {
		c.closeOnceSignal(c.readStarted)
	}
	<-c.closed
	if c.readStopping != nil {
		c.closeOnceSignal(c.readStopping)
		<-c.allowReadExit
	}
	if c.readExited != nil {
		c.closeOnceSignal(c.readExited)
	}
	return 0, nil, net.ErrClosed
}

func (c *lifecyclePacketConn) SetReadDeadline(time.Time) error { return nil }

func (c *lifecyclePacketConn) Close() error {
	c.mu.Lock()
	c.closeCount++
	c.mu.Unlock()
	c.closeOnce.Do(func() { close(c.closed) })
	return nil
}

func (c *lifecyclePacketConn) closeOnceSignal(ch chan struct{}) {
	select {
	case <-ch:
	default:
		close(ch)
	}
}

func (c *lifecyclePacketConn) closes() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closeCount
}

func TestLANRegistryStopConcurrentCallsJoinBothLoopsAndCloseOnce(t *testing.T) {
	send := newLifecyclePacketConn(false)
	recv := newLifecyclePacketConn(true)
	advertiseStarted := make(chan struct{})
	advertiseStopping := make(chan struct{})
	advertiseExited := make(chan struct{})
	allowAdvertiseExit := make(chan struct{})
	reg := &lanRegistry{
		swarm:  DefaultSwarmName,
		origin: testOriginA,
		send:   send,
		recv:   recv,
		advertise: func(ctx context.Context) {
			close(advertiseStarted)
			<-ctx.Done()
			close(advertiseStopping)
			<-allowAdvertiseExit
			close(advertiseExited)
		},
	}
	stop := startLANRegistryLoops(context.Background(), reg)
	<-advertiseStarted
	<-recv.readStarted

	const callers = 16
	returned := make(chan struct{}, callers)
	for i := 0; i < callers; i++ {
		go func() {
			stop()
			returned <- struct{}{}
		}()
	}
	<-advertiseStopping
	<-recv.readStopping
	select {
	case <-returned:
		t.Fatal("stop returned while registry loops were still blocked")
	default:
	}
	close(allowAdvertiseExit)
	close(recv.allowReadExit)
	for i := 0; i < callers; i++ {
		<-returned
	}

	select {
	case <-advertiseExited:
	default:
		t.Fatal("stop returned before advertise loop terminated")
	}
	select {
	case <-recv.readExited:
	default:
		t.Fatal("stop returned before ingest loop terminated")
	}
	if got := send.closes(); got != 1 {
		t.Fatalf("send close count = %d, want 1", got)
	}
	if got := recv.closes(); got != 1 {
		t.Fatalf("recv close count = %d, want 1", got)
	}

	// Repeated calls after shutdown remain joined and do not repeat side effects.
	stop()
	if send.closes() != 1 || recv.closes() != 1 {
		t.Fatalf("repeated stop closed sockets again: send=%d recv=%d", send.closes(), recv.closes())
	}
}

// TestListPeers_PrunesStaleRemote verifies the RemotePeerTTL prune: a remote
// peer last seen longer ago than the TTL is reaped by ListPeers.
func TestListPeers_PrunesStaleRemote(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	// Persist a remote peer whose LastSeenAt is already stale.
	stale := PeerPresence{
		Handle:      testOriginB + "~" + strings.Repeat("9", aliasIDLen),
		EndpointURL: "http://192.168.1.9:9000",
		Type:        PeerTypeRemote,
		LastSeenAt:  time.Now().Add(-2 * RemotePeerTTL).UTC(),
	}
	if err := upsertRemotePeer(DefaultSwarmName, stale); err != nil {
		t.Fatalf("upsertRemotePeer: %v", err)
	}

	peers, err := ListPeers(DefaultSwarmName)
	if err != nil {
		t.Fatalf("ListPeers: %v", err)
	}
	if len(peers) != 0 {
		t.Fatalf("stale remote peer should be pruned, got %d", len(peers))
	}
	path := filepath.Join(SwarmPath(DefaultSwarmName), "peers", stale.Handle+".json")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("stale remote JSON still exists: %v", err)
	}

	// A fresh remote peer must survive.
	fresh := stale
	fresh.Handle = testOriginB + "~" + strings.Repeat("a", aliasIDLen)
	fresh.LastSeenAt = time.Now().UTC()
	if err := upsertRemotePeer(DefaultSwarmName, fresh); err != nil {
		t.Fatalf("upsertRemotePeer fresh: %v", err)
	}
	peers, _ = ListPeers(DefaultSwarmName)
	if len(peers) != 1 {
		t.Fatalf("fresh remote peer should survive, got %d", len(peers))
	}
}

func TestLANRegistryAliasesAreStableDistinctRandomAndBounded(t *testing.T) {
	reg := &lanRegistry{origin: testOriginA}
	seen := make(map[string]bool)
	for i := 0; i < maxAdvertisedPeers; i++ {
		handle := fmt.Sprintf("local-sensitive-handle-%d", i)
		alias := reg.aliasFor(handle)
		if !validAliasID(alias) {
			t.Fatalf("alias %d is invalid: %q", i, alias)
		}
		if alias != reg.aliasFor(handle) {
			t.Fatalf("alias for %q was not stable", handle)
		}
		if seen[alias] {
			t.Fatalf("alias collision: %q", alias)
		}
		seen[alias] = true
		publicCorrelation := fmt.Sprintf("%x", sha256.Sum256([]byte(reg.origin+handle)))
		if alias == publicCorrelation[:aliasIDLen] || strings.Contains(alias, handle) ||
			strings.Contains(alias, reg.origin) {
			t.Fatalf("alias correlates public origin/local handle: %q", alias)
		}
	}
	if alias := reg.aliasFor("beyond-bounded-state"); alias != "" {
		t.Fatalf("alias table exceeded bound: %q", alias)
	}
	if len(reg.aliases) != maxAdvertisedPeers {
		t.Fatalf("alias state = %d, want %d", len(reg.aliases), maxAdvertisedPeers)
	}
}

func TestRemoveStaleRemotePeerPreservesReplacementAndEveryControlSocket(t *testing.T) {
	t.Setenv("SWARM_HOME", t.TempDir())
	swarm := "remote-compare-delete"
	handle := testOriginB + "~" + strings.Repeat("b", aliasIDLen)
	stale := PeerPresence{
		Handle:     handle,
		Status:     "stale",
		Type:       PeerTypeRemote,
		LastSeenAt: time.Now().Add(-2 * RemotePeerTTL).UTC(),
	}
	if err := upsertRemotePeer(swarm, stale); err != nil {
		t.Fatal(err)
	}
	peersDir := filepath.Join(SwarmPath(swarm), "peers")
	path := filepath.Join(peersDir, handle+".json")
	ctrlPath := filepath.Join(peersDir, handle+".ctrl")
	if err := os.WriteFile(ctrlPath, []byte("must-survive"), filePerm); err != nil {
		t.Fatal(err)
	}

	fresh := stale
	fresh.Status = "refreshed"
	fresh.LastSeenAt = time.Now().UTC()
	freshData, err := json.Marshal(fresh)
	if err != nil {
		t.Fatal(err)
	}
	realHook := registryConditionalCleanupAfterRead
	registryConditionalCleanupAfterRead = func() {
		if err := writeFileAtomicLocked(path, freshData); err != nil {
			t.Errorf("publish replacement in correlation seam: %v", err)
		}
	}
	removed, err := removeStaleRemotePeer(swarm, stale)
	registryConditionalCleanupAfterRead = realHook
	if err != nil || removed {
		t.Fatalf("compare-delete removed refreshed replacement: removed=%v err=%v", removed, err)
	}
	got, err := GetPeer(swarm, handle)
	if err != nil || got == nil || got.Status != "refreshed" {
		t.Fatalf("refreshed replacement not preserved: got=%+v err=%v", got, err)
	}
	if data, err := os.ReadFile(ctrlPath); err != nil || string(data) != "must-survive" {
		t.Fatalf("remote cleanup touched control socket: data=%q err=%v", data, err)
	}
}

func TestRemoveStaleRemotePeerPreservesLocalAndDaemon(t *testing.T) {
	for _, typ := range []PeerType{PeerTypeLocal, PeerTypeDaemon} {
		t.Run(string(typ), func(t *testing.T) {
			t.Setenv("SWARM_HOME", t.TempDir())
			handle := testOriginB + "~" + strings.Repeat(string(typ[0]), aliasIDLen)
			peer := PeerPresence{
				Handle:        handle,
				PID:           os.Getpid(),
				Type:          typ,
				InstanceToken: "must-survive",
				LastSeenAt:    time.Now().Add(-2 * RemotePeerTTL).UTC(),
			}
			if err := JoinSwarm("preserve-non-remote", peer); err != nil {
				t.Fatal(err)
			}
			expected := peer
			expected.Type = PeerTypeRemote
			removed, err := removeStaleRemotePeer("preserve-non-remote", expected)
			if err != nil || removed {
				t.Fatalf("removed %s publication: removed=%v err=%v", typ, removed, err)
			}
			if got, _ := GetPeer("preserve-non-remote", handle); got == nil || got.Type != typ {
				t.Fatalf("%s publication was not preserved: %+v", typ, got)
			}
		})
	}
}

func TestRemoteRetentionCapRefreshAndConcurrentFlood(t *testing.T) {
	t.Setenv("SWARM_HOME", t.TempDir())
	const swarm = "bounded-remote-retention"
	var wg sync.WaitGroup
	errs := make(chan error, maxRetainedRemotePeers+32)
	for i := 0; i < maxRetainedRemotePeers+32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			peer := PeerPresence{
				Handle:     testOriginB + "~" + fmt.Sprintf("%032x", i+1),
				Status:     "active",
				Type:       PeerTypeRemote,
				LastSeenAt: time.Now().UTC(),
			}
			errs <- upsertRemotePeer(swarm, peer)
		}(i)
	}
	wg.Wait()
	close(errs)
	successes := 0
	for err := range errs {
		if err == nil {
			successes++
		}
	}
	if successes != maxRetainedRemotePeers {
		t.Fatalf("successful remote writes = %d, want cap %d", successes, maxRetainedRemotePeers)
	}
	count, err := retainedRemotePeerCount(filepath.Join(SwarmPath(swarm), "peers"))
	if err != nil || count != maxRetainedRemotePeers {
		t.Fatalf("retained remote count=%d err=%v, want %d", count, err, maxRetainedRemotePeers)
	}
	entries, err := ListPeers(swarm)
	if err != nil || len(entries) != maxRetainedRemotePeers {
		t.Fatalf("ListPeers retained=%d err=%v", len(entries), err)
	}
	refresh := entries[0]
	refresh.Status = "refreshed-at-cap"
	refresh.LastSeenAt = time.Now().Add(peerSyncInterval).UTC()
	if err := upsertRemotePeer(swarm, refresh); err != nil {
		t.Fatalf("existing refresh failed at cap: %v", err)
	}
	if got, _ := GetPeer(swarm, refresh.Handle); got == nil || got.Status != refresh.Status {
		t.Fatalf("refresh at cap was not persisted: %+v", got)
	}
}

func TestRemoteRetentionLockFailureCausesZeroMutation(t *testing.T) {
	t.Setenv("SWARM_HOME", t.TempDir())
	const swarm = "remote-lock-failure"
	peer := PeerPresence{
		Handle:     testOriginB + "~" + strings.Repeat("c", aliasIDLen),
		Status:     "original",
		Type:       PeerTypeRemote,
		LastSeenAt: time.Now().Add(-2 * RemotePeerTTL).UTC(),
	}
	if err := upsertRemotePeer(swarm, peer); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(SwarmPath(swarm), "peers", peer.Handle+".json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	realAcquire := registryAcquireHandleLock
	registryAcquireHandleLock = func(string) (func() error, error) {
		return nil, errors.New("injected remote retention lock failure")
	}
	removed, deleteErr := removeStaleRemotePeer(swarm, peer)
	refresh := peer
	refresh.Status = "mutated"
	refresh.LastSeenAt = time.Now().UTC()
	upsertErr := upsertRemotePeer(swarm, refresh)
	registryAcquireHandleLock = realAcquire
	if removed || deleteErr == nil || upsertErr == nil {
		t.Fatalf("lock failure did not fail closed: removed=%v delete=%v upsert=%v", removed, deleteErr, upsertErr)
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(before) {
		t.Fatalf("lock failure mutated record: err=%v before=%s after=%s", err, before, after)
	}
}

// TestRewriteLoopbackHost checks loopback/wildcard hosts are rewritten to the
// LAN IP while already-routable and empty URLs are handled correctly.
func TestRewriteLoopbackHost(t *testing.T) {
	cases := []struct {
		in, lan, want string
	}{
		{"http://127.0.0.1:9000", "192.168.1.5", "http://192.168.1.5:9000"},
		{"http://localhost:8080/rpc", "192.168.1.5", "http://192.168.1.5:8080/rpc"},
		{"http://0.0.0.0:7000", "192.168.1.5", "http://192.168.1.5:7000"},
		{"http://192.168.1.9:9000", "192.168.1.5", "http://192.168.1.9:9000"},
		{"", "192.168.1.5", ""},
	}
	for _, c := range cases {
		if got := rewriteLoopbackHost(c.in, c.lan); got != c.want {
			t.Errorf("rewriteLoopbackHost(%q,%q) = %q, want %q", c.in, c.lan, got, c.want)
		}
	}
}

// TestAdvertisablePeers_ProjectsCapabilityFreeRemotePresence verifies the
// pre-broadcast transform: eligible local peers become remote display/liveness
// records with no endpoint, socket, token, or process authority.
func TestAdvertisablePeers_ProjectsCapabilityFreeRemotePresence(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	// Register a local daemon peer with a loopback endpoint + control socket.
	// Use this process's real PID so ListPeers' liveness check keeps it.
	if err := JoinSwarm(DefaultSwarmName, PeerPresence{
		Handle:        "daemon-local",
		PID:           os.Getpid(),
		EndpointURL:   "http://127.0.0.1:9100",
		ControlSocket: "/tmp/daemon-local.ctrl",
		Type:          PeerTypeDaemon,
	}); err != nil {
		t.Fatalf("JoinSwarm: %v", err)
	}

	reg := &lanRegistry{swarm: DefaultSwarmName, origin: testOriginA}
	adv := reg.advertisablePeers()
	// firstLANIPv4 may return "" on a host with no routable IPv4 (some CI); in
	// that case advertisablePeers returns nil by design — skip rather than fail.
	if firstLANIPv4() == "" {
		if adv != nil {
			t.Fatalf("expected nil advertisement with no LAN IP, got %d", len(adv))
		}
		t.Skip("no routable LAN IPv4 on this host; advertise transform not exercised")
	}
	if len(adv) != 1 {
		t.Fatalf("expected 1 advertisable peer, got %d", len(adv))
	}
	p := adv[0]
	if p.Type != PeerTypeRemote {
		t.Errorf("advertised type = %q, want remote", p.Type)
	}
	assertRemotePresenceCapabilityFree(t, p)
	if p.AddedBy != "" || p.Version != "" {
		t.Errorf("advertisement retained host/binary evidence: %+v", p)
	}
}

// ─── filesystem security boundary tests ─────────────────────────────────────
//
// These exercise the unexported chokepoints in discovery.go directly (this
// file is `package a2a`), covering: malicious handle rejection, traversal,
// symlink substitution, legacy-permission migration, wrong-owner detection
// where testable, atomic writes staying inside the owned root, and cleanup
// never trusting a presence-supplied path.

// TestValidateRegistryName_RejectsMaliciousHandles covers every handle shape
// that must never reach a path.Join: empty, ".", "..", separators (both
// slash flavors so this also holds on a Windows dev box), an embedded "..",
// and a NUL byte (which would truncate a C-string-based syscall path).
func TestValidateRegistryName_RejectsMaliciousHandles(t *testing.T) {
	bad := []string{
		"",
		".",
		"..",
		"a/b",
		"a\\b",
		"/etc/passwd",
		"../etc/passwd",
		"../../secret",
		"foo/../bar",
		"foo..bar",
		"x\x00y",
	}
	for _, name := range bad {
		if err := validateRegistryName(name); err == nil {
			t.Errorf("validateRegistryName(%q) = nil, want error", name)
		}
	}

	good := []string{
		"daemon-42",
		"hostA~daemon-1",
		"agent-alpha",
		"stable-bob",
		"w1",
	}
	for _, name := range good {
		if err := validateRegistryName(name); err != nil {
			t.Errorf("validateRegistryName(%q) = %v, want nil", name, err)
		}
	}
}

// TestSanitizeSwarmName_FallsBackOnMalice ensures SwarmPath's error-less
// swarm-name sanitizer never lets a hostile swarm name through — it silently
// redirects to DefaultSwarmName instead, since SwarmPath itself cannot
// return an error to its many call sites.
func TestSanitizeSwarmName_FallsBackOnMalice(t *testing.T) {
	for _, name := range []string{"../../etc", "a/b", "", ".."} {
		if got := sanitizeSwarmName(name); got != DefaultSwarmName {
			t.Errorf("sanitizeSwarmName(%q) = %q, want %q", name, got, DefaultSwarmName)
		}
	}
	if got := sanitizeSwarmName("my-swarm"); got != "my-swarm" {
		t.Errorf("sanitizeSwarmName(%q) = %q, want unchanged", "my-swarm", got)
	}
}

// TestSwarmPath_TraversalNameStaysUnderRoot proves the public SwarmPath
// entry point itself never returns a path outside SwarmDir(), even when
// handed a swarm name built entirely from ".." traversal components.
func TestSwarmPath_TraversalNameStaysUnderRoot(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := SwarmDir()
	got := SwarmPath("../../../../etc")
	rel, err := filepath.Rel(root, got)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		t.Fatalf("SwarmPath with traversal name escaped root: got %q, root %q", got, root)
	}
}

// TestResolveRegistryPath_ValidNameCanonicalizes verifies the happy path:
// a valid name joins exactly as expected under dir.
func TestResolveRegistryPath_ValidNameCanonicalizes(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := filepath.Join(SwarmDir(), "default", "peers")
	got, err := resolveRegistryPath(dir, "agent-1", ".json")
	if err != nil {
		t.Fatalf("resolveRegistryPath: %v", err)
	}
	want := filepath.Join(dir, "agent-1.json")
	if got != want {
		t.Errorf("resolveRegistryPath = %q, want %q", got, want)
	}
}

// TestResolveRegistryPath_RejectsMaliciousName ensures the choke point used
// by GetPeer/JoinSwarm/LeaveSwarm/etc. refuses a traversal handle outright,
// before any path.Join happens.
func TestResolveRegistryPath_RejectsMaliciousName(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := filepath.Join(SwarmDir(), "default", "peers")
	for _, name := range []string{"../../../etc/passwd", "a/b", "..", ""} {
		if _, err := resolveRegistryPath(dir, name, ".json"); err == nil {
			t.Errorf("resolveRegistryPath(dir, %q, .json) = nil error, want rejection", name)
		}
	}
}

// TestValidateWithinRegistryRoot_RejectsOutsidePath directly exercises the
// last-line-of-defense root check writeFileAtomic relies on, independent of
// name validation — this is what protects lan_registry.go's writes (a file
// this worker does not own) even though its handle isn't pre-validated by
// this package.
func TestValidateWithinRegistryRoot_RejectsOutsidePath(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	outside := filepath.Join(t.TempDir(), "evil.json")
	if err := validateWithinRegistryRoot(outside); err == nil {
		t.Errorf("validateWithinRegistryRoot(%q) = nil, want error (outside root)", outside)
	}
	inside := filepath.Join(SwarmDir(), "default", "peers", "ok.json")
	if err := validateWithinRegistryRoot(inside); err != nil {
		t.Errorf("validateWithinRegistryRoot(%q) = %v, want nil (inside root)", inside, err)
	}
}

// TestSecureDir_CreatesFresh0700 covers the base case: a missing directory is
// created at exactly dirPerm (0700).
func TestSecureDir_CreatesFresh0700(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := filepath.Join(SwarmPath("secure-dir-create"), "peers")
	if err := secureDir(dir); err != nil {
		t.Fatalf("secureDir: %v", err)
	}
	fi, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if !fi.IsDir() {
		t.Fatalf("expected a directory at %s", dir)
	}
	if perm := fi.Mode().Perm(); perm != dirPerm {
		t.Errorf("dir perm = %o, want %o", perm, dirPerm)
	}
}

// TestSecureDir_MigratesLegacyBroadPermissions proves an existing directory
// created (by old code, or another process) at the historical 0755 is
// tightened in place to 0700 — content-preserving, ownership-preserving,
// just a permission migration.
func TestSecureDir_MigratesLegacyBroadPermissions(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := filepath.Join(SwarmPath("secure-dir-migrate"), "peers")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir legacy: %v", err)
	}
	sentinel := filepath.Join(dir, "existing.json")
	if err := os.WriteFile(sentinel, []byte(`{"handle":"x"}`), 0o644); err != nil {
		t.Fatalf("write sentinel: %v", err)
	}

	if err := secureDir(dir); err != nil {
		t.Fatalf("secureDir: %v", err)
	}

	fi, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := fi.Mode().Perm(); perm != dirPerm {
		t.Errorf("legacy dir perm not migrated: got %o, want %o", perm, dirPerm)
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Errorf("migration must preserve existing content, but sentinel is gone: %v", err)
	}
}

// TestSecureDir_QuarantinesSymlinkSubstitution proves a symlink planted where
// the peers directory is expected is never trusted or written through: it is
// renamed aside (never deleted, never followed) and a fresh, real, safe
// directory takes its place. The symlink's target is left completely
// untouched — proving quarantine (a same-directory rename of the link entry)
// cannot affect whatever the link pointed at.
func TestSecureDir_QuarantinesSymlinkSubstitution(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	victimDir := t.TempDir()
	if err := os.MkdirAll(victimDir, 0o700); err != nil {
		t.Fatalf("mkdir victim: %v", err)
	}
	victimFile := filepath.Join(victimDir, "secret.txt")
	if err := os.WriteFile(victimFile, []byte("do-not-touch"), 0o600); err != nil {
		t.Fatalf("write victim file: %v", err)
	}

	dir := filepath.Join(SwarmPath("secure-dir-symlink"), "peers")
	if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
		t.Fatalf("mkdir parent: %v", err)
	}
	if err := os.Symlink(victimDir, dir); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	if err := secureDir(dir); err == nil {
		t.Fatal("secureDir must fail closed on a symlinked directory")
	}

	fi, err := os.Lstat(dir)
	if err != nil {
		t.Fatalf("lstat post-secureDir: %v", err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf("denied symlink entry was unexpectedly modified: mode=%v", fi.Mode())
	}

	// The victim directory/file must be completely unaffected.
	if data, err := os.ReadFile(victimFile); err != nil || string(data) != "do-not-touch" {
		t.Errorf("quarantine touched the symlink target: data=%q err=%v", data, err)
	}

}

// TestSecureDir_QuarantinesWrongOwner requires root (to chown a directory to
// a different uid), so it only runs meaningfully in privileged CI; elsewhere
// it documents and skips the untestable case rather than silently omitting
// coverage.
func TestSecureDir_QuarantinesWrongOwner(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("requires root to create a directory owned by another uid")
	}
	base := t.TempDir()
	dir := filepath.Join(base, "peers")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// uid 1: an account that (almost) certainly is not us and exists on any
	// standard Linux box (daemon/bin, depending on distro) — good enough as
	// "not current uid" for this root-only path.
	if err := os.Chown(dir, 1, -1); err != nil {
		t.Fatalf("chown: %v", err)
	}

	if err := secureDir(dir); err != nil {
		t.Fatalf("secureDir: %v", err)
	}

	fi, err := os.Lstat(dir)
	if err != nil {
		t.Fatalf("lstat: %v", err)
	}
	if uid, ok := fileOwnerUID(fi); !ok || int(uid) != os.Getuid() {
		t.Errorf("dir owner after secureDir = %v (ok=%v), want current uid %d", uid, ok, os.Getuid())
	}
}

// TestFileOwnerUID_ReportsCurrentUser sanity-checks the reflection-based
// owner lookup against a file we just created ourselves.
func TestFileOwnerUID_ReportsCurrentUser(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	fi, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("lstat: %v", err)
	}
	uid, ok := fileOwnerUID(fi)
	if !ok {
		t.Skip("platform does not expose file owner via FileInfo.Sys()")
	}
	if int(uid) != os.Getuid() {
		t.Errorf("fileOwnerUID = %d, want current uid %d", uid, os.Getuid())
	}
}

// TestSafeReadFile_RejectsSymlink proves a symlink planted at the expected
// registry-entry path is never followed for a read: the unsafe entry error
// is returned and — critically — the symlink's target content is never
// disclosed via the returned bytes.
func TestSafeReadFile_RejectsSymlink(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	secret := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(secret, []byte("top-secret"), 0o600); err != nil {
		t.Fatalf("write secret: %v", err)
	}
	dir := filepath.Join(SwarmPath("safe-read-symlink"), "peers")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir registry dir: %v", err)
	}
	link := filepath.Join(dir, "peer.json")
	if err := os.Symlink(secret, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	data, err := safeReadFile(link)
	if err == nil {
		t.Fatalf("safeReadFile followed a symlink and returned data: %q", data)
	}
	if !isUnsafeEntryError(err) {
		t.Errorf("expected an unsafeEntryError, got %v (%T)", err, err)
	}
	if data != nil {
		t.Errorf("safeReadFile must not return data for a rejected symlink, got %q", data)
	}
}

// TestSafeReadFile_MigratesLegacyFileMode proves a file we own but that has
// broader-than-filePerm permissions (e.g. the historical 0644) is tightened
// to 0600 in place as part of a safe read, without altering its content.
func TestSafeReadFile_MigratesLegacyFileMode(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := filepath.Join(SwarmPath("safe-read-mode"), "peers")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir registry dir: %v", err)
	}
	path := filepath.Join(dir, "peer.json")
	want := []byte(`{"handle":"legacy"}`)
	if err := os.WriteFile(path, want, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := safeReadFile(path)
	if err != nil {
		t.Fatalf("safeReadFile: %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("content changed: got %q, want %q", got, want)
	}

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := fi.Mode().Perm(); perm != filePerm {
		t.Errorf("legacy file perm not migrated: got %o, want %o", perm, filePerm)
	}
}

// TestWriteFileAtomic_RejectsPathOutsideRegistryRoot proves the shared write
// primitive — which lan_registry.go's upsertRemotePeer also funnels through
// — refuses to write anywhere outside SwarmDir(), and leaves no partial
// artifact behind when it refuses.
func TestWriteFileAtomic_RejectsPathOutsideRegistryRoot(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	outside := filepath.Join(t.TempDir(), "escape.json")
	if err := writeFileAtomic(outside, []byte("{}"), 0o644); err == nil {
		t.Fatalf("writeFileAtomic escaped the registry root and wrote %s", outside)
	}
	if _, err := os.Stat(outside); !os.IsNotExist(err) {
		t.Errorf("writeFileAtomic left a file behind after rejecting an escape: err=%v", err)
	}
}

// TestWriteFileAtomic_AlwaysUsesFilePerm0600 proves the write primitive
// enforces 0600 on the published file regardless of the perm argument a
// caller passes — this is what makes lan_registry.go's still-hardcoded
// writeFileAtomic(path, data, 0644) call site (a file this worker does not
// own) end up with a correctly-permissioned file anyway.
func TestWriteFileAtomic_AlwaysUsesFilePerm0600(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := filepath.Join(SwarmDir(), "default", "peers")
	path := filepath.Join(dir, "x.json")
	if err := writeFileAtomic(path, []byte(`{"handle":"x"}`), 0o644); err != nil {
		t.Fatalf("writeFileAtomic: %v", err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := fi.Mode().Perm(); perm != filePerm {
		t.Errorf("published file perm = %o, want %o", perm, filePerm)
	}
	dfi, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}
	if perm := dfi.Mode().Perm(); perm != dirPerm {
		t.Errorf("published file's dir perm = %o, want %o", perm, dirPerm)
	}
}

// TestWriteFileAtomic_OldOrNewNeverPartial simulates many concurrent writers
// racing to publish the same path and asserts every reader observes either a
// complete previous write or a complete new write — never a truncated /
// interleaved one. This is the atomic-update guarantee the temp+rename
// publication exists for, and it must survive under -race.
func TestWriteFileAtomic_OldOrNewNeverPartial(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := filepath.Join(SwarmDir(), "default", "peers")
	path := filepath.Join(dir, "racer.json")

	const writers = 8
	done := make(chan struct{})
	for i := range writers {
		go func(n int) {
			defer func() { done <- struct{}{} }()
			payload := []byte(fmt.Sprintf(`{"handle":"racer","n":%d,"pad":"%s"}`, n, strings.Repeat("x", n+1)))
			_ = writeFileAtomic(path, payload, filePerm)
		}(i)
	}
	for range writers {
		<-done
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("final read: %v", err)
	}
	var v map[string]any
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatalf("final file is not valid, complete JSON (torn write): %v; data=%q", err, data)
	}
}

// TestJoinSwarm_RejectsMaliciousHandle proves the exported entry point that
// first turns caller data into a filename rejects a traversal handle before
// any path is built, and creates nothing on disk.
func TestJoinSwarm_RejectsMaliciousHandle(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for _, handle := range []string{"../../etc/evil", "a/b", "", ".."} {
		err := JoinSwarm(DefaultSwarmName, PeerPresence{Handle: handle, PID: os.Getpid()})
		if err == nil {
			t.Errorf("JoinSwarm(handle=%q) = nil error, want rejection", handle)
		}
	}
	peersDir := filepath.Join(SwarmPath(DefaultSwarmName), "peers")
	entries, _ := os.ReadDir(peersDir)
	for _, e := range entries {
		t.Errorf("JoinSwarm with a malicious handle must create nothing, found %s", e.Name())
	}
}

// TestGetPeer_RejectsMaliciousHandle proves reads reject traversal handles
// the same way writes do.
func TestGetPeer_RejectsMaliciousHandle(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for _, handle := range []string{"../../etc/passwd", "a/b", ""} {
		if _, err := GetPeer(DefaultSwarmName, handle); err == nil {
			t.Errorf("GetPeer(handle=%q) = nil error, want rejection", handle)
		}
	}
}

// TestLeaveSwarm_NeverTrustsPresenceControlSocket is the direct regression
// test for the "never unlink a presence-supplied control socket or arbitrary
// path" requirement. A peer's on-disk presence file names an attacker- (or
// corruption-) controlled ControlSocket pointing at a sentinel file outside
// the registry entirely. LeaveSwarm must remove only the canonical sibling
// <handle>.ctrl it derives itself from the validated handle — the sentinel
// must survive untouched.
func TestLeaveSwarm_NeverTrustsPresenceControlSocket(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	sentinelDir := t.TempDir()
	sentinel := filepath.Join(sentinelDir, "do-not-delete.sock")
	if err := os.WriteFile(sentinel, []byte("precious"), 0o600); err != nil {
		t.Fatalf("write sentinel: %v", err)
	}

	handle := "victim-peer"
	if err := JoinSwarm(DefaultSwarmName, PeerPresence{
		Handle:        handle,
		PID:           os.Getpid(),
		ControlSocket: sentinel, // attacker/corruption-controlled arbitrary path
	}); err != nil {
		t.Fatalf("JoinSwarm: %v", err)
	}

	// Create the CANONICAL sibling .ctrl so we can prove it — and only it —
	// gets removed.
	peersDir := filepath.Join(SwarmPath(DefaultSwarmName), "peers")
	canonicalCtrl := filepath.Join(peersDir, handle+".ctrl")
	if err := os.WriteFile(canonicalCtrl, []byte(""), 0o600); err != nil {
		t.Fatalf("write canonical ctrl: %v", err)
	}

	if err := LeaveSwarm(DefaultSwarmName, handle); err != nil {
		t.Fatalf("LeaveSwarm: %v", err)
	}

	if _, err := os.Stat(sentinel); err != nil {
		t.Fatalf("LeaveSwarm removed/touched the presence-supplied sentinel path: %v", err)
	}
	if data, err := os.ReadFile(sentinel); err != nil || string(data) != "precious" {
		t.Fatalf("sentinel content changed: data=%q err=%v", data, err)
	}
	if _, err := os.Stat(canonicalCtrl); !os.IsNotExist(err) {
		t.Errorf("canonical sibling .ctrl should have been removed, stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(peersDir, handle+".json")); !os.IsNotExist(err) {
		t.Errorf("presence json should have been removed, stat err=%v", err)
	}
}

// TestListPeers_ReapsDeadPeer_NeverTrustsPresenceControlSocket is the same
// regression as above but through the auto-reap path in ListPeers (a dead
// local peer's PID check fails), rather than an explicit LeaveSwarm call.
func TestListPeers_ReapsDeadPeer_NeverTrustsPresenceControlSocket(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	sentinelDir := t.TempDir()
	sentinel := filepath.Join(sentinelDir, "do-not-delete.sock")
	if err := os.WriteFile(sentinel, []byte("precious"), 0o600); err != nil {
		t.Fatalf("write sentinel: %v", err)
	}

	handle := "dead-peer"
	peersDir := filepath.Join(SwarmPath(DefaultSwarmName), "peers")
	if err := os.MkdirAll(peersDir, 0o700); err != nil {
		t.Fatalf("mkdir peers: %v", err)
	}
	dead := PeerPresence{
		Handle:        handle,
		PID:           999999999, // never a real live PID
		Type:          PeerTypeLocal,
		ControlSocket: sentinel,
	}
	data, _ := json.Marshal(dead)
	if err := os.WriteFile(filepath.Join(peersDir, handle+".json"), data, 0o600); err != nil {
		t.Fatalf("write dead peer: %v", err)
	}

	if _, err := ListPeers(DefaultSwarmName); err != nil {
		t.Fatalf("ListPeers: %v", err)
	}

	if _, err := os.Stat(sentinel); err != nil {
		t.Fatalf("ListPeers reaping a dead peer must never touch its presence-supplied ControlSocket: %v", err)
	}
	if _, err := os.Stat(filepath.Join(peersDir, handle+".json")); err != nil {
		t.Errorf("legacy presence without instance evidence must not be auto-deleted: %v", err)
	}
}

// TestListPeers_QuarantinesSymlinkedPeerEntry proves a symlink planted in the
// peers directory (pointing anywhere, including outside the registry root)
// is never followed by ListPeers — it never becomes a peer, and it is moved
// aside so it cannot keep tripping discovery or the orphan-socket sweep.
func TestListPeers_QuarantinesSymlinkedPeerEntry(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	secretDir := t.TempDir()
	secret := filepath.Join(secretDir, "secret.json")
	if err := os.WriteFile(secret, []byte(`{"handle":"leaked","pid":1}`), 0o600); err != nil {
		t.Fatalf("write secret: %v", err)
	}

	peersDir := filepath.Join(SwarmPath(DefaultSwarmName), "peers")
	if err := os.MkdirAll(peersDir, 0o700); err != nil {
		t.Fatalf("mkdir peers: %v", err)
	}
	link := filepath.Join(peersDir, "hacked.json")
	if err := os.Symlink(secret, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	peers, err := ListPeers(DefaultSwarmName)
	if err != nil {
		t.Fatalf("ListPeers: %v", err)
	}
	for _, p := range peers {
		if p.Handle == "leaked" {
			t.Fatalf("ListPeers followed a symlinked entry and surfaced a peer from it: %+v", p)
		}
	}

	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf("unsafe symlink must be denied without destructive mutation: fi=%v err=%v", fi, err)
	}

	// The symlink target must never have been touched (still there, unchanged).
	if data, err := os.ReadFile(secret); err != nil || !strings.Contains(string(data), "leaked") {
		t.Errorf("secret target changed or removed: data=%q err=%v", data, err)
	}
}

// TestSweepOrphanControlSockets_RemovesSymlinkedSocketWithoutDialingThrough
// proves a *.ctrl entry that is itself a symlink is never dialed (which
// would follow it to an arbitrary target) — it is removed outright, and only
// the link entry, never whatever it pointed at.
func TestSweepOrphanControlSockets_RemovesSymlinkedSocketWithoutDialingThrough(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	peersDir := filepath.Join(SwarmPath("socket-symlink"), "peers")
	if err := os.MkdirAll(peersDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	targetDir := t.TempDir()
	target := filepath.Join(targetDir, "somewhere-else.sock")
	if err := os.WriteFile(target, []byte("not-a-real-socket-but-a-file"), 0o600); err != nil {
		t.Fatalf("write target: %v", err)
	}
	link := filepath.Join(peersDir, "x.ctrl")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	sweepOrphanControlSockets(peersDir)

	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf("symlinked .ctrl must be denied without destructive mutation: fi=%v err=%v", fi, err)
	}
	if _, err := os.Stat(target); err != nil {
		t.Errorf("sweep must never touch the symlink's target, err=%v", err)
	}
}

// ─── R10: JoinSwarm/LeaveSwarm/RemovePeerIfInstance remote-compatibility ───
// regressions. These cover the R10 CONTRACT.md findings: JoinSwarm must
// route PeerTypeRemote through upsertRemotePeer (fail closed on a
// non-opaque handle, refuse to replace local/daemon, allow refresh at cap),
// LeaveSwarm must re-read the current type under the same
// retention-lock-then-handle-lock ordering and only ever remove JSON (never
// a control socket) for a remote record, and RemovePeerIfInstance must
// refuse PeerTypeRemote even when a hostile legacy record carries a
// matching token.

func TestJoinSwarm_RemoteFailsClosedOnNonOpaqueHandle(t *testing.T) {
	t.Setenv("SWARM_HOME", t.TempDir())
	const swarm = "join-remote-non-opaque"
	for _, handle := range []string{
		"bare-handle",
		"hostile-host~legacy-remote",        // "~" present but neither half is valid hex
		testOriginA + "~" + "not-hex-alias", // valid origin, malformed alias
	} {
		err := JoinSwarm(swarm, PeerPresence{Handle: handle, Type: PeerTypeRemote, Status: "active"})
		if err == nil || !strings.Contains(err.Error(), "not opaque") {
			t.Errorf("JoinSwarm(remote, handle=%q) = %v, want an opaque-handle rejection", handle, err)
		}
	}
	peersDir := filepath.Join(SwarmPath(swarm), "peers")
	entries, _ := registryReadDir(peersDir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json") {
			t.Errorf("JoinSwarm(remote) with a non-opaque handle must create nothing, found %s", e.Name())
		}
	}
}

// TestJoinSwarm_RemoteOpaqueHandleRoutesThroughUpsertRemotePeer proves the
// success path: a valid opaque handle is accepted, persisted with the same
// capability-free projection upsertRemotePeer itself performs, and a second
// JoinSwarm call for the SAME handle is treated as a refresh (never an
// error, even though a record already exists) — the exact "allow refresh at
// cap" contract, exercised here below the cap for a fast, deterministic
// single-process check (the shared cross-process cap proof lives in
// TestJoinSwarmRemoteCapCrossProcessNeverExceedsSharedLimit below).
func TestJoinSwarm_RemoteOpaqueHandleRoutesThroughUpsertRemotePeer(t *testing.T) {
	t.Setenv("SWARM_HOME", t.TempDir())
	const swarm = "join-remote-opaque"
	handle := testOriginA + "~" + strings.Repeat("7", aliasIDLen)

	if err := JoinSwarm(swarm, PeerPresence{
		Handle:      handle,
		Type:        PeerTypeRemote,
		Status:      "active",
		EndpointURL: "http://attacker.example/rpc", // must never persist
	}); err != nil {
		t.Fatalf("JoinSwarm(remote, opaque handle): %v", err)
	}
	got, err := GetPeer(swarm, handle)
	if err != nil || got == nil {
		t.Fatalf("GetPeer after JoinSwarm(remote): peer=%+v err=%v", got, err)
	}
	if got.Handle != handle || got.EndpointURL != "" || got.Status != "active" {
		t.Fatalf("JoinSwarm(remote) did not persist the sanitized opaque record: %+v", got)
	}

	// Refresh: same handle, new status. Must succeed (not "already exists").
	if err := JoinSwarm(swarm, PeerPresence{Handle: handle, Type: PeerTypeRemote, Status: "refreshed"}); err != nil {
		t.Fatalf("JoinSwarm(remote) refresh of an existing opaque record failed: %v", err)
	}
	if got, err := GetPeer(swarm, handle); err != nil || got == nil || got.Status != "refreshed" {
		t.Fatalf("JoinSwarm(remote) refresh was not observed: peer=%+v err=%v", got, err)
	}
}

// TestJoinSwarm_RemoteRefusesReplacingLocalAndDaemon proves the public/
// internal JoinSwarm entry point inherits upsertRemotePeer's refusal to
// replace a local or daemon publication — an attacker (or a racing LAN
// ingest) can never use an opaque-looking handle collision to overwrite a
// live local/daemon peer's transport capabilities with a remote projection.
func TestJoinSwarm_RemoteRefusesReplacingLocalAndDaemon(t *testing.T) {
	for _, typ := range []PeerType{PeerTypeLocal, PeerTypeDaemon} {
		t.Run(string(typ), func(t *testing.T) {
			t.Setenv("SWARM_HOME", t.TempDir())
			const swarm = "join-remote-replace-denied"
			handle := testOriginB + "~" + strings.Repeat(string(typ[0]), aliasIDLen)
			if err := JoinSwarm(swarm, PeerPresence{
				Handle:        handle,
				Type:          typ,
				PID:           os.Getpid(),
				ControlSocket: "/tmp/must-survive.ctrl",
			}); err != nil {
				t.Fatalf("JoinSwarm(%s): %v", typ, err)
			}

			err := JoinSwarm(swarm, PeerPresence{Handle: handle, Type: PeerTypeRemote, Status: "hostile-takeover"})
			if err == nil {
				t.Fatalf("JoinSwarm(remote) replaced a %s publication", typ)
			}

			got, err := GetPeer(swarm, handle)
			if err != nil || got == nil || got.Type != typ || got.ControlSocket != "/tmp/must-survive.ctrl" {
				t.Fatalf("%s publication was not preserved: peer=%+v err=%v", typ, got, err)
			}
		})
	}
}

func TestLeaveSwarm_RemoteRemovesOnlyJSONPreservesControlSocket(t *testing.T) {
	t.Setenv("SWARM_HOME", t.TempDir())
	const swarm = "leave-remote-preserves-socket"
	handle := testOriginA + "~" + strings.Repeat("5", aliasIDLen)
	if err := upsertRemotePeer(swarm, PeerPresence{
		Handle:     handle,
		Type:       PeerTypeRemote,
		Status:     "active",
		LastSeenAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("upsertRemotePeer: %v", err)
	}

	peersDir := filepath.Join(SwarmPath(swarm), "peers")
	jsonPath := filepath.Join(peersDir, handle+".json")
	ctrlPath := filepath.Join(peersDir, handle+".ctrl")
	// A remote record never legitimately owns a control socket
	// (remotePresenceProjection always strips ControlSocket), so a *.ctrl
	// sibling here can only be a different local/daemon instance's socket
	// that happens to share this handle — a hostile/corrupt scenario. It
	// must survive a remote-typed LeaveSwarm regardless.
	if err := os.WriteFile(ctrlPath, []byte("must-survive"), filePerm); err != nil {
		t.Fatal(err)
	}

	if err := LeaveSwarm(swarm, handle); err != nil {
		t.Fatalf("LeaveSwarm: %v", err)
	}
	if _, err := os.Stat(jsonPath); !os.IsNotExist(err) {
		t.Errorf("remote presence JSON should have been removed, stat err=%v", err)
	}
	data, err := os.ReadFile(ctrlPath)
	if err != nil || string(data) != "must-survive" {
		t.Fatalf("remote LeaveSwarm touched a control socket: data=%q err=%v", data, err)
	}
}

// TestLeaveSwarm_RereadsCurrentTypeUnderLockRegardlessOfCaller proves
// LeaveSwarm never trusts anything about the target's type from the
// caller — only the handle is supplied, and the on-disk type (re-read
// fresh under the retention+handle locks) alone decides local/daemon/remote
// handling. Local removes json+ctrl; daemon (without a token) refuses;
// remote removes only json and preserves any ctrl sibling.
func TestLeaveSwarm_RereadsCurrentTypeUnderLockRegardlessOfCaller(t *testing.T) {
	for _, tc := range []struct {
		typ          PeerType
		wantErr      bool
		wantJSONGone bool
		wantCtrlGone bool
	}{
		{typ: PeerTypeLocal, wantErr: false, wantJSONGone: true, wantCtrlGone: true},
		{typ: PeerTypeDaemon, wantErr: true, wantJSONGone: false, wantCtrlGone: false},
		{typ: PeerTypeRemote, wantErr: false, wantJSONGone: true, wantCtrlGone: false},
	} {
		t.Run(string(tc.typ), func(t *testing.T) {
			t.Setenv("SWARM_HOME", t.TempDir())
			const swarm = "leave-reread-type"
			handle := testOriginB + "~" + strings.Repeat(string(tc.typ[0]), aliasIDLen)
			peersDir := filepath.Join(SwarmPath(swarm), "peers")
			if err := os.MkdirAll(peersDir, dirPerm); err != nil {
				t.Fatal(err)
			}
			peer := PeerPresence{Handle: handle, Type: tc.typ, PID: os.Getpid(), LastSeenAt: time.Now().UTC()}
			data, err := json.Marshal(peer)
			if err != nil {
				t.Fatal(err)
			}
			jsonPath := filepath.Join(peersDir, handle+".json")
			if err := os.WriteFile(jsonPath, data, filePerm); err != nil {
				t.Fatal(err)
			}
			ctrlPath := filepath.Join(peersDir, handle+".ctrl")
			if err := os.WriteFile(ctrlPath, []byte("sentinel"), filePerm); err != nil {
				t.Fatal(err)
			}

			err = LeaveSwarm(swarm, handle)
			if tc.wantErr != (err != nil) {
				t.Fatalf("LeaveSwarm(%s) err = %v, wantErr=%v", tc.typ, err, tc.wantErr)
			}
			_, jsonStatErr := os.Stat(jsonPath)
			if gone := os.IsNotExist(jsonStatErr); gone != tc.wantJSONGone {
				t.Errorf("LeaveSwarm(%s) json removed=%v, want %v (err=%v)", tc.typ, gone, tc.wantJSONGone, jsonStatErr)
			}
			_, ctrlStatErr := os.Stat(ctrlPath)
			if gone := os.IsNotExist(ctrlStatErr); gone != tc.wantCtrlGone {
				t.Errorf("LeaveSwarm(%s) ctrl removed=%v, want %v (err=%v)", tc.typ, gone, tc.wantCtrlGone, ctrlStatErr)
			}
		})
	}
}

// TestRemovePeerIfInstance_RefusesHostileRemoteRecordWithMatchingToken
// proves the token-conditional cleanup path refuses PeerTypeRemote
// unconditionally, even when a hostile (or corrupted-by-a-LAN-race) legacy
// record on disk carries an InstanceToken that exactly matches the caller's
// expected token. remotePresenceProjection never persists an InstanceToken
// for a genuine remote write, so any on-disk remote record carrying one is
// definitionally not a real capability grant — but RemovePeerIfInstance must
// still refuse to act on it rather than trust the token blindly.
func TestRemovePeerIfInstance_RefusesHostileRemoteRecordWithMatchingToken(t *testing.T) {
	t.Setenv("SWARM_HOME", t.TempDir())
	const swarm = "hostile-remote-token-cleanup"
	handle := testOriginB + "~" + strings.Repeat("9", aliasIDLen)
	peersDir := filepath.Join(SwarmPath(swarm), "peers")
	if err := os.MkdirAll(peersDir, dirPerm); err != nil {
		t.Fatal(err)
	}

	// rawLegacyPeerForCleanupTest is PeerPresence converted to a distinct
	// named type so json.Marshal uses plain struct-tag reflection instead of
	// PeerPresence's custom MarshalJSON (which would otherwise strip
	// InstanceToken for Type==Remote before this hostile fixture ever
	// reaches disk) — exactly simulating a hand-crafted or pre-hardening
	// on-disk record.
	type rawLegacyPeerForCleanupTest PeerPresence
	hostile := PeerPresence{
		Handle:        handle,
		Type:          PeerTypeRemote,
		Status:        "active",
		LastSeenAt:    time.Now().UTC(),
		InstanceToken: "hostile-token-match",
	}
	data, err := json.Marshal(rawLegacyPeerForCleanupTest(hostile))
	if err != nil {
		t.Fatal(err)
	}
	jsonPath := filepath.Join(peersDir, handle+".json")
	if err := os.WriteFile(jsonPath, data, filePerm); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "hostile-token-match") {
		t.Fatalf("test fixture bug: hostile token not actually on disk: %s", data)
	}

	removed, err := RemovePeerIfInstance(swarm, handle, "hostile-token-match")
	if err != nil {
		t.Fatalf("RemovePeerIfInstance: %v", err)
	}
	if removed {
		t.Fatal("RemovePeerIfInstance removed a PeerTypeRemote record despite a matching token")
	}
	if _, statErr := os.Stat(jsonPath); statErr != nil {
		t.Fatalf("hostile remote record was removed from disk: %v", statErr)
	}
}

// TestCrossProcessJoinSwarmRemoteCapHelper is the subprocess entry point for
// TestJoinSwarmRemoteCapCrossProcessNeverExceedsSharedLimit below. Gated by
// its own dedicated env var so an ordinary `go test` run (which never sets
// it) returns immediately instead of recursing.
func TestCrossProcessJoinSwarmRemoteCapHelper(t *testing.T) {
	origin := os.Getenv("A2A_JOINCAP_ORIGIN")
	if origin == "" {
		return
	}
	swarm := os.Getenv("A2A_JOINCAP_SWARM")
	count, err := strconv.Atoi(os.Getenv("A2A_JOINCAP_COUNT"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "bad A2A_JOINCAP_COUNT: %v\n", err)
		os.Exit(2)
	}

	// Deterministic handshake: announce readiness, then block until the
	// parent writes a release byte, so the parent controls exactly when this
	// process's JoinSwarm flood begins relative to its sibling — never a
	// sleep/timing guess.
	fmt.Println("READY")
	var release [1]byte
	if _, err := os.Stdin.Read(release[:]); err != nil {
		fmt.Fprintf(os.Stderr, "wait for release: %v\n", err)
		os.Exit(2)
	}

	successes := 0
	for i := 0; i < count; i++ {
		handle := origin + "~" + fmt.Sprintf("%032x", i+1)
		if err := JoinSwarm(swarm, PeerPresence{
			Handle: handle,
			Type:   PeerTypeRemote,
			Status: "active",
		}); err == nil {
			successes++
		}
	}
	fmt.Printf("DONE %d\n", successes)
}

// TestJoinSwarmRemoteCapCrossProcessNeverExceedsSharedLimit is the
// deterministic, real-subprocess proof that the retained-remote-peer cap
// holds across actual OS processes joining through the public/internal
// JoinSwarm entry point sharing one SWARM_HOME — not just goroutines inside
// a single process (already covered by
// TestRemoteRetentionCapRefreshAndConcurrentFlood above). Two child
// processes, each with its own opaque origin and 96 distinct opaque
// aliases (192 combined — comfortably more than maxRetainedRemotePeers),
// race to JoinSwarm. Entry and release are coordinated with an explicit
// stdin handshake (a READY line, then a release byte), never a
// sleep/timing ratio. The parent re-reads the final on-disk state
// independently and proves it never exceeds the shared cap.
func TestJoinSwarmRemoteCapCrossProcessNeverExceedsSharedLimit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows registry removal deliberately fails closed")
	}
	home := t.TempDir()
	const swarm = "cross-process-join-cap"
	const perChild = maxRetainedRemotePeers/2 + 32 // combined > cap

	type child struct {
		cmd    *exec.Cmd
		stdout *bufio.Reader
		stdin  io.WriteCloser
		stderr *bytes.Buffer
	}
	start := func(origin string) *child {
		t.Helper()
		cmd := exec.Command(os.Args[0], "-test.run=^TestCrossProcessJoinSwarmRemoteCapHelper$")
		cmd.Env = append(os.Environ(),
			"SWARM_HOME="+home,
			"A2A_JOINCAP_ORIGIN="+origin,
			"A2A_JOINCAP_SWARM="+swarm,
			"A2A_JOINCAP_COUNT="+strconv.Itoa(perChild),
		)
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		stdin, err := cmd.StdinPipe()
		if err != nil {
			t.Fatal(err)
		}
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		return &child{cmd: cmd, stdout: bufio.NewReader(stdout), stdin: stdin, stderr: &stderr}
	}

	childA := start(testOriginA)
	childB := start(testOriginB)
	children := []*child{childA, childB}
	t.Cleanup(func() {
		for _, c := range children {
			if c.cmd.ProcessState == nil {
				_ = c.cmd.Process.Kill()
				_ = c.cmd.Wait()
			}
		}
	})

	for _, c := range children {
		line, err := c.stdout.ReadString('\n')
		if err != nil || strings.TrimSpace(line) != "READY" {
			t.Fatalf("child handshake = %q, err=%v, stderr=%s", line, err, c.stderr.String())
		}
	}
	for _, c := range children {
		if _, err := c.stdin.Write([]byte{1}); err != nil {
			t.Fatalf("release child: %v", err)
		}
		_ = c.stdin.Close()
	}
	for _, c := range children {
		line, err := c.stdout.ReadString('\n')
		if err != nil || !strings.HasPrefix(strings.TrimSpace(line), "DONE") {
			t.Fatalf("child completion = %q, err=%v, stderr=%s", line, err, c.stderr.String())
		}
		if err := c.cmd.Wait(); err != nil {
			t.Fatalf("child process failed: %v (stderr=%s)", err, c.stderr.String())
		}
	}

	t.Setenv("SWARM_HOME", home)
	count, err := retainedRemotePeerCount(filepath.Join(SwarmPath(swarm), "peers"))
	if err != nil {
		t.Fatalf("retainedRemotePeerCount: %v", err)
	}
	if count > maxRetainedRemotePeers {
		t.Fatalf("retained remote count = %d, must never exceed shared cap %d across real subprocesses", count, maxRetainedRemotePeers)
	}
	if count == 0 {
		t.Fatal("no remote peers retained; the cross-process join path did not exercise anything")
	}
}
