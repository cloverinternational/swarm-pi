package builtin

import (
	"context"
	"errors"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
	sdkerr "github.com/Swarm-Code/mono/swarm-sdk/internal/sdkerr"
	"github.com/Swarm-Code/mono/swarm-sdk/tests/agent/mocks"
)

type countingFactory struct {
	createCalls int
}

func (f *countingFactory) CreateFromDefinition(context.Context, *agent.Definition, provider.Config) (*agent.Agent, error) {
	f.createCalls++
	return nil, errors.New("provider execution must not be reached")
}

func (f *countingFactory) CreateSubAgent(context.Context, agent.SubAgentConfig) (*agent.Agent, error) {
	f.createCalls++
	return nil, errors.New("provider execution must not be reached")
}

func (f *countingFactory) CreateWorker(context.Context, agent.WorkerConfig) (*agent.Agent, error) {
	f.createCalls++
	return nil, errors.New("provider execution must not be reached")
}

func (f *countingFactory) CreateSteering(context.Context, agent.SteeringAgentConfig) (*agent.Agent, error) {
	f.createCalls++
	return nil, errors.New("provider execution must not be reached")
}

func (f *countingFactory) CreateBackground(context.Context, agent.BackgroundConfig) (*agent.Agent, error) {
	f.createCalls++
	return nil, errors.New("provider execution must not be reached")
}

func TestSubagentRequiredInspectionFailsBeforeProviderExecution(t *testing.T) {
	for _, agentID := range []string{"explore", "code-reviewer"} {
		t.Run(agentID, func(t *testing.T) {
			factory := &countingFactory{}
			tool, err := NewSubagentTool(DelegateTaskConfig{
				Factory: factory,
				Logger:  mocks.NewMockLogger(),
				Tracer:  mocks.NewMockTracer(),
			})
			if err != nil {
				t.Fatalf("NewSubagentTool() error = %v", err)
			}

			_, err = tool.Run(context.Background(), SubagentParams{
				Task:    "inspect repository",
				AgentID: agentID,
			})
			var sdkError *sdkerr.Error
			if !errors.As(err, &sdkError) {
				t.Fatalf("Run() error = %T %v, want *sdkerr.Error", err, err)
			}
			if sdkError.Code != "delegate_task.missing_required_capability" {
				t.Fatalf("error code = %q, want delegate_task.missing_required_capability", sdkError.Code)
			}
			if factory.createCalls != 0 {
				t.Fatalf("factory create calls = %d, want 0", factory.createCalls)
			}
		})
	}
}

func TestInspectionRequiredAgentsResolveRepositoryInspect(t *testing.T) {
	for _, agentID := range []string{"explore", "code-reviewer"} {
		def := getBuiltinAgentDefinitions()[agentID]
		if def == nil {
			t.Fatalf("missing built-in %q", agentID)
		}
		found := false
		for _, name := range def.ToolHints {
			if name == "repository_inspect" {
				found = true
			}
		}
		if !found {
			t.Errorf("%s ToolHints = %v, missing repository_inspect", agentID, def.ToolHints)
		}
	}
}

func TestSubagentResolvesSafeInspectionFromWorkspace(t *testing.T) {
	tool, err := NewSubagentTool(DelegateTaskConfig{
		Factory:       &countingFactory{},
		Logger:        mocks.NewMockLogger(),
		Tracer:        mocks.NewMockTracer(),
		WorkspaceRoot: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("NewSubagentTool() error = %v", err)
	}
	if tool.repositoryInspector == nil {
		t.Fatal("safe repository inspection capability was not resolved")
	}
	if !tool.repositoryInspector.IsSafeRepositoryInspector() {
		t.Fatal("resolved inspection tool does not advertise the safe capability")
	}
	if got := tool.repositoryInspector.Name(); got != "repository_inspect" {
		t.Fatalf("resolved tool name = %q, want repository_inspect", got)
	}
}
