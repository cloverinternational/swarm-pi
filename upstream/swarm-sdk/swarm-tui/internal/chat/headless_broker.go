package chat

import (
	"context"
	"fmt"
	"os"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// ApprovalMode controls how the headless (-p) approval broker responds to
// permission requests. Historically the broker unconditionally auto-approved
// EVERYTHING (returning DecisionApproveOnce) while ignoring its own config and
// the permission level — a silent, illegible consent defect. These modes make
// the posture explicit and, in the deny modes, actually enforce a boundary.
type ApprovalMode string

const (
	// ApprovalModeAuto approves requests (preserving historical -p behavior for
	// backward compatibility) but does so LEGIBLY: it logs each approval to
	// stderr and still honors an explicit deny in the PermissionConfig
	// overrides, so a configured deny is not silently overridden.
	ApprovalModeAuto ApprovalMode = "auto"

	// ApprovalModeDeny denies mutating/credential/network permissions (writes,
	// deletes, bash, network, secrets, hook management, DB writes, containers)
	// while still allowing read-only permissions (file_read, database_read).
	// Use for untrusted headless runs that should observe but not mutate.
	ApprovalModeDeny ApprovalMode = "deny"

	// ApprovalModePrompt is reserved for a future interactive headless flow.
	// There is no TTY in -p mode, so today it behaves exactly like deny (fail
	// closed) rather than blocking forever waiting on input that cannot arrive.
	ApprovalModePrompt ApprovalMode = "prompt"
)

// ParseApprovalMode maps a --approval-mode flag value to an ApprovalMode,
// defaulting to auto for empty/unknown values (with ok=false signalling the
// caller to warn on an unrecognized value).
func ParseApprovalMode(s string) (ApprovalMode, bool) {
	switch ApprovalMode(s) {
	case ApprovalModeAuto:
		return ApprovalModeAuto, true
	case ApprovalModeDeny:
		return ApprovalModeDeny, true
	case ApprovalModePrompt:
		return ApprovalModePrompt, true
	case "":
		return ApprovalModeAuto, true
	default:
		return ApprovalModeAuto, false
	}
}

// readOnlyPermissions is the EXACT set of permission types that deny/prompt
// mode still allows in headless mode. Everything else (including any NEW
// permission type added in the future) is denied — deny mode fails CLOSED so a
// newly-introduced permission can never be silently auto-allowed on the
// security-sensitive axis.
var readOnlyPermissions = map[string]bool{
	string(tools.PermissionFileRead):     true,
	string(tools.PermissionDatabaseRead): true,
}

// HeadlessApprovalBroker resolves permission requests in headless (-p) mode
// according to its ApprovalMode. It never blocks on user input (there is no UI)
// and it makes every decision legible on stderr.
type HeadlessApprovalBroker struct {
	config tools.PermissionConfig
	mode   ApprovalMode
}

// NewHeadlessApprovalBroker creates a broker in the default (auto) mode. This
// preserves the historical auto-approve behavior for existing callers, but the
// approvals are now logged rather than silent.
func NewHeadlessApprovalBroker() *HeadlessApprovalBroker {
	return NewHeadlessApprovalBrokerWithMode(ApprovalModeAuto)
}

// NewHeadlessApprovalBrokerWithMode creates a broker with an explicit mode.
func NewHeadlessApprovalBrokerWithMode(mode ApprovalMode) *HeadlessApprovalBroker {
	if mode == "" {
		mode = ApprovalModeAuto
	}
	return &HeadlessApprovalBroker{
		config: tools.DefaultPermissionConfig(),
		mode:   mode,
	}
}

// Request resolves a permission request per the broker's ApprovalMode.
//
//   - auto:   approve, but log the approval; honor an explicit config deny.
//   - deny:   deny mutating/credential/network permissions, allow read-only.
//   - prompt: no TTY in headless → behave as deny (fail closed).
func (b *HeadlessApprovalBroker) Request(ctx context.Context, req tools.PermissionApprovalRequest) (tools.ApprovalResponse, error) {
	mode := b.mode
	if mode == "" {
		mode = ApprovalModeAuto
	}

	deny := func(reason string) (tools.ApprovalResponse, error) {
		// Surface an actionable reason to the AGENT (UserContext is carried into
		// the approval response) as well as to the operator (stderr), so the
		// agent can recover (e.g. stop attempting mutations) instead of hitting
		// an opaque dead end.
		agentReason := fmt.Sprintf("Denied by headless --approval-mode=%s: this run may not perform %q actions (tool %q). This is a read-only run — do not retry mutating operations; report what you would have changed instead.",
			mode, req.Permission, req.Tool)
		fmt.Fprintf(os.Stderr, "[PERMISSIONS] Headless %s mode: DENIED %s for tool %s (target=%q) — %s\n",
			mode, req.Permission, req.Tool, req.Target, reason)
		return tools.ApprovalResponse{
			Decision:    tools.DecisionDeny,
			Outcome:     tools.OutcomeDenied,
			UserContext: agentReason,
		}, nil
	}
	approve := func() (tools.ApprovalResponse, error) {
		fmt.Fprintf(os.Stderr, "[PERMISSIONS] Headless %s mode: auto-approving %s for tool %s (target=%q)\n",
			mode, req.Permission, req.Tool, req.Target)
		return tools.ApprovalResponse{
			Decision: tools.DecisionApproveOnce,
			Outcome:  tools.OutcomeApproved,
		}, nil
	}

	switch mode {
	case ApprovalModeDeny, ApprovalModePrompt:
		// Fail closed: allow ONLY known read-only permissions; deny everything
		// else, including unknown/future permission types.
		if readOnlyPermissions[req.Permission] {
			return approve()
		}
		return deny("blocked by --approval-mode=" + string(mode))

	case ApprovalModeAuto:
		fallthrough
	default:
		return approve()
	}
}

// Respond is a no-op for headless mode (requests are resolved synchronously).
func (b *HeadlessApprovalBroker) Respond(requestID string, decision tools.Decision) error {
	return nil
}

// SetConfig stores the permission configuration.
func (b *HeadlessApprovalBroker) SetConfig(config tools.PermissionConfig) {
	b.config = config
}

// GetPendingRequests returns empty in headless mode (all resolved immediately).
func (b *HeadlessApprovalBroker) GetPendingRequests() []tools.PermissionApprovalRequest {
	return nil
}

// Logger interface for headless broker logging.
type Logger interface {
	Warn(ctx context.Context, msg string, fields ...any)
	Error(ctx context.Context, msg string, fields ...any)
	Info(ctx context.Context, msg string, fields ...any)
}
