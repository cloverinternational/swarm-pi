package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/chrome/protocol"
	"github.com/gorilla/websocket"
)

const (
	testExtensionID  = "abcdefghijklmnopabcdefghijklmnop"
	testInstallation = "installation-test"
	testManager      = "manager-test"
	testCapability   = "capability-test"
)

var testSecret = []byte("0123456789abcdef0123456789abcdef")

func testAdvertisement() protocol.Advertisement {
	return protocol.Advertisement{
		RPC:          []protocol.VersionRange{{Major: 1, MinMinor: 0, MaxMinor: 0}},
		Events:       []protocol.VersionRange{{Major: 1, MinMinor: 0, MaxMinor: 0}},
		Capabilities: []string{"event_gap", "request_journal"},
	}
}

func newTestServer(t *testing.T, mutate func(*Config)) *Server {
	t.Helper()
	cfg := Config{
		ExtensionID: testExtensionID, InstallationID: testInstallation,
		InstallationSecret: testSecret, ManagerEpoch: 7, ManagerInstanceID: testManager,
		Supported: testAdvertisement(), RequiredCapabilities: []string{"event_gap"},
		QueueSize: 8, PendingLimit: 8, MaxSnapshotRecords: 4,
		HandshakeTimeout: time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: time.Second,
	}
	if mutate != nil {
		mutate(&cfg)
	}
	server, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if err := server.Listen(listener); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := server.Close(ctx); err != nil {
			t.Errorf("close: %v", err)
		}
	})
	return server
}

func dialAuthenticated(t *testing.T, server *Server, secret []byte) *websocket.Conn {
	t.Helper()
	headers := http.Header{"Origin": []string{"chrome-extension://" + testExtensionID}}
	ws, response, err := websocket.DefaultDialer.Dial(server.URL(), headers)
	if err != nil {
		if response != nil {
			t.Fatalf("dial: %v (status %d)", err, response.StatusCode)
		}
		t.Fatal(err)
	}
	hello := protocol.AuthHello{
		Contract: protocol.ControlContractV1, Kind: protocol.ControlAuthHello,
		InstallationID: testInstallation, ClientNonce: "client-nonce", Supported: testAdvertisement(),
	}
	if err := ws.WriteJSON(hello); err != nil {
		t.Fatal(err)
	}
	var challenge protocol.AuthChallenge
	if err := ws.ReadJSON(&challenge); err != nil {
		t.Fatal(err)
	}
	serverProof := handshakeProof(secret, "server", challenge.InstallationID, challenge.ClientNonce,
		challenge.ServerNonce, challenge.ManagerEpoch, challenge.ManagerInstanceID, challenge.Selected)
	if !secureEqual(serverProof, challenge.ServerProof) {
		t.Fatal("server proof mismatch")
	}
	proof := protocol.AuthProof{
		Contract: protocol.ControlContractV1, Kind: protocol.ControlAuthProof,
		InstallationID: challenge.InstallationID, ClientNonce: challenge.ClientNonce,
		ServerNonce: challenge.ServerNonce, ManagerEpoch: challenge.ManagerEpoch,
		ManagerInstanceID: challenge.ManagerInstanceID, Selected: challenge.Selected,
	}
	proof.ClientProof = handshakeProof(secret, "client", proof.InstallationID, proof.ClientNonce,
		proof.ServerNonce, proof.ManagerEpoch, proof.ManagerInstanceID, proof.Selected)
	if err := ws.WriteJSON(proof); err != nil {
		t.Fatal(err)
	}
	var negotiated protocol.NegotiatedControl
	if err := ws.ReadJSON(&negotiated); err != nil {
		t.Fatal(err)
	}
	if negotiated.SessionID == "" || negotiated.ManagerEpoch != 7 {
		t.Fatalf("negotiated = %#v", negotiated)
	}
	t.Cleanup(func() { _ = ws.Close() })
	return ws
}

