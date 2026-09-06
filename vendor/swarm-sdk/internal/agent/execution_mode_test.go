package agent

import (
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider/mock"
)

func TestDefinition_ExecutionMode(t *testing.T) {
	tests := []struct {
		name          string
		mode          ExecutionMode
		managedConfig *ManagedConfig
		wantIsManaged bool
		wantIsLocal   bool
		wantIsHybrid  bool
	}{
		{
			name:          "empty defaults to local",
			mode:          "",
			wantIsManaged: false,
			wantIsLocal:   true,
			wantIsHybrid:  false,
		},
		{
			name:          "local mode",
			mode:          ExecutionModeLocal,
			wantIsManaged: false,
			wantIsLocal:   true,
			wantIsHybrid:  false,
		},
		{
			name: "managed mode",
			mode: ExecutionModeManaged,
			managedConfig: &ManagedConfig{
				Endpoint:  "https://cloud.swarmcode.ai",
				APIKey:    "test-key",
				ProjectID: "test-project",
			},
			wantIsManaged: true,
			wantIsLocal:   false,
			wantIsHybrid:  false,
		},
		{
			name: "hybrid mode",
			mode: ExecutionModeHybrid,
			managedConfig: &ManagedConfig{
				Endpoint:  "https://cloud.swarmcode.ai",
				ProjectID: "test-project",
			},
			wantIsManaged: true, // Hybrid with managed config is considered managed
			wantIsLocal:   false,
			wantIsHybrid:  true,
		},
		{
			name:          "hybrid mode without managed config",
			mode:          ExecutionModeHybrid,
			wantIsManaged: false, // No managed config, so not managed
			wantIsLocal:   false,
			wantIsHybrid:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &Definition{
				ID:            "test-agent",
				Provider:      "anthropic",
				Model:         "claude-sonnet-4-5",
				ExecutionMode: tt.mode,
				ManagedConfig: tt.managedConfig,
			}

			if got := d.IsManaged(); got != tt.wantIsManaged {
				t.Errorf("IsManaged() = %v, want %v", got, tt.wantIsManaged)
			}
			if got := d.IsLocal(); got != tt.wantIsLocal {
				t.Errorf("IsLocal() = %v, want %v", got, tt.wantIsLocal)
			}
			if got := d.IsHybrid(); got != tt.wantIsHybrid {
				t.Errorf("IsHybrid() = %v, want %v", got, tt.wantIsHybrid)
			}
		})
	}
}

