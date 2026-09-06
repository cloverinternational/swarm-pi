package anthropic

import (
	"testing"
)

func TestParseModelVersion(t *testing.T) {
	tests := []struct {
		name          string
		model         string
		expectFamily  string
		expectMajor   int
		expectMinor   int
		expectInvalid bool
	}{
		{
			name:         "opus 4.6",
			model:        "claude-opus-4-6",
			expectFamily: "opus",
			expectMajor:  4,
			expectMinor:  6,
		},
		{
			name:         "opus 4.6 with date",
			model:        "claude-opus-4-6-20260206",
			expectFamily: "opus",
			expectMajor:  4,
			expectMinor:  6,
		},
		{
			name:         "opus 4.5",
			model:        "claude-opus-4-5-20251101",
			expectFamily: "opus",
			expectMajor:  4,
			expectMinor:  5,
		},
		{
			name:         "sonnet 4.5",
			model:        "claude-sonnet-4-5-20250929",
			expectFamily: "sonnet",
			expectMajor:  4,
			expectMinor:  5,
		},
		{
			name:         "haiku 3.5",
			model:        "claude-haiku-3-5-20240307",
			expectFamily: "haiku",
			expectMajor:  3,
			expectMinor:  5,
		},
		{
			name:         "opus 4 with date only (no minor version)",
			model:        "claude-opus-4-20250514",
			expectFamily: "opus",
			expectMajor:  4,
			expectMinor:  0, // Date stamp should be detected and set to 0
		},
		{
			name:         "sonnet 4 with date only (no minor version)",
			model:        "claude-sonnet-4-20240229",
			expectFamily: "sonnet",
			expectMajor:  4,
			expectMinor:  0, // Date stamp should be detected and set to 0
		},
		{
			name:          "invalid model format",
			model:         "gpt-4",
			expectInvalid: true,
		},
		{
			name:          "incomplete version",
			model:         "claude-opus",
			expectInvalid: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			version := parseModelVersion(tt.model)

			if tt.expectInvalid {
				if version.Family != "" || version.Major != 0 || version.Minor != 0 {
					t.Errorf("Expected invalid version, got family=%s, major=%d, minor=%d",
						version.Family, version.Major, version.Minor)
				}
				return
			}

			if version.Family != tt.expectFamily {
				t.Errorf("Family = %v, want %v", version.Family, tt.expectFamily)
			}
			if version.Major != tt.expectMajor {
				t.Errorf("Major = %v, want %v", version.Major, tt.expectMajor)
			}
			if version.Minor != tt.expectMinor {
				t.Errorf("Minor = %v, want %v", version.Minor, tt.expectMinor)
			}
			if version.RawName != tt.model {
				t.Errorf("RawName = %v, want %v", version.RawName, tt.model)
			}
		})
	}
}

func TestSupportsAdaptiveThinking(t *testing.T) {
	tests := []struct {
		name     string
		model    string
		expected bool
	}{
		{
			name:     "opus 4.6 supports adaptive",
			model:    "claude-opus-4-6",
			expected: true,
		},
		{
			name:     "opus 4.6 with date supports adaptive",
			model:    "claude-opus-4-6-20260206",
			expected: true,
		},
		{
			name:     "opus 4.7 supports adaptive (future version)",
			model:    "claude-opus-4-7",
			expected: true,
		},
		{
			name:     "opus 5.0 supports adaptive (future version)",
			model:    "claude-opus-5-0",
			expected: true,
		},
		{
			name:     "opus 4.5 does not support adaptive",
			model:    "claude-opus-4-5-20251101",
			expected: false,
		},
		{
			name:     "opus 4 with date only does not support adaptive",
			model:    "claude-opus-4-20250514",
			expected: false,
		},
		{
			name:     "sonnet 4.6 supports adaptive",
			model:    "claude-sonnet-4-6",
			expected: true,
		},
		{
			name:     "sonnet 5 supports adaptive",
			model:    "claude-sonnet-5-20260301",
			expected: true,
		},
		{
			name:     "sonnet 4.5 does not support adaptive",
			model:    "claude-sonnet-4-5-20250929",
			expected: false,
		},
		{
			name:     "haiku 4.6 does not support adaptive (wrong family)",
			model:    "claude-haiku-4-6",
			expected: false,
		},
		{
			name:     "opus 3.7 does not support adaptive",
			model:    "claude-opus-3-7",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := SupportsAdaptiveThinking(tt.model)
			if result != tt.expected {
				t.Errorf("SupportsAdaptiveThinking(%q) = %v, want %v",
					tt.model, result, tt.expected)
			}
		})
	}
}

