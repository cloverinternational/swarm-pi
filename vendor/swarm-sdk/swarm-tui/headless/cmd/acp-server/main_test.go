package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-core/core"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
	chathooks "github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/hooks"
)

func TestRegisterCoreToolsIncludesWebFetch(t *testing.T) {
	registry := tools.NewRegistry()
	registerCoreTools(
		registry,
		t.TempDir(),
		observability.NewNopLogger(),
		observability.NewNoopTracer(),
		func(string, ...any) {},
		nil,
		"",
	)
	if !registry.IsRegistered("web_fetch") {
		t.Fatal("headless core tool catalog does not contain web_fetch")
	}
}

func TestRegisterOpenAICompatibleProvidersRejectsInvalidBaseURL(t *testing.T) {
	registry := provider.NewSimpleRegistry(nil)
	providers := []core.ProviderConfig{
		{
			Name:    "bad",
			APIType: "openai-compatible",
			BaseURL: "http://example.com",
		},
	}

	registerOpenAICompatibleProviders(
		registry,
		providers,
		"",
		observability.NewNopLogger(),
		observability.NewNoopTracer(),
		func(string, ...any) {},
	)

	if registry.IsRegistered("bad") {
		t.Fatal("expected invalid provider base URL to skip registration")
	}
}

func TestRegisterOpenAICompatibleProvidersValidatesConfigBaseURL(t *testing.T) {
	registry := provider.NewSimpleRegistry(nil)
	providers := []core.ProviderConfig{
		{
			Name:    "custom",
			APIType: "openai-compatible",
			BaseURL: "https://example.com/v1",
		},
	}

	registerOpenAICompatibleProviders(
		registry,
		providers,
		"",
		observability.NewNopLogger(),
		observability.NewNoopTracer(),
		func(string, ...any) {},
	)

	if !registry.IsRegistered("custom") {
		t.Fatal("expected provider to be registered")
	}

	_, err := registry.Create(provider.Config{
		Name:    "custom",
		APIKey:  "test",
		BaseURL: "http://example.com",
	})
	if err == nil {
		t.Fatal("expected invalid config base URL to error")
	}
}

func TestRefreshHooksLogsReadErrors(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	workspace := filepath.Join(tmp, "workspace")
	globalDir := filepath.Join(home, ".swarmos")
	projectDir := filepath.Join(workspace, ".swarmos")

	if err := os.MkdirAll(globalDir, 0o700); err != nil {
		t.Fatalf("mkdir global: %v", err)
	}
	if err := os.MkdirAll(projectDir, 0o700); err != nil {
		t.Fatalf("mkdir project: %v", err)
	}

	if err := os.WriteFile(filepath.Join(globalDir, "hooks.json"), []byte("not json"), 0o600); err != nil {
		t.Fatalf("write global hooks: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "hooks.json"), []byte("not json"), 0o600); err != nil {
		t.Fatalf("write project hooks: %v", err)
	}

	t.Setenv("HOME", home)

	hm := chathooks.NewHooksManager(nil, nil, workspace)
	var logs []string
	logDebug := func(format string, args ...any) {
		logs = append(logs, fmt.Sprintf(format, args...))
	}

	if err := refreshHooks(hm, workspace, "", logDebug); err != nil {
		t.Fatalf("refreshHooks: %v", err)
	}

	var globalLogged bool
	var projectLogged bool
	for _, entry := range logs {
		if strings.Contains(entry, "failed to read global hooks") {
			globalLogged = true
		}
		if strings.Contains(entry, "failed to read project hooks") {
			projectLogged = true
		}
	}

	if !globalLogged {
		t.Fatal("expected global hooks read warning")
	}
	if !projectLogged {
		t.Fatal("expected project hooks read warning")
	}
}
