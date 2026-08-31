package observability

import "time"

// MetricType represents the type of metric being recorded.
type MetricType int

const (
	// MetricTypeCounter is a monotonically increasing value.
	// Examples: requests_total, errors_total
	MetricTypeCounter MetricType = iota

	// MetricTypeGauge is a value that can go up or down.
	// Examples: active_conversations, memory_bytes
	MetricTypeGauge

	// MetricTypeHistogram records distributions of values.
	// Examples: request_duration_seconds, response_size_bytes
	MetricTypeHistogram

	// MetricTypeSummary records quantiles over a sliding time window.
	// Examples: request_duration_percentiles
	MetricTypeSummary
)

// Metrics defines the interface for recording metrics.
// All metrics follow Prometheus naming conventions for compatibility.
type Metrics interface {
	// Counter increments a counter metric by the given value.
	// Use for counting events (requests, errors, cache hits, etc.)
	Counter(name string, value float64, labels map[string]string)

	// Gauge sets a gauge metric to the given value.
	// Use for current state (active connections, queue depth, etc.)
	Gauge(name string, value float64, labels map[string]string)

	// Histogram records a value in a histogram.
	// Use for measuring distributions (latency, sizes, etc.)
	Histogram(name string, value float64, labels map[string]string)

	// Summary records a value in a summary.
	// Use for calculating quantiles (p50, p95, p99, etc.)
	Summary(name string, value float64, labels map[string]string)

	// Timing records a duration in seconds.
	// Convenience method for Histogram with time values.
	Timing(name string, duration time.Duration, labels map[string]string)
}

// MetricLabels provides standard label keys for metrics.
const (
	LabelProvider      = "provider"
	LabelModel         = "model"
	LabelAgent         = "agent"
	LabelMode          = "mode"
	LabelGroup         = "group"
	LabelTool          = "tool"
	LabelStatus        = "status"
	LabelErrorType     = "error_type"
	LabelErrorCategory = "error_category"
	LabelCacheLayer    = "cache_layer"
	LabelWorkspace     = "workspace"
)

// StandardMetrics defines the standard metrics emitted by the SDK.
const (
	// Conversation metrics
	MetricConversationDuration   = "conversation.duration_seconds"
	MetricConversationTokens     = "conversation.total_tokens"
	MetricConversationCost       = "conversation.total_cost_usd"
	MetricConversationMessages   = "conversation.messages_count"
	MetricConversationTransition = "conversation.mode_transitions_count"

	// Agent metrics
	MetricAgentDuration      = "agent.execution_duration_seconds"
	MetricAgentTokensInput   = "agent.tokens_input"
	MetricAgentTokensOutput  = "agent.tokens_output"
	MetricAgentToolCalls     = "agent.tool_calls_count"
	MetricAgentRetries       = "agent.retry_count"
	MetricAgentInterventions = "agent.steering_interventions"

	// Provider metrics
	MetricProviderDuration     = "provider.request_duration_seconds"
	MetricProviderTokensPerSec = "provider.tokens_per_second"
	MetricProviderErrors       = "provider.error_count_total"
	MetricProviderRateLimits   = "provider.rate_limit_hits_total"
	MetricProviderCost         = "provider.cost_per_request"

	// Tool metrics
	MetricToolDuration         = "tool.execution_duration_seconds"
	MetricToolSuccess          = "tool.success_count_total"
	MetricToolPermissionDenied = "tool.permission_denied_count_total"
	MetricToolResultSize       = "tool.result_size_bytes"

	// Steering metrics
	MetricSteeringDuration      = "steering.decision_duration_seconds"
	MetricSteeringApprovals     = "steering.approval_count_total"
	MetricSteeringInterventions = "steering.intervention_count_total"
	MetricSteeringEscalations   = "steering.escalation_count_total"

	// Cache metrics
	MetricCacheHits       = "cache.hit_count_total"
	MetricCacheMisses     = "cache.miss_count_total"
	MetricCacheSize       = "cache.size_bytes"
	MetricCacheEvictions  = "cache.eviction_count_total"
	MetricCacheLookupTime = "cache.lookup_duration_ms"

	// Error metrics
	MetricErrors              = "error.count_total"
	MetricErrorRetries        = "error.retry_count_total"
	MetricErrorRecoveries     = "error.recovery_success_total"
	MetricErrorRecoveryTime   = "error.time_to_recovery_seconds"
	MetricCircuitBreakerState = "circuit_breaker.state_changes_total"
	MetricFailovers           = "failover.trigger_count_total"
)