func TestAuthenticationPinsOriginHostAndRejectsURLCredentials(t *testing.T) {
	server := newTestServer(t, nil)

	for _, attempt := range []struct {
		url    string
		origin string
	}{
		{server.URL(), "chrome-extension://wrong"},
		{server.URL() + "?token=secret", "chrome-extension://" + testExtensionID},
	} {
		headers := http.Header{"Origin": []string{attempt.origin}}
		ws, response, err := websocket.DefaultDialer.Dial(attempt.url, headers)
		if ws != nil {
			_ = ws.Close()
		}
		if err == nil || response == nil || response.StatusCode != http.StatusForbidden {
			t.Fatalf("unexpected dial result: err=%v response=%v", err, response)
		}
	}
	httpURL := strings.Replace(server.URL(), "ws://", "http://", 1)
	request, err := http.NewRequest(http.MethodGet, httpURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Host = strings.Replace(request.URL.Host, "127.0.0.1", "localhost", 1)
	request.Header.Set("Origin", "chrome-extension://"+testExtensionID)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("localhost Host status = %d", response.StatusCode)
	}
	ws := dialAuthenticated(t, server, testSecret)
	if ws == nil {
		t.Fatal("authenticated websocket is nil")
	}
}

func TestAuthenticationRejectsWrongMutualProof(t *testing.T) {
	server := newTestServer(t, nil)
	headers := http.Header{"Origin": []string{"chrome-extension://" + testExtensionID}}
	ws, _, err := websocket.DefaultDialer.Dial(server.URL(), headers)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	if err := ws.WriteJSON(protocol.AuthHello{
		Contract: protocol.ControlContractV1, Kind: protocol.ControlAuthHello,
		InstallationID: testInstallation, ClientNonce: "nonce", Supported: testAdvertisement(),
	}); err != nil {
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
		ManagerInstanceID: challenge.ManagerInstanceID, Selected: challenge.Selected, ClientProof: "wrong",
	}
	if err := ws.WriteJSON(proof); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ws.ReadMessage(); err == nil {
		t.Fatal("wrong proof authenticated")
	}
}

func makeRequest(id, operation string) protocol.Request {
	return protocol.Request{
		Contract: protocol.RPCContractV1, Kind: "request", RequestID: id,
		FamilyCapability: testCapability, Generation: 3, Operation: operation,
		DeadlineUnixMS: time.Now().Add(3 * time.Second).UnixMilli(), Payload: json.RawMessage(`{}`),
	}
}

func completed(request protocol.Request) protocol.Response {
	return protocol.Response{
		Contract: protocol.RPCContractV1, Kind: "response", RequestID: request.RequestID,
		ClaimID: "claim-1", Generation: request.Generation, Operation: request.Operation,
		Status: protocol.StatusOK, Terminal: protocol.TerminalCompleted,
		Result: json.RawMessage(`{"ok":true}`), Error: nil,
	}
}

func TestCorrelationInterleavingAndReverseResponses(t *testing.T) {
	var mu sync.Mutex
	var events []string
	server := newTestServer(t, func(cfg *Config) {
		cfg.OnEvent = func(event protocol.Event) {
			// Re-entering a lock-taking API proves callbacks are not under Server.mu.
			_ = serverDeliveryProbe
			mu.Lock()
			events = append(events, event.EventID)
			mu.Unlock()
		}
	})
	ws := dialAuthenticated(t, server, testSecret)
	if err := server.Bind("claim-1", 3, testCapability); err != nil {
		t.Fatal(err)
	}
	requests := []protocol.Request{makeRequest("request-a", "tabs"), makeRequest("request-b", "session_status")}
	type callResult struct {
		response protocol.Response
		err      error
	}
	results := make(chan callResult, 2)
	for _, request := range requests {
		request := request
		go func() {
			response, err := server.Dispatch(context.Background(), "claim-1", request)
			results <- callResult{response, err}
		}()
	}
	received := make(map[string]protocol.Request)
	for range requests {
		var request protocol.Request
		if err := ws.ReadJSON(&request); err != nil {
			t.Fatal(err)
		}
		received[request.RequestID] = request
	}
	event := protocol.Event{
		Contract: protocol.EventsContractV1, Kind: "event", EventID: "event-before-response",
		Sequence: 1, Event: "human_activity", Generation: 3, Payload: json.RawMessage(`{}`),
	}
	if err := ws.WriteJSON(event); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"request-b", "request-a"} {
		response := completed(received[id])
		if err := ws.WriteJSON(response); err != nil {
			t.Fatal(err)
		}
	}
	seen := make(map[string]bool)
	for range requests {
		result := <-results
		if result.err != nil {
			t.Fatal(result.err)
		}
		seen[result.response.RequestID] = true
	}
	if !seen["request-a"] || !seen["request-b"] {
		t.Fatalf("responses = %v", seen)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(events) != 1 || events[0] != event.EventID {
		t.Fatalf("events = %v", events)
	}
}

