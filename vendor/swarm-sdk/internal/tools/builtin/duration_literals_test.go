package builtin

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability/noop"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
)

var errConfigCaptured = errors.New("config captured")

type durationCaptureFactory struct {
	background agent.BackgroundConfig
	subagent   agent.SubAgentConfig
}

func (f *durationCaptureFactory) CreateFromDefinition(context.Context, *agent.Definition, provider.Config) (*agent.Agent, error) {
	return nil, errConfigCaptured
}

func (f *durationCaptureFactory) CreateSubAgent(_ context.Context, cfg agent.SubAgentConfig) (*agent.Agent, error) {
	f.subagent = cfg
	return nil, errConfigCaptured
}

func (f *durationCaptureFactory) CreateWorker(context.Context, agent.WorkerConfig) (*agent.Agent, error) {
	return nil, errConfigCaptured
}

func (f *durationCaptureFactory) CreateSteering(context.Context, agent.SteeringAgentConfig) (*agent.Agent, error) {
	return nil, errConfigCaptured
}

func (f *durationCaptureFactory) CreateBackground(_ context.Context, cfg agent.BackgroundConfig) (*agent.Agent, error) {
	f.background = cfg
	return nil, errConfigCaptured
}

func TestSpawnBackgroundAgent_UsesThirtyMinuteDuration(t *testing.T) {
	factory := &durationCaptureFactory{}
	tool, err := NewSpawnBackgroundAgentTool(SpawnBackgroundAgentConfig{
		Factory: factory, BGManager: newMockBackgroundAgentManager(),
		Logger: noop.NewLogger(), Tracer: noop.NewTracer(),
	})
	if err != nil {
		t.Fatalf("NewSpawnBackgroundAgentTool: %v", err)
	}
	_, _ = tool.Run(context.Background(), SpawnBackgroundAgentParams{Task: "capture config"})
	if factory.background.Timeout != 30*time.Minute {
		t.Fatalf("background timeout = %s, want 30m (raw integer literals are nanoseconds)", factory.background.Timeout)
	}
}

func TestDelegateTask_UsesOneMinuteDuration(t *testing.T) {
	factory := &durationCaptureFactory{}
	tool, err := NewDelegateTaskTool(DelegateTaskConfig{
		Factory: factory, Logger: noop.NewLogger(), Tracer: noop.NewTracer(),
	})
	if err != nil {
		t.Fatalf("NewDelegateTaskTool: %v", err)
	}
	_, _ = tool.Run(context.Background(), DelegateTaskParams{Task: "capture config"})
	if factory.subagent.Timeout != time.Minute {
		t.Fatalf("subagent timeout = %s, want 1m (raw integer literals are nanoseconds)", factory.subagent.Timeout)
	}
}
