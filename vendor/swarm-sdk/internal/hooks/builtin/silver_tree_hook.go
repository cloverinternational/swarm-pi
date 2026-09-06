package builtin

import (
	"context"
	"sync"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/silver"
)

// SilverConfig controls the Silver tree build hook behavior.
type SilverConfig struct {
	// Enabled toggles the hook.
	Enabled bool

	// ProjectHash is the active project identifier.
	ProjectHash string

	// BronzeDir is the path to bronze event files.
	BronzeDir string

	// SilverDir is the path to store Silver tree JSON files.
	SilverDir string

	// MinEventsForTree is the minimum number of events to justify building a tree.
	// Default: 20.
	MinEventsForTree int

	// BuildTimeout is the maximum duration for a tree build operation.
	// Default: 2 minutes.
	BuildTimeout time.Duration

	// WindowDuration is the event grouping window size.
	// Default: 5 minutes.
	WindowDuration time.Duration
}

// SilverTreeHook triggers Silver tree generation at session end.
// It listens for agent.stopped events and builds a hierarchical tree index
// over the bronze events from that session.
type SilverTreeHook struct {
	cfg     SilverConfig
	logger  observability.Logger
	emitter SteeringEventEmitter

	mu          sync.Mutex
	lastBuildAt time.Time
	building    bool
}

// NewSilverTreeHook creates a new Silver tree build trigger hook.
func NewSilverTreeHook(cfg SilverConfig, logger observability.Logger) *SilverTreeHook {
	if cfg.MinEventsForTree <= 0 {
		cfg.MinEventsForTree = 20
	}
	if cfg.BuildTimeout <= 0 {
		cfg.BuildTimeout = 2 * time.Minute
	}
	return &SilverTreeHook{
		cfg:    cfg,
		logger: logger,
	}
}

// Name returns the hook name.
func (h *SilverTreeHook) Name() string { return "silver-tree-builder" }

// Priority runs after bronze capture (which is priority 5).
func (h *SilverTreeHook) Priority() int { return 3 }

// SetEventEmitter wires Silver tree build events into the hook stream.
func (h *SilverTreeHook) SetEventEmitter(emitter SteeringEventEmitter) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.emitter = emitter
}

// Filter triggers on agent stop events.
func (h *SilverTreeHook) Filter(event hooks.Event) bool {
	if !h.cfg.Enabled {
		return false
	}
	return event.Type == hooks.EventAgentStopped
}

// OnEvent handles session-end events by triggering a Silver tree build.
// The build runs asynchronously so it doesn't block the agent shutdown.
func (h *SilverTreeHook) OnEvent(ctx context.Context, event hooks.Event) (hooks.HookResult, error) {
	h.mu.Lock()
	if h.building {
		h.mu.Unlock()
		return hooks.Continue(), nil // already building
	}
	h.building = true
	h.mu.Unlock()

	// Build asynchronously.
	go func() {
		defer func() {
			h.mu.Lock()
			h.building = false
			h.lastBuildAt = time.Now()
			h.mu.Unlock()
		}()

		buildCtx, cancel := context.WithTimeout(context.Background(), h.cfg.BuildTimeout)
		defer cancel()

		if err := h.buildTree(buildCtx); err != nil {
			if h.logger != nil {
				h.logger.Warn(context.Background(), "silver_tree.build_failed",
					observability.F("error", err.Error()))
			}
		}
	}()

	return hooks.Continue(), nil
}

// buildTree reads recent bronze events and generates a Silver tree.
func (h *SilverTreeHook) buildTree(ctx context.Context) error {
	cfg := h.cfg

	// Read bronze files from today.
	now := time.Now()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	files, err := silver.ListBronzeFiles(cfg.BronzeDir, todayStart, now)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return nil // no data
	}

	// Read all events.
	events, err := silver.ReadBronzeEvents(files)
	if err != nil {
		return err
	}
	if len(events) < cfg.MinEventsForTree {
		return nil // not enough data
	}

	// Detect sessions.
	sessions := silver.DetectSessions(events, silver.SessionDetectorConfig{
		WindowDuration: cfg.WindowDuration,
	})
	sessions = silver.MergeSessions(sessions, 2*time.Minute)

	if len(sessions) == 0 {
		return nil
	}

	// Build and save a tree for each session.
	store, err := silver.NewSilverStore(cfg.SilverDir)
	if err != nil {
		return err
	}

	for _, session := range sessions {
		if session.EventCount < cfg.MinEventsForTree {
			continue
		}

		tree, buildErr := silver.BuildTree(ctx, session, silver.TreeBuilderConfig{
			WindowDuration: cfg.WindowDuration,
		})
		if buildErr != nil {
			if h.logger != nil {
				h.logger.Warn(ctx, "silver_tree.session_build_failed",
					observability.F("error", buildErr.Error()),
					observability.F("conv_id", session.ConversationID))
			}
			continue
		}

		// Set project hash.
		tree.ProjectHash = cfg.ProjectHash

		// Verify tree integrity.
		verification := silver.VerifyTree(tree, session.Windows)
		if h.logger != nil {
			h.logger.Info(ctx, "silver_tree.built",
				observability.F("conv_id", session.ConversationID),
				observability.F("phases", len(tree.Structure)),
				observability.F("events", tree.TotalEvents),
				observability.F("domain", tree.DominantDomain),
				observability.F("verification_accuracy", verification.Accuracy),
				observability.F("verification_issues", len(verification.Issues)))
		}

		// Save tree.
		path, saveErr := store.SaveTree(tree)
		if saveErr != nil {
			if h.logger != nil {
				h.logger.Warn(ctx, "silver_tree.save_failed",
					observability.F("error", saveErr.Error()))
			}
			continue
		}

		if h.logger != nil {
			h.logger.Info(ctx, "silver_tree.saved",
				observability.F("path", path))
		}
		h.emitTreeBuilt(ctx, tree, verification, path)
	}

	return nil
}

func (h *SilverTreeHook) emitTreeBuilt(ctx context.Context, tree silver.SilverTree, verification silver.VerificationResult, path string) {
	h.mu.Lock()
	emitter := h.emitter
	h.mu.Unlock()
	if emitter == nil {
		return
	}
	artifactID := tree.ProjectHash + ":" + tree.ConversationID + ":" + tree.GeneratedAt.UTC().Format(time.RFC3339Nano)
	evt := hooks.Event{
		Type:           hooks.EventSilverTreeBuilt,
		Timestamp:      tree.GeneratedAt,
		ConversationID: tree.ConversationID,
		Data: map[string]any{
			"artifact_type":   "silver_tree",
			"artifact_id":     artifactID,
			"project_hash":    tree.ProjectHash,
			"conversation_id": tree.ConversationID,
			"path":            path,
			"verification":    verification,
			"tree":            tree,
		},
	}
	if _, err := emitter.Emit(ctx, evt); err != nil && h.logger != nil {
		h.logger.Warn(ctx, "silver_tree.emit_failed",
			observability.F("error", err.Error()),
			observability.F("conversation_id", tree.ConversationID))
	}
}

// SetEnabled toggles the hook at runtime.
func (h *SilverTreeHook) SetEnabled(enabled bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.cfg.Enabled = enabled
}

// IsEnabled reports whether the hook is active.
func (h *SilverTreeHook) IsEnabled() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.cfg.Enabled
}
