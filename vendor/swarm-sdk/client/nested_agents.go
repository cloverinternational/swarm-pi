package client

import (
	"fmt"
	"sync"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
	toolsbuiltin "github.com/Swarm-Code/mono/swarm-sdk/internal/tools/builtin"
)

// sdkBackgroundAgentManager is the lifecycle store shared by the nested-agent
// tools in one Client. It intentionally has no TUI dependencies: embedders
// need the same cancellation and retrieval semantics without importing the
// terminal application.
type sdkBackgroundAgentManager struct {
	mu     sync.RWMutex
	agents map[string]*agent.BackgroundAgent
	tasks  map[string]string
}

func newSDKBackgroundAgentManager() *sdkBackgroundAgentManager {
	return &sdkBackgroundAgentManager{
		agents: make(map[string]*agent.BackgroundAgent),
		tasks:  make(map[string]string),
	}
}

func (m *sdkBackgroundAgentManager) Add(bg *agent.BackgroundAgent, task string) error {
	if bg == nil {
		return fmt.Errorf("background agent cannot be nil")
	}
	id := bg.AgentID()
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.agents[id]; ok {
		return fmt.Errorf("agent with ID %q already exists", id)
	}
	m.agents[id] = bg
	m.tasks[id] = task
	return nil
}

func (m *sdkBackgroundAgentManager) Get(id string) (*agent.BackgroundAgent, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	bg, ok := m.agents[id]
	if !ok {
		return nil, fmt.Errorf("agent %q not found", id)
	}
	return bg, nil
}

func (m *sdkBackgroundAgentManager) List() []toolsbuiltin.BackgroundAgentInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]toolsbuiltin.BackgroundAgentInfo, 0, len(m.agents))
	for id, bg := range m.agents {
		info := toolsbuiltin.BackgroundAgentInfo{
			AgentID:  id,
			ParentID: bg.ParentID(),
			Task:     m.tasks[id],
			Status:   bg.Status(),
			Result:   bg.Result(),
		}
		if info.Result != nil {
			info.StartTime = info.Result.StartTime
			info.Duration = info.Result.Duration
		}
		out = append(out, info)
	}
	return out
}

func (m *sdkBackgroundAgentManager) Cancel(id string) error {
	bg, err := m.Get(id)
	if err != nil {
		return err
	}
	bg.Cancel()
	return nil
}

func (m *sdkBackgroundAgentManager) Remove(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.agents[id]; !ok {
		return fmt.Errorf("agent %q not found", id)
	}
	delete(m.agents, id)
	delete(m.tasks, id)
	return nil
}

func registerNestedAgentTools(
	logger observability.Logger,
	tracer observability.Tracer,
	providerRegistry *provider.SimpleRegistry,
	parent tools.Registry,
	providerConfig provider.Config,
	workspace string,
) error {
	factory, err := agent.NewSimpleFactory(agent.FactoryConfig{
		ProviderRegistry: providerRegistry,
		Logger:           logger,
		Tracer:           tracer,
	})
	if err != nil {
		return err
	}
	bgManager := newSDKBackgroundAgentManager()
	config := toolsbuiltin.DelegateTaskConfig{
		Factory:        factory,
		ProviderConfig: providerConfig,
		ProviderConfigResolver: func(name string) (provider.Config, error) {
			cfg := providerConfig
			cfg.Name = normalizeProviderName(name)
			if cfg.Name != providerConfig.Name {
				cfg.APIKey, cfg.BaseURL = resolveCredentials(name)
			}
			return cfg, nil
		},
		ContextWindowResolver: func(name, model string) int {
			if window, ok := LookupModelContextWindow(name, model); ok {
				return window
			}
			return providerConfig.ContextWindow
		},
		ParentToolReg: parent,
		Logger:        logger,
		Tracer:        tracer,
		BGManager:     bgManager,
		WorkspaceRoot: workspace,
	}

	register := func(tool tools.Tool) error {
		if err := parent.Register(tool); err != nil {
			return fmt.Errorf("register %s: %w", tool.Name(), err)
		}
		return nil
	}

	task, err := toolsbuiltin.NewDelegateTaskTool(config)
	if err != nil {
		return err
	}
	if err := register(task); err != nil {
		return err
	}
	subagent, err := toolsbuiltin.NewSubagentTool(config)
	if err != nil {
		return err
	}
	if err := register(subagent); err != nil {
		return err
	}
	background, err := toolsbuiltin.NewSpawnBackgroundAgentTool(toolsbuiltin.SpawnBackgroundAgentConfig{
		Factory:        factory,
		BGManager:      bgManager,
		ProviderConfig: providerConfig,
		ParentToolReg:  parent,
		Logger:         logger,
		Tracer:         tracer,
	})
	if err != nil {
		return err
	}
	if err := register(background); err != nil {
		return err
	}
	output, err := toolsbuiltin.NewCheckBackgroundAgentTool(toolsbuiltin.CheckBackgroundAgentConfig{
		BGManager: bgManager, Logger: logger, Tracer: tracer,
	})
	if err != nil {
		return err
	}
	if err := register(output); err != nil {
		return err
	}
	wait, err := toolsbuiltin.NewWaitForAgentTool(toolsbuiltin.WaitForAgentConfig{
		BGManager: bgManager, Logger: logger, Tracer: tracer,
	})
	if err != nil {
		return err
	}
	if err := register(wait); err != nil {
		return err
	}
	multiWait, err := toolsbuiltin.NewMultiAgentWaitTool(toolsbuiltin.MultiAgentWaitConfig{
		BGManager: bgManager, Logger: logger, Tracer: tracer,
	})
	if err != nil {
		return err
	}
	return register(multiWait)
}