var serverDeliveryProbe = func() {}

func TestEventDedupGapAndCallbackReentrancy(t *testing.T) {
	var server *Server
	gaps := make(chan EventGap, 1)
	events := make(chan uint64, 3)
	server = newTestServer(t, func(cfg *Config) {
		cfg.OnEvent = func(event protocol.Event) {
			server.Pending()
			events <- event.Sequence
		}
		cfg.OnEventGap = func(gap EventGap) {
			server.Pending()
			gaps <- gap
		}
	})
	ws := dialAuthenticated(t, server, testSecret)
	if err := server.Bind("claim-1", 3, testCapability); err != nil {
		t.Fatal(err)
	}
	for _, sequence := range []uint64{1, 1, 3} {
		if err := ws.WriteJSON(protocol.Event{
			Contract: protocol.EventsContractV1, Kind: "event", EventID: "event",
			Sequence: sequence, Event: "human_activity", Generation: 3, Payload: json.RawMessage(`{}`),
		}); err != nil {
			t.Fatal(err)
		}
	}
	if got := <-events; got != 1 {
		t.Fatalf("first sequence = %d", got)
	}
	if got := <-events; got != 3 {
		t.Fatalf("gap sequence = %d", got)
	}
	select {
	case got := <-events:
		t.Fatalf("duplicate delivered: %d", got)
	case <-time.After(50 * time.Millisecond):
	}
	gap := <-gaps
	if gap.ClaimID != "claim-1" || gap.Expected != 2 || gap.Received != 3 {
		t.Fatalf("gap = %#v", gap)
	}
}

