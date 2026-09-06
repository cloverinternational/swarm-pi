package agent

import "testing"

// TestGetAutoCompactThreshold_DefaultMargin verifies the default (no user
// override) threshold is effectiveInputWindow - compactionMargin. With a nil
// provider, maxOutput is 0 so effectiveInputWindow == contextWindow.
func TestGetAutoCompactThreshold_DefaultMargin(t *testing.T) {
	a := &Agent{configuredContextWindow: 200_000}
	got := a.getAutoCompactThreshold()
	want := 200_000 - compactionMargin
	if got != want {
		t.Fatalf("default threshold: got %d want %d", got, want)
	}
}

// TestGetAutoCompactThreshold_HonorsFixedTokens is the regression test for the
// dual-path bug: a user-configured absolute token count (AutoCompactThresholdTokens)
// must drive the in-loop trigger instead of the hardcoded margin. Previously the
// fixed-token setting never reached the agent and was ignored.
func TestGetAutoCompactThreshold_HonorsFixedTokens(t *testing.T) {
	a := &Agent{
		configuredContextWindow: 200_000,
		autoCompactionConfig: AutoCompactionConfig{
			Threshold: AutoCompactionThreshold{
				Mode:  AutoCompactionThresholdFixedTokens,
				Value: 50_000,
			},
		},
	}
	if got := a.getAutoCompactThreshold(); got != 50_000 {
		t.Fatalf("fixed-token threshold: got %d want 50000", got)
	}
}

func TestGetAutoCompactThreshold_ResolvesPercentAgainstRealWindow(t *testing.T) {
	a := &Agent{
		configuredContextWindow: 200_000,
		autoCompactionConfig: AutoCompactionConfig{
			Threshold: AutoCompactionThreshold{
				Mode:  AutoCompactionThresholdPercent,
				Value: 0.50,
			},
		},
	}
	if got := a.getAutoCompactThreshold(); got != 100_000 {
		t.Fatalf("percent threshold: got %d want 100000", got)
	}
}

func TestGetAutoCompactThreshold_InvalidDescriptorUsesLegacyPercent(t *testing.T) {
	a := &Agent{
		configuredContextWindow: 200_000,
		autoCompactionConfig: AutoCompactionConfig{
			Threshold:                      AutoCompactionThreshold{Mode: AutoCompactionThresholdPercent, Value: 2},
			AutoCompactionThresholdPercent: 0.50,
		},
	}
	if got := a.getAutoCompactThreshold(); got != 100_000 {
		t.Fatalf("legacy percent fallback: got %d want 100000", got)
	}
}

// TestGetAutoCompactThreshold_ClampsAboveDefault ensures a user value larger
// than the safe default (effectiveInputWindow - margin) is clamped down so we
// still compact before the blocking limit.
func TestGetAutoCompactThreshold_ClampsAboveDefault(t *testing.T) {
	a := &Agent{
		configuredContextWindow: 128_000,
		autoCompactionConfig: AutoCompactionConfig{
			Threshold: AutoCompactionThreshold{
				Mode:  AutoCompactionThresholdFixedTokens,
				Value: 5_000_000,
			},
		},
	}
	want := 128_000 - compactionMargin
	if got := a.getAutoCompactThreshold(); got != want {
		t.Fatalf("clamp-above-default: got %d want %d", got, want)
	}
}

// TestGetAutoCompactThreshold_ClampsBelowFloor ensures a tiny user value is
// floored to minAutoCompactThreshold.
func TestGetAutoCompactThreshold_ClampsBelowFloor(t *testing.T) {
	a := &Agent{
		configuredContextWindow: 200_000,
		autoCompactionConfig: AutoCompactionConfig{
			Threshold: AutoCompactionThreshold{
				Mode:  AutoCompactionThresholdFixedTokens,
				Value: 10,
			},
		},
	}
	if got := a.getAutoCompactThreshold(); got != minAutoCompactThreshold {
		t.Fatalf("clamp-below-floor: got %d want %d", got, minAutoCompactThreshold)
	}
}

func TestGetAutoCompactThreshold_ProactiveThresholdLowersEffectiveThreshold(t *testing.T) {
	a := &Agent{
		configuredContextWindow:     200_000,
		proactiveSummarizeThreshold: 0.60,
		autoCompactionConfig: AutoCompactionConfig{
			Threshold: AutoCompactionThreshold{
				Mode:  AutoCompactionThresholdPercent,
				Value: 0.80,
			},
		},
	}
	if got := a.getAutoCompactThreshold(); got != 120_000 {
		t.Fatalf("proactive threshold: got %d want 120000", got)
	}
}

func TestGetAutoCompactThreshold_ZeroProactiveThresholdIsUnchanged(t *testing.T) {
	a := &Agent{
		configuredContextWindow:     200_000,
		proactiveSummarizeThreshold: 0,
		autoCompactionConfig: AutoCompactionConfig{
			Threshold: AutoCompactionThreshold{
				Mode:  AutoCompactionThresholdPercent,
				Value: 0.80,
			},
		},
	}
	if got := a.getAutoCompactThreshold(); got != 160_000 {
		t.Fatalf("threshold with proactive disabled: got %d want 160000", got)
	}
}
