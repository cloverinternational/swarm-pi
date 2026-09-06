package observability

import "strings"

const redactedValue = "[REDACTED]"

// DefaultRedactor redacts sensitive keys and truncates long string values.
type DefaultRedactor struct {
	SensitiveKeys   []string
	MaxStringLength int
}

// NewDefaultRedactor returns the default redactor policy.
func NewDefaultRedactor() *DefaultRedactor {
	return &DefaultRedactor{
		SensitiveKeys:   []string{"api_key", "authorization", "token", "password", "secret"},
		MaxStringLength: 512,
	}
}

// RedactAttributes recursively redacts a map of attributes.
func (r *DefaultRedactor) RedactAttributes(attrs map[string]any) map[string]any {
	if len(attrs) == 0 {
		return nil
	}
	out := make(map[string]any, len(attrs))
	for k, v := range attrs {
		out[k] = r.RedactValue(k, v)
	}
	return out
}

// RedactValue redacts sensitive values and truncates long strings.
func (r *DefaultRedactor) RedactValue(key string, value any) any {
	if r.isSensitive(key) {
		return redactedValue
	}

	switch v := value.(type) {
	case map[string]any:
		return r.RedactAttributes(v)
	case []any:
		out := make([]any, len(v))
		for i := range v {
			out[i] = r.RedactValue(key, v[i])
		}
		return out
	case string:
		maxLen := r.MaxStringLength
		if maxLen <= 0 {
			maxLen = 512
		}
		if len(v) <= maxLen {
			return v
		}
		if maxLen <= 3 {
			return v[:maxLen]
		}
		return v[:maxLen-3] + "..."
	default:
		return value
	}
}

func (r *DefaultRedactor) isSensitive(key string) bool {
	if key == "" {
		return false
	}
	k := strings.ToLower(key)
	for _, sensitive := range r.SensitiveKeys {
		if sensitive == "" {
			continue
		}
		if strings.Contains(k, strings.ToLower(sensitive)) {
			return true
		}
	}
	return false
}