func TestSupportsMaxEffort(t *testing.T) {
	tests := []struct {
		name     string
		model    string
		expected bool
	}{
		{
			name:     "opus 4.6 supports max effort",
			model:    "claude-opus-4-6",
			expected: true,
		},
		{
			name:     "opus 4.5 does not support max effort",
			model:    "claude-opus-4-5-20251101",
			expected: false,
		},
		{
			name:     "sonnet 4.6 does not support max effort",
			model:    "claude-sonnet-4-6",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := SupportsMaxEffort(tt.model)
			if result != tt.expected {
				t.Errorf("SupportsMaxEffort(%q) = %v, want %v",
					tt.model, result, tt.expected)
			}
		})
	}
}

func TestSupportsExtendedThinking(t *testing.T) {
	tests := []struct {
		name     string
		model    string
		expected bool
	}{
		{
			name:     "opus 4.6 does NOT support extended thinking (uses adaptive)",
			model:    "claude-opus-4-6",
			expected: false,
		},
		{
			name:     "opus 4.5 supports extended thinking",
			model:    "claude-opus-4-5-20251101",
			expected: true,
		},
		{
			name:     "sonnet 4.5 supports extended thinking",
			model:    "claude-sonnet-4-5-20250929",
			expected: true,
		},
		{
			name:     "opus 3.7 supports extended thinking",
			model:    "claude-opus-3-7",
			expected: true,
		},
		{
			name:     "haiku 3.5 supports extended thinking",
			model:    "claude-haiku-3-5-20240307",
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := SupportsExtendedThinking(tt.model)
			if result != tt.expected {
				t.Errorf("SupportsExtendedThinking(%q) = %v, want %v",
					tt.model, result, tt.expected)
			}
		})
	}
}

func TestIsOpus47(t *testing.T) {
	tests := []struct {
		name     string
		model    string
		expected bool
	}{
		{"opus 4.7 bare", "claude-opus-4-7", true},
		{"opus 4.7 with date", "claude-opus-4-7-20260416", true},
		{"opus 4.6 is not 4.7", "claude-opus-4-6", false},
		{"opus 4.5 is not 4.7", "claude-opus-4-5-20251101", false},
		{"opus 4 with date is not 4.7", "claude-opus-4-20250514", false},
		{"sonnet 4.7 is not opus 4.7", "claude-sonnet-4-7", false},
		{"haiku 4.7 is not opus 4.7", "claude-haiku-4-7", false},
		{"opus 5.0 is not 4.7", "claude-opus-5-0", false},
		{"non-claude model", "gpt-4", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsOpus47(tt.model); got != tt.expected {
				t.Errorf("IsOpus47(%q) = %v, want %v", tt.model, got, tt.expected)
			}
		})
	}
}

func TestRequiresAdaptiveThinking(t *testing.T) {
	if !RequiresAdaptiveThinking("claude-opus-4-7") {
		t.Error("opus 4.7 must require adaptive thinking")
	}
	if !RequiresAdaptiveThinking("claude-sonnet-5") {
		t.Error("sonnet 5 must require adaptive thinking")
	}
	if !RequiresAdaptiveThinking("claude-opus-5-0") {
		t.Error("opus 5 must require adaptive thinking")
	}
	if RequiresAdaptiveThinking("claude-opus-4-6") {
		t.Error("opus 4.6 still supports manual extended thinking; must not be forced to adaptive")
	}
	if RequiresAdaptiveThinking("claude-sonnet-4-5-20250929") {
		t.Error("sonnet 4.5 must not be forced to adaptive thinking")
	}
}

