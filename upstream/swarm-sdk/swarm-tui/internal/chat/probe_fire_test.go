package chat

import (
	"os"
	"testing"

	sdkclient "github.com/Swarm-Code/mono/swarm-sdk/client"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/paths"
)

// TestProbe_FireProviderEndToEnd reproduces the exact failure path the user
// reported:
//
//	SDK unavailable: NewSDKIntegration: CONTRACT VIOLATION: Unknown provider: "fire"
//
// The flow:
//
//  1. The active agent profile names
//     provider="fire" — a custom name pointing at Fireworks via api_type
//     "openai-compatible".
//  2. NewSDKIntegrationWithOptions calls sdkclient.ParseProvider("fire"),
//     which fails because "fire" isn't in the SDK's hardcoded validProviders
//     map.
//  3. The tolerance branch then calls getProviderConfigFromFile("fire") to
//     check whether a custom provider with this name exists. Pre-fix that
//     lookup ignored the canonical paths.ProvidersFile() store and instead
//     consulted hardcoded legacy home-directory paths, so "fire" was reported
//     as not-found and the contract violation surfaced to the UI.
//
// This test asserts the post-fix behaviour: the TUI lookup must find "fire"
// (with api_type set) and the SDK's compatibility registry must agree.
func TestProbe_FireProviderEndToEnd(t *testing.T) {
	t.Setenv("SWARM_HOME", t.TempDir())
	if err := os.WriteFile(paths.ProvidersFile(), []byte(`[
		{"name":"fire","display_name":"Fireworks","base_url":"https://api.fireworks.ai/inference/v1","api_type":"openai-compatible"}
	]`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := getProviderConfigFromFile("fire")
	if err != nil {
		t.Fatalf("load fixture provider: %v", err)
	}
	if cfg == nil || cfg.APIType == "" {
		t.Fatalf("getProviderConfigFromFile(\"fire\") returned cfg=%+v — APIType must be set so the TUI tolerance branch lets the SDK init proceed", cfg)
	}
	t.Logf("TUI resolved 'fire' → api_type=%q base_url=%q", cfg.APIType, cfg.BaseURL)

	// ParseProvider must accept the custom name now (the sac side originally
	// failed here when generating commit messages: "Failed to generate
	// commit message: CONTRACT VIOLATION: Unknown provider: 'fire'"). The
	// returned Provider preserves the original custom name so downstream
	// credential lookup pulls the per-name api_key/base_url.
	p, parseErr := sdkclient.ParseProvider("fire")
	if parseErr != nil {
		t.Errorf("ParseProvider(\"fire\") must succeed for custom providers; got %v", parseErr)
	}
	if string(p) != "fire" {
		t.Errorf("ParseProvider should preserve the custom name; got %q", p)
	}
}
