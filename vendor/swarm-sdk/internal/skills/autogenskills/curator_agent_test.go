package autogenskills

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability/noop"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/skills"
)

// stubFactory satisfies agent.Factory for testing.
type stubFactory struct {
	createSubAgentFn func(ctx context.Context, config agent.SubAgentConfig) (*agent.Agent, error)
}

func (f *stubFactory) CreateFromDefinition(ctx context.Context, def *agent.Definition, provCfg provider.Config) (*agent.Agent, error) {
	return nil, nil
}

func (f *stubFactory) CreateWorker(ctx context.Context, config agent.WorkerConfig) (*agent.Agent, error) {
	return nil, nil
}

func (f *stubFactory) CreateSubAgent(ctx context.Context, config agent.SubAgentConfig) (*agent.Agent, error) {
	if f.createSubAgentFn != nil {
		return f.createSubAgentFn(ctx, config)
	}
	return nil, nil
}

func (f *stubFactory) CreateSteering(ctx context.Context, config agent.SteeringAgentConfig) (*agent.Agent, error) {
	return nil, nil
}

func (f *stubFactory) CreateBackground(ctx context.Context, config agent.BackgroundConfig) (*agent.Agent, error) {
	return nil, nil
}

func TestNewCuratorAgent_ValidatesInputs(t *testing.T) {
	logger := noop.NewLogger()
	provCfg := provider.Config{Name: "test"}
	svc := &Service{cfg: Config{Mode: ModeAuto}}

	tests := []struct {
		name    string
		factory agent.Factory
		provCfg provider.Config
		service *Service
		logger  observability.Logger
		wantErr string
	}{
		{
			name:    "nil factory",
			factory: nil,
			provCfg: provCfg,
			service: svc,
			logger:  logger,
			wantErr: "non-nil factory",
		},
		{
			name:    "empty provider name",
			factory: &stubFactory{},
			provCfg: provider.Config{},
			service: svc,
			logger:  logger,
			wantErr: "non-empty Name",
		},
		{
			name:    "nil service",
			factory: &stubFactory{},
			provCfg: provCfg,
			service: nil,
			logger:  logger,
			wantErr: "non-nil service",
		},
		{
			name:    "nil logger",
			factory: &stubFactory{},
			provCfg: provCfg,
			service: svc,
			logger:  nil,
			wantErr: "non-nil logger",
		},
		{
			name:    "all valid",
			factory: &stubFactory{},
			provCfg: provCfg,
			service: svc,
			logger:  logger,
			wantErr: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ca, err := NewCuratorAgent(tt.factory, tt.provCfg, tt.service, tt.logger)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("expected error containing %q, got %q", tt.wantErr, err.Error())
				}
				if ca != nil {
					t.Fatal("expected nil CuratorAgent on error")
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if ca == nil {
					t.Fatal("expected non-nil CuratorAgent")
				}
			}
		})
	}
}

func TestBuildCuratorPrompt_IncludesSkillInventory(t *testing.T) {
	// Create a registry with autogen skills.
	reg := skills.NewRegistry()
	reg.RegisterSkill(&skills.Skill{
		Metadata: skills.SkillMetadata{
			Name:        "git-workflow",
			Version:     "1.0.0",
			Description: "Automates git branching and merging",
			Tags:        []string{"git", "vcs"},
		},
		Source:   "autogen",
		LoadedAt: time.Date(2025, 3, 15, 10, 0, 0, 0, time.UTC),
	}, false)
	reg.RegisterSkill(&skills.Skill{
		Metadata: skills.SkillMetadata{
			Name:        "error-handler",
			Version:     "2.1.0",
			Description: "Handles common error patterns",
			Tags:        []string{"errors", "debugging"},
		},
		Source:   "autogen",
		LoadedAt: time.Date(2025, 4, 1, 12, 0, 0, 0, time.UTC),
	}, false)

	factory, err := NewSkillFactory(Config{Mode: ModeAuto, AutogenDir: t.TempDir()}, reg, nil)
	if err != nil {
		t.Fatalf("NewSkillFactory: %v", err)
	}

	svc := &Service{
		cfg:     Config{Mode: ModeAuto},
		factory: factory,
	}

	ca := &CuratorAgent{
		service: svc,
		logger:  noop.NewLogger(),
	}

	prompt := ca.buildCuratorPrompt(nil, false)

	// Verify skill inventory is present.
	if !strings.Contains(prompt, "git-workflow") {
		t.Error("prompt should contain skill name 'git-workflow'")
	}
	if !strings.Contains(prompt, "error-handler") {
		t.Error("prompt should contain skill name 'error-handler'")
	}
	if !strings.Contains(prompt, "v1.0.0") {
		t.Error("prompt should contain version 'v1.0.0'")
	}
	if !strings.Contains(prompt, "Automates git branching") {
		t.Error("prompt should contain skill description")
	}
	if !strings.Contains(prompt, "tags: git, vcs") {
		t.Error("prompt should contain tags")
	}
	if !strings.Contains(prompt, "last_loaded:") {
		t.Error("prompt should contain last_loaded timestamp")
	}
}

