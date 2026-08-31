package protocol

import (
	"bytes"
	"encoding/json"
	"fmt"
)

const ControlContractV1 = "swarm.chrome.control/1.0"

const (
	ControlAuthHello            = "auth_hello"
	ControlAuthChallenge        = "auth_challenge"
	ControlAuthProof            = "auth_proof"
	ControlNegotiated           = "negotiated"
	ControlCreateOwnedWindow    = "create_owned_window"
	ControlWindowClaimRequested = "window_claim_requested"
	ControlClaimPrepare         = "claim_prepare"
	ControlClaimStaged          = "claim_staged"
	ControlClaimCommitted       = "claim_committed"
	ControlWindowClaimed        = "window_claimed"
	ControlCapabilityChallenge  = "capability_challenge"
	ControlCapabilityProof      = "capability_proof"
	ControlCapabilityRebind     = "capability_rebind"
	ControlOwnershipSnapshot    = "ownership_snapshot"
	ControlOwnershipReconcile   = "ownership_reconcile"
	ControlClosePrepare         = "close_prepare"
	ControlClosePrepared        = "close_prepared"
	ControlCloseCancel          = "close_cancel"
	ControlCloseCancelled       = "close_cancelled"
	ControlCloseCommit          = "close_commit"
	ControlCloseCommitted       = "close_committed"
	ControlReconcileRequest     = "reconcile_request"
	ControlReconcileResult      = "reconcile_result"
	ControlTerminalAcknowledged = "terminal_acknowledged"
)

// Control is implemented by every strict, versioned bridge control DTO.
type Control interface {
	controlMessage()
}

type controlHeader struct {
	Contract string `json:"contract"`
	Kind     string `json:"kind"`
}

type AuthHello struct {
	Contract       string        `json:"contract"`
	Kind           string        `json:"kind"`
	InstallationID string        `json:"installation_id"`
	ClientNonce    string        `json:"client_nonce"`
	Supported      Advertisement `json:"supported"`
}

type AuthChallenge struct {
	Contract          string     `json:"contract"`
	Kind              string     `json:"kind"`
	InstallationID    string     `json:"installation_id"`
	ClientNonce       string     `json:"client_nonce"`
	ServerNonce       string     `json:"server_nonce"`
	ManagerEpoch      uint64     `json:"manager_epoch"`
	ManagerInstanceID string     `json:"manager_instance_id"`
	Selected          Negotiated `json:"selected"`
	ServerProof       string     `json:"server_proof"`
}

type AuthProof struct {
	Contract          string     `json:"contract"`
	Kind              string     `json:"kind"`
	InstallationID    string     `json:"installation_id"`
	ClientNonce       string     `json:"client_nonce"`
	ServerNonce       string     `json:"server_nonce"`
	ManagerEpoch      uint64     `json:"manager_epoch"`
	ManagerInstanceID string     `json:"manager_instance_id"`
	Selected          Negotiated `json:"selected"`
	ClientProof       string     `json:"client_proof"`
}

type NegotiatedControl struct {
	Contract          string     `json:"contract"`
	Kind              string     `json:"kind"`
	SessionID         string     `json:"session_id"`
	ManagerEpoch      uint64     `json:"manager_epoch"`
	ManagerInstanceID string     `json:"manager_instance_id"`
	Selected          Negotiated `json:"selected"`
}

type CreateOwnedWindow struct {
	Contract string `json:"contract"`
	Kind     string `json:"kind"`
	LaunchID string `json:"launch_id"`
}

type WindowClaimRequested struct {
	Contract string  `json:"contract"`
	Kind     string  `json:"kind"`
	LaunchID string  `json:"launch_id"`
	WindowID int64   `json:"window_id"`
	TabIDs   []int64 `json:"tab_ids"`
}

type ClaimControl struct {
	Contract         string `json:"contract"`
	Kind             string `json:"kind"`
	LaunchID         string `json:"launch_id"`
	ClaimID          string `json:"claim_id"`
	Generation       uint64 `json:"generation"`
	WindowID         int64  `json:"window_id"`
	FamilyCapability string `json:"family_capability,omitempty"`
}

type CapabilityChallenge struct {
	Contract   string `json:"contract"`
	Kind       string `json:"kind"`
	ClaimID    string `json:"claim_id"`
	Generation uint64 `json:"generation"`
	Challenge  string `json:"challenge"`
}

