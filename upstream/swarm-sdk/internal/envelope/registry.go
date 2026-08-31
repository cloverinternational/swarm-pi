package envelope

import (
	"encoding/json"
	"fmt"
	"sync"
)

// TransformRegistry is the "Side Table" that stores transformation rules as data.
// Instead of hard-coding: if provider == A then do X
// We store the rules in a lookup table, indexed by provider_id.
//
// Principle 2: Side Table
// - Lookup transformation rules by provider_id
// - To add Provider #21, just add a new entry - no new code
// - Separation of rules (data) from processor (code)
type TransformRegistry struct {
	mu      sync.RWMutex
	schemas map[ProviderID]*TransformSchema
}

// TransformSchema defines the transformation rules for a specific provider.
// This is the "data" that tells the processor how to handle this provider's JSON.
type TransformSchema struct {
	ProviderID ProviderID `json:"provider_id"`
	Name       string     `json:"name"`
	Version    string     `json:"version"`

	// Field Mappings: maps provider field paths to canonical field names
	FieldMappings map[string]FieldMapping `json:"field_mappings"`

	// Event Type Handlers: how to handle each event type
	EventHandlers map[EventType]EventHandler `json:"event_handlers"`

	// Custom transformers for complex logic
	transformers map[string]TransformFunc
}

// FieldMapping defines how to map a provider field to canonical format
type FieldMapping struct {
	SourcePath  string `json:"source_path"`  // JSONPath in provider format
	TargetField string `json:"target_field"` // Canonical field name
	Type        string `json:"type"`         // "direct", "compute", "nested"
	Default     any    `json:"default,omitempty"`
	Transform   string `json:"transform,omitempty"` // Named transform function
}

// EventHandler defines how to process a specific event type
type EventHandler struct {
	EventType    EventType         `json:"event_type"`
	ExtractPaths map[string]string `json:"extract_paths"` // What to extract
	Accumulate   bool              `json:"accumulate"`    // Should accumulate?
}

// TransformFunc is a custom transformation function
type TransformFunc func(raw json.RawMessage) (any, error)

// NewTransformRegistry creates a new registry with default provider schemas
func NewTransformRegistry() *TransformRegistry {
	r := &TransformRegistry{
		schemas: make(map[ProviderID]*TransformSchema),
	}

	// Register default schemas
	r.RegisterSchema(NewAnthropicSchema())
	r.RegisterSchema(NewOpenAISchema())
	r.RegisterSchema(NewGeminiSchema())
	r.RegisterSchema(NewClaudeCodeSchema())

	return r
}

// RegisterSchema adds or updates a provider schema
func (r *TransformRegistry) RegisterSchema(schema *TransformSchema) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.schemas[schema.ProviderID] = schema
}

// GetSchema retrieves a provider schema
func (r *TransformRegistry) Schema(providerID ProviderID) (*TransformSchema, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	schema, ok := r.schemas[providerID]
	if !ok {
		return nil, fmt.Errorf("no schema registered for provider: %s", providerID)
	}
	return schema, nil
}

// Transform applies the schema to raw JSON and returns canonical payload
func (s *TransformSchema) Transform(raw json.RawMessage, eventType EventType) (*CanonicalPayload, error) {
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, fmt.Errorf("failed to parse raw JSON: %w", err)
	}

	canonical := &CanonicalPayload{}

	// Apply field mappings
	for _, mapping := range s.FieldMappings {
		value := extractPath(data, mapping.SourcePath)
		if value == nil && mapping.Default != nil {
			value = mapping.Default
		}
		if value != nil {
			applyToCanonical(canonical, mapping.TargetField, value)
		}
	}

	// Apply event-specific handlers
	if handler, ok := s.EventHandlers[eventType]; ok {
		for targetField, sourcePath := range handler.ExtractPaths {
			value := extractPath(data, sourcePath)
			if value != nil {
				applyToCanonical(canonical, targetField, value)
			}
		}
	}

	// Apply custom transformers
	for name, fn := range s.transformers {
		if result, err := fn(raw); err == nil {
			applyToCanonical(canonical, name, result)
		}
	}

	// Calculate derived fields
	s.calculateDerivedFields(canonical)

	return canonical, nil
}

// calculateDerivedFields computes fields that depend on other fields
func (s *TransformSchema) calculateDerivedFields(c *CanonicalPayload) {
	if c.Usage != nil {
		c.Usage.TotalInputWithCache = c.Usage.InputTokens +
			c.Usage.CacheReadTokens +
			c.Usage.CacheCreationTokens

		if c.Usage.TotalTokens == 0 {
			c.Usage.TotalTokens = c.Usage.TotalInputWithCache + c.Usage.OutputTokens
		}
	}
}

