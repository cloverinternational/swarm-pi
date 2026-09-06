package tools

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

// YOLOPermissionChecker is a decorator that bypasses all permission checks.
// YOLO = "You Only Live Once" - grants all permissions automatically.
// This is Ring 1 - implementation using decorator pattern.
//
// This checker wraps any PermissionChecker and short-circuits all permission
// checks, returning true for all requests. It can optionally log all grants
// and compare against the underlying checker's decision for audit purposes.
type YOLOPermissionChecker struct {
	mu                sync.RWMutex
	underlying        PermissionChecker // Optional underlying checker for comparison
	yoloPolicy        *YOLOPolicy       // YOLO configuration
	logger            Logger            // Optional logger for audit trail
	grantCount        int64             // Number of grants issued
	lastGrantedTime   time.Time         // When the last grant was issued
	underlyingDenials int64             // Times underlying checker would have denied
	yoloAuditLog      []YOLOAuditEntry  // Audit trail of all YOLO grants
}

// YOLOPolicy configures YOLO mode behavior.
type YOLOPolicy struct {
	// Enabled activates YOLO mode.
	Enabled bool

	// Scope defines the scope of YOLO mode (global, project, mode, agent).
	// Empty means global scope.
	Scope ToolScope

	// ScopeID identifies the specific scope instance (projectID, modeID, etc).
	ScopeID string

	// ExcludeTools contains tool names that are excluded from YOLO mode.
	// These tools will still require normal permission checking.
	ExcludeTools []string

	// DangerousOnly makes YOLO only apply to dangerous actions.
	// Safe actions still require normal permission checking.
	DangerousOnly bool

	// ExpiresAt is when YOLO mode automatically expires.
	// nil means no expiry.
	ExpiresAt *time.Time

	// CreatedAt is when YOLO mode was created.
	CreatedAt time.Time

	// CreatedBy indicates who enabled YOLO mode.
	CreatedBy string
}

// YOLOAuditEntry records a single YOLO permission grant.
type YOLOAuditEntry struct {
	// Timestamp of the grant.
	Timestamp time.Time

	// Tool that was granted permission.
	Tool string

	// Permissions that were granted.
	Permissions []Permission

	// Reason for the grant (policy/expiry/exclusion).
	Reason string

	// AgentID of the requesting agent.
	AgentID string

	// WouldUnderlyingAllow indicates what the underlying checker would have decided.
	WouldUnderlyingAllow bool
}

// Logger defines the logging interface for YOLO auditing.
type Logger interface {
	// Info logs an informational message with fields.
	Info(ctx context.Context, msg string, fields ...any)

	// Warn logs a warning message.
	Warn(ctx context.Context, msg string, fields ...any)

	// Error logs an error message.
	Error(ctx context.Context, msg string, fields ...any)
}

// YOLOConfig configures a YOLO permission checker.
type YOLOConfig struct {
	// Policy is the YOLO policy to apply.
	Policy *YOLOPolicy

	// UnderlyingChecker is the original checker to delegate to (optional).
	// If set, YOLO will compare decisions for audit purposes.
	UnderlyingChecker PermissionChecker

	// Logger for audit trail (optional).
	Logger Logger

	// MaxAuditEntries is the maximum number of audit entries to keep in memory.
	// 0 means unlimited. Default is 1000.
	MaxAuditEntries int
}

// NewYOLOPermissionChecker creates a new YOLO permission checker.
func NewYOLOPermissionChecker(config YOLOConfig) *YOLOPermissionChecker {
	maxAudit := config.MaxAuditEntries
	if maxAudit == 0 {
		maxAudit = 1000
	}

	return &YOLOPermissionChecker{
		underlying:   config.UnderlyingChecker,
		yoloPolicy:   config.Policy,
		logger:       config.Logger,
		grantCount:   0,
		yoloAuditLog: make([]YOLOAuditEntry, 0, maxAudit),
	}
}

