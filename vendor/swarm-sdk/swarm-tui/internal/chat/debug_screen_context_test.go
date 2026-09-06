package chat

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/contextaudit"
)

func contextScreenTestCall(id string, ordinal, input int) DebugContextCall {
	return DebugContextCall{
		ID:       id,
		RunID:    "run-test",
		Ordinal:  ordinal,
		Provider: "fake",
		Model:    "model",
		Path:     "stream",
		Outcome:  contextCallOutcomeDone,
		Usage: &DebugContextUsage{
			InputTokens:         input,
			UncachedInputTokens: input - 10,
			CacheReadTokens:     10,
			OutputTokens:        7,
		},
		Report: contextaudit.ReportJSON{
			Format:           "canonical-final",
			Estimate:         true,
			System:           contextaudit.BucketJSON{Name: "system", Tokens: 20, Bytes: 80, Pct: 20},
			Tools:            contextaudit.BucketJSON{Name: "tools", Tokens: 30, Bytes: 120, Pct: 30},
			Messages:         contextaudit.BucketJSON{Name: "messages", Tokens: 50, Bytes: 200, Pct: 50},
			GrandTotalBytes:  400,
			GrandTotalTokens: 100,
		},
	}
}

func TestContextTabRequiresPairedProviderCall(t *testing.T) {
	d := NewDebugScreen()
	d.AddRequest(DebugRequest{Body: `{"synthetic":true}`, TokensInput: 999})
	if d.contextAuditRequestAvailable() {
		t.Fatal("synthetic DebugRequest.Body incorrectly enabled Context tab")
	}

	d.AddContextCalls([]DebugContextCall{contextScreenTestCall("run-test/call-1", 1, 100)})
	if !d.contextAuditRequestAvailable() {
		t.Fatal("paired provider call did not enable Context tab")
	}
}

func TestContextTimelineAppendNavigateAndAutoSelect(t *testing.T) {
	d := NewDebugScreen()
	d.AddContextCalls([]DebugContextCall{
		contextScreenTestCall("run-test/call-1", 1, 100),
		contextScreenTestCall("run-test/call-2", 2, 125),
	})

	call, idx, ok := d.contextCallIndexed()
	if !ok || idx != 1 || call.Ordinal != 2 {
		t.Fatalf("auto-selected call = idx %d, %+v, ok=%v", idx, call, ok)
	}
	if !d.stepContextRequest(-1) {
		t.Fatal("failed to step to previous provider call")
	}
	call, idx, ok = d.contextCallIndexed()
	if !ok || idx != 0 || call.Ordinal != 1 {
		t.Fatalf("selected call after step = idx %d, %+v, ok=%v", idx, call, ok)
	}
	if d.stepContextRequest(-1) {
		t.Fatal("stepped before beginning of context timeline")
	}
}

func TestContextTimelineRenderingIsTruthful(t *testing.T) {
	d := NewDebugScreen()
	d.theme = DefaultTheme
	d.SetSize(120, 60)
	d.AddContextCalls([]DebugContextCall{
		contextScreenTestCall("run-test/call-1", 1, 100),
		contextScreenTestCall("run-test/call-2", 2, 125),
	})

	rendered := d.renderContextView(d.getLayout(), 50)
	for _, want := range []string{
		"canonical-final",
		"Canonical component estimate",
		"Same-call provider usage (actual): input 125",
		"cache read 10",
		"actual input +25",
		"provider wire encoding may differ",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("render missing %q:\n%s", want, rendered)
		}
	}
	if strings.Contains(rendered, "actual captured wire body") {
		t.Fatalf("render overclaimed wire precision:\n%s", rendered)
	}
}

func TestContextTimelineShowsUnavailableUsageAndClears(t *testing.T) {
	d := NewDebugScreen()
	d.theme = DefaultTheme
	d.SetSize(100, 50)
	failed := contextScreenTestCall("run-test/call-1", 1, 0)
	failed.Outcome = contextCallOutcomeError
	failed.Error = "provider failed"
	failed.Usage = nil
	d.AddContextCalls([]DebugContextCall{failed})

	rendered := d.renderContextView(d.getLayout(), 40)
	if !strings.Contains(rendered, "Provider usage unavailable") {
		t.Fatalf("missing unavailable-usage label:\n%s", rendered)
	}

	d.Show()
	d.Update(tea.KeyPressMsg{Code: 'c', Text: "c"})
	if len(d.contextCalls) != 0 || d.selectedContextCall != -1 {
		t.Fatalf("clear left context timeline state: calls=%d selected=%d", len(d.contextCalls), d.selectedContextCall)
	}
}

func TestContextTimelineDoesNotDeltaAcrossRuns(t *testing.T) {
	d := NewDebugScreen()
	d.theme = DefaultTheme
	d.SetSize(100, 50)
	first := contextScreenTestCall("run-a/call-1", 1, 100)
	first.RunID = "run-a"
	second := contextScreenTestCall("run-b/call-1", 1, 200)
	second.RunID = "run-b"
	d.AddContextCalls([]DebugContextCall{first, second})

	rendered := d.renderContextView(d.getLayout(), 40)
	if strings.Contains(rendered, "Δ vs call") {
		t.Fatalf("render compared unrelated runs:\n%s", rendered)
	}
}
