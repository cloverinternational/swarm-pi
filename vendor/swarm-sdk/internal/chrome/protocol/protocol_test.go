package protocol

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/chrome"
)

func TestNegotiateIndependentContractsAndCapabilities(t *testing.T) {
	local := Advertisement{
		RPC:          []VersionRange{{Major: 1, MinMinor: 0, MaxMinor: 4}},
		Events:       []VersionRange{{Major: 1, MinMinor: 0, MaxMinor: 2}},
		Capabilities: []string{"request_journal", "event_gap"},
	}
	peer := Advertisement{
		RPC:          []VersionRange{{Major: 1, MinMinor: 2, MaxMinor: 6}},
		Events:       []VersionRange{{Major: 1, MinMinor: 1, MaxMinor: 1}},
		Capabilities: []string{"event_gap", "other"},
	}
	got, err := Negotiate(local, peer, []string{"event_gap"})
	if err != nil {
		t.Fatal(err)
	}
	if got.RPC != "swarm.chrome.rpc/1.4" || got.Events != "swarm.chrome.events/1.1" ||
		len(got.Capabilities) != 1 || got.Capabilities[0] != "event_gap" {
		t.Fatalf("negotiated = %#v", got)
	}
	if _, err := Negotiate(local, peer, []string{"request_journal"}); err == nil ||
		err.Code != chrome.ErrProtocolIncompatible {
		t.Fatalf("missing capability error = %#v", err)
	}
	peer.RPC = []VersionRange{{Major: 2, MinMinor: 0, MaxMinor: 0}}
	if _, err := Negotiate(local, peer, nil); err == nil || err.Code != chrome.ErrProtocolIncompatible {
		t.Fatalf("major mismatch error = %#v", err)
	}
}

func TestResponseTerminalMappings(t *testing.T) {
	base := Response{
		Contract: RPCContractV1, Kind: "response", RequestID: "req-1",
		ClaimID: "claim-1", Generation: 3, Operation: "navigate",
	}
	success := base
	success.Status, success.Terminal, success.Result = StatusOK, TerminalCompleted, json.RawMessage(`{}`)
	rejected := base
	rejected.Status, rejected.Terminal, rejected.Result = StatusError, TerminalRejected, json.RawMessage(`null`)
	rejected.Error = chrome.NewError(chrome.ErrInvalidArguments, "Invalid navigation target.")
	failed := base
	failed.Status, failed.Terminal, failed.Result = StatusError, TerminalFailed, json.RawMessage(`null`)
	failed.Error = chrome.NewError(chrome.ErrExecutionFailed, "Navigation failed.")
	for _, valid := range []*Response{&success, &rejected, &failed} {
		if err := ValidateMessage(valid); err != nil {
			t.Errorf("valid %#v: %v", valid, err)
		}
	}
	bad := success
	bad.Status = StatusError
	if err := ValidateMessage(&bad); errorCode(err) != chrome.ErrProtocolMismatch {
		t.Fatalf("bad terminal mapping = %v", err)
	}
}

func TestDecodeStrictDiscriminatorsAndCorrelation(t *testing.T) {
	fixture := readFixture(t, "interleaving.json")
	var sequence []json.RawMessage
	if err := json.Unmarshal(fixture, &sequence); err != nil {
		t.Fatal(err)
	}
	if len(sequence) != 3 {
		t.Fatalf("fixture message count = %d", len(sequence))
	}
	first, err := DecodeMessage(sequence[0])
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := first.(*Event); !ok {
		t.Fatalf("first message type = %T; notification could displace response", first)
	}
	second, err := DecodeMessage(sequence[1])
	if err != nil {
		t.Fatal(err)
	}
	response := second.(*Response)
	pending := Request{RequestID: "req-tabs", Generation: 3, Operation: "tabs"}
	if err := ValidateResponseCorrelation(*response, pending, "claim-a"); err != nil {
		t.Fatal(err)
	}
	wrong := pending
	wrong.Operation = "navigate"
	if err := ValidateResponseCorrelation(*response, wrong, "claim-a"); errorCode(err) != chrome.ErrProtocolMismatch {
		t.Fatalf("wrong operation = %v", err)
	}
	if _, err := DecodeMessage([]byte(`{"contract":"swarm.chrome.rpc/1.0","kind":"request","extra":true}`)); err == nil {
		t.Fatal("unknown field accepted")
	}
	if _, err := DecodeMessage([]byte(`{"contract":"swarm.chrome.rpc/1.0","kind":"response","request_id":"r","claim_id":"c","generation":1,"operation":"tabs","status":"ok","terminal":"completed","result":{}}`)); err == nil {
		t.Fatal("response missing explicit error field accepted")
	}
	if _, err := DecodeMessage([]byte(`{"contract":"swarm.chrome.rpc/2.0","kind":"request"}`)); errorCode(err) != chrome.ErrProtocolIncompatible {
		t.Fatalf("unknown major = %v", err)
	}
}

func TestExtensionJavaScriptFixturePinsGoDiscriminators(t *testing.T) {
	fixture := string(readFixture(t, "contracts_v1.mjs"))
	for _, expected := range []string{
		RPCContractV1, EventsContractV1,
		string(TerminalCompleted), string(TerminalRejected), string(TerminalFailed),
		string(chrome.ExecutionNotStarted), string(chrome.ExecutionFailed), string(chrome.ExecutionIndeterminate),
	} {
		if !strings.Contains(fixture, `"`+expected+`"`) {
			t.Errorf("extension fixture missing %q", expected)
		}
	}
}

func TestEventRequestIDOnlyForProgress(t *testing.T) {
	base := Event{
		Contract: EventsContractV1, Kind: "event", EventID: "event-1",
		Sequence: 1, Generation: 3, Payload: json.RawMessage(`{}`),
	}
	base.Event = "human_activity"
	if err := ValidateMessage(&base); err != nil {
		t.Fatal(err)
	}
	base.RequestID = "req-1"
	if err := ValidateMessage(&base); errorCode(err) != chrome.ErrProtocolMismatch {
		t.Fatalf("ordinary event request_id = %v", err)
	}
	base.Event = "request_started"
	if err := ValidateMessage(&base); err != nil {
		t.Fatal(err)
	}
	base.RequestID = ""
	if err := ValidateMessage(&base); errorCode(err) != chrome.ErrProtocolMismatch {
		t.Fatalf("progress missing request_id = %v", err)
	}
}

func TestParseContractVersionStrict(t *testing.T) {
	major, minor, err := ParseContractVersion(RPCContractV1, "swarm.chrome.rpc")
	if err != nil || major != 1 || minor != 0 {
		t.Fatalf("parse = %d.%d, %v", major, minor, err)
	}
	for _, bad := range []string{"1.0", "swarm.chrome.rpc/v1.0", "swarm.chrome.rpc/1.0.0", "swarm.chrome.events/1.0"} {
		if _, _, err := ParseContractVersion(bad, "swarm.chrome.rpc"); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func errorCode(err error) chrome.ErrorCode {
	var typed *chrome.Error
	if errors.As(err, &typed) {
		return typed.Code
	}
	return ""
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}