type CapabilityProof struct {
	Contract       string `json:"contract"`
	Kind           string `json:"kind"`
	InstallationID string `json:"installation_id"`
	ClaimID        string `json:"claim_id"`
	Generation     uint64 `json:"generation"`
	WindowID       int64  `json:"window_id"`
	Challenge      string `json:"challenge"`
	Proof          string `json:"proof"`
}

type CapabilityRebind struct {
	Contract         string `json:"contract"`
	Kind             string `json:"kind"`
	ClaimID          string `json:"claim_id"`
	Generation       uint64 `json:"generation"`
	FamilyCapability string `json:"family_capability"`
}

type OwnershipRecord struct {
	ClaimID    string  `json:"claim_id"`
	Generation uint64  `json:"generation"`
	WindowID   int64   `json:"window_id"`
	TabIDs     []int64 `json:"tab_ids"`
}

type OwnershipSnapshot struct {
	Contract string            `json:"contract"`
	Kind     string            `json:"kind"`
	Records  []OwnershipRecord `json:"records"`
}

type ClaimTuple struct {
	ClaimID    string `json:"claim_id"`
	Generation uint64 `json:"generation"`
}
type OwnershipReconcile struct {
	Contract      string       `json:"contract"`
	Kind          string       `json:"kind"`
	LiveLaunchIDs []string     `json:"live_launch_ids"`
	Pending       []ClaimTuple `json:"pending"`
	Committed     []ClaimTuple `json:"committed"`
	Authorized    []ClaimTuple `json:"authorized"`
}
type CloseControl struct {
	Contract   string `json:"contract"`
	Kind       string `json:"kind"`
	ClaimID    string `json:"claim_id"`
	Generation uint64 `json:"generation"`
	CloseEpoch uint64 `json:"close_epoch"`
}

type ReconcileRequest struct {
	Contract   string   `json:"contract"`
	Kind       string   `json:"kind"`
	ClaimID    string   `json:"claim_id"`
	Generation uint64   `json:"generation"`
	RequestIDs []string `json:"request_ids"`
}

type ReconcileState string

const (
	ReconcileNotStarted      ReconcileState = "not_started"
	ReconcileStarted         ReconcileState = "started"
	ReconcileCompletedCached ReconcileState = "completed_cached"
	ReconcileUnknown         ReconcileState = "unknown"
)

type ReconcileEntry struct {
	RequestID string          `json:"request_id"`
	State     ReconcileState  `json:"state"`
	Response  json.RawMessage `json:"response"`
}

type ReconcileResult struct {
	Contract   string           `json:"contract"`
	Kind       string           `json:"kind"`
	ClaimID    string           `json:"claim_id"`
	Generation uint64           `json:"generation"`
	Entries    []ReconcileEntry `json:"entries"`
}

type TerminalAcknowledged struct {
	Contract   string `json:"contract"`
	Kind       string `json:"kind"`
	ClaimID    string `json:"claim_id"`
	Generation uint64 `json:"generation"`
	RequestID  string `json:"request_id"`
}

func (AuthHello) controlMessage()            {}
func (AuthChallenge) controlMessage()        {}
func (AuthProof) controlMessage()            {}
func (NegotiatedControl) controlMessage()    {}
func (CreateOwnedWindow) controlMessage()    {}
func (WindowClaimRequested) controlMessage() {}
func (ClaimControl) controlMessage()         {}
func (CapabilityChallenge) controlMessage()  {}
func (CapabilityProof) controlMessage()      {}
func (CapabilityRebind) controlMessage()     {}
func (OwnershipSnapshot) controlMessage()    {}
func (OwnershipReconcile) controlMessage()   {}
func (CloseControl) controlMessage()         {}
func (ReconcileRequest) controlMessage()     {}
func (ReconcileResult) controlMessage()      {}
func (TerminalAcknowledged) controlMessage() {}