// extractPath extracts a value from nested map using dot notation with array index support
// Supports paths like: "candidates.0.content.role" or "usage.tokens"
func extractPath(data map[string]any, path string) any {
	parts := splitPath(path)
	current := any(data)

	for _, part := range parts {
		// Check if part is an array index (numeric)
		if idx := parseArrayIndex(part); idx >= 0 {
			// Handle array access
			switch v := current.(type) {
			case []any:
				if idx < len(v) {
					current = v[idx]
				} else {
					return nil
				}
			default:
				return nil
			}
		} else {
			// Handle map access
			switch v := current.(type) {
			case map[string]any:
				current = v[part]
			default:
				return nil
			}
		}
	}

	return current
}

// parseArrayIndex checks if a string is a valid array index and returns it
// Returns -1 if not a valid index
func parseArrayIndex(s string) int {
	if len(s) == 0 {
		return -1
	}
	idx := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return -1
		}
		idx = idx*10 + int(c-'0')
	}
	return idx
}

// splitPath splits a dot-notation path into parts
func splitPath(path string) []string {
	var parts []string
	current := ""
	for _, c := range path {
		if c == '.' {
			if current != "" {
				parts = append(parts, current)
				current = ""
			}
		} else {
			current += string(c)
		}
	}
	if current != "" {
		parts = append(parts, current)
	}
	return parts
}

// applyToCanonical sets a value on the canonical payload
func applyToCanonical(c *CanonicalPayload, field string, value any) {
	switch field {
	case "role":
		if s, ok := value.(string); ok {
			c.Role = s
		}
	case "content":
		if s, ok := value.(string); ok {
			c.Content = s
			// Also populate Delta for streaming chunks
			if c.Delta == "" {
				c.Delta = s
			}
		}
	case "thinking":
		if s, ok := value.(string); ok {
			c.Thinking = s
		}
	case "delta":
		if s, ok := value.(string); ok {
			c.Delta = s
		}
	case "finish_reason":
		if s, ok := value.(string); ok {
			c.FinishReason = s
		}
	case "done":
		if b, ok := value.(bool); ok {
			c.Done = b
		}
	case "input_tokens":
		if c.Usage == nil {
			c.Usage = &CanonicalUsage{}
		}
		c.Usage.InputTokens = toInt(value)
	case "output_tokens":
		if c.Usage == nil {
			c.Usage = &CanonicalUsage{}
		}
		c.Usage.OutputTokens = toInt(value)
	case "cache_creation_tokens":
		if c.Usage == nil {
			c.Usage = &CanonicalUsage{}
		}
		c.Usage.CacheCreationTokens = toInt(value)
	case "cache_read_tokens":
		if c.Usage == nil {
			c.Usage = &CanonicalUsage{}
		}
		c.Usage.CacheReadTokens = toInt(value)
	case "cache_creation_1h_tokens":
		if c.Usage == nil {
			c.Usage = &CanonicalUsage{}
		}
		c.Usage.CacheCreation1hTokens = toInt(value)
	case "cache_creation_5m_tokens":
		if c.Usage == nil {
			c.Usage = &CanonicalUsage{}
		}
		c.Usage.CacheCreation5mTokens = toInt(value)
	}
}

func toInt(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	case float32:
		return int(n)
	default:
		return 0
	}
}

// ============================================================================
// PROVIDER-SPECIFIC SCHEMAS
// ============================================================================

// NewAnthropicSchema creates the transformation schema for Anthropic
func NewAnthropicSchema() *TransformSchema {
	return &TransformSchema{
		ProviderID: ProviderAnthropic,
		Name:       "Anthropic Messages API",
		Version:    "2024-01",
		FieldMappings: map[string]FieldMapping{
			"role":           {SourcePath: "role", TargetField: "role", Type: "direct"},
			"input_tokens":   {SourcePath: "usage.input_tokens", TargetField: "input_tokens", Type: "direct"},
			"output_tokens":  {SourcePath: "usage.output_tokens", TargetField: "output_tokens", Type: "direct"},
			"cache_creation": {SourcePath: "usage.cache_creation_input_tokens", TargetField: "cache_creation_tokens", Type: "direct"},
			"cache_read":     {SourcePath: "usage.cache_read_input_tokens", TargetField: "cache_read_tokens", Type: "direct"},
			"cache_1h":       {SourcePath: "usage.cache_creation.ephemeral_1h_input_tokens", TargetField: "cache_creation_1h_tokens", Type: "direct"},
			"cache_5m":       {SourcePath: "usage.cache_creation.ephemeral_5m_input_tokens", TargetField: "cache_creation_5m_tokens", Type: "direct"},
			"stop_reason":    {SourcePath: "stop_reason", TargetField: "finish_reason", Type: "direct"},
		},
		EventHandlers: map[EventType]EventHandler{
			EventTypeSSE: {
				EventType: EventTypeSSE,
				ExtractPaths: map[string]string{
					"delta":         "delta.text",
					"finish_reason": "delta.stop_reason",
				},
			},
		},
	}
}

