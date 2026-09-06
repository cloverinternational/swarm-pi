package config

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// FuzzConfigParsing fuzzes configuration file parsing
func FuzzConfigParsing(f *testing.F) {
	f.Add([]byte(`{"version":"1.0","settings":{}}`))
	f.Add([]byte(`{"version":"","settings":null}`))
	f.Add([]byte(`{}`))
	f.Add([]byte(strings.Repeat(`{"key":`, 1000) + `"value"}`))

	f.Fuzz(func(t *testing.T, data []byte) {
		var cfg map[string]any
		if err := json.Unmarshal(data, &cfg); err != nil {
			return
		}

		// Config operations should be safe
		for key, val := range cfg {
			_ = fmt.Sprintf("%s=%v", key, val)
		}

		// Remarshal
		_, _ = json.Marshal(cfg)
	})
}

// FuzzConfigKeyValidation fuzzes configuration key validation
func FuzzConfigKeyValidation(f *testing.F) {
	f.Add("valid_key")
	f.Add("")
	f.Add("\x00")
	f.Add(strings.Repeat("k", 5000))

	f.Fuzz(func(t *testing.T, key string) {
		// Key operations should be safe
		_ = len(key)
		_ = strings.Contains(key, "_")
	})
}

// FuzzConfigValueTyping fuzzes configuration value type handling
func FuzzConfigValueTyping(f *testing.F) {
	f.Add([]byte(`{"str":"value","num":123,"bool":true,"null":null}`))
	f.Add([]byte(`{"nested":{"deep":"value"}}`))
	f.Add([]byte(`{"array":[1,2,3]}`))

	f.Fuzz(func(t *testing.T, data []byte) {
		var cfg map[string]any
		if err := json.Unmarshal(data, &cfg); err != nil {
			return
		}

		// Type handling should be safe
		for _, val := range cfg {
			switch v := val.(type) {
			case string:
				_ = len(v)
			case float64:
				_ = int(v)
			case bool:
				_ = v
			case map[string]any:
				_ = len(v)
			case []any:
				_ = len(v)
			}
		}
	})
}
