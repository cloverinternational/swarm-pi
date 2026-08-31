package mcp

import "testing"

func TestMergeServerAppliesPatch(t *testing.T) {
	base := ServerConfig{
		Name:       "alpha",
		Type:       "stdio",
		Enabled:    false,
		Command:    "node",
		Args:       []string{"server.js"},
		Env:        map[string]string{"LOG_LEVEL": "info"},
		SecretEnv:  []string{"API_KEY"},
		Headers:    map[string]string{"User-Agent": "SwarmOS"},
		TimeoutSec: 30,
		Retries:    2,
		Tools:      &ToolsConfig{Mode: "blocklist", Disabled: []string{"danger"}},
	}
	overrideArgs := []string{"-y", "server.js"}
	overrideEnv := map[string]string{"DEBUG": "1"}
	overrideHeaders := map[string]string{"User-Agent": "Override"}
	overrideSecretEnv := []string{"NEW_KEY"}
	overrideTimeout := 10
	overrideRetries := 5
	overrideTools := &ToolsConfig{Mode: "allowlist", Enabled: []string{"safe"}}
	patch := ServerPatch{
		Enabled:    new(true),
		Args:       &overrideArgs,
		Env:        &overrideEnv,
		SecretEnv:  &overrideSecretEnv,
		Headers:    &overrideHeaders,
		TimeoutSec: &overrideTimeout,
		Retries:    &overrideRetries,
		Tools:      overrideTools,
	}

	merged := MergeServer(base, patch)

	if !merged.Enabled {
		t.Fatalf("expected enabled override")
	}
	if merged.Command != "node" {
		t.Fatalf("expected command to remain unchanged")
	}
	if len(merged.Args) != 2 || merged.Args[0] != "-y" {
		t.Fatalf("expected args override")
	}
	if merged.Env["DEBUG"] != "1" {
		t.Fatalf("expected env override")
	}
	if len(merged.SecretEnv) != 1 || merged.SecretEnv[0] != "NEW_KEY" {
		t.Fatalf("expected secret_env override")
	}
	if merged.Headers["User-Agent"] != "Override" {
		t.Fatalf("expected headers override")
	}
	if merged.TimeoutSec != 10 || merged.Retries != 5 {
		t.Fatalf("expected numeric overrides")
	}
	if merged.Tools == nil || merged.Tools.Mode != "allowlist" {
		t.Fatalf("expected tools override")
	}
	if base.Args[0] != "server.js" || base.Env["LOG_LEVEL"] != "info" {
		t.Fatalf("base should remain unchanged")
	}
}
