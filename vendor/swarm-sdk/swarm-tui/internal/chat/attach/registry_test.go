package attach

import (
	"errors"
	"reflect"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/a2a"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/builtin"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/headless/automation/state"
)

func TestListTargetsMapsFiltersAndSorts(t *testing.T) {
	sources := registrySources{
		agents: func() []builtin.BackgroundAgentInfo {
			return []builtin.BackgroundAgentInfo{
				{AgentID: "agent-b", Task: "Bravo\nmore", Status: "running", Summary: "reading files", LastTool: "Read", ToolCount: 2},
				{AgentID: "agent-a", Task: "Alpha", Status: "running"},
			}
		},
		peers: func(string) ([]a2a.PeerPresence, error) {
			return []a2a.PeerPresence{
				{Handle: "self", Name: "Self", Status: "idle"},
				{Handle: "host~remote", Status: "busy", Type: a2a.PeerTypeRemote, AddedBy: "fallback"},
				{Handle: "local", Name: "Charlie", Status: "idle", CurrentTask: "waiting for input", ControlSocket: "/tmp/c", ServeURL: "http://x"},
			}, nil
		},
		selfHandle: func() string { return "self" },
	}

	got, err := listTargets(sources, "test")
	if err != nil {
		t.Fatal(err)
	}
	want := []Target{
		{Kind: TargetKindSubAgent, ID: "agent-a", Name: "Alpha", Status: "running", AgentID: "agent-a", Snapshot: StateSnapshot{Streaming: true}},
		{Kind: TargetKindSubAgent, ID: "agent-b", Name: "Bravo", Status: "running", AgentID: "agent-b", Summary: "reading files", LastTool: "Read", ToolCount: 2, Snapshot: StateSnapshot{Summary: "reading files", Preview: "reading files", Streaming: true, Messages: []state.MessageState{{Role: "assistant", Content: "reading files"}}}},
		{Kind: TargetKindPeer, ID: "local", Name: "Charlie", Status: "idle", Steerable: true, ControlSocket: "/tmp/c", ServeURL: "http://x", Summary: "", Snapshot: StateSnapshot{Summary: "waiting for input", Preview: "waiting for input", Messages: []state.MessageState{{Role: "assistant", Content: "waiting for input"}}}},
		{Kind: TargetKindPeer, ID: "host~remote", Name: "host~remote", Status: "busy", Machine: "host"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("targets = %#v, want %#v", got, want)
	}
}

func TestListTargetsPropagatesPeerError(t *testing.T) {
	want := errors.New("boom")
	_, err := listTargets(registrySources{
		agents:     func() []builtin.BackgroundAgentInfo { return nil },
		peers:      func(string) ([]a2a.PeerPresence, error) { return nil, want },
		selfHandle: func() string { return "" },
	}, "test")
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want wrapped %v", err, want)
	}
}

func TestPeerMachineUsesAddedByForRemoteHandleWithoutMachinePrefix(t *testing.T) {
	tests := []struct {
		name string
		peer a2a.PeerPresence
		want string
	}{
		{
			name: "remote fallback",
			peer: a2a.PeerPresence{
				Handle:  "remote",
				Type:    a2a.PeerTypeRemote,
				AddedBy: "adder",
			},
			want: "adder",
		},
		{
			name: "handle prefix wins",
			peer: a2a.PeerPresence{
				Handle:  "host~remote",
				Type:    a2a.PeerTypeRemote,
				AddedBy: "adder",
			},
			want: "host",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := peerMachine(tt.peer); got != tt.want {
				t.Fatalf("peerMachine(%#v) = %q, want %q", tt.peer, got, tt.want)
			}
		})
	}
}
