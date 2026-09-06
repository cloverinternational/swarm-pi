package chrome

// SessionState is the externally observable browser-family lifecycle state.
type SessionState string

const (
	SessionUninitialized SessionState = "uninitialized"
	SessionLaunching     SessionState = "launching"
	SessionReady         SessionState = "ready"
	SessionActive        SessionState = "active"
	SessionClosing       SessionState = "closing"
	SessionClosed        SessionState = "closed"
	SessionFailed        SessionState = "failed"
)

// ClosePhase identifies the reversible and irreversible close boundaries.
type ClosePhase string

const (
	CloseNone      ClosePhase = ""
	CloseReserved  ClosePhase = "close_reserved"
	CloseSent      ClosePhase = "close_sent"
	CloseCommitted ClosePhase = "close_committed"
)

// RequestState records dispatch progress without conflating ACKs with terminal responses.
type RequestState string

const (
	RequestCreated   RequestState = "created"
	RequestQueued    RequestState = "queued"
	RequestSent      RequestState = "sent"
	RequestAccepted  RequestState = "accepted"
	RequestStarted   RequestState = "started"
	RequestCompleted RequestState = "completed"
	RequestRejected  RequestState = "rejected"
	RequestFailed    RequestState = "failed"
)

// ReconciliationState is returned when querying an accepted request after reconnect.
type ReconciliationState string

const (
	ReconcileNotStarted      ReconciliationState = "not_started"
	ReconcileStarted         ReconciliationState = "started"
	ReconcileCompletedCached ReconciliationState = "completed_cached"
	ReconcileUnknown         ReconciliationState = "unknown"
)

// IsTerminal reports whether a request state can no longer advance.
func (state RequestState) IsTerminal() bool {
	switch state {
	case RequestCompleted, RequestRejected, RequestFailed:
		return true
	default:
		return false
	}
}
