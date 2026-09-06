package builtin

import (
	"context"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
)

func TestResolveSubagentToolNames_UsesExecutableRegistryNames(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		requested []string
		available []string
		want      []string
	}{
		{
			name:      "forge registry satisfies builtin read aliases",
			requested: []string{"file_read", "grep", "list_dir"},
			available: []string{"Read", "grep", "apply_patch"},
			want:      []string{"Read", "grep"},
		},
		{
			name:      "builtin registry satisfies forge aliases",
			requested: []string{"Read", "Grep", "Shell"},
			available: []string{"file_read", "grep", "bash"},
			want:      []string{"file_read", "grep", "bash"},
		},
		{
			name:      "semantic search is not silently downgraded to text grep",
			requested: []string{"semantic_grep"},
			available: []string{"grep"},
			want:      nil,
		},
		{
			name:      "wildcard inherits executable parent tools but not recursive controls",
			requested: []string{"*"},
			available: []string{
				"Read",
				"bash",
				"Subagent",
				"SubagentOutput",
				"Delegate",
				"DelegateOutput",
				"BackgroundTask",
				"TaskOutput",
				"wait_for_agent",
				"multi_agent_wait",
				"TaskManage",
			},
			want: []string{"Read", "bash", "TaskManage"},
		},
		{
			name:      "explicit recursive controls remain unavailable",
			requested: []string{"Read", "Delegate", "spawn_background_agent"},
			available: []string{"Read", "Delegate", "spawn_background_agent"},
			want:      []string{"Read"},
		},
		{
			name:      "empty allow list stays tool free",
			requested: nil,
			available: []string{"Read", "grep"},
			want:      nil,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := resolveSubagentToolNames(tt.requested, tt.available)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("resolveSubagentToolNames(%v, %v) = %v, want %v",
					tt.requested, tt.available, got, tt.want)
			}
		})
	}
}

func TestBuiltinAgentToolHintsResolveAcrossClientRegistries(t *testing.T) {
	t.Parallel()

	tests := []struct {
		agentID   string
		available []string
		want      []string
	}{
		{
			agentID:   "code-reviewer",
			available: []string{"repository_inspect", "Read", "grep", "apply_patch"},
			want:      []string{"repository_inspect"},
		},
		{
			agentID:   "research-agent",
			available: []string{"Read", "grep", "bash", "apply_patch"},
			want:      []string{"Read", "grep", "bash"},
		},
		{
			agentID:   "explore",
			available: []string{"repository_inspect", "file_read", "grep", "list_dir", "file_write"},
			want:      []string{"repository_inspect"},
		},
	}

	defs := getBuiltinAgentDefinitions()
	for _, tt := range tests {
		tt := tt
		t.Run(tt.agentID, func(t *testing.T) {
			t.Parallel()
			def := defs[tt.agentID]
			if def == nil {
				t.Fatalf("missing builtin definition %q", tt.agentID)
			}
			got := resolveSubagentToolNames(def.ToolHints, tt.available)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("%s resolved tools = %v, want %v", tt.agentID, got, tt.want)
			}
		})
	}
}

func TestDetachedSubagentContext_PreservesValuesWithoutParentCancellation(t *testing.T) {
	t.Parallel()

	type contextKey string
	const key contextKey = "workspace"

	parent, cancel := context.WithCancel(context.WithValue(context.Background(), key, "/workspace"))
	cancel()

	detached := detachedSubagentContext(parent)
	if got := detached.Value(key); got != "/workspace" {
		t.Fatalf("detached context value = %v, want /workspace", got)
	}
	if err := detached.Err(); err != nil {
		t.Fatalf("detached context inherited parent cancellation: %v", err)
	}
	select {
	case <-detached.Done():
		t.Fatal("detached context unexpectedly has a cancellation signal")
	case <-time.After(10 * time.Millisecond):
	}
	if !agent.IsSubAgent(detached) {
		t.Fatal("detached context is not marked as a sub-agent")
	}
}

func TestSubagentToolCustomDefinitionsConcurrentRefreshAndRead(t *testing.T) {
	t.Parallel()

	tool := &SubagentTool{customAgentDefs: make(map[string]*agent.Definition)}
	const workers = 16
	const iterations = 50

	var wg sync.WaitGroup
	wg.Add(workers)
	for worker := range workers {
		worker := worker
		go func() {
			defer wg.Done()
			for iteration := range iterations {
				id := fmt.Sprintf("custom-%d-%d", worker, iteration)
				tool.SetCustomAgentDefGetter(func() []*agent.Definition {
					return []*agent.Definition{{ID: id, Name: id}}
				})
				tool.refreshCustomAgentDefs()
				_ = tool.getAllAvailableAgents()
				_, _ = tool.customAgentDefinition(id)
			}
		}()
	}
	wg.Wait()
}

func TestWithSubAgentIDDoesNotMutateSharedDefinition(t *testing.T) {
	t.Parallel()

	original := &agent.Definition{
		ID:       "shared",
		Metadata: map[string]any{"type": "sub_agent"},
	}
	cloned := withSubAgentID(original, "shared-call-123")

	if cloned == original {
		t.Fatal("withSubAgentID returned the shared definition pointer")
	}
	if cloned.ID != "shared-call-123" {
		t.Fatalf("cloned ID = %q, want shared-call-123", cloned.ID)
	}
	if original.ID != "shared" {
		t.Fatalf("original definition was mutated: ID = %q", original.ID)
	}
	cloned.Metadata["changed"] = true
	if _, exists := original.Metadata["changed"]; exists {
		t.Fatal("cloned definition shares mutable metadata with original")
	}
}