// Check always returns true (grants permission).
func (y *YOLOPermissionChecker) Check(ctx context.Context, required []Permission) bool {
	y.mu.Lock()
	defer y.mu.Unlock()

	if !y.shouldYOLOApply(nil) {
		// YOLO not active, delegate to underlying if available
		if y.underlying != nil {
			return y.underlying.Check(ctx, required)
		}
		return false
	}

	y.recordGrant("Check", "", required)

	if y.logger != nil {
		y.logger.Info(ctx, "yolo.permission_granted",
			"permissions", permissionsToStrings(required),
			"method", "Check")
	}

	return true
}

// CheckWithContext always returns true (grants permission).
func (y *YOLOPermissionChecker) CheckWithContext(ctx context.Context, required []Permission, ctxData map[string]any) bool {
	y.mu.Lock()
	defer y.mu.Unlock()

	tool := ""
	agentID := ""
	if ctxData != nil {
		if t, ok := ctxData["tool"].(string); ok {
			tool = t
		}
		if a, ok := ctxData["agent_id"].(string); ok {
			agentID = a
		}
	}

	if !y.shouldYOLOApply(ctxData) {
		// YOLO not active, delegate to underlying if available
		if y.underlying != nil {
			return y.underlying.CheckWithContext(ctx, required, ctxData)
		}
		return false
	}

	reason := "yolo_granted"
	wouldAllow := true

	// Optionally check what underlying checker would have decided
	if y.underlying != nil {
		wouldAllow = y.underlying.CheckWithContext(ctx, required, ctxData)
		if !wouldAllow {
			reason = "yolo_override_deny"
			y.underlyingDenials++
		}
	}

	y.recordAuditEntry(YOLOAuditEntry{
		Timestamp:            time.Now(),
		Tool:                 tool,
		Permissions:          required,
		Reason:               reason,
		AgentID:              agentID,
		WouldUnderlyingAllow: wouldAllow,
	})

	if y.logger != nil {
		y.logger.Info(ctx, "yolo.permission_granted_with_context",
			"tool", tool,
			"permissions", permissionsToStrings(required),
			"agent_id", agentID,
			"would_underlying_allow", wouldAllow)
	}

	return true
}

// RequestApproval always returns true (auto-approves).
func (y *YOLOPermissionChecker) RequestApproval(ctx context.Context, required []Permission, reason string) bool {
	y.mu.Lock()
	defer y.mu.Unlock()

	if !y.shouldYOLOApply(nil) {
		// YOLO not active, delegate to underlying if available
		if y.underlying != nil {
			return y.underlying.RequestApproval(ctx, required, reason)
		}
		return false
	}

	y.recordGrant("RequestApproval", reason, required)

	if y.logger != nil {
		y.logger.Info(ctx, "yolo.approval_auto_granted",
			"permissions", permissionsToStrings(required),
			"reason", reason)
	}

	return true
}

// Grant is a no-op (everything already granted in YOLO mode).
func (y *YOLOPermissionChecker) Grant(permission Permission, scope ToolScope, scopeID string) error {
	// No-op: everything is already granted
	if y.logger != nil {
		y.logger.Warn(context.Background(), "yolo.grant_noop",
			"permission", permission,
			"scope", scope,
			"scope_id", scopeID)
	}
	return nil
}

// Revoke is a no-op (can't revoke in YOLO mode).
func (y *YOLOPermissionChecker) Revoke(permission Permission, scope ToolScope, scopeID string) error {
	// No-op: can't revoke in YOLO mode
	if y.logger != nil {
		y.logger.Warn(context.Background(), "yolo.revoke_noop",
			"permission", permission,
			"scope", scope,
			"scope_id", scopeID)
	}
	return nil
}

