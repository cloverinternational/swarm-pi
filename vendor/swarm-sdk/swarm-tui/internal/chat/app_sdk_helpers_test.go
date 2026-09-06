package chat

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/metrics"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
)

func TestAsIntCoercesEveryJSONNumericRepresentation(t *testing.T) {
	tests := []struct {
		name  string
		value any
		want  int
		ok    bool
	}{
		// This is the representation encoding/json actually produces for a
		// map[string]any, and the one the previous .(int) assertion silently
		// rejected, leaving the CACHE section permanently at zero.
		{name: "float64 from encoding/json", value: float64(4664), want: 4664, ok: true},
		{name: "float64 zero", value: float64(0), want: 0, ok: true},
		{name: "int", value: 42, want: 42, ok: true},
		{name: "int64", value: int64(99), want: 99, ok: true},
		{name: "json.Number", value: json.Number("123"), want: 123, ok: true},
		{name: "string is not numeric", value: "123", want: 0, ok: false},
		{name: "nil", value: nil, want: 0, ok: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := asInt(tt.value)
			if ok != tt.ok || got != tt.want {
				t.Errorf("asInt(%#v) = (%d, %v), want (%d, %v)", tt.value, got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestRecordCacheMetricsFromResponseAcceptsDecodedJSON(t *testing.T) {
	app := &App{metrics: metrics.NewMetricsStore(t.TempDir())}
	// This mirrors exactly what a real response produces: metadata decoded
	// through encoding/json into map[string]any, where every number is float64.
	metadata := map[string]any{
		"cache_metrics": map[string]any{
			"cache_creation_tokens":    float64(4664),
			"cache_creation_5m_tokens": float64(0),
			"cache_creation_1h_tokens": float64(0),
			"cache_read_tokens":        float64(36486),
		},
	}
	app.recordCacheMetricsFromResponse(metadata)

	hits, reads, writes, _, _ := app.metrics.Cache.GetMetrics()
	if reads == 0 {
		t.Fatal("cache read was not recorded: the float64 assertion bug regressed")
	}
	if hits == 0 {
		t.Errorf("expected a recorded cache hit, got hits=%d reads=%d writes=%d", hits, reads, writes)
	}
	if writes == 0 {
		t.Errorf("expected a recorded cache write, got writes=%d", writes)
	}
}

// TestConvertSDKMessagesRendersCompactionHandoffInsteadOfDropping verifies that
// a compaction-generated handoff message (Metadata[conversation.CompactionGeneratedMetadataKey] == true)
// is no longer silently skipped by convertSDKMessages: it must appear in the
// output, tagged so a renderer can collapse it by default, with its original
// content still present (and identifiable via a synthetic marker prefix) so a
// user can inspect exactly what the agent was handed after compaction.
func TestConvertSDKMessagesRendersCompactionHandoffInsteadOfDropping(t *testing.T) {
	ts := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	sdkMessages := []*conversation.Message{
		{
			Role:      conversation.RoleUser,
			Content:   "Summarize the previous conversation.",
			Timestamp: ts,
			Metadata: map[string]any{
				conversation.CompactionGeneratedMetadataKey: true,
			},
		},
	}

	got := convertSDKMessages(sdkMessages)

	if len(got) != 1 {
		t.Fatalf("expected compaction-generated message to be rendered (not dropped), got %d messages", len(got))
	}

	msg := got[0]

	if msg.Role != string(conversation.RoleSystem) {
		t.Errorf("expected compaction handoff message Role = %q, got %q", conversation.RoleSystem, msg.Role)
	}

	wantContent := compactionHandoffMarker + "Summarize the previous conversation."
	if msg.Content != wantContent {
		t.Errorf("expected compaction handoff Content = %q, got %q", wantContent, msg.Content)
	}

	if generated, _ := msg.Metadata[conversation.CompactionGeneratedMetadataKey].(bool); !generated {
		t.Errorf("expected Metadata[%q] = true to be carried through, got %#v", conversation.CompactionGeneratedMetadataKey, msg.Metadata[conversation.CompactionGeneratedMetadataKey])
	}

	if handoff, _ := msg.Metadata[compactionHandoffMetadataKey].(bool); !handoff {
		t.Errorf("expected Metadata[%q] = true to be set, got %#v", compactionHandoffMetadataKey, msg.Metadata[compactionHandoffMetadataKey])
	}

	if !msg.Timestamp.Equal(ts) {
		t.Errorf("expected Timestamp preserved as %v, got %v", ts, msg.Timestamp)
	}
}

// TestConvertSDKMessagesRegressionNormalMessagesUnaffected is a regression test
// covering a mix of normal (non-compaction) messages plus a compaction-generated
// message, asserting the exact expected output shape: normal messages must be
// converted exactly as before, and the compaction message must now be included
// (rendered, not dropped) rather than change the count or shape of the
// surrounding normal messages.
func TestConvertSDKMessagesRegressionNormalMessagesUnaffected(t *testing.T) {
	ts1 := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	ts2 := time.Date(2026, 1, 1, 9, 5, 0, 0, time.UTC)
	ts3 := time.Date(2026, 1, 1, 9, 10, 0, 0, time.UTC)

	sdkMessages := []*conversation.Message{
		{
			Role:      conversation.RoleUser,
			Content:   "Hello there",
			Timestamp: ts1,
		},
		{
			Role:      conversation.RoleAssistant,
			Content:   "This is a compaction summary.",
			Timestamp: ts2,
			Metadata: map[string]any{
				conversation.CompactionGeneratedMetadataKey: true,
			},
		},
		{
			Role:      conversation.RoleAssistant,
			Content:   "Hi! How can I help?",
			Timestamp: ts3,
			Model:     "test-model",
		},
	}

	got := convertSDKMessages(sdkMessages)

	if len(got) != 3 {
		t.Fatalf("expected 3 messages (2 normal + 1 rendered compaction handoff), got %d", len(got))
	}

	// First message: plain user message, converted exactly as before.
	want0 := &Message{
		Role:      string(conversation.RoleUser),
		Content:   "Hello there",
		Timestamp: ts1,
		Metadata:  nil,
	}
	if !reflect.DeepEqual(got[0], want0) {
		t.Errorf("normal user message shape changed:\n got:  %#v\n want: %#v", got[0], want0)
	}

	// Second message: compaction-generated handoff, now rendered as a
	// collapsed-by-default system message instead of being dropped.
	if got[1].Role != string(conversation.RoleSystem) {
		t.Errorf("expected compaction handoff Role = %q, got %q", conversation.RoleSystem, got[1].Role)
	}
	wantContent := compactionHandoffMarker + "This is a compaction summary."
	if got[1].Content != wantContent {
		t.Errorf("expected compaction handoff Content = %q, got %q", wantContent, got[1].Content)
	}
	if generated, _ := got[1].Metadata[conversation.CompactionGeneratedMetadataKey].(bool); !generated {
		t.Errorf("expected compaction handoff to carry Metadata[%q] = true", conversation.CompactionGeneratedMetadataKey)
	}

	// Third message: plain assistant message, converted exactly as before
	// (IsComplete=true, Model set, unaffected by the compaction handling above).
	want2 := &Message{
		Role:       string(conversation.RoleAssistant),
		Content:    "Hi! How can I help?",
		Timestamp:  ts3,
		Metadata:   nil,
		IsComplete: true,
		Model:      "test-model",
	}
	if !reflect.DeepEqual(got[2], want2) {
		t.Errorf("normal assistant message shape changed:\n got:  %#v\n want: %#v", got[2], want2)
	}
}
