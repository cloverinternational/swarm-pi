package taskstore

import (
	"encoding/json"
	"reflect"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/journalredact"
)

// redactTaskAuditEvents returns a detached, sanitized audit-event slice.
// The bool reports whether any serialized value changed and therefore whether
// a loaded legacy store needs to be rewritten.
func redactTaskAuditEvents(events []TaskAuditEvent) ([]TaskAuditEvent, bool) {
	if events == nil {
		return nil, false
	}
	out := make([]TaskAuditEvent, len(events))
	changed := false
	for i, event := range events {
		out[i] = event
		out[i].Type = journalredact.RedactText(event.Type)
		out[i].Actor = journalredact.RedactText(event.Actor)
		out[i].Summary = journalredact.RedactText(event.Summary)
		out[i].Metadata, changed = redactAuditMap(event.Metadata, changed)
		if out[i].Type != event.Type || out[i].Actor != event.Actor || out[i].Summary != event.Summary {
			changed = true
		}
	}
	return out, changed
}

func redactAuditMap(values map[string]any, changed bool) (map[string]any, bool) {
	if values == nil {
		return nil, changed
	}
	out := make(map[string]any, len(values))
	for key, value := range values {
		var valueChanged bool
		out[key], valueChanged = redactAuditValue(key, value)
		changed = changed || valueChanged
	}
	return out, changed
}

func redactAuditValue(field string, value any) (any, bool) {
	return redactAuditValueDepth(field, value, 0)
}

func redactAuditValueDepth(field string, value any, depth int) (any, bool) {
	if value == nil {
		return nil, false
	}
	if journalredact.RedactFieldValue(field, "", "") == "[REDACTED]" {
		if text, ok := value.(string); ok && text == "[REDACTED]" {
			return text, false
		}
		return "[REDACTED]", true
	}
	if depth >= 64 {
		return "[REDACTED]", true
	}
	if raw, ok := value.(json.RawMessage); ok {
		var decoded any
		if err := json.Unmarshal(raw, &decoded); err != nil {
			redacted := journalredact.RedactText(string(raw))
			return redacted, redacted != string(raw)
		}
		return redactAuditValueDepth(field, decoded, depth+1)
	}
	rv := reflect.ValueOf(value)
	for rv.Kind() == reflect.Interface || rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return nil, false
		}
		rv = rv.Elem()
		depth++
		if depth >= 64 {
			return "[REDACTED]", true
		}
	}
	switch rv.Kind() {
	case reflect.String:
		original := rv.String()
		redacted := journalredact.RedactFieldValue(field, original, "")
		return redacted, redacted != original
	case reflect.Map:
		if rv.Type().Key().Kind() != reflect.String {
			return "[REDACTED]", true
		}
		out := make(map[string]any, rv.Len())
		changed := false
		iter := rv.MapRange()
		for iter.Next() {
			key := iter.Key().String()
			entry, entryChanged := redactAuditValueDepth(key, iter.Value().Interface(), depth+1)
			out[key] = entry
			changed = changed || entryChanged
		}
		return out, changed
	case reflect.Slice, reflect.Array:
		out := make([]any, rv.Len())
		changed := false
		for i := 0; i < rv.Len(); i++ {
			entry, entryChanged := redactAuditValueDepth(field, rv.Index(i).Interface(), depth+1)
			out[i] = entry
			changed = changed || entryChanged
		}
		return out, changed
	case reflect.Struct:
		encoded, err := json.Marshal(value)
		if err != nil {
			return "[REDACTED]", true
		}
		var decoded any
		if err := json.Unmarshal(encoded, &decoded); err != nil {
			return "[REDACTED]", true
		}
		return redactAuditValueDepth(field, decoded, depth+1)
	default:
		return value, false
	}
}

func redactStoreTaskAudits(tasks []Task) bool {
	changed := false
	for i := range tasks {
		var taskChanged bool
		tasks[i].AuditEvents, taskChanged = redactTaskAuditEvents(tasks[i].AuditEvents)
		changed = changed || taskChanged
	}
	return changed
}
