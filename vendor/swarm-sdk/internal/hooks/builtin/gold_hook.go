package builtin

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/gold"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/silver"
	"github.com/google/uuid"
)

// GoldConfig controls the Gold analysis trigger hook behavior.
type GoldConfig struct {
	// Enabled toggles the hook.
	Enabled bool

	// SilverDirs maps project hashes to their Silver tree directories.
	// When non-empty, these override auto-discovery.
	SilverDirs map[string]string

	// BronzeDirs maps project hashes to their Bronze event directories.
	BronzeDirs map[string]string

	// GoldDir is where Gold insights are stored.
	// If empty, defaults to ~/.swarm/gold/ (global cross-project directory).
	GoldDir string

	// LookbackDays is how many days of Silver trees to include.
	// Default: 7.
	LookbackDays int

	// MinTreesForCrossProject is the minimum number of trees needed before
	// cross-project analysis is triggered.
	// Default: 3.
	MinTreesForCrossProject int

	// AnalysisTimeout is the maximum duration for a Gold analysis run.
	// Default: 5 minutes.
	AnalysisTimeout time.Duration

	// MinRunInterval prevents duplicate analysis runs from closely-spaced
	// session-stop and silver-tree-built events. Default: 10 minutes.
	MinRunInterval time.Duration
}

// GoldHook triggers Gold analysis after Silver trees are built.
// It listens for EventAgentStopped events and runs analysis asynchronously.
// The hook replaces the dead gold_simple.go stub with a fully operational engine.
type GoldHook struct {
	cfg     GoldConfig
	logger  observability.Logger
	emitter SteeringEventEmitter // reuse existing interface for Bronze emission

	mu        sync.Mutex
	lastRunAt time.Time
	running   bool
}

// NewGoldHook creates a new Gold analysis trigger hook.
func NewGoldHook(cfg GoldConfig, logger observability.Logger) *GoldHook {
	if cfg.LookbackDays <= 0 {
		cfg.LookbackDays = 7
	}
	if cfg.MinTreesForCrossProject <= 0 {
		cfg.MinTreesForCrossProject = 3
	}
	if cfg.AnalysisTimeout <= 0 {
		cfg.AnalysisTimeout = 5 * time.Minute
	}
	if cfg.MinRunInterval <= 0 {
		cfg.MinRunInterval = 10 * time.Minute
	}
	return &GoldHook{
		cfg:    cfg,
		logger: logger,
	}
}

// SetEventEmitter registers a Bronze event emitter for Gold lifecycle events.
func (h *GoldHook) SetEventEmitter(emitter SteeringEventEmitter) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.emitter = emitter
}

// Name returns the hook identifier.
func (h *GoldHook) Name() string { return "gold-analyzer" }

// Priority runs after Silver tree hook (priority 3) to give Silver time to build.
func (h *GoldHook) Priority() int { return 2 }

// Filter triggers primarily after Silver trees are saved. Agent stop remains a
// delayed fallback for sessions where Silver was disabled or emitted no tree.
func (h *GoldHook) Filter(event hooks.Event) bool {
	if !h.cfg.Enabled {
		return false
	}
	return event.Type == hooks.EventSilverTreeBuilt || event.Type == hooks.EventAgentStopped
}

// OnEvent schedules Gold analysis asynchronously.
// Silver tree events run quickly because the tree is already persisted. Session
// stop events wait briefly so the Silver hook can win when it is enabled.
func (h *GoldHook) OnEvent(ctx context.Context, event hooks.Event) (hooks.HookResult, error) {
	reason := "silver_tree_built"
	delay := 500 * time.Millisecond
	if event.Type == hooks.EventAgentStopped {
		reason = "session_end_fallback"
		delay = 5 * time.Second
	}

	go func() {
		if delay > 0 {
			time.Sleep(delay)
		}

		if !h.reserveRun() {
			return
		}

		runCtx, cancel := context.WithTimeout(context.Background(), h.cfg.AnalysisTimeout)
		defer cancel()
		defer h.finishRun()

		if err := h.runAnalysis(runCtx, reason); err != nil {
			if h.logger != nil {
				h.logger.Warn(context.Background(), "gold.analysis_failed",
					observability.F("error", err.Error()))
			}
		}
	}()

	return hooks.Continue(), nil
}

func (h *GoldHook) reserveRun() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.running {
		return false
	}
	if !h.lastRunAt.IsZero() && time.Since(h.lastRunAt) < h.cfg.MinRunInterval {
		return false
	}
	h.running = true
	return true
}

func (h *GoldHook) finishRun() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.running = false
	h.lastRunAt = time.Now()
}

