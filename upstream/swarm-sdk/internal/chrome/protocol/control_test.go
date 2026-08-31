package protocol

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

type controlParityFixture struct {
	ExtensionToManager []json.RawMessage `json:"extension_to_manager"`
	ManagerToExtension []json.RawMessage `json:"manager_to_extension"`
}

func TestDecodeControlStrictAndVersioned(t *testing.T) {
	message := AuthHello{
		Contract: ControlContractV1, Kind: ControlAuthHello,
		InstallationID: "installation-1", ClientNonce: "nonce-1",
		Supported: Advertisement{
			RPC:    []VersionRange{{Major: 1, MinMinor: 0, MaxMinor: 0}},
			Events: []VersionRange{{Major: 1, MinMinor: 0, MaxMinor: 0}},
		},
	}
	raw, err := json.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeControl(raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := decoded.(*AuthHello); !ok {
		t.Fatalf("decoded type = %T", decoded)
	}

	bad := []string{
		`{"contract":"swarm.chrome.control/2.0","kind":"auth_hello"}`,
		`{"contract":"swarm.chrome.control/1.0","kind":"unknown"}`,
		`{"contract":"swarm.chrome.control/1.0","kind":"create_owned_window","launch_id":"l","extra":true}`,
		`{"contract":"swarm.chrome.control/1.0","kind":"create_owned_window"}`,
	}
	for _, input := range bad {
		if _, err := DecodeControl([]byte(input)); err == nil {
			t.Errorf("accepted %s", input)
		}
	}
}

func TestControlPhasesAndReconciliationValidation(t *testing.T) {
	valid := []Control{
		&ClaimControl{Contract: ControlContractV1, Kind: ControlClaimPrepare, LaunchID: "l", ClaimID: "c", Generation: 1, WindowID: 3},
		&CloseControl{Contract: ControlContractV1, Kind: ControlCloseCancel, ClaimID: "c", Generation: 1, CloseEpoch: 2},
		&ReconcileResult{
			Contract: ControlContractV1, Kind: ControlReconcileResult, ClaimID: "c", Generation: 1,
			Entries: []ReconcileEntry{
				{RequestID: "r1", State: ReconcileStarted, Response: json.RawMessage(`null`)},
				{RequestID: "r2", State: ReconcileCompletedCached, Response: json.RawMessage(`{"status":"ok"}`)},
			},
		},
	}
	for _, message := range valid {
		if err := ValidateControl(message); err != nil {
			t.Errorf("%T: %v", message, err)
		}
	}
	invalid := valid[2].(*ReconcileResult)
	invalid.Entries[0].Response = json.RawMessage(`{}`)
	if err := ValidateControl(invalid); err == nil {
		t.Fatal("accepted cached response for non-terminal reconciliation state")
	}
}

func TestCloseEpochClaimCapabilityDirectionsAndOwnershipReconcile(t *testing.T) {
	closeMessage := CloseControl{
		Contract: ControlContractV1, Kind: ControlCloseCommit,
		ClaimID: "claim", Generation: 4, CloseEpoch: 9,
	}
	raw, err := json.Marshal(closeMessage)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "close_id") || !strings.Contains(string(raw), `"close_epoch":9`) {
		t.Fatalf("close JSON = %s", raw)
	}
	committed := `{"contract":"swarm.chrome.control/1.0","kind":"claim_committed","launch_id":"launch","claim_id":"claim","generation":4,"window_id":7,"family_capability":"cap"}`
	if _, err := DecodeControl([]byte(committed)); err != nil {
		t.Fatal(err)
	}
	for _, malformed := range []string{
		`{"contract":"swarm.chrome.control/1.0","kind":"claim_committed","launch_id":"launch","claim_id":"claim","generation":4,"window_id":7}`,
		`{"contract":"swarm.chrome.control/1.0","kind":"claim_staged","launch_id":"launch","claim_id":"claim","generation":4,"window_id":7,"family_capability":"cap"}`,
	} {
		if _, err := DecodeControl([]byte(malformed)); err == nil {
			t.Fatalf("accepted claim capability shape %s", malformed)
		}
	}
	reconcile := OwnershipReconcile{
		Contract: ControlContractV1, Kind: ControlOwnershipReconcile,
		LiveLaunchIDs: []string{"launch"},
		Pending:       []ClaimTuple{{ClaimID: "pending", Generation: 1}},
		Committed:     []ClaimTuple{},
		Authorized:    []ClaimTuple{{ClaimID: "authorized", Generation: 2}},
	}
	if err := ValidateControl(&reconcile); err != nil {
		t.Fatal(err)
	}
	if !ValidOutboundControl(ControlOwnershipReconcile) || ValidInboundControl(ControlOwnershipReconcile) ||
		!ValidOutboundControl(ControlClaimCommitted) || ValidInboundControl(ControlClaimCommitted) ||
		ValidOutboundControl(ControlAuthChallenge) {
		t.Fatal("control direction table is not closed")
	}
}