// DecodeControl rejects unknown contracts, kinds, fields, missing fields, and
// trailing JSON values.
func DecodeControl(data []byte) (Control, error) {
	if len(data) > MaxMessageBytes {
		return nil, fmt.Errorf("chrome control: message exceeds %d bytes", MaxMessageBytes)
	}
	var header controlHeader
	if err := json.Unmarshal(data, &header); err != nil {
		return nil, fmt.Errorf("chrome control: malformed message: %w", err)
	}
	if header.Contract != ControlContractV1 {
		return nil, fmt.Errorf("chrome control: incompatible contract %q", header.Contract)
	}
	var target Control
	switch header.Kind {
	case ControlAuthHello:
		target = &AuthHello{}
	case ControlAuthChallenge:
		target = &AuthChallenge{}
	case ControlAuthProof:
		target = &AuthProof{}
	case ControlNegotiated:
		target = &NegotiatedControl{}
	case ControlCreateOwnedWindow:
		target = &CreateOwnedWindow{}
	case ControlWindowClaimRequested:
		target = &WindowClaimRequested{}
	case ControlClaimPrepare, ControlClaimStaged, ControlClaimCommitted, ControlWindowClaimed:
		target = &ClaimControl{}
	case ControlCapabilityChallenge:
		target = &CapabilityChallenge{}
	case ControlCapabilityProof:
		target = &CapabilityProof{}
	case ControlCapabilityRebind:
		target = &CapabilityRebind{}
	case ControlOwnershipSnapshot:
		target = &OwnershipSnapshot{}
	case ControlOwnershipReconcile:
		target = &OwnershipReconcile{}
	case ControlClosePrepare, ControlClosePrepared, ControlCloseCancel, ControlCloseCancelled,
		ControlCloseCommit, ControlCloseCommitted:
		target = &CloseControl{}
	case ControlReconcileRequest:
		target = &ReconcileRequest{}
	case ControlReconcileResult:
		target = &ReconcileResult{}
	case ControlTerminalAcknowledged:
		target = &TerminalAcknowledged{}
	default:
		return nil, fmt.Errorf("chrome control: unknown kind %q", header.Kind)
	}
	if err := decodeStrict(data, target); err != nil {
		return nil, fmt.Errorf("chrome control: invalid %s: %w", header.Kind, err)
	}
	if claim, ok := target.(*ClaimControl); ok {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(data, &fields); err != nil {
			return nil, err
		}
		_, present := fields["family_capability"]
		if (claim.Kind == ControlClaimCommitted) != present {
			return nil, fmt.Errorf("chrome control: family_capability presence does not match claim phase")
		}
	}
	if err := ValidateControl(target); err != nil {
		return nil, err
	}
	return target, nil
}