// runAnalysis executes one Gold analysis pass.
func (h *GoldHook) runAnalysis(ctx context.Context, triggerReason string) error {
	runID := fmt.Sprintf("gold-%s", uuid.New().String()[:8])

	h.emitEvent(ctx, hooks.EventGoldAnalysisStarted, map[string]any{
		"artifact_type":  "gold_run",
		"artifact_id":    runID + ":started",
		"run_id":         runID,
		"trigger_reason": triggerReason,
	})

	// Build analyzer config from hook config
	analyzerCfg := gold.DefaultAnalyzerConfig()
	analyzerCfg.LookbackDays = h.cfg.LookbackDays
	analyzerCfg.GoldDir = h.cfg.GoldDir

	// Use configured dirs or auto-discover
	silverDirs := h.cfg.SilverDirs
	if len(silverDirs) == 0 {
		silverDirs = discoverSilverDirs()
	}
	analyzerCfg.SilverDirs = silverDirs

	bronzeDirs := h.cfg.BronzeDirs
	if len(bronzeDirs) == 0 {
		bronzeDirs = discoverBronzeDirs()
	}
	analyzerCfg.BronzeDirs = bronzeDirs

	// Resolve Gold output directory
	goldDir := h.cfg.GoldDir
	if goldDir == "" {
		var err error
		goldDir, err = gold.GlobalGoldDir()
		if err != nil {
			h.emitEvent(ctx, hooks.EventGoldAnalysisFailed, map[string]any{
				"artifact_type":  "gold_run",
				"artifact_id":    runID + ":failed",
				"run_id":         runID,
				"error":          err.Error(),
				"trigger_reason": triggerReason,
			})
			return fmt.Errorf("gold hook: resolve gold dir: %w", err)
		}
	}
	analyzerCfg.GoldDir = goldDir

	// Run analysis
	analyzer := gold.NewAnalyzer(analyzerCfg)
	run, insights, err := analyzer.Run(ctx, runID)
	if err != nil {
		h.emitEvent(ctx, hooks.EventGoldAnalysisFailed, map[string]any{
			"artifact_type":  "gold_run",
			"artifact_id":    runID + ":failed",
			"run_id":         runID,
			"error":          err.Error(),
			"trigger_reason": triggerReason,
		})
		return fmt.Errorf("gold hook: analysis: %w", err)
	}

	// Persist results
	store, storeErr := gold.NewGoldStore(goldDir)
	if storeErr == nil {
		_ = store.SaveRun(run, insights)
	}

	// Emit completion event
	noInsightReason := classifyGoldRunOutcome(run.TreesAnalyzed, run.InsightsGenerated)
	h.emitEvent(ctx, hooks.EventGoldAnalysisComplete, map[string]any{
		"artifact_type":      "gold_run",
		"artifact_id":        runID,
		"run_id":             runID,
		"trigger_reason":     triggerReason,
		"no_insight_reason":  noInsightReason,
		"trees_analyzed":     run.TreesAnalyzed,
		"insights_generated": run.InsightsGenerated,
		"duration_ms":        run.DurationMs,
		"run":                run,
	})

	// Emit individual insight events to Bronze/remote analytics.
	for _, ins := range insights {
		h.emitEvent(ctx, hooks.EventGoldInsightCreated, map[string]any{
			"artifact_type": "gold_insight",
			"artifact_id":   ins.ID,
			"insight_id":    ins.ID,
			"title":         ins.Title,
			"category":      string(ins.Category),
			"severity":      string(ins.Severity),
			"run_id":        runID,
			"project_hash":  ins.ProjectHash,
			"insight":       ins,
		})
	}

	if h.logger != nil {
		h.logger.Info(ctx, "gold.analysis_complete",
			observability.F("run_id", runID),
			observability.F("trees_analyzed", run.TreesAnalyzed),
			observability.F("insights_generated", run.InsightsGenerated),
			observability.F("duration_ms", run.DurationMs))
	}

	return nil
}

func classifyGoldRunOutcome(treesAnalyzed, insightsGenerated int) string {
	if treesAnalyzed == 0 {
		return "no_trees"
	}
	if insightsGenerated == 0 {
		return "no_patterns"
	}
	return "insights_generated"
}

// emitEvent fires a Bronze event for a Gold lifecycle event.
func (h *GoldHook) emitEvent(ctx context.Context, eventType string, payload map[string]any) {
	h.mu.Lock()
	emitter := h.emitter
	h.mu.Unlock()
	if emitter == nil {
		return
	}
	evt := hooks.Event{
		Type:      eventType,
		Timestamp: time.Now(),
		Data:      payload,
	}
	_, _ = emitter.Emit(ctx, evt)
}

// SetEnabled toggles the hook at runtime.
func (h *GoldHook) SetEnabled(enabled bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.cfg.Enabled = enabled
}

// IsEnabled reports whether the hook is active.
func (h *GoldHook) IsEnabled() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.cfg.Enabled
}

// discoverSilverDirs finds all project Silver directories under ~/.swarm/projects/.
func discoverSilverDirs() map[string]string {
	dirs := make(map[string]string)
	projectsDir, err := swarmProjectsDir()
	if err != nil {
		return dirs
	}

	entries, err := listDirEntries(projectsDir)
	if err != nil {
		return dirs
	}

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		projectHash := e.Name()
		silverDir, findErr := silver.FindSilverDir(projectHash)
		if findErr == nil {
			dirs[projectHash] = silverDir
		}
	}
	return dirs
}

// discoverBronzeDirs finds all project Bronze directories under ~/.swarm/projects/.
func discoverBronzeDirs() map[string]string {
	dirs := make(map[string]string)
	projectsDir, err := swarmProjectsDir()
	if err != nil {
		return dirs
	}

	entries, err := listDirEntries(projectsDir)
	if err != nil {
		return dirs
	}

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		projectHash := e.Name()
		bronzeDir := projectsDir + "/" + projectHash + "/findings/bronze"
		dirs[projectHash] = bronzeDir
	}
	return dirs
}

// swarmProjectsDir returns the ~/.swarm/projects/ directory.
func swarmProjectsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return home + "/.swarm/projects", nil
}

// listDirEntries returns the directory entries in a directory.
func listDirEntries(dir string) ([]os.DirEntry, error) {
	return os.ReadDir(dir)
}
