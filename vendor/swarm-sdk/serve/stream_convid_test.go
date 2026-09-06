package serve

import (
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/client"
)

// TestEncodeEvent_LocalConvID verifies the stretch-goal additive behaviour:
// local main-turn events now carry source.convID so multiplexing clients can
// filter the stream, while local events with no convID still omit Source
// entirely (preserving the historical "ordinary local events omit source"
// contract).
func TestEncodeEvent_LocalConvID(t *testing.T) {
	t.Run("local_with_convID_emits_source", func(t *testing.T) {
		ev := client.Event{
			Kind:   client.EventAgent,
			At:     time.Now(),
			Source: client.EventSource{Kind: client.SourceLocal, ConvID: "conv-123"},
		}
		env := EncodeEvent(ev)
		if env.Source == nil {
			t.Fatalf("expected Source to be emitted for local event with convID")
		}
		if env.Source.ConvID != "conv-123" {
			t.Fatalf("expected convID conv-123, got %q", env.Source.ConvID)
		}
		if env.Source.Kind != string(client.SourceLocal) {
			t.Fatalf("expected kind local, got %q", env.Source.Kind)
		}
	})

	t.Run("local_without_convID_omits_source", func(t *testing.T) {
		ev := client.Event{
			Kind:   client.EventAgent,
			At:     time.Now(),
			Source: client.EventSource{Kind: client.SourceLocal},
		}
		if env := EncodeEvent(ev); env.Source != nil {
			t.Fatalf("expected Source omitted for local event without convID, got %+v", env.Source)
		}
	})

	t.Run("non_local_still_emitted", func(t *testing.T) {
		ev := client.Event{
			Kind:   client.EventAgent,
			At:     time.Now(),
			Source: client.EventSource{Kind: client.SourceSubAgent, AgentID: "sub-1", ConvID: "conv-9"},
		}
		env := EncodeEvent(ev)
		if env.Source == nil || env.Source.AgentID != "sub-1" || env.Source.ConvID != "conv-9" {
			t.Fatalf("expected sub-agent source preserved, got %+v", env.Source)
		}
	})
}