func ValidateControl(message Control) error {
	contract, kind, allowed := controlIdentity(message)
	if contract != ControlContractV1 || !contains(allowed, kind) {
		return fmt.Errorf("chrome control: contract/kind does not match %T", message)
	}
	validID := func(values ...string) bool {
		for _, value := range values {
			if !validIdentifier(value) {
				return false
			}
		}
		return true
	}
	switch value := message.(type) {
	case *AuthHello:
		if !validID(value.InstallationID, value.ClientNonce) || !validAdvertisement(value.Supported) {
			return fmt.Errorf("chrome control: malformed auth hello")
		}
	case *AuthChallenge:
		if !validID(value.InstallationID, value.ClientNonce, value.ServerNonce, value.ManagerInstanceID, value.ServerProof) ||
			!validWireUint(value.ManagerEpoch) || !validNegotiated(value.Selected) {
			return fmt.Errorf("chrome control: malformed auth challenge")
		}
	case *AuthProof:
		if !validID(value.InstallationID, value.ClientNonce, value.ServerNonce, value.ManagerInstanceID, value.ClientProof) ||
			!validWireUint(value.ManagerEpoch) || !validNegotiated(value.Selected) {
			return fmt.Errorf("chrome control: malformed auth proof")
		}
	case *NegotiatedControl:
		if !validID(value.SessionID, value.ManagerInstanceID) || !validWireUint(value.ManagerEpoch) ||
			!validNegotiated(value.Selected) {
			return fmt.Errorf("chrome control: malformed negotiated message")
		}
	case *CreateOwnedWindow:
		if !validID(value.LaunchID) {
			return fmt.Errorf("chrome control: malformed create window")
		}
	case *WindowClaimRequested:
		if !validID(value.LaunchID) || !validWireInt(value.WindowID) ||
			!validWireIntList(value.TabIDs) {
			return fmt.Errorf("chrome control: malformed claim request")
		}
	case *ClaimControl:
		committed := value.Kind == ControlClaimCommitted
		if !validID(value.LaunchID, value.ClaimID) || !validWireUint(value.Generation) ||
			!validWireInt(value.WindowID) ||
			(committed && !validID(value.FamilyCapability)) || (!committed && value.FamilyCapability != "") {
			return fmt.Errorf("chrome control: malformed claim phase")
		}
	case *CapabilityChallenge:
		if !validID(value.ClaimID, value.Challenge) || !validWireUint(value.Generation) {
			return fmt.Errorf("chrome control: malformed capability challenge")
		}
	case *CapabilityProof:
		if !validID(value.InstallationID, value.ClaimID, value.Challenge, value.Proof) ||
			!validWireUint(value.Generation) || !validWireInt(value.WindowID) {
			return fmt.Errorf("chrome control: malformed capability proof")
		}
	case *CapabilityRebind:
		if !validID(value.ClaimID, value.FamilyCapability) || !validWireUint(value.Generation) {
			return fmt.Errorf("chrome control: malformed capability rebind")
		}
	case *OwnershipSnapshot:
		if value.Records == nil || len(value.Records) > MaxListEntries {
			return fmt.Errorf("chrome control: malformed ownership snapshot")
		}
		for _, record := range value.Records {
			if !validID(record.ClaimID) || !validWireUint(record.Generation) ||
				!validWireInt(record.WindowID) || !validWireIntList(record.TabIDs) {
				return fmt.Errorf("chrome control: malformed ownership record")
			}
		}
	case *OwnershipReconcile:
		if value.LiveLaunchIDs == nil || value.Pending == nil || value.Committed == nil || value.Authorized == nil ||
			len(value.LiveLaunchIDs) > MaxListEntries || len(value.Pending) > MaxListEntries ||
			len(value.Committed) > MaxListEntries || len(value.Authorized) > MaxListEntries ||
			!validStrings(value.LiveLaunchIDs, MaxIdentifierBytes) || !uniqueStrings(value.LiveLaunchIDs) ||
			!validClaimTuples(value.Pending) || !validClaimTuples(value.Committed) || !validClaimTuples(value.Authorized) {
			return fmt.Errorf("chrome control: malformed ownership reconcile")
		}
	case *CloseControl:
		if !validID(value.ClaimID) || !validWireUint(value.Generation) ||
			!validWireUint(value.CloseEpoch) {
			return fmt.Errorf("chrome control: malformed close phase")
		}
	case *ReconcileRequest:
		if !validID(value.ClaimID) || !validWireUint(value.Generation) || len(value.RequestIDs) == 0 ||
			len(value.RequestIDs) > MaxListEntries {
			return fmt.Errorf("chrome control: malformed reconciliation request")
		}
		for _, id := range value.RequestIDs {
			if !validID(id) {
				return fmt.Errorf("chrome control: malformed reconciliation request")
			}
		}
	case *ReconcileResult:
		if !validID(value.ClaimID) || !validWireUint(value.Generation) || value.Entries == nil ||
			len(value.Entries) > MaxListEntries {
			return fmt.Errorf("chrome control: malformed reconciliation result")
		}
		for _, entry := range value.Entries {
			if !validID(entry.RequestID) || len(entry.Response) > MaxMessageBytes || !validReconcileEntry(entry) {
				return fmt.Errorf("chrome control: malformed reconciliation entry")
			}
		}
	case *TerminalAcknowledged:
		if !validID(value.ClaimID, value.RequestID) || !validWireUint(value.Generation) {
			return fmt.Errorf("chrome control: malformed terminal acknowledgement")
		}
	default:
		return fmt.Errorf("chrome control: unsupported message type %T", message)
	}
	return nil
}

