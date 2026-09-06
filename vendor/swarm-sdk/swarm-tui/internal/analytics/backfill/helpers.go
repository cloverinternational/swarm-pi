package backfill

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

func stableID(parts ...string) string {
	hasher := sha256.New()
	hasher.Write([]byte("swarm-backfill-v1"))
	for _, part := range parts {
		hasher.Write([]byte{0})
		hasher.Write([]byte(part))
	}
	sum := hasher.Sum(nil)
	return "backfill_" + hex.EncodeToString(sum[:16])
}

func stableContentHash(value any) string {
	body, _ := json.Marshal(value)
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func projectHashForWorkspace(workspace string) string {
	if strings.TrimSpace(workspace) == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(workspace))
	return hex.EncodeToString(sum[:])[:16]
}

func parseDateOrTime(value string) (time.Time, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return time.Time{}, nil
	}
	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02",
		"2006-01-02 15:04:05",
	}
	var lastErr error
	for _, layout := range layouts {
		parsed, err := time.Parse(layout, trimmed)
		if err == nil {
			return parsed.UTC(), nil
		}
		lastErr = err
	}
	return time.Time{}, fmt.Errorf("parse time %q: %w", value, lastErr)
}

func anyTime(values ...any) time.Time {
	for _, value := range values {
		switch typed := value.(type) {
		case time.Time:
			if !typed.IsZero() {
				return typed.UTC()
			}
		case string:
			if parsed, err := parseDateOrTime(typed); err == nil && !parsed.IsZero() {
				return parsed.UTC()
			}
		case float64:
			if typed > 0 {
				return time.Unix(int64(typed), 0).UTC()
			}
		case int64:
			if typed > 0 {
				return time.Unix(typed, 0).UTC()
			}
		case int:
			if typed > 0 {
				return time.Unix(int64(typed), 0).UTC()
			}
		case json.Number:
			if parsed, err := strconv.ParseInt(string(typed), 10, 64); err == nil && parsed > 0 {
				return time.Unix(parsed, 0).UTC()
			}
		}
	}
	return time.Time{}
}

func stringValue(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case fmt.Stringer:
		return typed.String()
	case nil:
		return ""
	default:
		return fmt.Sprint(typed)
	}
}

func mapString(value map[string]any, keys ...string) string {
	for _, key := range keys {
		if result := strings.TrimSpace(stringValue(value[key])); result != "" {
			return result
		}
	}
	return ""
}

func readJSONMap(path string) (map[string]any, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var value map[string]any
	if err := json.Unmarshal(body, &value); err != nil {
		return nil, err
	}
	return value, nil
}

func readJSONArray(path string) ([]map[string]any, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var items []map[string]any
	if err := json.Unmarshal(body, &items); err != nil {
		return nil, err
	}
	return items, nil
}

func fileModTime(path string) time.Time {
	info, err := os.Stat(path)
	if err != nil {
		return time.Now().UTC()
	}
	return info.ModTime().UTC()
}
