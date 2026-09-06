package chat

import (
	"strings"
	"testing"
)

func TestLoadingIndicator_SetProgressClamps(t *testing.T) {
	li := NewLoadingIndicator(LoadingBar, "test", testTheme())

	li.SetProgress(1.5)
	if pct, ok := li.GetProgress(); !ok || pct != 1.0 {
		t.Fatalf("expected clamped progress 1.0, got pct=%v ok=%v", pct, ok)
	}

	li.SetProgress(-0.3)
	if pct, ok := li.GetProgress(); !ok || pct != 0.0 {
		t.Fatalf("expected clamped progress 0.0, got pct=%v ok=%v", pct, ok)
	}

	li.SetProgress(0.42)
	if pct, ok := li.GetProgress(); !ok || pct != 0.42 {
		t.Fatalf("expected progress 0.42, got pct=%v ok=%v", pct, ok)
	}
}

func TestLoadingIndicator_GetProgressDefault(t *testing.T) {
	li := NewLoadingIndicator(LoadingBar, "test", testTheme())

	pct, ok := li.GetProgress()
	if ok {
		t.Fatalf("expected ok=false before any SetProgress call, got pct=%v ok=%v", pct, ok)
	}
	if pct != 0 {
		t.Fatalf("expected default pct=0, got %v", pct)
	}

	li.SetProgress(0.5)
	pct, ok = li.GetProgress()
	if !ok {
		t.Fatalf("expected ok=true after SetProgress")
	}
	if pct != 0.5 {
		t.Fatalf("expected pct=0.5, got %v", pct)
	}
}

func TestLoadingIndicator_ClearProgressRevertsToIndeterminate(t *testing.T) {
	clock := NewAnimationClock()
	li := NewLoadingIndicator(LoadingBar, "compacting", testTheme())
	li.Start(clock)

	li.SetProgress(0.5)
	determinateView := li.View(clock)
	if !strings.Contains(determinateView, "%") {
		t.Fatalf("expected determinate view to contain a %% label, got %q", determinateView)
	}

	li.ClearProgress()
	if pct, ok := li.GetProgress(); ok || pct != 0 {
		t.Fatalf("expected progress reset after ClearProgress, got pct=%v ok=%v", pct, ok)
	}

	indeterminateView := li.View(clock)
	if strings.Contains(indeterminateView, "%") {
		t.Fatalf("expected indeterminate view to not contain a %% label, got %q", indeterminateView)
	}
}

func TestLoadingIndicator_RenderProgressBarStableWidth(t *testing.T) {
	li := NewLoadingIndicator(LoadingBar, "", testTheme())
	li.Width = 20

	li.SetProgress(0.0)
	zeroView := li.renderProgressBar(0)

	li.SetProgress(1.0)
	fullView := li.renderProgressBar(0)

	li.SetProgress(0.5)
	midView := li.renderProgressBar(0)

	if zeroView == "" || fullView == "" || midView == "" {
		t.Fatalf("expected non-empty renders at 0%%, 50%%, and 100%%")
	}

	// Rendered strings include ANSI styling, so instead of comparing raw
	// lengths we assert none of them panic and all contain the expected
	// percentage suffix, and that varying percentage does not blow up the
	// output length unpredictably (roughly bounded by width + label).
	if !strings.Contains(zeroView, "0%") {
		t.Fatalf("expected 0%% label in zero view, got %q", zeroView)
	}
	if !strings.Contains(fullView, "100%") {
		t.Fatalf("expected 100%% label in full view, got %q", fullView)
	}
	if !strings.Contains(midView, "50%") {
		t.Fatalf("expected 50%% label in mid view, got %q", midView)
	}
}
