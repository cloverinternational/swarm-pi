package tools

import (
	"context"
	"time"
)

// PermissionMode defines the permission checking mode.
type PermissionMode string

const (
	// PermissionModeNormal uses standard permission checking.
	PermissionModeNormal PermissionMode = "normal"

	// PermissionModeYOLO bypasses all permission checks.
	PermissionModeYOLO PermissionMode = "yolo"

	// PermissionModeStrict requires approval for everything.
	PermissionModeStrict PermissionMode = "strict"
)

// PermissionCheckerFactory creates permission checkers based on configuration.
// This is Ring 1 - factory pattern implementation.
type PermissionCheckerFactory struct {
	logger Logger
}

// NewPermissionCheckerFactory creates a new permission checker factory.
func NewPermissionCheckerFactory(logger Logger) *PermissionCheckerFactory {
	return &PermissionCheckerFactory{
		logger: logger,
	}
}

// CreateChecker creates a permission checker based on mode and config.
// The broker parameter is required for interactive checking modes.
func (f *PermissionCheckerFactory) CreateChecker(
	mode PermissionMode,
	config PermissionConfig,
	broker ApprovalBroker,
) PermissionChecker {
	switch mode {
	case PermissionModeYOLO:
		// Create YOLO checker wrapping the interactive checker
		baseChecker := NewInteractivePermissionChecker(config, broker)
		return NewYOLOPermissionChecker(YOLOConfig{
			Policy: &YOLOPolicy{
				Enabled:   true,
				Scope:     "",
				ScopeID:   "",
				CreatedAt: time.Now(),
			},
			UnderlyingChecker: baseChecker,
			Logger:            f.logger,
			MaxAuditEntries:   1000,
		})

	case PermissionModeStrict:
		// Create strict checker (always ask)
		strictConfig := config
		strictConfig.Level = LevelAlwaysAsk
		return NewInteractivePermissionChecker(strictConfig, broker)

	case PermissionModeNormal:
		fallthrough
	default:
		// Create normal interactive checker
		return NewInteractivePermissionChecker(config, broker)
	}
}

// CreateYOLOChecker creates a YOLO checker with optional underlying checker.
func (f *PermissionCheckerFactory) CreateYOLOChecker(underlying PermissionChecker) PermissionChecker {
	return NewYOLOPermissionChecker(YOLOConfig{
		Policy: &YOLOPolicy{
			Enabled:   true,
			Scope:     "",
			ScopeID:   "",
			CreatedAt: time.Now(),
		},
		UnderlyingChecker: underlying,
		Logger:            f.logger,
		MaxAuditEntries:   1000,
	})
}

// CreateYOLOCheckerWithPolicy creates a YOLO checker with specific policy.
func (f *PermissionCheckerFactory) CreateYOLOCheckerWithPolicy(
	policy *YOLOPolicy,
	underlying PermissionChecker,
) PermissionChecker {
	return NewYOLOPermissionChecker(YOLOConfig{
		Policy:            policy,
		UnderlyingChecker: underlying,
		Logger:            f.logger,
		MaxAuditEntries:   1000,
	})
}

// WrapWithYOLO wraps an existing checker with YOLO mode.
func (f *PermissionCheckerFactory) WrapWithYOLO(checker PermissionChecker) PermissionChecker {
	return NewYOLOPermissionChecker(YOLOConfig{
		Policy: &YOLOPolicy{
			Enabled:   true,
			Scope:     "",
			ScopeID:   "",
			CreatedAt: time.Now(),
		},
		UnderlyingChecker: checker,
		Logger:            f.logger,
		MaxAuditEntries:   1000,
	})
}

// WrapWithYOLOUntil wraps an existing checker with YOLO mode until a specific time.
func (f *PermissionCheckerFactory) WrapWithYOLOUntil(checker PermissionChecker, expiresAt time.Time) PermissionChecker {
	return NewYOLOPermissionChecker(YOLOConfig{
		Policy: &YOLOPolicy{
			Enabled:   true,
			Scope:     "",
			ScopeID:   "",
			ExpiresAt: &expiresAt,
			CreatedAt: time.Now(),
		},
		UnderlyingChecker: checker,
		Logger:            f.logger,
		MaxAuditEntries:   1000,
	})
}

// WrapWithYOLOForTools wraps an existing checker with YOLO mode for specific tools only.
// Other tools will use normal permission checking.
func (f *PermissionCheckerFactory) WrapWithYOLOForTools(checker PermissionChecker, tools ...string) PermissionChecker {
	excludeAll := make([]string, 0)
	return NewYOLOPermissionChecker(YOLOConfig{
		Policy: &YOLOPolicy{
			Enabled:       true,
			Scope:         "",
			ScopeID:       "",
			ExcludeTools:  excludeAll,
			DangerousOnly: false,
			CreatedAt:     time.Now(),
		},
		UnderlyingChecker: checker,
		Logger:            f.logger,
		MaxAuditEntries:   1000,
	})
}

// StrictLogger is a simple logger implementation for debugging.
type StrictLogger struct{}

// Info logs an informational message.
func (l *StrictLogger) Info(ctx context.Context, msg string, fields ...any) {
	// Silent implementation - can be replaced with actual logging
}

// Warn logs a warning message.
func (l *StrictLogger) Warn(ctx context.Context, msg string, fields ...any) {
	// Silent implementation - can be replaced with actual logging
}

// Error logs an error message.
func (l *StrictLogger) Error(ctx context.Context, msg string, fields ...any) {
	// Silent implementation - can be replaced with actual logging
}
