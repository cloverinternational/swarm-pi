package cloudsync

import (
	"bytes"
	"encoding/json"
	"sort"
	"strconv"
	"strings"
)

func CanonicalJSON(value any) ([]byte, error) {
	var buf bytes.Buffer
	if err := writeCanonical(&buf, value); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func writeCanonical(buf *bytes.Buffer, value any) error {
	switch typed := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		buf.WriteByte('{')
		for i, key := range keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			keyBytes, err := json.Marshal(key)
			if err != nil {
				return err
			}
			buf.Write(keyBytes)
			buf.WriteByte(':')
			if err := writeCanonical(buf, typed[key]); err != nil {
				return err
			}
		}
		buf.WriteByte('}')
	case []any:
		buf.WriteByte('[')
		for i, item := range typed {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := writeCanonical(buf, item); err != nil {
				return err
			}
		}
		buf.WriteByte(']')
	case json.Number:
		buf.WriteString(formatNumber(typed))
	default:
		payload, err := json.Marshal(typed)
		if err != nil {
			return err
		}
		buf.Write(payload)
	}
	return nil
}

func formatNumber(number json.Number) string {
	raw := strings.TrimSpace(number.String())
	if raw == "" {
		return "0"
	}

	if !strings.ContainsAny(raw, ".eE") {
		if intValue, err := number.Int64(); err == nil {
			return strconv.FormatInt(intValue, 10)
		}
	}

	floatValue, err := number.Float64()
	if err != nil {
		return raw
	}

	return strconv.FormatFloat(floatValue, 'f', -1, 64)
}
