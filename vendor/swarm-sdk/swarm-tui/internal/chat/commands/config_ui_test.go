package commands

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// emit runs a command's Execute and returns the OpenConfigUIMsg it produced.
func emit(t *testing.T, c Command, args ...string) OpenConfigUIMsg {
	t.Helper()
	if c.IsInteractive() {
		t.Fatalf("%s: config commands must be non-interactive", c.Name())
	}
	cmd := c.Execute(args)
	if cmd == nil {
		t.Fatalf("%s: Execute returned nil tea.Cmd", c.Name())
	}
	msg := cmd()
	m, ok := msg.(OpenConfigUIMsg)
	if !ok {
		t.Fatalf("%s: expected OpenConfigUIMsg, got %T", c.Name(), msg)
	}
	return m
}

func TestConfigUICommandsEmitCorrectTarget(t *testing.T) {
	cases := []struct {
		newCmd func() Command
		target string
	}{
		{NewProfileSwitchCommand, "profile"},
		{NewAgentsCommand, "agents"},
		{NewPromptCommand, "prompt"},
		{NewCompactionCommand, "compaction"},
		{NewProvidersCommand, "providers"},
	}
	for _, tc := range cases {
		c := tc.newCmd()
		if got := emit(t, c); got.Target != tc.target || got.Arg != "" {
			t.Errorf("%s: bare emit = %+v, want target=%q arg=\"\"", c.Name(), got, tc.target)
		}
		// Direct-arg fast path carries the joined args.
		if got := emit(t, c, "alpha", "beta"); got.Target != tc.target || got.Arg != "alpha beta" {
			t.Errorf("%s: arg emit = %+v, want target=%q arg=%q", c.Name(), got, tc.target, "alpha beta")
		}
	}
}

func TestConfigUICommandsRegistered(t *testing.T) {
	reg := InitRegistry(nil)

	// New config command names + aliases resolve.
	for _, name := range []string{
		"profile", "agents", "subagents", "prompt", "systemprompt",
		"compaction", "providers", "provider",
	} {
		if _, ok := reg.Get(name); !ok {
			t.Errorf("command /%s not registered", name)
		}
	}

	// /profile is the model-profile switcher, NOT the pprof profiler.
	profile, ok := reg.Get("profile")
	if !ok {
		t.Fatal("/profile missing")
	}
	if profile.IsInteractive() {
		t.Errorf("/profile should be the non-interactive switcher command")
	}
	if _, isProfiler := profile.(*ProfileCommand); isProfiler {
		t.Errorf("/profile still resolves to the pprof ProfileCommand; rename not applied")
	}

	// The pprof profiler moved to /pprof (with prof/perf aliases).
	for _, name := range []string{"pprof", "prof", "perf"} {
		c, ok := reg.Get(name)
		if !ok {
			t.Errorf("pprof command not reachable via /%s", name)
			continue
		}
		if !strings.Contains(strings.ToLower(c.Description()), "profiling") {
			t.Errorf("/%s did not resolve to the profiler (desc=%q)", name, c.Description())
		}
	}
}

// compile-time guards: constructors satisfy the Command interface.
var (
	_ Command = (*configUICommand)(nil)
	_ tea.Cmd = (&configUICommand{}).Execute(nil)
)