// IsGranted always returns true.
func (y *YOLOPermissionChecker) IsGranted(permission Permission, scope ToolScope, scopeID string) bool {
	y.mu.RLock()
	defer y.mu.RUnlock()

	if !y.shouldYOLOApply(nil) {
		// YOLO not active, delegate to underlying if available
		if y.underlying != nil {
			return y.underlying.IsGranted(permission, scope, scopeID)
		}
		return false
	}

	return true
}

// shouldYOLOApply checks if YOLO mode applies given the context.
// Caller must hold mu lock.
func (y *YOLOPermissionChecker) shouldYOLOApply(ctxData map[string]any) bool {
	if y.yoloPolicy == nil || !y.yoloPolicy.Enabled {
		return false
	}

	// Check expiry
	if y.yoloPolicy.ExpiresAt != nil && time.Now().After(*y.yoloPolicy.ExpiresAt) {
		return false
	}

	// Check tool exclusions
	if ctxData != nil {
		if tool, ok := ctxData["tool"].(string); ok {
			tool = strings.ToLower(tool)
			for _, excluded := range y.yoloPolicy.ExcludeTools {
				if strings.ToLower(excluded) == tool {
					return false
				}
			}
		}

		// Check dangerous-only flag
		if y.yoloPolicy.DangerousOnly {
			if dangerous, ok := ctxData["dangerous"].(bool); ok && !dangerous {
				return false
			}
		}
	}

	return true
}

// recordGrant records a permission grant in the audit log.
// Caller must hold mu lock.
func (y *YOLOPermissionChecker) recordGrant(method string, reason string, permissions []Permission) {
	y.grantCount++
	y.lastGrantedTime = time.Now()

	reason = fmt.Sprintf("%s via %s", reason, method)
	y.recordAuditEntry(YOLOAuditEntry{
		Timestamp:            time.Now(),
		Permissions:          permissions,
		Reason:               reason,
		WouldUnderlyingAllow: true,
	})
}

// recordAuditEntry adds an entry to the audit log.
// Caller must hold mu lock.
func (y *YOLOPermissionChecker) recordAuditEntry(entry YOLOAuditEntry) {
	if len(y.yoloAuditLog) >= cap(y.yoloAuditLog) && cap(y.yoloAuditLog) > 0 {
		// Remove oldest entry
		y.yoloAuditLog = y.yoloAuditLog[1:]
	}
	y.yoloAuditLog = append(y.yoloAuditLog, entry)
}

// IsYOLOActive returns whether YOLO mode is currently active.
func (y *YOLOPermissionChecker) IsYOLOActive() bool {
	y.mu.RLock()
	defer y.mu.RUnlock()

	if y.yoloPolicy == nil || !y.yoloPolicy.Enabled {
		return false
	}

	if y.yoloPolicy.ExpiresAt != nil && time.Now().After(*y.yoloPolicy.ExpiresAt) {
		return false
	}

	return true
}

// GetStats returns statistics about YOLO grants.
func (y *YOLOPermissionChecker) Stats() (grantCount int64, underlyingDenials int64) {
	y.mu.RLock()
	defer y.mu.RUnlock()
	return y.grantCount, y.underlyingDenials
}

// GetAuditLog returns a copy of the audit log.
func (y *YOLOPermissionChecker) AuditLog() []YOLOAuditEntry {
	y.mu.RLock()
	defer y.mu.RUnlock()

	log := make([]YOLOAuditEntry, len(y.yoloAuditLog))
	copy(log, y.yoloAuditLog)
	return log
}

// DisableYOLO disables YOLO mode.
func (y *YOLOPermissionChecker) DisableYOLO() {
	y.mu.Lock()
	defer y.mu.Unlock()

	if y.yoloPolicy != nil {
		y.yoloPolicy.Enabled = false
	}
}

// permissionsToStrings converts Permission slice to string slice.
func permissionsToStrings(permissions []Permission) []string {
	result := make([]string, len(permissions))
	for i, p := range permissions {
		result[i] = string(p)
	}
	return result
}