func TestBuildCuratorPrompt_NoSkills(t *testing.T) {
	// Create a service with no skills.
	reg := skills.NewRegistry()

	factory, err := NewSkillFactory(Config{Mode: ModeAuto, AutogenDir: t.TempDir()}, reg, nil)
	if err != nil {
		t.Fatalf("NewSkillFactory: %v", err)
	}

	svc := &Service{
		cfg:     Config{Mode: ModeAuto},
		factory: factory,
	}

	ca := &CuratorAgent{
		service: svc,
		logger:  noop.NewLogger(),
	}

	prompt := ca.buildCuratorPrompt(nil, false)

	if !strings.Contains(prompt, "No skills exist yet") {
		t.Error("prompt should mention 'No skills exist yet' when inventory is empty")
	}
}

func TestBuildCuratorPrompt_ContainsRules(t *testing.T) {
	svc := &Service{
		cfg: Config{Mode: ModeAuto},
	}

	ca := &CuratorAgent{
		service: svc,
		logger:  noop.NewLogger(),
	}

	// Pass results covering every action type so the dynamically-built task
	// list includes the ARCHIVAL and PATCHING sections; consolidate=true adds
	// the UMBRELLA CONSOLIDATION section (now agent-judged, not rule-provided).
	prompt := ca.buildCuratorPrompt([]ReviewResult{
		{SkillName: "old-skill", Action: ActionArchive, Reason: "unused"},
		{SkillName: "outdated", Action: ActionPatch, Reason: "stale content"},
	}, true)

	// Verify key rules and instructions are present.
	rules := []string{
		"NEVER delete a skill",
		"ALWAYS view a skill before patching",
		"class-level",
		"UMBRELLA CONSOLIDATION",
		"PATCHING",
		"LIFECYCLE",
		"SkillManage",
		"structured consolidations and prunings",
		"write_file",
		"absorbed_into",
	}
	for _, rule := range rules {
		if !strings.Contains(prompt, rule) {
			t.Errorf("prompt should contain rule %q", rule)
		}
	}
}

// TestBuildCuratorPrompt_WithReviewResults verifies that when review results
// are provided, the prompt includes the rule-based curator recommendations.
func TestBuildCuratorPrompt_WithReviewResults(t *testing.T) {
	svc := &Service{
		cfg: Config{Mode: ModeAuto},
	}

	ca := &CuratorAgent{
		service: svc,
		logger:  noop.NewLogger(),
	}

	results := []ReviewResult{
		{SkillName: "old-skill", Action: ActionArchive, Reason: "unused for 31 days"},
		{SkillName: "skill-a", Action: ActionConsolidate, Reason: "shares >50% tags", RelatedSkills: []string{"skill-b"}},
		{SkillName: "outdated-skill", Action: ActionPatch, Reason: "created 61 days ago"},
	}

	// consolidate=true. Any ActionConsolidate results are legacy/no-ops now:
	// the prompt must NOT inject them as candidates — consolidation is judged
	// by the agent from the inventory.
	prompt := ca.buildCuratorPrompt(results, true)

	// Verify rule-based review results are present
	if !strings.Contains(prompt, "RULE-BASED REVIEW RESULTS") {
		t.Error("prompt should contain rule-based review results section")
	}
	if !strings.Contains(prompt, "[ARCHIVE] old-skill") {
		t.Error("prompt should contain archive recommendation")
	}
	if !strings.Contains(prompt, "[PATCH] outdated-skill") {
		t.Error("prompt should contain patch recommendation")
	}
	// Regression: programmatic consolidation candidates must never be injected.
	if strings.Contains(prompt, "[CONSOLIDATE]") {
		t.Error("prompt must NOT inject programmatic [CONSOLIDATE] candidates; consolidation is agent-judged")
	}
	if strings.Contains(prompt, "related: skill-b") {
		t.Error("prompt must NOT inject a pre-computed related-skills list")
	}
	if !strings.Contains(prompt, "identify PREFIX/DOMAIN CLUSTERS yourself") {
		t.Error("prompt should instruct the agent to form clusters itself from the inventory")
	}
}