func TestControlIdentifierAndListBounds(t *testing.T) {
	tooLong := strings.Repeat("x", MaxIdentifierBytes+1)
	if err := ValidateControl(&CreateOwnedWindow{
		Contract: ControlContractV1, Kind: ControlCreateOwnedWindow, LaunchID: tooLong,
	}); err == nil {
		t.Fatal("accepted oversized identifier")
	}
	ids := make([]string, MaxListEntries+1)
	for index := range ids {
		ids[index] = "request"
	}
	if err := ValidateControl(&ReconcileRequest{
		Contract: ControlContractV1, Kind: ControlReconcileRequest,
		ClaimID: "claim", Generation: 1, RequestIDs: ids,
	}); err == nil {
		t.Fatal("accepted oversized list")
	}
}

func TestSharedControlParityFixture(t *testing.T) {
	var fixture controlParityFixture
	if err := json.Unmarshal(readFixture(t, "control_parity_v1.json"), &fixture); err != nil {
		t.Fatal(err)
	}
	check := func(raw json.RawMessage, extensionToManager bool) {
		t.Helper()
		decoded, err := DecodeControl(raw)
		if err != nil {
			t.Fatalf("DecodeControl(%s): %v", raw, err)
		}
		kind := ControlKind(decoded)
		preAuth := kind == ControlAuthHello || kind == ControlAuthProof ||
			kind == ControlAuthChallenge || kind == ControlNegotiated
		if !preAuth {
			if extensionToManager != ValidInboundControl(kind) {
				t.Fatalf("direction mismatch for %q", kind)
			}
			if extensionToManager == ValidOutboundControl(kind) {
				t.Fatalf("control %q is valid in both directions", kind)
			}
		}
		encoded, err := json.Marshal(decoded)
		if err != nil {
			t.Fatal(err)
		}
		var want, got any
		if err := json.Unmarshal(raw, &want); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(encoded, &got); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("round trip for %q = %s, want %s", kind, encoded, raw)
		}
	}
	for _, raw := range fixture.ExtensionToManager {
		check(raw, true)
	}
	for _, raw := range fixture.ManagerToExtension {
		check(raw, false)
	}
}

func TestGoWireBoundsMatchJavaScriptSafeValues(t *testing.T) {
	valid := &CloseControl{
		Contract: ControlContractV1, Kind: ControlCloseCommit,
		ClaimID: "claim", Generation: MaxSafeInteger, CloseEpoch: MaxSafeInteger,
	}
	if err := ValidateControl(valid); err != nil {
		t.Fatalf("maximum safe integer rejected: %v", err)
	}
	invalid := *valid
	invalid.Generation++
	if err := ValidateControl(&invalid); err == nil {
		t.Fatal("accepted generation JavaScript cannot represent exactly")
	}
	invalid = *valid
	invalid.ClaimID = "claim\nforged"
	if err := ValidateControl(&invalid); err == nil {
		t.Fatal("accepted identifier containing an ASCII control character")
	}
	if _, err := DecodeMessage([]byte(`{
		"contract":"swarm.chrome.rpc/1.0","kind":"request",
		"request_id":"request","family_capability":"capability","generation":1,
		"operation":"tabs","deadline_unix_ms":1,"payload":[]
	}`)); err == nil {
		t.Fatal("accepted a non-object request payload")
	}
}
