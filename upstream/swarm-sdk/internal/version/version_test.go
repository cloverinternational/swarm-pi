package version

import (
	"strings"
	"testing"
)

func TestInfo(t *testing.T) {
	info := Info()

	// Check that Info contains expected components
	if !strings.Contains(info, "SwarmOS SDK") {
		t.Error("Info should contain 'SwarmOS SDK'")
	}
	if !strings.Contains(info, Version) {
		t.Errorf("Info should contain version %s", Version)
	}
	if !strings.Contains(info, APIVersion) {
		t.Errorf("Info should contain API version %s", APIVersion)
	}
}

func TestUserAgent(t *testing.T) {
	ua := UserAgent()

	// Check that UserAgent has expected format
	if !strings.HasPrefix(ua, "SwarmOS-SDK/") {
		t.Error("UserAgent should start with 'SwarmOS-SDK/'")
	}
	if !strings.Contains(ua, Version) {
		t.Errorf("UserAgent should contain version %s", Version)
	}
}

func TestIsCompatibleAPI(t *testing.T) {
	tests := []struct {
		apiVersion string
		want       bool
	}{
		{APIVersion, true},
		{"v1", true},
		{"v2", false},
		{"", false},
		{"v0", false},
	}

	for _, tt := range tests {
		got := IsCompatibleAPI(tt.apiVersion)
		if got != tt.want {
			t.Errorf("IsCompatibleAPI(%q) = %v, want %v", tt.apiVersion, got, tt.want)
		}
	}
}