func TestBuildCuratorPrompt_HermesContractAndExactSchema(t *testing.T) {
	ca := &CuratorAgent{service: &Service{cfg: Config{Mode: ModeAuto}}, logger: noop.NewLogger()}
	prompt := ca.buildCuratorPrompt(nil, true)
	required := []string{
		"LIBRARY OF CLASS-LEVEL INSTRUCTIONS AND EXPERIENTIAL KNOWLEDGE",
		"If you end the pass with fewer than 10 archives, you stopped too early",
		"## Structured summary (required)\n```yaml\nconsolidations:\n  - from: <old-skill-name>\n    into: <umbrella-skill-name>\n    reason: <one short sentence — why merged, not just 'similar'>\nprunings:\n  - name: <skill-name>\n    reason: <one short sentence — why archived with no merge target>\n```",
	}
	for _, clause := range required {
		if !strings.Contains(prompt, clause) {
			t.Errorf("prompt missing required literal clause/schema: %q", clause)
		}
	}
}

func TestBuildCuratorPrompt_InventoryUsesCuratorStateAndUnknownCounters(t *testing.T) {
	reg := skills.NewRegistry()
	reg.RegisterSkill(&skills.Skill{
		Metadata: skills.SkillMetadata{Name: "stateful-skill", Version: "1.2.3", Description: "state metadata"},
		Source:   "autogen",
	}, false)
	dir := t.TempDir()
	factory, err := NewSkillFactory(Config{Mode: ModeAuto, AutogenDir: dir}, reg, nil)
	if err != nil {
		t.Fatal(err)
	}
	curator := NewCurator(CuratorConfig{}, nil, "")
	created := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
	used := created.Add(24 * time.Hour)
	archived := used.Add(24 * time.Hour)
	curator.mu.Lock()
	curator.state.SkillStates["stateful-skill"] = SkillMeta{
		State:         SkillStateConsolidated,
		Pinned:        true,
		CreatedAt:     created,
		LastUsedAt:    used,
		ArchivedAt:    &archived,
		AbsorbedInto:  "umbrella",
		ArchiveReason: "absorbed",
	}
	curator.mu.Unlock()
	svc := &Service{cfg: Config{Mode: ModeAuto, AutogenDir: dir}, factory: factory, curator: curator}
	prompt := (&CuratorAgent{service: svc}).buildCuratorPrompt(nil, true)
	required := []string{
		"state=consolidated", "pinned=yes", "created=" + created.Format(time.RFC3339),
		"last_used=" + used.Format(time.RFC3339), "archived_at=" + archived.Format(time.RFC3339),
		"absorbed_into=umbrella", "archive_reason=absorbed",
		"cron=unknown", "use=unknown", "view=unknown", "patches=unknown",
	}
	for _, field := range required {
		if !strings.Contains(prompt, field) {
			t.Errorf("inventory missing %q", field)
		}
	}
}

func TestCuratorExecutionTask_NoConsolidationWhenDisabled(t *testing.T) {
	task := curatorExecutionTask(false, false)
	if strings.Contains(strings.ToLower(task), "consolidat") {
		t.Fatalf("disabled task mentions consolidation: %q", task)
	}
}