func TestRejectsSamplingParams(t *testing.T) {
	if !RejectsSamplingParams("claude-opus-4-7") {
		t.Error("opus 4.7 must reject sampling params")
	}
	// Opus 4.8 and later inherit the 4.7 contract — this is the live 400 bug.
	if !RejectsSamplingParams("claude-opus-4-8") {
		t.Error("opus 4.8 must reject sampling params (inherits 4.7 contract)")
	}
	if !RejectsSamplingParams("claude-opus-4-8-20260101") {
		t.Error("opus 4.8 dated must reject sampling params")
	}
	if !RejectsSamplingParams("claude-sonnet-5") {
		t.Error("sonnet 5 must reject deprecated sampling params")
	}
	if !RejectsSamplingParams("claude-opus-5-0") {
		t.Error("opus 5 must reject deprecated sampling params")
	}
	if RejectsSamplingParams("claude-opus-4-6") {
		t.Error("opus 4.6 still accepts temperature; must not be gated")
	}
	if RejectsSamplingParams("claude-haiku-4-5-20251001") {
		t.Error("haiku 4.5 still accepts temperature; must not be gated")
	}
	// Later Sonnet/Haiku are a different family and not (yet) gated by version.
	if RejectsSamplingParams("claude-sonnet-4-8") {
		t.Error("sonnet 4.8 is not Opus; must not be gated by the Opus contract")
	}
}

func TestIsSonnet5OrLater(t *testing.T) {
	for _, model := range []string{"claude-sonnet-5", "claude-sonnet-5-0", "claude-sonnet-6-1-20270101"} {
		if !IsSonnet5OrLater(model) {
			t.Errorf("IsSonnet5OrLater(%q) = false, want true", model)
		}
	}
	for _, model := range []string{"claude-sonnet-4-8", "claude-opus-5-0", "claude-haiku-5-0"} {
		if IsSonnet5OrLater(model) {
			t.Errorf("IsSonnet5OrLater(%q) = true, want false", model)
		}
	}
}

func TestIsOpus47OrLater(t *testing.T) {
	for _, m := range []string{"claude-opus-4-7", "claude-opus-4-7-20260416", "claude-opus-4-8", "claude-opus-4-9-20270101", "claude-opus-5-0"} {
		if !IsOpus47OrLater(m) {
			t.Errorf("IsOpus47OrLater(%q) = false, want true", m)
		}
	}
	for _, m := range []string{"claude-opus-4-6", "claude-opus-4-5-20251101", "claude-sonnet-4-8", "claude-haiku-4-5-20251001"} {
		if IsOpus47OrLater(m) {
			t.Errorf("IsOpus47OrLater(%q) = true, want false", m)
		}
	}
}

func TestSupportsXHighEffort(t *testing.T) {
	if !SupportsXHighEffort("claude-opus-4-7") {
		t.Error("opus 4.7 must support xhigh effort")
	}
	if SupportsXHighEffort("claude-opus-4-6") {
		t.Error("opus 4.6 must not advertise xhigh effort")
	}
	if SupportsXHighEffort("claude-sonnet-4-5-20250929") {
		t.Error("sonnet 4.5 must not advertise xhigh effort")
	}
}

func TestGetRecommendedThinkingMode(t *testing.T) {
	tests := []struct {
		name     string
		model    string
		expected string
	}{
		{
			name:     "opus 4.6 recommends adaptive",
			model:    "claude-opus-4-6",
			expected: "adaptive",
		},
		{
			name:     "opus 4.5 recommends enabled (manual)",
			model:    "claude-opus-4-5-20251101",
			expected: "enabled",
		},
		{
			name:     "sonnet 4.5 recommends enabled (manual)",
			model:    "claude-sonnet-4-5-20250929",
			expected: "enabled",
		},
		{
			name:     "haiku recommends enabled (manual)",
			model:    "claude-haiku-4-5-20251001",
			expected: "enabled",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := GetRecommendedThinkingMode(tt.model)
			if result != tt.expected {
				t.Errorf("GetRecommendedThinkingMode(%q) = %v, want %v",
					tt.model, result, tt.expected)
			}
		})
	}
}