func TestDefinition_GetExecutionMode(t *testing.T) {
	tests := []struct {
		name string
		mode ExecutionMode
		want ExecutionMode
	}{
		{
			name: "empty defaults to local",
			mode: "",
			want: ExecutionModeLocal,
		},
		{
			name: "local mode",
			mode: ExecutionModeLocal,
			want: ExecutionModeLocal,
		},
		{
			name: "managed mode",
			mode: ExecutionModeManaged,
			want: ExecutionModeManaged,
		},
		{
			name: "hybrid mode",
			mode: ExecutionModeHybrid,
			want: ExecutionModeHybrid,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &Definition{
				ExecutionMode: tt.mode,
			}
			if got := d.GetExecutionMode(); got != tt.want {
				t.Errorf("GetExecutionMode() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDefinition_Clone_WithExecutionMode(t *testing.T) {
	original := &Definition{
		ID:            "test-agent",
		Name:          "Test Agent",
		Provider:      "anthropic",
		Model:         "claude-sonnet-4-5",
		ExecutionMode: ExecutionModeManaged,
		ManagedConfig: &ManagedConfig{
			Endpoint:   "https://cloud.swarmcode.ai",
			ProjectID:  "test-project",
			SessionID:  "test-session",
			Timeout:    5 * time.Minute,
			MaxRetries: 3,
			EnableSSE:  true,
			// APIKey intentionally NOT copied for security
		},
	}

	clone := original.Clone()

	// Verify clone has same execution mode
	if clone.ExecutionMode != original.ExecutionMode {
		t.Errorf("clone.ExecutionMode = %v, want %v", clone.ExecutionMode, original.ExecutionMode)
	}

	// Verify managed config was copied
	if clone.ManagedConfig == nil {
		t.Fatal("clone.ManagedConfig is nil")
	}

	if clone.ManagedConfig.Endpoint != original.ManagedConfig.Endpoint {
		t.Errorf("clone.ManagedConfig.Endpoint = %v, want %v", clone.ManagedConfig.Endpoint, original.ManagedConfig.Endpoint)
	}

	if clone.ManagedConfig.ProjectID != original.ManagedConfig.ProjectID {
		t.Errorf("clone.ManagedConfig.ProjectID = %v, want %v", clone.ManagedConfig.ProjectID, original.ManagedConfig.ProjectID)
	}

	// Verify APIKey is NOT copied for security
	if original.ManagedConfig.APIKey != "" && clone.ManagedConfig.APIKey != "" {
		t.Error("clone.ManagedConfig.APIKey should be empty for security")
	}

	// Verify it's a deep copy
	clone.ManagedConfig.Endpoint = "https://modified.example.com"
	if original.ManagedConfig.Endpoint == clone.ManagedConfig.Endpoint {
		t.Error("modifying clone affected original")
	}
}

func TestBuilder_Managed(t *testing.T) {
	p := mock.NewProvider()

	ag, err := Build().
		Provider(p).
		Model("claude-sonnet-4-5").
		ID("test-managed-agent").
		Managed("https://cloud.swarmcode.ai", "test-api-key", "test-project").
		Create()

	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}

	// Verify execution mode
	if ag.Definition().ExecutionMode != ExecutionModeManaged {
		t.Errorf("ExecutionMode = %v, want %v", ag.Definition().ExecutionMode, ExecutionModeManaged)
	}

	// Verify managed config
	if ag.Definition().ManagedConfig == nil {
		t.Fatal("ManagedConfig is nil")
	}

	if ag.Definition().ManagedConfig.Endpoint != "https://cloud.swarmcode.ai" {
		t.Errorf("Endpoint = %v, want https://cloud.swarmcode.ai", ag.Definition().ManagedConfig.Endpoint)
	}

	// Note: APIKey is intentionally NOT copied in Definition() for security.
	// The APIKey is stored internally but not exposed through Definition().
	// This is by design to prevent accidental exposure of secrets.

	if ag.Definition().ManagedConfig.ProjectID != "test-project" {
		t.Errorf("ProjectID = %v, want test-project", ag.Definition().ManagedConfig.ProjectID)
	}
}

func TestBuilder_Hybrid(t *testing.T) {
	p := mock.NewProvider()

	ag, err := Build().
		Provider(p).
		Model("claude-sonnet-4-5").
		ID("test-hybrid-agent").
		Hybrid("https://cloud.swarmcode.ai", "test-api-key", "test-project").
		Create()

	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}

	// Verify execution mode
	if ag.Definition().ExecutionMode != ExecutionModeHybrid {
		t.Errorf("ExecutionMode = %v, want %v", ag.Definition().ExecutionMode, ExecutionModeHybrid)
	}

	// Verify IsHybrid returns true
	if !ag.Definition().IsHybrid() {
		t.Error("IsHybrid() = false, want true")
	}
}

func TestBuilder_ManagedWithConfig(t *testing.T) {
	p := mock.NewProvider()

	cfg := &ManagedConfig{
		Endpoint:   "https://cloud.swarmcode.ai",
		APIKey:     "test-api-key",
		ProjectID:  "test-project",
		Timeout:    10 * time.Minute,
		MaxRetries: 5,
		EnableSSE:  true,
	}

	ag, err := Build().
		Provider(p).
		Model("claude-sonnet-4-5").
		ID("test-managed-config-agent").
		ManagedWithConfig(cfg).
		Create()

	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}

	// Verify all config values were copied
	if ag.Definition().ManagedConfig.Timeout != cfg.Timeout {
		t.Errorf("Timeout = %v, want %v", ag.Definition().ManagedConfig.Timeout, cfg.Timeout)
	}

	if ag.Definition().ManagedConfig.MaxRetries != cfg.MaxRetries {
		t.Errorf("MaxRetries = %v, want %v", ag.Definition().ManagedConfig.MaxRetries, cfg.MaxRetries)
	}

	if ag.Definition().ManagedConfig.EnableSSE != cfg.EnableSSE {
		t.Errorf("EnableSSE = %v, want %v", ag.Definition().ManagedConfig.EnableSSE, cfg.EnableSSE)
	}
}

func TestBuilder_ExecutionMode(t *testing.T) {
	p := mock.NewProvider()

	ag, err := Build().
		Provider(p).
		Model("claude-sonnet-4-5").
		ID("test-mode-agent").
		ExecutionMode(ExecutionModeLocal).
		Create()

	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}

	if ag.Definition().ExecutionMode != ExecutionModeLocal {
		t.Errorf("ExecutionMode = %v, want %v", ag.Definition().ExecutionMode, ExecutionModeLocal)
	}

	if !ag.Definition().IsLocal() {
		t.Error("IsLocal() = false, want true")
	}
}
