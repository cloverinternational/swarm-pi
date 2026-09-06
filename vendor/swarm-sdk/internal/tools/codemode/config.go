package codemode

import (
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/codemode/sandbox"
)

// Config holds configuration for code mode that can be embedded in agent config.
// It provides a serializable configuration structure that can be used at the
// agent construction level, separate from the runtime CodeMode instance.
//
// Usage:
//
//	cfg := agent.Config{
//		Provider: p,
//		Model:    "claude-sonnet-4-5",
//		CodeMode: &codemode.Config{
//			Enabled:  true,
//			Timeout:  30 * time.Second,
//			Persist:  true,
//		},
//	}
type Config struct {
	// Enabled controls whether code mode is installed.
	// When false (default), code mode is not installed and all tools remain
	// as individual tools in the registry.
	// When true, selected tools are wrapped inside a run_code tool.
	Enabled bool `json:"enabled" yaml:"enabled"`

	// Selector determines which tools are sandboxed inside run_code.
	// If nil, defaults to AllTools{} (all non-permissioned tools).
	Selector Selector `json:"-" yaml:"-"` // Cannot serialize interface

	// ToolNames is a convenience list of tool names for ByNames selector.
	// If non-empty, this takes precedence over Selector field.
	// Use this for simple configuration without constructing a Selector.
	ToolNames []string `json:"tool_names,omitempty" yaml:"tool_names,omitempty"`

	// Timeout is the maximum execution time per run_code call.
	// Default is 60 seconds.
	Timeout time.Duration `json:"timeout,omitempty" yaml:"timeout,omitempty"`

	// MaxRetries is the maximum number of retries on syntax errors.
	// Default is 3.
	MaxRetries int `json:"max_retries,omitempty" yaml:"max_retries,omitempty"`

	// Persist determines whether REPL state persists across calls.
	// Default is true.
	Persist bool `json:"persist,omitempty" yaml:"persist,omitempty"`

	// SandboxFactory creates new sandboxes for each conversation.
	// If nil, defaults to GojaSandbox (pure-Go JavaScript VM).
	SandboxFactory sandbox.Factory `json:"-" yaml:"-"` // Cannot serialize function
}

// DefaultConfig returns the default configuration for code mode.
// Enabled is false by default for backward compatibility.
func DefaultConfig() *Config {
	return &Config{
		Enabled:    false,
		Selector:   AllTools{},
		Timeout:    60 * time.Second,
		MaxRetries: 3,
		Persist:    true,
	}
}

// ToOptions converts Config to functional options for codemode.New().
func (c *Config) ToOptions() []Option {
	if c == nil {
		return nil
	}

	opts := []Option{}

	// Handle selector
	if len(c.ToolNames) > 0 {
		opts = append(opts, WithSelector(NewByNames(c.ToolNames...)))
	} else if c.Selector != nil {
		opts = append(opts, WithSelector(c.Selector))
	}
	// If neither is set, New() will use default AllTools{}

	if c.Timeout > 0 {
		opts = append(opts, WithTimeout(c.Timeout))
	}
	if c.MaxRetries > 0 {
		opts = append(opts, WithMaxRetries(c.MaxRetries))
	}
	// Persist defaults to true, only set if explicitly false
	if !c.Persist {
		opts = append(opts, WithPersist(false))
	}
	if c.SandboxFactory != nil {
		opts = append(opts, WithSandboxFactory(c.SandboxFactory))
	}

	return opts
}

// NewFromConfig creates a CodeMode instance from Config.
func NewFromConfig(cfg *Config) *CodeMode {
	if cfg == nil {
		return New()
	}
	return New(cfg.ToOptions()...)
}

// Clone creates a deep copy of the config.
func (c *Config) Clone() *Config {
	if c == nil {
		return nil
	}
	clone := &Config{
		Enabled:    c.Enabled,
		Selector:   c.Selector, // Shallow copy (interface)
		Timeout:    c.Timeout,
		MaxRetries: c.MaxRetries,
		Persist:    c.Persist,
	}
	if c.ToolNames != nil {
		clone.ToolNames = make([]string, len(c.ToolNames))
		copy(clone.ToolNames, c.ToolNames)
	}
	return clone
}
