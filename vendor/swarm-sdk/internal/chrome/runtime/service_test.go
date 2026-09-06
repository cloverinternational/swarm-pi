package runtime

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/chrome"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/chrome/protocol"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/gorilla/websocket"
)

const (
	serviceExtensionID = "abcdefghijklmnopabcdefghijklmnop"
	serviceInstallID   = "service-installation"
	serviceSecret      = "0123456789abcdef0123456789abcdef"
)

type serviceLookup map[string]*conversation.Conversation

func (m serviceLookup) LoadConversation(_ context.Context, id string) (*conversation.Conversation, error) {
	value := m[id]
	if value == nil {
		return nil, os.ErrNotExist
	}
	return value, nil
}

func serviceFamily(t *testing.T, id string) chrome.FamilyContext {
	t.Helper()
	resolver, err := chrome.NewFamilyResolver(serviceLookup{id: {
		ID: id, Metadata: conversation.ConversationMetadata{Custom: map[string]any{
			conversation.CustomKeyAgentID: "agent",
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	family, err := resolver.Resolve(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	return family
}

type fakeServiceLauncher struct {
	mu  sync.Mutex
	ids []string
}

func (f *fakeServiceLauncher) Launch(_ context.Context, req chrome.LauncherRequest) error {
	f.mu.Lock()
	f.ids = append(f.ids, req.LaunchID)
	f.mu.Unlock()
	return nil
}

func newServiceStore(t *testing.T) *Store {
	t.Helper()
	dir := secureTestDir(t)
	store, err := NewStore(Paths{
		Lock: filepath.Join(dir, "authority.lock"), State: filepath.Join(dir, "authority.json"),
		Credential: filepath.Join(dir, "installation.json"),
	})
	if err != nil {
		t.Fatal(err)
	}
	lock, err := store.Acquire()
	if err != nil {
		t.Fatal(err)
	}
	probe, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := probe.Addr().(*net.TCPAddr).Port
	if err := probe.Close(); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveCredential(lock, InstallationCredential{
		Version: CredentialVersion, InstallationID: serviceInstallID, Secret: serviceSecret,
		LoopbackPort: uint16(port),
	}); err != nil {
		t.Fatal(err)
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	return store
}

func newService(t *testing.T, store *Store, launcher chrome.Launcher, idle, startup time.Duration) *Service {
	t.Helper()
	svc, err := NewService(Config{
		Store: store, Launcher: launcher, ExtensionID: serviceExtensionID,
		IdleTimeout: idle, StartupGrace: startup, RecoveryGrace: 5 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := svc.Shutdown(ctx); err != nil {
			t.Errorf("Shutdown: %v", err)
		}
	})
	return svc
}

func persistServiceClaim(t *testing.T, store *Store, claim Claim) {
	t.Helper()
	lock, err := store.Acquire()
	if err != nil {
		t.Fatal(err)
	}
	state, err := store.AdvanceEpoch(lock)
	if err != nil {
		t.Fatal(err)
	}
	state.Claims = []Claim{claim}
	if err := store.SaveState(lock, state); err != nil {
		t.Fatal(err)
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
}

func serviceAdvertisement() protocol.Advertisement {
	return protocol.Advertisement{
		RPC: []protocol.VersionRange{{Major: 1}}, Events: []protocol.VersionRange{{Major: 1}},
		Capabilities: []string{"claim_v1", "execution_journal_v1", "close_epoch_v1",
			"ownership_rebind_v1", "ownership_reconcile_v1"},
	}
}

func dialService(t *testing.T, svc *Service) *websocket.Conn {
	t.Helper()
	headers := map[string][]string{"Origin": {"chrome-extension://" + serviceExtensionID}}
	ws, _, err := websocket.DefaultDialer.Dial(svc.Bridge().URL(), headers)
	if err != nil {
		t.Fatal(err)
	}
	hello := protocol.AuthHello{
		Contract: protocol.ControlContractV1, Kind: protocol.ControlAuthHello,
		InstallationID: serviceInstallID, ClientNonce: "service-client-nonce",
		Supported: serviceAdvertisement(),
	}
	if err := ws.WriteJSON(hello); err != nil {
		t.Fatal(err)
	}
	var challenge protocol.AuthChallenge
	if err := ws.ReadJSON(&challenge); err != nil {
		t.Fatal(err)
	}
	proof := protocol.AuthProof{
		Contract: protocol.ControlContractV1, Kind: protocol.ControlAuthProof,
		InstallationID: challenge.InstallationID, ClientNonce: challenge.ClientNonce,
		ServerNonce: challenge.ServerNonce, ManagerEpoch: challenge.ManagerEpoch,
		ManagerInstanceID: challenge.ManagerInstanceID, Selected: challenge.Selected,
	}
	proof.ClientProof = serviceHandshakeProof("client", proof.InstallationID, proof.ClientNonce,
		proof.ServerNonce, proof.ManagerEpoch, proof.ManagerInstanceID, proof.Selected)
	if err := ws.WriteJSON(proof); err != nil {
		t.Fatal(err)
	}
	var negotiated protocol.NegotiatedControl
	if err := ws.ReadJSON(&negotiated); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ws.Close() })
	return ws
}

func serviceHandshakeProof(role, installation, client, server string, epoch uint64, instance string, selected protocol.Negotiated) string {
	mac := hmac.New(sha256.New, []byte(serviceSecret))
	selectedJSON, _ := json.Marshal(selected)
	fmt.Fprintf(mac, "swarm.chrome.bridge/1\x00%s\x00%s\x00%s\x00%s\x00%d\x00%s\x00%s",
		role, installation, client, server, epoch, instance, selectedJSON)
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func serviceRebindProof(claim string, generation uint64, window int64, challenge string) string {
	mac := hmac.New(sha256.New, []byte(serviceSecret))
	fmt.Fprintf(mac, "swarm.chrome.rebind/1\x00%s\x00%s\x00%d\x00%d\x00%s",
		serviceInstallID, claim, generation, window, challenge)
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func readControl(t *testing.T, ws *websocket.Conn) protocol.Control {
	t.Helper()
	_ = ws.SetReadDeadline(time.Now().Add(time.Second))
	_, raw, err := ws.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	value, err := protocol.DecodeControl(raw)
	if err != nil {
		t.Fatalf("DecodeControl(%s): %v", raw, err)
	}
	return value
}

func claimFreshFamily(t *testing.T, svc *Service, ws *websocket.Conn, family chrome.FamilyContext, window int64) (string, string, uint64) {
	t.Helper()
	if err := svc.StartFamily(family); err != nil {
		t.Fatal(err)
	}
	create, ok := readControl(t, ws).(*protocol.CreateOwnedWindow)
	if !ok {
		t.Fatal("expected create_owned_window")
	}
	if err := ws.WriteJSON(protocol.WindowClaimRequested{
		Contract: protocol.ControlContractV1, Kind: protocol.ControlWindowClaimRequested,
		LaunchID: create.LaunchID, WindowID: window, TabIDs: []int64{window + 100},
	}); err != nil {
		t.Fatal(err)
	}
	prepare, ok := readControl(t, ws).(*protocol.ClaimControl)
	if !ok || prepare.Kind != protocol.ControlClaimPrepare {
		t.Fatalf("expected claim_prepare, got %#v", prepare)
	}
	staged := *prepare
	staged.Kind = protocol.ControlClaimStaged
	if err := ws.WriteJSON(staged); err != nil {
		t.Fatal(err)
	}
	committed, ok := readControl(t, ws).(*protocol.ClaimControl)
	if !ok || committed.Kind != protocol.ControlClaimCommitted {
		t.Fatalf("expected claim_committed, got %#v", committed)
	}
	claimed := *committed
	claimed.Kind = protocol.ControlWindowClaimed
	claimed.FamilyCapability = ""
	if err := ws.WriteJSON(claimed); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool {
		snapshot, err := svc.Snapshot(family)
		return err == nil && snapshot.State == chrome.SessionReady
	})
	return committed.ClaimID, committed.FamilyCapability, committed.Generation
}

func eventually(t *testing.T, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition did not become true")
}

func TestServiceConcurrentFamiliesUseDistinctSingleUseLaunchIDs(t *testing.T) {
	store := newServiceStore(t)
	launcher := &fakeServiceLauncher{}
	svc := newService(t, store, launcher, time.Hour, 20*time.Millisecond)
	const count = 50
	var wg sync.WaitGroup
	for i := range count {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := svc.StartFamily(serviceFamily(t, fmt.Sprintf("root-%d", i))); err != nil {
				t.Errorf("StartFamily: %v", err)
			}
		}(i)
	}
	wg.Wait()
	eventually(t, func() bool {
		launcher.mu.Lock()
		defer launcher.mu.Unlock()
		return len(launcher.ids) == count
	})
	launcher.mu.Lock()
	defer launcher.mu.Unlock()
	seen := make(map[string]bool)
	for _, id := range launcher.ids {
		if len(id) < 16 || seen[id] {
			t.Fatalf("invalid or duplicate launch ID %q", id)
		}
		seen[id] = true
	}
}

func TestServiceClaimCloseActivityAndSecretFreeState(t *testing.T) {
	store := newServiceStore(t)
	svc := newService(t, store, &fakeServiceLauncher{}, 25*time.Millisecond, time.Second)
	ws := dialService(t, svc)
	family := serviceFamily(t, "fresh-root")
	claim, _, generation := claimFreshFamily(t, svc, ws, family, 41)
	before, _ := svc.Snapshot(family)
	event := protocol.Event{
		Contract: protocol.EventsContractV1, Kind: "event", EventID: "activity-1", Sequence: 1,
		Event: "human_activity", Generation: generation,
		Payload: json.RawMessage(fmt.Sprintf(`{"claim_id":%q,"activity":"keyboard"}`, claim)),
	}
	if err := ws.WriteJSON(event); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool {
		after, err := svc.Snapshot(family)
		return err == nil && after.LastHumanActivity.After(before.LastHumanActivity)
	})
	var prepare *protocol.CloseControl
	for prepare == nil {
		if value, ok := readControl(t, ws).(*protocol.CloseControl); ok && value.Kind == protocol.ControlClosePrepare {
			prepare = value
		}
	}
	leaseResult := make(chan *chrome.Lease, 1)
	go func() {
		lease, _ := svc.Admit(context.Background(), family, chrome.ActionOptions{CountsAsActivity: true})
		leaseResult <- lease
	}()
	cancelMessage, ok := readControl(t, ws).(*protocol.CloseControl)
	if !ok || cancelMessage.Kind != protocol.ControlCloseCancel ||
		cancelMessage.CloseEpoch != prepare.CloseEpoch {
		t.Fatalf("expected exact close_cancel, got %#v", cancelMessage)
	}
	cancelled := *cancelMessage
	cancelled.Kind = protocol.ControlCloseCancelled
	if err := ws.WriteJSON(cancelled); err != nil {
		t.Fatal(err)
	}
	select {
	case lease := <-leaseResult:
		if lease == nil {
			t.Fatal("cancelled close did not admit waiting action")
		}
		lease.Release()
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for close cancellation")
	}
	prepare = nil
	for prepare == nil {
		if value, ok := readControl(t, ws).(*protocol.CloseControl); ok && value.Kind == protocol.ControlClosePrepare {
			prepare = value
		}
	}
	prepared := *prepare
	prepared.Kind = protocol.ControlClosePrepared
	if err := ws.WriteJSON(prepared); err != nil {
		t.Fatal(err)
	}
	commit, ok := readControl(t, ws).(*protocol.CloseControl)
	if !ok || commit.Kind != protocol.ControlCloseCommit {
		t.Fatalf("expected close_commit, got %#v", commit)
	}
	committed := *commit
	committed.Kind = protocol.ControlCloseCommitted
	if err := ws.WriteJSON(committed); err != nil {
		t.Fatal(err)
	}
	event.Sequence++
	event.EventID = "removed-1"
	event.Event = "window_removed"
	event.Payload = json.RawMessage(fmt.Sprintf(`{"claim_id":%q,"close_epoch":%d}`, claim, commit.CloseEpoch))
	if err := ws.WriteJSON(event); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool {
		snapshot, err := svc.Snapshot(family)
		return err == nil && snapshot.State == chrome.SessionClosed
	})
	raw, err := os.ReadFile(store.Paths().State)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{serviceSecret, `"window_id"`, `"family_capability"`} {
		if stringContains(string(raw), forbidden) {
			t.Fatalf("state leaked %q: %s", forbidden, raw)
		}
	}
}

func TestServicePersistedClaimRequiresSnapshotAndProof(t *testing.T) {
	store := newServiceStore(t)
	family := serviceFamily(t, "restored-root")
	lock, err := store.Acquire()
	if err != nil {
		t.Fatal(err)
	}
	state, err := store.AdvanceEpoch(lock)
	if err != nil {
		t.Fatal(err)
	}
	state.Claims = []Claim{{
		FamilyHash: familyHash(family), Generation: 7, ClaimID: "persisted-claim",
		InstallationID: serviceInstallID, Lifecycle: "open", UpdatedAt: time.Now().UTC(),
	}}
	if err := store.SaveState(lock, state); err != nil {
		t.Fatal(err)
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	svc := newService(t, store, &fakeServiceLauncher{}, time.Hour, time.Second)
	if err := svc.StartFamily(family); err != nil {
		t.Fatal(err)
	}
	snapshot, _ := svc.Snapshot(family)
	if snapshot.State != chrome.SessionLaunching {
		t.Fatalf("before proof state = %s", snapshot.State)
	}
	ws := dialService(t, svc)
	if err := ws.WriteJSON(protocol.OwnershipSnapshot{
		Contract: protocol.ControlContractV1, Kind: protocol.ControlOwnershipSnapshot,
		Records: []protocol.OwnershipRecord{{ClaimID: "persisted-claim", Generation: 7, WindowID: 71, TabIDs: []int64{72}}},
	}); err != nil {
		t.Fatal(err)
	}
	var challenge *protocol.CapabilityChallenge
	for challenge == nil {
		if value, ok := readControl(t, ws).(*protocol.CapabilityChallenge); ok {
			challenge = value
		}
	}
	snapshot, _ = svc.Snapshot(family)
	if snapshot.State != chrome.SessionLaunching {
		t.Fatalf("before capability proof state = %s", snapshot.State)
	}
	if err := ws.WriteJSON(protocol.CapabilityProof{
		Contract: protocol.ControlContractV1, Kind: protocol.ControlCapabilityProof,
		InstallationID: serviceInstallID, ClaimID: challenge.ClaimID, Generation: challenge.Generation,
		WindowID: 71, Challenge: challenge.Challenge,
		Proof: serviceRebindProof(challenge.ClaimID, challenge.Generation, 71, challenge.Challenge),
	}); err != nil {
		t.Fatal(err)
	}
	var rebind *protocol.CapabilityRebind
	for rebind == nil {
		if value, ok := readControl(t, ws).(*protocol.CapabilityRebind); ok {
			rebind = value
		}
	}
	if rebind.ClaimID != "persisted-claim" {
		t.Fatalf("expected exact capability rebind, got %#v", rebind)
	}
	eventually(t, func() bool {
		snapshot, err := svc.Snapshot(family)
		return err == nil && snapshot.State == chrome.SessionReady && snapshot.Generation == 7
	})
	if err := ws.WriteJSON(protocol.Event{
		Contract: protocol.EventsContractV1, Kind: "event", EventID: "restored-removed",
		Sequence: 1, Event: "window_removed", Generation: 7,
		Payload: json.RawMessage(`{"claim_id":"persisted-claim"}`),
	}); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool {
		snapshot, err := svc.Snapshot(family)
		return err == nil && snapshot.State == chrome.SessionClosed
	})
}

func TestServiceLaunchTimeoutAndRetryableShutdown(t *testing.T) {
	store := newServiceStore(t)
	svc := newService(t, store, &fakeServiceLauncher{}, time.Hour, 15*time.Millisecond)
	family := serviceFamily(t, "timeout-root")
	if err := svc.StartFamily(family); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool {
		snapshot, err := svc.Snapshot(family)
		return err == nil && snapshot.State == chrome.SessionFailed &&
			snapshot.Failure != nil && snapshot.Failure.Code == chrome.ErrHandshakeTimeout
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := svc.Shutdown(ctx); err == nil {
		t.Fatal("cancelled shutdown unexpectedly succeeded")
	}
	if err := svc.Shutdown(context.Background()); err != nil {
		t.Fatalf("retry shutdown: %v", err)
	}
}

func TestServiceBootstrapCreatesStableCredentialAndEndpoint(t *testing.T) {
	store := testStore(t)
	start := func() *Service {
		svc, err := NewService(Config{
			Store: store, Launcher: &fakeServiceLauncher{}, ExtensionID: serviceExtensionID,
			IdleTimeout: time.Hour,
		})
		if err != nil {
			t.Fatal(err)
		}
		return svc
	}
	first := start()
	firstInfo, err := first.PairingInfo()
	if err != nil {
		t.Fatal(err)
	}
	credential, err := store.LoadCredential()
	if err != nil {
		t.Fatal(err)
	}
	if credential.LoopbackPort == 0 || firstInfo.Endpoint == "" ||
		firstInfo.InstallationID != credential.InstallationID ||
		firstInfo.InstallationSecret != credential.Secret {
		t.Fatalf("invalid initial pairing: %+v, credential=%+v", firstInfo, credential)
	}
	for _, value := range []string{credential.InstallationID, credential.Secret} {
		for _, ch := range value {
			if !((ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') ||
				(ch >= '0' && ch <= '9') || ch == '-' || ch == '_') {
				t.Fatalf("credential is not URL-safe: %q", value)
			}
		}
	}
	if err := first.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	second := start()
	defer second.Shutdown(context.Background())
	secondInfo, err := second.PairingInfo()
	if err != nil {
		t.Fatal(err)
	}
	if secondInfo != firstInfo {
		t.Fatalf("pairing rotated across restart: first=%+v second=%+v", firstInfo, secondInfo)
	}
	state, err := store.LoadState()
	if err != nil || state.ManagerEpoch != 2 {
		t.Fatalf("state after two starts = %+v, %v", state, err)
	}
}

func TestServiceOccupiedPersistedPortFailsWithoutRotation(t *testing.T) {
	store := testStore(t)
	svc, err := NewService(Config{
		Store: store, Launcher: &fakeServiceLauncher{}, ExtensionID: serviceExtensionID,
		IdleTimeout: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	credential, err := store.LoadCredential()
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	occupied, err := net.Listen("tcp4", fmt.Sprintf("127.0.0.1:%d", credential.LoopbackPort))
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	if _, err := NewService(Config{
		Store: store, Launcher: &fakeServiceLauncher{}, ExtensionID: serviceExtensionID,
		IdleTimeout: time.Hour,
	}); err == nil {
		t.Fatal("occupied persisted port unexpectedly rotated")
	}
	after, err := store.LoadCredential()
	if err != nil || after != credential {
		t.Fatalf("credential changed after occupied port: %+v, %v", after, err)
	}
}

func TestServicePartialBootstrapReleasesListenerAndLock(t *testing.T) {
	store := testStore(t)
	if _, err := NewService(Config{
		Store: store, Launcher: &fakeServiceLauncher{},
		ExtensionID: strings.Repeat("x", protocol.MaxIdentifierBytes+1),
		IdleTimeout: time.Hour,
	}); err == nil {
		t.Fatal("invalid bridge configuration unexpectedly succeeded")
	}
	credential, err := store.LoadCredential()
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp4", fmt.Sprintf("127.0.0.1:%d", credential.LoopbackPort))
	if err != nil {
		t.Fatalf("partial failure leaked listener: %v", err)
	}
	listener.Close()
	lock, err := store.Acquire()
	if err != nil {
		t.Fatalf("partial failure leaked lock: %v", err)
	}
	lock.Close()
}

func TestServiceFamilyMapBoundFailsBeforeManagerCreation(t *testing.T) {
	store := newServiceStore(t)
	svc := newService(t, store, &fakeServiceLauncher{}, time.Hour, time.Second)
	for i := range protocol.MaxListEntries {
		family := serviceFamily(t, fmt.Sprintf("bounded-%d", i))
		svc.families[family.ID()] = family
	}
	extra := serviceFamily(t, "bounded-extra")
	if err := svc.StartFamily(extra); err == nil {
		t.Fatal("family beyond protocol bound unexpectedly accepted")
	}
	if _, err := svc.Manager().Snapshot(extra); err == nil {
		t.Fatal("manager record was created after family bound failure")
	}
}

func TestServiceRestoredCommittedCloseNeverReadyAndAbsenceCompletes(t *testing.T) {
	store := newServiceStore(t)
	family := serviceFamily(t, "restored-closing-root")
	lock, err := store.Acquire()
	if err != nil {
		t.Fatal(err)
	}
	state, err := store.AdvanceEpoch(lock)
	if err != nil {
		t.Fatal(err)
	}
	state.Claims = []Claim{{
		FamilyHash: familyHash(family), Generation: 11, ClaimID: "closing-claim",
		InstallationID: serviceInstallID, Lifecycle: "closing", CloseEpoch: 9,
		ClosePhase: string(chrome.CloseCommitted), UpdatedAt: time.Now().UTC(),
	}}
	if err := store.SaveState(lock, state); err != nil {
		t.Fatal(err)
	}
	lock.Close()
	svc := newService(t, store, &fakeServiceLauncher{}, time.Hour, time.Second)
	if err := svc.StartFamily(family); err != nil {
		t.Fatal(err)
	}
	if snapshot, _ := svc.Snapshot(family); snapshot.State != chrome.SessionClosing ||
		snapshot.ClosePhase != chrome.CloseCommitted {
		t.Fatalf("restored snapshot = %+v", snapshot)
	}
	ws := dialService(t, svc)
	if err := ws.WriteJSON(protocol.OwnershipSnapshot{
		Contract: protocol.ControlContractV1, Kind: protocol.ControlOwnershipSnapshot,
		Records: []protocol.OwnershipRecord{{
			ClaimID: "closing-claim", Generation: 11, WindowID: 91, TabIDs: []int64{},
		}},
	}); err != nil {
		t.Fatal(err)
	}
	var challenge *protocol.CapabilityChallenge
	for challenge == nil {
		if value, ok := readControl(t, ws).(*protocol.CapabilityChallenge); ok {
			challenge = value
		}
	}
	if err := ws.WriteJSON(protocol.CapabilityProof{
		Contract: protocol.ControlContractV1, Kind: protocol.ControlCapabilityProof,
		InstallationID: serviceInstallID, ClaimID: challenge.ClaimID, Generation: challenge.Generation,
		WindowID: 91, Challenge: challenge.Challenge,
		Proof: serviceRebindProof(challenge.ClaimID, challenge.Generation, 91, challenge.Challenge),
	}); err != nil {
		t.Fatal(err)
	}
	var sawRebind, sawCommit bool
	for !sawRebind || !sawCommit {
		switch value := readControl(t, ws).(type) {
		case *protocol.CapabilityRebind:
			sawRebind = value.ClaimID == "closing-claim"
		case *protocol.CloseControl:
			sawCommit = value.Kind == protocol.ControlCloseCommit && value.CloseEpoch == 9
		}
	}
	if snapshot, _ := svc.Snapshot(family); snapshot.State != chrome.SessionClosing {
		t.Fatalf("proof made restored close ready: %+v", snapshot)
	}
	if err := ws.WriteJSON(protocol.OwnershipSnapshot{
		Contract: protocol.ControlContractV1, Kind: protocol.ControlOwnershipSnapshot,
		Records: []protocol.OwnershipRecord{},
	}); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool {
		snapshot, err := svc.Snapshot(family)
		return err == nil && snapshot.State == chrome.SessionClosed
	})
	raw, err := os.ReadFile(store.Paths().State)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{serviceSecret, `"window_id"`, `"family_capability"`} {
		if stringContains(string(raw), forbidden) {
			t.Fatalf("restored close state leaked %q: %s", forbidden, raw)
		}
	}
}

func TestServiceCloseCommittedSaveFailureRollsBackBeforeManagerCommit(t *testing.T) {
	store := newServiceStore(t)
	family := serviceFamily(t, "close-commit-save-failure")
	persistServiceClaim(t, store, Claim{
		FamilyHash: familyHash(family), Generation: 13, ClaimID: "save-failure-claim",
		InstallationID: serviceInstallID, Lifecycle: "closing", CloseEpoch: 17,
		ClosePhase: string(chrome.CloseSent), UpdatedAt: time.Now().UTC(),
	})
	svc := newService(t, store, &fakeServiceLauncher{}, time.Hour, time.Second)
	if err := svc.StartFamily(family); err != nil {
		t.Fatal(err)
	}
	if svc.closes[closeKey("save-failure-claim", 13)] == nil {
		t.Fatal("restored close record is missing")
	}
	message := protocol.CloseControl{
		Contract: protocol.ControlContractV1, Kind: protocol.ControlCloseCommitted,
		ClaimID: "save-failure-claim", Generation: 13, CloseEpoch: 17,
	}
	statePath := store.paths.State
	store.paths.State = filepath.Dir(statePath)
	svc.closeControl(message)
	store.paths.State = statePath

	svc.mu.Lock()
	phase := svc.claims["save-failure-claim"].persisted.ClosePhase
	svc.mu.Unlock()
	if phase != string(chrome.CloseSent) {
		t.Fatalf("in-memory close phase after save failure = %q", phase)
	}
	if snapshot, err := svc.Snapshot(family); err != nil || snapshot.ClosePhase != chrome.CloseSent {
		t.Fatalf("manager advanced despite save failure: snapshot=%+v err=%v", snapshot, err)
	}
	persisted, err := store.LoadState()
	if err != nil {
		t.Fatal(err)
	}
	if got := persisted.Claims[0].ClosePhase; got != string(chrome.CloseSent) {
		t.Fatalf("persisted close phase after save failure = %q", got)
	}

	svc.closeControl(message)
	if snapshot, err := svc.Snapshot(family); err != nil || snapshot.ClosePhase != chrome.CloseCommitted {
		t.Fatalf("manager did not advance after durable retry: snapshot=%+v err=%v", snapshot, err)
	}
	persisted, err = store.LoadState()
	if err != nil {
		t.Fatal(err)
	}
	if got := persisted.Claims[0].ClosePhase; got != string(chrome.CloseCommitted) {
		t.Fatalf("persisted close phase after retry = %q", got)
	}
	svc.completeAbsentClose(svc.closes[closeKey("save-failure-claim", 13)])
}

func TestServiceRestoredCloseSentAbsenceCompletes(t *testing.T) {
	store := newServiceStore(t)
	family := serviceFamily(t, "restored-close-sent-absent")
	persistServiceClaim(t, store, Claim{
		FamilyHash: familyHash(family), Generation: 19, ClaimID: "close-sent-claim",
		InstallationID: serviceInstallID, Lifecycle: "closing", CloseEpoch: 23,
		ClosePhase: string(chrome.CloseSent), UpdatedAt: time.Now().UTC(),
	})
	svc := newService(t, store, &fakeServiceLauncher{}, time.Hour, time.Second)
	if err := svc.StartFamily(family); err != nil {
		t.Fatal(err)
	}
	svc.ownershipSnapshot(protocol.OwnershipSnapshot{
		Contract: protocol.ControlContractV1, Kind: protocol.ControlOwnershipSnapshot,
		Records: []protocol.OwnershipRecord{},
	})
	if snapshot, err := svc.Snapshot(family); err != nil || snapshot.State != chrome.SessionClosed {
		t.Fatalf("absent CloseSent recovery did not close: snapshot=%+v err=%v", snapshot, err)
	}
	persisted, err := store.LoadState()
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Claims[0].Lifecycle != "closed" || persisted.Claims[0].ClosePhase != "" {
		t.Fatalf("absent CloseSent recovery state = %+v", persisted.Claims[0])
	}
}

func TestServiceCapabilityProofBindFailureRollsBackAndRechallenges(t *testing.T) {
	store := newServiceStore(t)
	family := serviceFamily(t, "proof-bind-failure")
	persistServiceClaim(t, store, Claim{
		FamilyHash: familyHash(family), Generation: 29, ClaimID: "proof-failure-claim",
		InstallationID: serviceInstallID, Lifecycle: "open", UpdatedAt: time.Now().UTC(),
	})
	svc := newService(t, store, &fakeServiceLauncher{}, time.Hour, time.Second)
	if err := svc.StartFamily(family); err != nil {
		t.Fatal(err)
	}
	snapshot := protocol.OwnershipSnapshot{
		Contract: protocol.ControlContractV1, Kind: protocol.ControlOwnershipSnapshot,
		Records: []protocol.OwnershipRecord{{
			ClaimID: "proof-failure-claim", Generation: 29, WindowID: 31, TabIDs: []int64{},
		}},
	}
	svc.ownershipSnapshot(snapshot)
	svc.mu.Lock()
	firstChallenge := svc.claims["proof-failure-claim"].challenge
	svc.mu.Unlock()
	if firstChallenge == "" {
		t.Fatal("initial ownership snapshot did not issue a challenge")
	}
	svc.capabilityProof(protocol.CapabilityProof{
		Contract: protocol.ControlContractV1, Kind: protocol.ControlCapabilityProof,
		InstallationID: serviceInstallID, ClaimID: "proof-failure-claim", Generation: 29,
		WindowID: 31, Challenge: firstChallenge,
		Proof: serviceRebindProof("proof-failure-claim", 29, 31, firstChallenge),
	})
	svc.mu.Lock()
	claim := svc.claims["proof-failure-claim"]
	proved, capability, consumedChallenge := claim.proved, claim.capability, claim.challenge
	svc.mu.Unlock()
	if proved || capability != "" || consumedChallenge != "" {
		t.Fatalf("failed Bind left proof state: proved=%v capability=%q challenge=%q",
			proved, capability, consumedChallenge)
	}

	svc.ownershipSnapshot(snapshot)
	svc.mu.Lock()
	secondChallenge := svc.claims["proof-failure-claim"].challenge
	svc.mu.Unlock()
	if secondChallenge == "" || secondChallenge == firstChallenge {
		t.Fatalf("later snapshot did not issue a fresh challenge: first=%q second=%q",
			firstChallenge, secondChallenge)
	}
	if err := svc.manager.MarkWindowAbsent(family, claim.token); err != nil {
		t.Fatal(err)
	}
}
