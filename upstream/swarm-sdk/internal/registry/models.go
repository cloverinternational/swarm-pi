package registry

// Model represents a standardized AI model's capabilities and costs.
type Model struct {
	ID                     string   `json:"id"`
	Name                   string   `json:"name"`
	CostPer1MIn            float64  `json:"cost_per_1m_in"`
	CostPer1MOut           float64  `json:"cost_per_1m_out"`
	CostPer1MInCached      float64  `json:"cost_per_1m_in_cached"`
	CostPer1MOutCached     float64  `json:"cost_per_1m_out_cached"`
	ContextWindow          int64    `json:"context_window"`
	DefaultMaxTokens       int64    `json:"default_max_tokens"`
	CanReason              bool     `json:"can_reason"`
	ReasoningLevels        []string `json:"reasoning_levels,omitempty"`
	DefaultReasoningEffort string   `json:"default_reasoning_effort,omitempty"`
	SupportsImages         bool     `json:"supports_attachments"`
}

// Provider represents an LLM provider and its available models.
type Provider struct {
	ID                  string  `json:"id"`
	Name                string  `json:"name"`
	Type                string  `json:"type"`
	DefaultLargeModelID string  `json:"default_large_model_id"`
	DefaultSmallModelID string  `json:"default_small_model_id"`
	Models              []Model `json:"models"`
}
