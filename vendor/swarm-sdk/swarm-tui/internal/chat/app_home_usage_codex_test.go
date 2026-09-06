package chat

import (
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider/codex"
)

// TestRenderUsageSubscriptionTabIncludesCodex: a cached Codex snapshot renders
// as a Codex block with plan, window bar, and an honest freshness note.
func TestRenderUsageSubscriptionTabIncludesCodex(t *testing.T) {
	a := NewApp()
	a.usageResult = &usageDataResult{
		FetchedAt: time.Now(),
		Entries: []providerUsageEntry{{
			Provider:  "Codex",
			Email:     "user@example.com",
			AccountID: "acct_1",
			CodexData: &codexUsageData{Snapshot: codex.RateLimitSnapshot{
				CapturedAt: time.Now().Add(-2 * time.Minute),
				AccountID:  "acct_1",
				PlanType:   "pro",
				Primary:    &codex.RateLimitWindow{UsedPercent: 61, WindowMinutes: 10080, ResetAt: time.Now().Add(48 * time.Hour).Unix()},
				Secondary:  &codex.RateLimitWindow{UsedPercent: 12, WindowMinutes: 300},
			}},
		}},
	}

	th := a.theme
	st := func(fg string) lipgloss.Style { return lipgloss.NewStyle().Foreground(lipgloss.Color(fg)) }
	lines := a.renderUsageSubscriptionTab(100, &th, st)
	out := strings.Join(lines, "\n")

	for _, want := range []string{"Codex (ChatGPT)", "user@example.com", "PRO plan", "Weekly", "5h", "cached usage"} {
		if !strings.Contains(out, want) {
			t.Errorf("usage tab missing %q\n----\n%s", want, out)
		}
	}
}

func TestRenderUsageSubscriptionTabLabelsLiveCodexUsage(t *testing.T) {
	a := NewApp()
	a.usageResult = &usageDataResult{Entries: []providerUsageEntry{{
		Provider: "Codex",
		CodexData: &codexUsageData{
			Live: true,
			Snapshot: codex.RateLimitSnapshot{
				CapturedAt: time.Now(),
				PlanType:   "plus",
				Primary:    &codex.RateLimitWindow{UsedPercent: 10, WindowMinutes: 300},
			},
		},
	}}}

	th := a.theme
	st := func(fg string) lipgloss.Style { return lipgloss.NewStyle().Foreground(lipgloss.Color(fg)) }
	out := strings.Join(a.renderUsageSubscriptionTab(100, &th, st), "\n")
	if !strings.Contains(out, "live usage · updated just now") {
		t.Fatalf("live usage freshness missing:\n%s", out)
	}
	if strings.Contains(out, "cached usage") {
		t.Fatalf("live response mislabeled as cached:\n%s", out)
	}
}

func TestUsageEntryArrivesBeforeProviderSweepCompletes(t *testing.T) {
	history := &usageDataResult{}
	a := &App{
		usageLoading:        true,
		usageLoadGeneration: 4,
		usageResult:         history,
	}
	entry := providerUsageEntry{
		Provider:  "Codex",
		AccountID: "acct_live",
		CodexData: &codexUsageData{
			Live: true,
			Snapshot: codex.RateLimitSnapshot{
				AccountID: "acct_live",
				Primary:   &codex.RateLimitWindow{UsedPercent: 25},
			},
		},
	}

	a.applyUsageEntryFetched(usageEntryFetchedMsg{generation: 4, entry: entry})

	if !a.usageLoading {
		t.Fatal("a progressive account result must not mark the whole provider sweep complete")
	}
	if len(a.usageResult.Entries) != 1 || a.usageResult.Entries[0].CodexData == nil ||
		!a.usageResult.Entries[0].CodexData.Live {
		t.Fatalf("progressive Codex result not applied: %+v", a.usageResult.Entries)
	}
	if !a.viewNeedsRefresh {
		t.Fatal("progressive result must refresh the visible Usage tab")
	}

	a.viewNeedsRefresh = false
	a.applyUsageEntryFetched(usageEntryFetchedMsg{
		generation: 3,
		entry: providerUsageEntry{
			Provider:  "Codex",
			AccountID: "stale",
		},
	})
	if len(a.usageResult.Entries) != 1 || a.viewNeedsRefresh {
		t.Fatal("stale progressive result must be ignored")
	}
}