// NewOpenAISchema creates the transformation schema for OpenAI
func NewOpenAISchema() *TransformSchema {
	return &TransformSchema{
		ProviderID: ProviderOpenAI,
		Name:       "OpenAI Chat Completions",
		Version:    "2024-01",
		FieldMappings: map[string]FieldMapping{
			"role":          {SourcePath: "choices.0.message.role", TargetField: "role", Type: "direct"},
			"content":       {SourcePath: "choices.0.message.content", TargetField: "content", Type: "direct"},
			"prompt_tokens": {SourcePath: "usage.prompt_tokens", TargetField: "input_tokens", Type: "direct"},
			"completion":    {SourcePath: "usage.completion_tokens", TargetField: "output_tokens", Type: "direct"},
			"cached_tokens": {SourcePath: "usage.prompt_tokens_details.cached_tokens", TargetField: "cache_read_tokens", Type: "direct"},
			"finish_reason": {SourcePath: "choices.0.finish_reason", TargetField: "finish_reason", Type: "direct"},
		},
		EventHandlers: map[EventType]EventHandler{
			EventTypeSSE: {
				EventType: EventTypeSSE,
				ExtractPaths: map[string]string{
					"delta":         "choices.0.delta.content",
					"finish_reason": "choices.0.finish_reason",
				},
			},
		},
	}
}

// NewGeminiSchema creates the transformation schema for Gemini
func NewGeminiSchema() *TransformSchema {
	return &TransformSchema{
		ProviderID: ProviderGemini,
		Name:       "Google Gemini API",
		Version:    "2024-01",
		FieldMappings: map[string]FieldMapping{
			"role":          {SourcePath: "candidates.0.content.role", TargetField: "role", Type: "direct"},
			"content":       {SourcePath: "candidates.0.content.parts.0.text", TargetField: "content", Type: "direct"},
			"prompt_tokens": {SourcePath: "usageMetadata.promptTokenCount", TargetField: "input_tokens", Type: "direct"},
			"output_tokens": {SourcePath: "usageMetadata.candidatesTokenCount", TargetField: "output_tokens", Type: "direct"},
			"cached_tokens": {SourcePath: "usageMetadata.cachedContentTokenCount", TargetField: "cache_read_tokens", Type: "direct"},
			"finish_reason": {SourcePath: "candidates.0.finishReason", TargetField: "finish_reason", Type: "direct"},
		},
	}
}

// NewClaudeCodeSchema creates the transformation schema for Claude Code internal format
func NewClaudeCodeSchema() *TransformSchema {
	return &TransformSchema{
		ProviderID: ProviderClaudeCode,
		Name:       "Claude Code Internal Format",
		Version:    "2.1.27",
		FieldMappings: map[string]FieldMapping{
			"parent_uuid":    {SourcePath: "parentUuid", TargetField: "parent_id", Type: "direct"},
			"session_id":     {SourcePath: "sessionId", TargetField: "session_id", Type: "direct"},
			"role":           {SourcePath: "message.role", TargetField: "role", Type: "direct"},
			"input_tokens":   {SourcePath: "message.usage.input_tokens", TargetField: "input_tokens", Type: "direct"},
			"output_tokens":  {SourcePath: "message.usage.output_tokens", TargetField: "output_tokens", Type: "direct"},
			"cache_creation": {SourcePath: "message.usage.cache_creation_input_tokens", TargetField: "cache_creation_tokens", Type: "direct"},
			"cache_read":     {SourcePath: "message.usage.cache_read_input_tokens", TargetField: "cache_read_tokens", Type: "direct"},
			"cache_1h":       {SourcePath: "message.usage.cache_creation.ephemeral_1h_input_tokens", TargetField: "cache_creation_1h_tokens", Type: "direct"},
		},
	}
}
