package profiling

import (
	"context"
)

// NoOpProfiler implements CacheProfiler with zero overhead.
// All methods are no-ops that return immediately.
// Use this when cache profiling is disabled to avoid any performance impact.
type NoOpProfiler struct{}

// NewNoOpProfiler creates a profiler that does nothing.
func NewNoOpProfiler() *NoOpProfiler {
	return &NoOpProfiler{}
}

// BeforeTranslation is a no-op.
func (n *NoOpProfiler) BeforeTranslation(ctx context.Context, messages any) (string, error) {
	return "", nil
}

// AfterTranslation is a no-op.
func (n *NoOpProfiler) AfterTranslation(ctx context.Context, preHash string, translatedJSON any) error {
	return nil
}

// TrackMutation is a no-op.
func (n *NoOpProfiler) TrackMutation(ctx context.Context, event *MutationEvent) error {
	return nil
}

// RecordAPIResponse is a no-op.
func (n *NoOpProfiler) RecordAPIResponse(ctx context.Context, metrics *APIMetrics) error {
	return nil
}

// GetMetrics returns nil (no data collected).
func (n *NoOpProfiler) Metrics(conversationID string) *CacheMetrics {
	return nil
}

// GenerateReport returns an empty report.
func (n *NoOpProfiler) GenerateReport(conversationID string) *CacheBreakReport {
	return &CacheBreakReport{}
}

// ExportMetrics returns nil.
func (n *NoOpProfiler) ExportMetrics(ctx context.Context, conversationID string, format string) (any, error) {
	return nil, nil
}

// Reset is a no-op.
func (n *NoOpProfiler) Reset(conversationID string) error {
	return nil
}