func controlIdentity(message Control) (string, string, []string) {
	switch value := message.(type) {
	case *AuthHello:
		return value.Contract, value.Kind, []string{ControlAuthHello}
	case *AuthChallenge:
		return value.Contract, value.Kind, []string{ControlAuthChallenge}
	case *AuthProof:
		return value.Contract, value.Kind, []string{ControlAuthProof}
	case *NegotiatedControl:
		return value.Contract, value.Kind, []string{ControlNegotiated}
	case *CreateOwnedWindow:
		return value.Contract, value.Kind, []string{ControlCreateOwnedWindow}
	case *WindowClaimRequested:
		return value.Contract, value.Kind, []string{ControlWindowClaimRequested}
	case *ClaimControl:
		return value.Contract, value.Kind, []string{ControlClaimPrepare, ControlClaimStaged, ControlClaimCommitted, ControlWindowClaimed}
	case *CapabilityChallenge:
		return value.Contract, value.Kind, []string{ControlCapabilityChallenge}
	case *CapabilityProof:
		return value.Contract, value.Kind, []string{ControlCapabilityProof}
	case *CapabilityRebind:
		return value.Contract, value.Kind, []string{ControlCapabilityRebind}
	case *OwnershipSnapshot:
		return value.Contract, value.Kind, []string{ControlOwnershipSnapshot}
	case *OwnershipReconcile:
		return value.Contract, value.Kind, []string{ControlOwnershipReconcile}
	case *CloseControl:
		return value.Contract, value.Kind, []string{
			ControlClosePrepare, ControlClosePrepared, ControlCloseCancel,
			ControlCloseCancelled, ControlCloseCommit, ControlCloseCommitted,
		}
	case *ReconcileRequest:
		return value.Contract, value.Kind, []string{ControlReconcileRequest}
	case *ReconcileResult:
		return value.Contract, value.Kind, []string{ControlReconcileResult}
	case *TerminalAcknowledged:
		return value.Contract, value.Kind, []string{ControlTerminalAcknowledged}
	default:
		return "", "", nil
	}
}

func validNegotiated(value Negotiated) bool {
	return validIdentifier(value.RPC) && validIdentifier(value.Events) &&
		value.Capabilities != nil && len(value.Capabilities) <= MaxCapabilities &&
		validStrings(value.Capabilities, MaxIdentifierBytes)
}
func uniqueStrings(values []string) bool {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if _, exists := seen[value]; exists {
			return false
		}
		seen[value] = struct{}{}
	}
	return true
}
func validClaimTuples(values []ClaimTuple) bool {
	seen := make(map[struct {
		claimID    string
		generation uint64
	}]struct{}, len(values))
	for _, value := range values {
		if !validIdentifier(value.ClaimID) || !validWireUint(value.Generation) {
			return false
		}
		key := struct {
			claimID    string
			generation uint64
		}{value.ClaimID, value.Generation}
		if _, exists := seen[key]; exists {
			return false
		}
		seen[key] = struct{}{}
	}
	return true
}

func validWireIntList(values []int64) bool {
	if values == nil || len(values) > MaxListEntries {
		return false
	}
	for _, value := range values {
		if !validWireInt(value) {
			return false
		}
	}
	return true
}

// ValidInboundControl reports whether an authenticated extension may send kind.
func ValidInboundControl(kind string) bool {
	switch kind {
	case ControlWindowClaimRequested, ControlClaimStaged, ControlWindowClaimed,
		ControlCapabilityProof, ControlOwnershipSnapshot, ControlClosePrepared, ControlCloseCancelled,
		ControlCloseCommitted, ControlReconcileResult:
		return true
	default:
		return false
	}
}

// ValidOutboundControl reports whether the server may send kind after authentication.
func ValidOutboundControl(kind string) bool {
	switch kind {
	case ControlCreateOwnedWindow, ControlClaimPrepare, ControlClaimCommitted,
		ControlCapabilityChallenge, ControlCapabilityRebind,
		ControlOwnershipReconcile, ControlClosePrepare, ControlCloseCancel, ControlCloseCommit,
		ControlReconcileRequest, ControlTerminalAcknowledged:
		return true
	default:
		return false
	}
}

// ControlKind returns the DTO discriminator.
func ControlKind(message Control) string {
	_, kind, _ := controlIdentity(message)
	return kind
}
func validReconcileEntry(entry ReconcileEntry) bool {
	null := len(entry.Response) == 0 || bytes.Equal(bytes.TrimSpace(entry.Response), []byte("null"))
	switch entry.State {
	case ReconcileNotStarted, ReconcileStarted, ReconcileUnknown:
		return null
	case ReconcileCompletedCached:
		return !null
	default:
		return false
	}
}