func TestReconnectRevokesBindingsAndDoesNotReplay(t *testing.T) {
	disconnected := make(chan Disconnect, 1)
	server := newTestServer(t, func(cfg *Config) { cfg.OnDisconnect = func(value Disconnect) { disconnected <- value } })
	first := dialAuthenticated(t, server, testSecret)
	if err := server.Bind("claim-1", 3, testCapability); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		_, err := server.Dispatch(context.Background(), "claim-1", makeRequest("mutating", "click"))
		result <- err
	}()
	var dispatched protocol.Request
	if err := first.ReadJSON(&dispatched); err != nil {
		t.Fatal(err)
	}
	second := dialAuthenticated(t, server, testSecret)
	select {
	case err := <-result:
		if !errors.Is(err, ErrClosed) {
			t.Fatalf("disconnect result = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("pending dispatch did not observe disconnect")
	}
	notice := <-disconnected
	if len(notice.Pending) != 1 || !notice.Pending[0].Dispatched || notice.Pending[0].State != DeliveryDisconnected {
		t.Fatalf("disconnect = %#v", notice)
	}
	if _, err := server.Dispatch(context.Background(), "claim-1", makeRequest("no-replay", "click")); !errors.Is(err, ErrStaleBinding) {
		t.Fatalf("old capability survived reconnect: %v", err)
	}
	_ = second.SetReadDeadline(time.Now().Add(75 * time.Millisecond))
	if _, _, err := second.ReadMessage(); err == nil {
		t.Fatal("mutating request replayed on new connection")
	}
}

func TestPendingAndSnapshotBounds(t *testing.T) {
	controls := make(chan protocol.Control, 1)
	server := newTestServer(t, func(cfg *Config) {
		cfg.PendingLimit = 1
		cfg.MaxSnapshotRecords = 1
		cfg.OnControl = func(control protocol.Control) { controls <- control }
	})
	ws := dialAuthenticated(t, server, testSecret)
	if err := server.Bind("claim-1", 3, testCapability); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	firstDone := make(chan error, 1)
	go func() {
		_, err := server.Dispatch(ctx, "claim-1", makeRequest("first", "tabs"))
		firstDone <- err
	}()
	var request protocol.Request
	if err := ws.ReadJSON(&request); err != nil {
		t.Fatal(err)
	}
	if _, err := server.Dispatch(context.Background(), "claim-1", makeRequest("second", "tabs")); !errors.Is(err, ErrPendingFull) {
		t.Fatalf("pending bound = %v", err)
	}
	if err := ws.WriteJSON(protocol.OwnershipSnapshot{
		Contract: protocol.ControlContractV1, Kind: protocol.ControlOwnershipSnapshot,
		Records: []protocol.OwnershipRecord{
			{ClaimID: "a", Generation: 1, WindowID: 1, TabIDs: []int64{}},
			{ClaimID: "b", Generation: 1, WindowID: 2, TabIDs: []int64{}},
		},
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case control := <-controls:
		t.Fatalf("oversized snapshot delivered: %T", control)
	case <-time.After(50 * time.Millisecond):
	}
	cancel()
	if err := <-firstDone; !errors.Is(err, ErrClosed) {
		t.Fatalf("cancel = %v", err)
	}
}

func TestCapabilityProofBoundToClaimGenerationAndWindow(t *testing.T) {
	server := newTestServer(t, nil)
	proof := protocol.CapabilityProof{
		Contract: protocol.ControlContractV1, Kind: protocol.ControlCapabilityProof,
		InstallationID: testInstallation, ClaimID: "claim-1", Generation: 4,
		WindowID: 22, Challenge: "challenge",
	}
	proof.Proof = rebindProof(testSecret, proof.InstallationID, proof.ClaimID, proof.Generation, proof.WindowID, proof.Challenge)
	if !server.VerifyCapabilityProof(proof) {
		t.Fatal("valid proof rejected")
	}
	proof.Generation++
	if server.VerifyCapabilityProof(proof) {
		t.Fatal("proof accepted for another generation")
	}
}

func TestConfigSlicesAreDeepCopiedAndSelectedTranscriptIsBound(t *testing.T) {
	secret := append([]byte(nil), testSecret...)
	required := []string{"event_gap"}
	supported := testAdvertisement()
	server, err := New(Config{
		ExtensionID: testExtensionID, InstallationID: testInstallation,
		InstallationSecret: secret, ManagerEpoch: 7, ManagerInstanceID: testManager,
		Supported: supported, RequiredCapabilities: required,
	})
	if err != nil {
		t.Fatal(err)
	}
	secret[0] ^= 0xff
	required[0] = "changed"
	supported.Capabilities[0] = "changed"
	if server.cfg.InstallationSecret[0] != testSecret[0] ||
		server.cfg.RequiredCapabilities[0] != "event_gap" ||
		server.cfg.Supported.Capabilities[0] != "event_gap" {
		t.Fatal("New retained caller-owned config slices")
	}
	selected := protocol.Negotiated{
		RPC: protocol.RPCContractV1, Events: protocol.EventsContractV1,
		Capabilities: []string{"event_gap"},
	}
	proof := handshakeProof(testSecret, "server", "i", "c", "s", 1, "m", selected)
	selected.Capabilities[0] = "request_journal"
	if secureEqual(proof, handshakeProof(testSecret, "server", "i", "c", "s", 1, "m", selected)) {
		t.Fatal("HMAC transcript did not bind selected capabilities")
	}
}

func TestPostAuthWrongDirectionAndMalformedMessageDisconnect(t *testing.T) {
	disconnected := make(chan Disconnect, 2)
	server := newTestServer(t, func(cfg *Config) {
		cfg.OnDisconnect = func(value Disconnect) { disconnected <- value }
	})
	ws := dialAuthenticated(t, server, testSecret)
	if err := ws.WriteJSON(protocol.AuthHello{
		Contract: protocol.ControlContractV1, Kind: protocol.ControlAuthHello,
		InstallationID: testInstallation, ClientNonce: "again", Supported: testAdvertisement(),
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-disconnected:
	case <-time.After(time.Second):
		t.Fatal("post-auth auth control did not disconnect")
	}

	ws = dialAuthenticated(t, server, testSecret)
	if err := ws.WriteMessage(websocket.TextMessage, []byte(`{"kind":"event","broken":true}`)); err != nil {
		t.Fatal(err)
	}
	select {
	case <-disconnected:
	case <-time.After(time.Second):
		t.Fatal("malformed post-auth message did not disconnect")
	}
}

func TestOutboundDirectionAndEncodedSizeAreBounded(t *testing.T) {
	server := newTestServer(t, nil)
	_ = dialAuthenticated(t, server, testSecret)
	if err := server.SendControl(&protocol.AuthHello{
		Contract: protocol.ControlContractV1, Kind: protocol.ControlAuthHello,
		InstallationID: testInstallation, ClientNonce: "nonce", Supported: testAdvertisement(),
	}); err == nil {
		t.Fatal("server emitted inbound-only auth control")
	}
	if err := server.Bind("claim-1", 3, testCapability); err != nil {
		t.Fatal(err)
	}
	request := makeRequest("oversized", "tabs")
	request.Payload = json.RawMessage(`{"value":"` + strings.Repeat("x", protocol.MaxMessageBytes) + `"}`)
	if _, err := server.Dispatch(context.Background(), "claim-1", request); err == nil {
		t.Fatal("oversized outbound request was accepted")
	}
}

func TestHeartbeatDisconnectsDeadPeerWithoutApplicationEvent(t *testing.T) {
	disconnected := make(chan Disconnect, 1)
	events := make(chan protocol.Event, 1)
	server := newTestServer(t, func(cfg *Config) {
		cfg.ReadTimeout = 120 * time.Millisecond
		cfg.PingInterval = 20 * time.Millisecond
		cfg.OnDisconnect = func(value Disconnect) { disconnected <- value }
		cfg.OnEvent = func(value protocol.Event) { events <- value }
	})
	_ = dialAuthenticated(t, server, testSecret)
	select {
	case <-disconnected:
	case <-time.After(time.Second):
		t.Fatal("dead peer was not disconnected")
	}
	select {
	case event := <-events:
		t.Fatalf("heartbeat generated application activity: %#v", event)
	default:
	}
}

func TestOnlyIPv4LocalhostAddressAccepted(t *testing.T) {
	if !isIPv4Localhost(testAddr("127.0.0.1:9")) {
		t.Fatal("127.0.0.1 rejected")
	}
	for _, addr := range []testAddr{"127.0.0.2:9", "[::1]:9", "localhost:9"} {
		if isIPv4Localhost(addr) {
			t.Fatalf("accepted %s", addr)
		}
	}
}

func TestSharedUTF8AuthenticationVectors(t *testing.T) {
	raw, err := os.ReadFile("../protocol/testdata/control_parity_v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Secret    string              `json:"secret"`
		Selected  protocol.Negotiated `json:"selected"`
		Handshake struct {
			InstallationID    string `json:"installation_id"`
			ClientNonce       string `json:"client_nonce"`
			ServerNonce       string `json:"server_nonce"`
			ManagerEpoch      uint64 `json:"manager_epoch"`
			ManagerInstanceID string `json:"manager_instance_id"`
			ServerProof       string `json:"server_proof"`
			ClientProof       string `json:"client_proof"`
		} `json:"handshake"`
		Rebind struct {
			ClaimID    string `json:"claim_id"`
			Generation uint64 `json:"generation"`
			WindowID   int64  `json:"window_id"`
			Challenge  string `json:"challenge"`
			Proof      string `json:"proof"`
		} `json:"rebind"`
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	secret := []byte(fixture.Secret)
	for role, want := range map[string]string{
		"server": fixture.Handshake.ServerProof,
		"client": fixture.Handshake.ClientProof,
	} {
		got := handshakeProof(
			secret, role, fixture.Handshake.InstallationID,
			fixture.Handshake.ClientNonce, fixture.Handshake.ServerNonce,
			fixture.Handshake.ManagerEpoch, fixture.Handshake.ManagerInstanceID,
			fixture.Selected,
		)
		if got != want {
			t.Fatalf("%s proof = %q, want %q", role, got, want)
		}
	}
	got := rebindProof(
		secret, fixture.Handshake.InstallationID, fixture.Rebind.ClaimID,
		fixture.Rebind.Generation, fixture.Rebind.WindowID, fixture.Rebind.Challenge,
	)
	if got != fixture.Rebind.Proof {
		t.Fatalf("rebind proof = %q, want %q", got, fixture.Rebind.Proof)
	}
}

type testAddr string

func (testAddr) Network() string      { return "tcp" }
func (value testAddr) String() string { return string(value) }
