package commands

import (
	"testing"
)

func TestShouldAutoConnect(t *testing.T) {
	falseVal := false
	trueVal := true

	tests := []struct {
		name     string
		config   *MCPServerConfig
		expected bool
	}{
		{
			name: "Enabled stdio server should auto-connect",
			config: &MCPServerConfig{
				Name:    "test1",
				Type:    MCPTypeStdio,
				Enabled: true,
			},
			expected: true,
		},
		{
			name: "Disabled stdio server should NOT auto-connect",
			config: &MCPServerConfig{
				Name:    "test2",
				Type:    MCPTypeStdio,
				Enabled: false,
			},
			expected: false,
		},
		{
			name: "Enabled stdio with explicit AutoConnect=false should NOT auto-connect",
			config: &MCPServerConfig{
				Name:        "test3",
				Type:        MCPTypeStdio,
				Enabled:     true,
				AutoConnect: &falseVal,
			},
			expected: false,
		},
		{
			name: "Enabled HTTP server should auto-connect",
			config: &MCPServerConfig{
				Name:    "test4",
				Type:    MCPTypeHTTP,
				Enabled: true,
			},
			expected: true,
		},
		{
			name: "Disabled HTTP server should auto-connect by transport default",
			config: &MCPServerConfig{
				Name:    "test5",
				Type:    MCPTypeHTTP,
				Enabled: false,
			},
			expected: true, // HTTP defaults to true
		},
		{
			name: "Enabled stdio with explicit AutoConnect=true should auto-connect",
			config: &MCPServerConfig{
				Name:        "test6",
				Type:        MCPTypeStdio,
				Enabled:     true,
				AutoConnect: &trueVal,
			},
			expected: true,
		},
		{
			name: "Default type (stdio) enabled should auto-connect",
			config: &MCPServerConfig{
				Name:    "test7",
				Enabled: true, // Type not set, defaults to stdio
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.config.ShouldAutoConnect()
			if result != tt.expected {
				t.Errorf("ShouldAutoConnect() = %v, expected %v", result, tt.expected)
			}
		})
	}
}
