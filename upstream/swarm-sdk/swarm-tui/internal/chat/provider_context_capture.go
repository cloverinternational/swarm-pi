package chat

import (
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/contextaudit"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
)

const (
	contextCallOutcomeDone      = "done"
	contextCallOutcomeError     = "error"
	contextCallOutcomeClosed    = "closed"
	contextCallOutcomeCancelled = "cancelled"
)

// DebugContextUsage is provider-reported usage for exactly one provider call.
type DebugContextUsage struct {
	InputTokens         int
	UncachedInputTokens int
	CacheCreationTokens int
	CacheReadTokens     int
	OutputTokens        int
	TotalTokens         int
}

// DebugContextCall is an immutable, content-free attribution snapshot for one
// actual provider invocation. Report contains labels and sizes, not prompt text.
type DebugContextCall struct {
	ID          string
	RunID       string
	Ordinal     int
	Provider    string
	Model       string
	Path        string
	StartedAt   time.Time
	CompletedAt time.Time
	Outcome     string
	Error       string
	Usage       *DebugContextUsage
	Report      contextaudit.ReportJSON
}

type providerContextCaptureStore struct {
	mu         sync.Mutex
	callsByRun map[string][]DebugContextCall
	nextByRun  map[string]int
	now        func() time.Time
}

type providerContextCandidate struct {
	store *providerContextCaptureStore
	call  DebugContextCall
	once  sync.Once
}

func newProviderContextCaptureStore() *providerContextCaptureStore {
	return &providerContextCaptureStore{
		callsByRun: make(map[string][]DebugContextCall),
		nextByRun:  make(map[string]int),
		now:        time.Now,
	}
}

func (s *providerContextCaptureStore) begin(req provider.ChatRequest, providerName, path string) *providerContextCandidate {
	if s == nil {
		return nil
	}
	runID := ""
	if req.Metadata != nil {
		runID, _ = req.Metadata["context_run_id"].(string)
	}
	if runID == "" {
		return nil
	}

	// Build the report before invoking downstream providers. This makes the
	// candidate immutable even if translators or later turns mutate slices/maps.
	report := contextaudit.BuildProviderRequestReportJSON(req)
	startedAt := s.now()

	s.mu.Lock()
	ordinal := s.nextByRun[runID] + 1
	s.nextByRun[runID] = ordinal
	s.mu.Unlock()

	return &providerContextCandidate{
		store: s,
		call: DebugContextCall{
			ID:        fmt.Sprintf("%s/call-%d", runID, ordinal),
			RunID:     runID,
			Ordinal:   ordinal,
			Provider:  providerName,
			Model:     req.Model,
			Path:      path,
			StartedAt: startedAt,
			Report:    report,
		},
	}
}

func (c *providerContextCandidate) finish(usage *conversation.TokenUsage, outcome string, err error) {
	if c == nil || c.store == nil {
		return
	}
	c.once.Do(func() {
		c.call.CompletedAt = c.store.now()
		c.call.Outcome = outcome
		if err != nil {
			c.call.Error = err.Error()
			if c.call.Outcome == "" {
				c.call.Outcome = contextCallOutcomeError
			}
		}
		if c.call.Outcome == "" {
			c.call.Outcome = contextCallOutcomeDone
		}
		if usage != nil {
			c.call.Usage = &DebugContextUsage{
				InputTokens:         usage.InputContextSize(),
				UncachedInputTokens: usage.Input,
				CacheCreationTokens: usage.CacheCreation,
				CacheReadTokens:     usage.CacheRead,
				OutputTokens:        usage.Output,
				TotalTokens:         usage.Total,
			}
		}

		c.store.mu.Lock()
		c.store.callsByRun[c.call.RunID] = append(c.store.callsByRun[c.call.RunID], c.call)
		c.store.mu.Unlock()
	})
}

// take returns one run's calls in invocation order and removes all store state
// for that run. This bounds memory and prevents later executions borrowing data.
func (s *providerContextCaptureStore) take(runID string) []DebugContextCall {
	if s == nil || runID == "" {
		return nil
	}
	s.mu.Lock()
	calls := append([]DebugContextCall(nil), s.callsByRun[runID]...)
	delete(s.callsByRun, runID)
	delete(s.nextByRun, runID)
	s.mu.Unlock()

	sort.SliceStable(calls, func(i, j int) bool {
		return calls[i].Ordinal < calls[j].Ordinal
	})
	return calls
}

func (sdk *SDKIntegration) flushProviderContextCaptures(runID string) {
	if sdk == nil || sdk.contextCapture == nil {
		return
	}
	calls := sdk.contextCapture.take(runID)
	if sdk.debugScreen != nil && len(calls) > 0 {
		sdk.debugScreen.AddContextCalls(calls)
	}
}
