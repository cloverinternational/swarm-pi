package commands

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestModelConfigThinkingEnabledPresenceRoundTrip(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantValue bool
		wantSet   bool
		wantKey   bool
	}{
		{name: "omitted", input: `{"id":"catalog"}`, wantSet: false, wantKey: false},
		{name: "explicit false", input: `{"id":"disabled","thinking_enabled":false}`, wantValue: false, wantSet: true, wantKey: true},
		{name: "explicit true", input: `{"id":"enabled","thinking_enabled":true}`, wantValue: true, wantSet: true, wantKey: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var model ModelConfig
			if err := json.Unmarshal([]byte(tt.input), &model); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			if model.ThinkingEnabled != tt.wantValue || model.ThinkingEnabledSet != tt.wantSet {
				t.Fatalf("value=%v set=%v, want value=%v set=%v", model.ThinkingEnabled, model.ThinkingEnabledSet, tt.wantValue, tt.wantSet)
			}

			encoded, err := json.Marshal(model)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			hasKey := strings.Contains(string(encoded), `"thinking_enabled"`)
			if hasKey != tt.wantKey {
				t.Fatalf("encoded=%s, thinking_enabled present=%v, want %v", encoded, hasKey, tt.wantKey)
			}
		})
	}
}
