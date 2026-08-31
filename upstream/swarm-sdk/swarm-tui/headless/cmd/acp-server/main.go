// Package main provides an Agent Client Protocol (ACP) server for Swarm.
// It exposes the Swarm headless engine over the ACP protocol, making Swarm
// available as a coding agent in editors like Zed.
//
// Transport: newline-delimited JSON-RPC 2.0 over stdin/stdout (like LSP).
//
// Usage:
//
//	acp-server [--config <dir>] [--debug]
//
// The editor starts this binary and communicates via its stdin/stdout.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/Swarm-Code/mono/swarm-core/core"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/mcp"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/headless/acp"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/headless/approval"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/headless/profile"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/headless/sdk"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/bgprocess"
	chatpkg "github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat"
	chatcontext "github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/context"
	chathooks "github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/hooks"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation/manager"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation/storage"
	sdkhooks "github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider/anthropic"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider/gemini"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider/openai"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/builtin"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/forge"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/ii"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/web_fetch"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/websearch"
)

func main() {
	configDir := flag.String("config", "", "Configuration directory (default: ~/.swarmos)")
	debug := flag.Bool("debug", false, "Enable debug logging to stderr")
	flag.Parse()

	if *configDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			fmt.Fprintf(os.Stderr, "acp-server: failed to get home directory: %v\n", err)
			os.Exit(1)
		}
		*configDir = filepath.Join(home, ".swarmos")
	}

	logDebug := func(format string, args ...any) {
		if *debug {
			fmt.Fprintf(os.Stderr, "[acp-server] "+format+"\n", args...)
		}
	}
	logDebug("starting, config dir: %s", *configDir)

	// ── Config ────────────────────────────────────────────────────────────────
	fs := &nativeFS{}
	baseConfig := core.NewFileConfigManager(*configDir, fs)
	if err := baseConfig.Load(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "acp-server: warning: failed to load config: %v\n", err)
	}
	configManager := core.ConfigManager(core.NewPolicyConfigManager(baseConfig))

	providerConfigs := loadProviderConfigs(configManager)
	providerConfigIndex := indexProviderConfigs(providerConfigs)
	providerCfg := loadProviderConfig(*configDir, configManager)
	logDebug("provider=%s model=%s", providerCfg.Name, providerCfg.Model)

	// ── Observability ─────────────────────────────────────────────────────────
	logger := observability.NewNopLogger()
	tracer := &nopTracer{}

	// ── Credentials ───────────────────────────────────────────────────────────
	if _, err := getProviderAuth(providerCfg.Name, *configDir, providerConfigIndex, logDebug); err != nil {
		logDebug("warning: no credentials for %s: %v", providerCfg.Name, err)
	}

	// ── Provider registry ─────────────────────────────────────────────────────
	providerRegistry := provider.NewSimpleRegistry(logger)
	providerRegistry.Register("anthropic", func(cfg provider.Config) (provider.Provider, error) {
		isOAuth := false
		if cfg.Custom != nil {
			if v, ok := cfg.Custom["isOAuth"].(bool); ok {
				isOAuth = v
			}
		}
		return anthropic.New(anthropic.Config{
			APIKey:       cfg.APIKey,
			BaseURL:      cfg.BaseURL,
			DefaultModel: cfg.Model,
			IsOAuth:      isOAuth,
			Logger:       logger,
			Tracer:       tracer,
		})
	})
	providerRegistry.Register("claudecode", func(cfg provider.Config) (provider.Provider, error) {
		isOAuth := false
		if cfg.Custom != nil {
			if v, ok := cfg.Custom["isOAuth"].(bool); ok {
				isOAuth = v
			}
		}
		return anthropic.New(anthropic.Config{
			APIKey:       cfg.APIKey,
			BaseURL:      cfg.BaseURL,
			DefaultModel: cfg.Model,
			IsOAuth:      isOAuth,
			Logger:       logger,
			Tracer:       tracer,
		})
	})
	providerRegistry.Register("openai", func(cfg provider.Config) (provider.Provider, error) {
		return openai.New(openai.Config{
			APIKey:  cfg.APIKey,
			BaseURL: cfg.BaseURL,
			Logger:  logger,
			Tracer:  tracer,
		})
	})
	providerRegistry.Register("gemini", func(cfg provider.Config) (provider.Provider, error) {
		isOAuth := false
		if cfg.Custom != nil {
			if v, ok := cfg.Custom["isOAuth"].(bool); ok {
				isOAuth = v
			}
		}
		gcfg := gemini.Config{Model: cfg.Model, Timeout: 300, Logger: logger, Tracer: tracer}
		if isOAuth {
			gcfg.AuthMode = gemini.AuthModeOAuth
		} else {
			gcfg.AuthMode = gemini.AuthModeAPIKey
			gcfg.APIKey = cfg.APIKey
		}
		return gemini.New(gcfg)
	})
	providerRegistry.Register("cerebras", func(cfg provider.Config) (provider.Provider, error) {
		if cfg.BaseURL == "" {
			cfg.BaseURL = "https://api.cerebras.ai/v1"
		}
		return openai.New(openai.Config{APIKey: cfg.APIKey, BaseURL: cfg.BaseURL, Name: "cerebras", Logger: logger, Tracer: tracer})
	})
	providerRegistry.Register("openrouter", func(cfg provider.Config) (provider.Provider, error) {
		if cfg.BaseURL == "" {
			cfg.BaseURL = "https://openrouter.ai/api/v1"
		}
		return openai.New(openai.Config{APIKey: cfg.APIKey, BaseURL: cfg.BaseURL, Name: "openrouter", Logger: logger, Tracer: tracer})
	})
	providerRegistry.Register("fireworks", func(cfg provider.Config) (provider.Provider, error) {
		if cfg.BaseURL == "" {
			cfg.BaseURL = "https://api.fireworks.ai/inference/v1"
		}
		return openai.New(openai.Config{APIKey: cfg.APIKey, BaseURL: cfg.BaseURL, Name: "fireworks", Logger: logger, Tracer: tracer})
	})
	providerRegistry.Register("groq", func(cfg provider.Config) (provider.Provider, error) {
		if cfg.BaseURL == "" {
			cfg.BaseURL = "https://api.groq.com/openai/v1"
		}
		return openai.New(openai.Config{APIKey: cfg.APIKey, BaseURL: cfg.BaseURL, Name: "groq", Logger: logger, Tracer: tracer})
	})
	providerRegistry.Register("together", func(cfg provider.Config) (provider.Provider, error) {
		if cfg.BaseURL == "" {
			cfg.BaseURL = "https://api.together.xyz/v1"
		}
		return openai.New(openai.Config{APIKey: cfg.APIKey, BaseURL: cfg.BaseURL, Name: "together", Logger: logger, Tracer: tracer})
	})
	providerRegistry.RegisterAlias("together-ai", "together")
	providerRegistry.RegisterAlias("togetherai", "together")
	providerRegistry.Register("deepseek", func(cfg provider.Config) (provider.Provider, error) {
		if cfg.BaseURL == "" {
			cfg.BaseURL = "https://api.deepseek.com/v1"
		}
		return openai.New(openai.Config{APIKey: cfg.APIKey, BaseURL: cfg.BaseURL, Name: "deepseek", Logger: logger, Tracer: tracer})
	})
	providerRegistry.Register("perplexity", func(cfg provider.Config) (provider.Provider, error) {
		if cfg.BaseURL == "" {
			cfg.BaseURL = "https://api.perplexity.ai"
		}
		return openai.New(openai.Config{APIKey: cfg.APIKey, BaseURL: cfg.BaseURL, Name: "perplexity", Logger: logger, Tracer: tracer})
	})
	registerOpenAICompatibleProviders(providerRegistry, providerConfigs, *configDir, logger, tracer, logDebug)
	providerRegistry.RegisterAlias("google", "gemini")
	providerRegistry.RegisterAlias("Google", "gemini")

	// ── Workspace ─────────────────────────────────────────────────────────────
	workspaceRoot, err := os.Getwd()
	if err != nil {
		logDebug("warning: failed to get working directory: %v", err)
	}

	// ── Shell hooks ───────────────────────────────────────────────────────────
	hooksManager := chathooks.NewHooksManager(logger, tracer, workspaceRoot)
	if err := refreshHooks(hooksManager, workspaceRoot, *configDir, logDebug); err != nil {
		logDebug("warning: failed to initialise shell hooks: %v", err)
	}

	// ── Tool registry ─────────────────────────────────────────────────────────
	toolRegistry := tools.NewSimpleRegistry(logger, tracer)
	var exaAPIKey string
	for _, p := range configManager.GetProviders() {
		if p.APIType == "exa" && p.APIKey != "" {
			exaAPIKey = p.APIKey
			break
		}
	}
	wsCoreCfg := configManager.GetConfig().WebSearch
	registerCoreTools(toolRegistry, workspaceRoot, logger, tracer, logDebug, wsCoreCfg, exaAPIKey)

	// ── Approval broker ───────────────────────────────────────────────────────
	approvalBroker := approval.NewApprovalBroker(approval.DefaultConfig())
	toolsApprovalBroker := approval.NewToolsApprovalBroker(approvalBroker)
	if err := configureToolPermissions(toolRegistry, configManager, workspaceRoot, toolsApprovalBroker, logDebug); err != nil {
		logDebug("warning: failed to configure permissions: %v", err)
	}

	// ── MCP ───────────────────────────────────────────────────────────────────
	credentialStore := &mcp.ConfigManagerCredentialsStore{Manager: configManager}
	mcpConfigManager := mcp.NewConfigManager(configManager.ConfigDir(), workspaceRoot, nil, credentialStore)
	mcpRuntime := mcp.NewRuntimeManager(mcpConfigManager, toolRegistry, credentialStore, logger, tracer)

	// ── Context sources ───────────────────────────────────────────────────────
	contextLoader := chatcontext.NewFileLoaderWithConfigDir(configManager.ConfigDir(), workspaceRoot)
	contextOrchestrator := chatcontext.NewContextOrchestrator(contextLoader.GetConfig(), workspaceRoot, contextLoader, mcpRuntime, logger)

	// ── Profile store ─────────────────────────────────────────────────────────
	profileStore := profile.NewProfileStore(configManager.SharedConfigDir())

	// ── Agent factory ─────────────────────────────────────────────────────────
	agentFactory, err := agent.NewSimpleFactory(agent.FactoryConfig{
		ProviderRegistry: providerRegistry,
		Logger:           logger,
		Tracer:           tracer,
		Auditor:          &nopAuditor{},
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "acp-server: warning: agent factory unavailable: %v\n", err)
	}

	// ── Conversation storage ──────────────────────────────────────────────────
	convDir := filepath.Join(*configDir, "conversations")
	convStorage, err := storage.NewFileStorage(storage.FileStorageConfig{BaseDir: convDir})
	if err != nil {
		fmt.Fprintf(os.Stderr, "acp-server: warning: conversation storage unavailable: %v\n", err)
	}
	var convManager *manager.Manager
	if convStorage != nil {
		convManager, err = manager.NewManager(manager.Config{Storage: convStorage, Logger: logger, Tracer: tracer})
		if err != nil {
			fmt.Fprintf(os.Stderr, "acp-server: warning: conversation manager unavailable: %v\n", err)
		}
	}

	// ── Bridge factory ────────────────────────────────────────────────────────
	bridgeFactory := func(providerName, model string) (core.SDKBridge, error) {
		providerID := normalizeProviderName(providerName)
		if providerID == "" {
			return nil, fmt.Errorf("provider name required")
		}
		if agentFactory == nil {
			return nil, fmt.Errorf("agent factory not available")
		}
		currentProviders := loadProviderConfigs(configManager)
		currentIndex := indexProviderConfigs(currentProviders)
		registerOpenAICompatibleProviders(providerRegistry, currentProviders, *configDir, logger, tracer, logDebug)

		a, err := getProviderAuth(providerID, *configDir, currentIndex, logDebug)
		if err != nil {
			return nil, err
		}
		resolvedModel := strings.TrimSpace(model)
		if resolvedModel == "" {
			if cfg := configManager.GetConfig(); cfg != nil && cfg.DefaultModel != "" {
				resolvedModel = cfg.DefaultModel
			}
		}

		makeAgent := func(ctx context.Context, agentModel string, reg tools.Registry) (*agent.Agent, error) {
			if agentModel == "" {
				agentModel = resolvedModel
			}
			if !providerRegistry.IsRegistered(providerID) {
				registerCustomProvider(providerRegistry, providerID, a.baseURL, logger, tracer, logDebug)
			}
			prov, err := providerRegistry.Create(provider.Config{
				Name:    providerID,
				APIKey:  a.apiKey,
				BaseURL: a.baseURL,
				Model:   agentModel,
				Custom:  map[string]any{"isOAuth": a.isOAuth},
			})
			if err != nil {
				return nil, err
			}
			prov = chatpkg.NewContextInjectingProvider(prov, contextOrchestrator)
			def := &agent.Definition{
				ID:        "acp-worker",
				Name:      "Swarm ACP Agent",
				Provider:  providerID,
				Model:     agentModel,
				ToolHints: []string{"*"},
				Capabilities: &agent.Capabilities{
					MaxTurns:          20,
					Timeout:           300 * time.Second,
					Temperature:       0.7,
					SupportsTools:     true,
					SupportsStreaming: true,
				},
			}
			ag, err := agent.New(agent.Config{
				Definition:       def,
				Provider:         prov,
				ProviderRegistry: providerRegistry,
				ToolRegistry:     reg,
				Logger:           logger,
				Tracer:           tracer,
				Auditor:          &nopAuditor{},
			})
			if err != nil {
				return nil, err
			}
			ag.SetHooksManager(hooksManager)
			// Set ephemeral system prompt function for task nudges
			// This injects a hidden reminder when the agent has no active tasks
			ag.SetEphemeralSystemFn(ii.BuildTaskNudgeFn(ag.ToolCallsTotal))
			if err := ag.Initialize(); err != nil {
				return nil, err
			}
			return ag, nil
		}

		return sdk.NewBridgeFromConfig(sdk.BridgeConfig{
			AgentFactory: func(ctx context.Context, m string) (*agent.Agent, error) {
				return makeAgent(ctx, m, toolRegistry)
			},
			AgentFactoryWithTools: func(ctx context.Context, m string, reg tools.Registry) (*agent.Agent, error) {
				return makeAgent(ctx, m, reg)
			},
			ConversationManager: convManager,
			ConversationStorage: convStorage,
			ToolRegistry:        toolRegistry,
			ProfileStore:        profileStore,
			ConfigProvider:      configManager,
			DefaultProvider:     providerID,
			DefaultModel:        resolvedModel,
		})
	}

	// ── Initial bridge ────────────────────────────────────────────────────────
	var sdkBridge core.SDKBridge
	if b, err := bridgeFactory(providerCfg.Name, providerCfg.Model); err != nil {
		fmt.Fprintf(os.Stderr, "acp-server: warning: SDK bridge failed: %v\n", err)
	} else {
		sdkBridge = b
	}

	// ── Engine ────────────────────────────────────────────────────────────────
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	engine := core.NewEngine(core.EngineConfig{
		SDKBridge:       sdkBridge,
		Config:          configManager,
		InitialModel:    providerCfg.Model,
		InitialProvider: providerCfg.Name,
		OperatingMode:   "act",
	})
	engine.Start()

	if mcpRuntime != nil {
		if err := mcpRuntime.Start(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "acp-server: warning: MCP runtime start failed: %v\n", err)
		}
	}

	// ── ACP server ────────────────────────────────────────────────────────────
	acpServer := acp.NewServer(acp.ServerConfig{
		Engine:         engine,
		Config:         configManager,
		Broker:         approvalBroker,
		Reader:         os.Stdin,
		Writer:         os.Stdout,
		ToolNames:      registeredToolNames(toolRegistry),
		AgentOptions:   buildAgentOptions(profileStore),
		Providers:      providerNames(loadProviderConfigs(configManager)),
		ProviderModels: providerModelMap(loadProviderConfigs(configManager)),
		GoalHook:       hooksManager.GetGoalHook(),
	})
	logDebug("ACP server ready (pid=%d)", os.Getpid())

	// ── Signal handling ───────────────────────────────────────────────────────
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		acpServer.Stop()
		if mcpRuntime != nil {
			mcpRuntime.Stop()
		}
		engine.Stop()
		cancel()
	}()

	if err := acpServer.Start(ctx); err != nil && err != context.Canceled && err != io.EOF {
		fmt.Fprintf(os.Stderr, "acp-server: %v\n", err)
		os.Exit(1)
	}
}

// ── Engine setup helpers ──────────────────────────────────────────────────────
// These mirror the helpers in headless/cmd/ipc-server/main.go.
// TODO: extract to headless/enginesetup for deduplication.

type providerCfgSpec struct {
	Name  string
	Model string
}

func loadProviderConfig(configDir string, cfgMgr core.ConfigManager) providerCfgSpec {
	cfg := providerCfgSpec{Name: "anthropic", Model: "claude-sonnet-4-20250514"}
	if cfgMgr != nil {
		if mc := cfgMgr.GetConfig(); mc != nil {
			if mc.DefaultProvider != "" {
				cfg.Name = mc.DefaultProvider
			}
			if mc.DefaultModel != "" {
				cfg.Model = mc.DefaultModel
			}
		}
	}
	return cfg
}

func loadProviderConfigs(cfgMgr core.ConfigManager) []core.ProviderConfig {
	if cfgMgr == nil {
		return nil
	}
	return cfgMgr.GetProviders()
}

func indexProviderConfigs(providers []core.ProviderConfig) map[string]core.ProviderConfig {
	idx := make(map[string]core.ProviderConfig, len(providers))
	for _, p := range providers {
		key := strings.ToLower(strings.TrimSpace(p.Name))
		if key != "" {
			idx[key] = p
		}
	}
	return idx
}

func normalizeProviderName(name string) string {
	p := strings.ToLower(strings.TrimSpace(name))
	switch p {
	case "claudecode", "anthropic":
		return "anthropic"
	case "openai", "codex":
		return "openai"
	case "gemini", "google", "gemini-code-assist":
		return "gemini"
	case "cerebras":
		return "cerebras"
	case "openrouter":
		return "openrouter"
	case "":
		return "anthropic"
	default:
		return p
	}
}

type providerAuth struct {
	apiKey  string
	baseURL string
	isOAuth bool
}

func getProviderAuth(providerName, configDir string, idx map[string]core.ProviderConfig, logDebug func(string, ...any)) (providerAuth, error) {
	normalized := normalizeProviderName(providerName)
	var a providerAuth
	var err error
	switch normalized {
	case "anthropic":
		a, err = getAnthropicAuth(configDir, logDebug)
	case "openai":
		a, err = getOpenAIAuth(configDir, logDebug)
	case "gemini":
		a, err = getGeminiAuth(logDebug)
	default:
		a, err = getCustomAuth(providerName, configDir, logDebug)
	}
	if err != nil {
		return providerAuth{}, err
	}
	a.baseURL = resolveBaseURL(providerName, a.baseURL, configDir, idx, logDebug)
	return a, nil
}

func getAnthropicAuth(configDir string, logDebug func(string, ...any)) (providerAuth, error) {
	if k := os.Getenv("ANTHROPIC_API_KEY"); k != "" {
		return providerAuth{apiKey: k}, nil
	}
	if k := os.Getenv("CLAUDE_API_KEY"); k != "" {
		return providerAuth{apiKey: k}, nil
	}
	if tok, err := anthropic.GetStoredOAuthToken(); err == nil && tok != nil && tok.AccessToken != "" {
		logDebug("using Anthropic OAuth token")
		return providerAuth{apiKey: tok.AccessToken, isOAuth: true}, nil
	}
	if apiKey, baseURL := loadCredentials("anthropic", configDir); apiKey != "" {
		return providerAuth{apiKey: apiKey, baseURL: baseURL}, nil
	}
	return providerAuth{}, fmt.Errorf("no Anthropic credentials: set ANTHROPIC_API_KEY or run /auth")
}

func getOpenAIAuth(configDir string, logDebug func(string, ...any)) (providerAuth, error) {
	if k := os.Getenv("OPENAI_API_KEY"); k != "" {
		return providerAuth{apiKey: k}, nil
	}
	if tok, err := openai.GetStoredOAuthToken(); err == nil && tok != nil {
		key := tok.APIKey
		if key == "" {
			key = tok.AccessToken
		}
		if key != "" {
			return providerAuth{apiKey: key, isOAuth: true}, nil
		}
	}
	if apiKey, baseURL := loadCredentials("openai", configDir); apiKey != "" {
		return providerAuth{apiKey: apiKey, baseURL: baseURL}, nil
	}
	return providerAuth{}, fmt.Errorf("no OpenAI credentials: set OPENAI_API_KEY or run /auth openai")
}

func getGeminiAuth(logDebug func(string, ...any)) (providerAuth, error) {
	if k := os.Getenv("GEMINI_API_KEY"); k != "" {
		return providerAuth{apiKey: k}, nil
	}
	if k := os.Getenv("GOOGLE_API_KEY"); k != "" {
		return providerAuth{apiKey: k}, nil
	}
	if gemini.HasStoredOAuthToken() {
		return providerAuth{isOAuth: true}, nil
	}
	return providerAuth{}, fmt.Errorf("no Gemini credentials: set GEMINI_API_KEY or run /auth gemini")
}

func getCustomAuth(providerName, configDir string, logDebug func(string, ...any)) (providerAuth, error) {
	envKey := strings.ToUpper(strings.ReplaceAll(providerName, "-", "_")) + "_API_KEY"
	if k := os.Getenv(envKey); k != "" {
		return providerAuth{apiKey: k}, nil
	}
	if apiKey, baseURL := loadCredentials(providerName, configDir); apiKey != "" {
		return providerAuth{apiKey: apiKey, baseURL: baseURL}, nil
	}
	return providerAuth{}, fmt.Errorf("no credentials for %s: set %s", providerName, envKey)
}

func loadCredentials(providerName, configDir string) (apiKey, baseURL string) {
	data, err := os.ReadFile(filepath.Join(configDir, "credentials.json"))
	if err != nil {
		return "", ""
	}
	var creds struct {
		Providers map[string]struct {
			APIKey  string `json:"api_key"`
			BaseURL string `json:"base_url"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(data, &creds); err != nil {
		return "", ""
	}
	key := strings.ToLower(strings.TrimSpace(providerName))
	if c, ok := creds.Providers[key]; ok && c.APIKey != "" {
		return c.APIKey, c.BaseURL
	}
	return "", ""
}

func resolveBaseURL(providerName, current, configDir string, idx map[string]core.ProviderConfig, logDebug func(string, ...any)) string {
	if _, credURL := loadCredentials(providerName, configDir); credURL != "" {
		return credURL
	}
	if v, err := validateTLSBaseURL(current); err == nil && v != "" {
		return v
	}
	key := strings.ToLower(strings.TrimSpace(providerName))
	if idx != nil {
		if p, ok := idx[key]; ok {
			if v, err := validateTLSBaseURL(p.BaseURL); err == nil && v != "" {
				return v
			}
		}
	}
	return ""
}

func validateTLSBaseURL(value string) (string, error) {
	normalized := strings.TrimRight(strings.TrimSpace(value), "/")
	if normalized == "" {
		return "", nil
	}
	parsed, err := url.Parse(normalized)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("baseUrl must be an absolute URL")
	}
	scheme := strings.ToLower(parsed.Scheme)
	host := strings.ToLower(parsed.Hostname())
	isLocal := host == "localhost" || host == "127.0.0.1" ||
		strings.HasPrefix(host, "192.168.") || strings.HasPrefix(host, "10.")
	if scheme == "http" && !isLocal {
		return "", fmt.Errorf("baseUrl must use https (http allowed only for localhost)")
	}
	if scheme != "http" && scheme != "https" {
		return "", fmt.Errorf("baseUrl must use http or https")
	}
	return normalized, nil
}

func registerOpenAICompatibleProviders(
	registry *provider.SimpleRegistry,
	providers []core.ProviderConfig,
	configDir string,
	logger observability.Logger,
	tracer observability.Tracer,
	logDebug func(string, ...any),
) {
	for _, p := range providers {
		apiType := strings.ToLower(strings.TrimSpace(p.APIType))
		if apiType == "" {
			apiType = strings.ToLower(strings.TrimSpace(p.Type))
		}
		if apiType != "openai-compatible" {
			continue
		}
		id := strings.ToLower(strings.TrimSpace(p.Name))
		if id == "" || registry.IsRegistered(id) {
			continue
		}
		baseURL := p.BaseURL
		if baseURL != "" {
			validated, err := validateTLSBaseURL(baseURL)
			if err != nil {
				logDebug("warning: invalid base URL for provider %s: %v", id, err)
				continue
			}
			baseURL = validated
		}
		registry.RegisterOpenAICompatible(id)
		regID := id
		registry.Register(regID, func(cfg provider.Config) (provider.Provider, error) {
			u := cfg.BaseURL
			if u == "" {
				u = baseURL
			}
			if u != "" {
				validated, err := validateTLSBaseURL(u)
				if err != nil {
					logDebug("warning: invalid base URL for provider %s: %v", regID, err)
					return nil, err
				}
				u = validated
			}
			return openai.New(openai.Config{APIKey: cfg.APIKey, BaseURL: u, Name: regID, Logger: logger, Tracer: tracer})
		})
		logDebug("registered openai-compatible provider: %s", id)
	}
}

func registerCustomProvider(
	registry *provider.SimpleRegistry,
	id, defaultBaseURL string,
	logger observability.Logger,
	tracer observability.Tracer,
	logDebug func(string, ...any),
) {
	if registry.IsRegistered(id) {
		return
	}
	registry.RegisterOpenAICompatible(id)
	regID := id
	registry.Register(regID, func(cfg provider.Config) (provider.Provider, error) {
		u := cfg.BaseURL
		if u == "" {
			u = defaultBaseURL
		}
		return openai.New(openai.Config{APIKey: cfg.APIKey, BaseURL: u, Name: regID, Logger: logger, Tracer: tracer})
	})
	logDebug("registered dynamic provider: %s", id)
}

// registerCoreTools registers the essential built-in tools for the ACP server.
func registerCoreTools(
	registry *tools.SimpleRegistry,
	workspaceRoot string,
	logger observability.Logger,
	tracer observability.Tracer,
	logDebug func(string, ...any),
	wsCoreCfg *core.WebSearchConfig,
	exaAPIKey string,
) {
	bgManager := bgprocess.NewManager(bgprocess.DefaultManagerConfig())

	// ii workspace tools (file read/write/edit/grep).
	var wm *ii.WorkspaceManager
	if workspaceRoot != "" {
		var err error
		wm, err = ii.NewWorkspaceManager(workspaceRoot)
		if err != nil {
			logDebug("warning: workspace manager: %v", err)
		}
	}
	// Register Forge tools (1:1 with Forge's implementation)
	// These are simple, predictable tools without hash-based verification
	if wm != nil {
		for _, t := range forge.DefaultTools(workspaceRoot) {
			if err := registry.Register(t); err != nil {
				logDebug("warning: register forge tool %s: %v", t.Name(), err)
			}
		}
	}

	// Bash + background commands.
	bashCfg := builtin.DefaultBashConfig()
	if workspaceRoot != "" {
		bashCfg.AllowedPaths = []string{workspaceRoot, "/tmp"}
	} else {
		bashCfg.AllowedPaths = []string{"/tmp"}
	}
	bgBash := bgprocess.NewBackgroundBashTool(bgprocess.BackgroundBashToolConfig{
		BashConfig: bashCfg,
		Manager:    bgManager,
	})
	if err := registry.Register(bgBash); err != nil {
		logDebug("warning: register bash tool: %v", err)
	}
	if err := registry.Register(bgprocess.NewReadBackgroundCommandTool(bgManager)); err != nil {
		logDebug("warning: register ReadBackgroundCommand tool: %v", err)
	}

	// Additional builtins.
	if err := registry.Register(builtin.NewListDirTool()); err != nil {
		logDebug("warning: register list_dir: %v", err)
	}

	if err := registry.Register(web_fetch.New()); err != nil {
		logDebug("warning: register web_fetch: %v", err)
	}
	// Register websearch using core config preferences.
	wsTool := websearch.NewFromCoreConfig(wsCoreCfg, exaAPIKey)
	if websearch.IsAuthConfigured() || exaAPIKey != "" {
		if err := registry.Register(wsTool); err != nil {
			logDebug("warning: register websearch: %v", err)
		}
	}
}

// configureToolPermissions sets up the permission checker on the registry.
func configureToolPermissions(
	registry *tools.SimpleRegistry,
	cfgMgr core.ConfigManager,
	workspaceRoot string,
	broker *approval.ToolsApprovalBroker,
	logDebug func(string, ...any),
) error {
	if registry == nil || cfgMgr == nil {
		return nil
	}
	global := cfgMgr.GetPermissionPolicies()
	globalCfg := crossModulePermissionConfig(global)

	project, err := loadProjectPermissionConfig(workspaceRoot)
	if err != nil {
		logDebug("warning: project permissions: %v", err)
	}

	if existing, ok := registry.PermissionChecker().(*tools.InteractivePermissionChecker); ok {
		existing.SetConfig(globalCfg)
		existing.SetProjectConfig(project)
		return nil
	}
	checker := tools.NewInteractivePermissionChecker(globalCfg, broker)
	checker.SetProjectConfig(project)
	registry.SetPermissionChecker(checker)
	return nil
}

// crossModulePermissionConfig converts *core.PermissionConfig to tools.PermissionConfig via JSON.
func crossModulePermissionConfig(src *core.PermissionConfig) tools.PermissionConfig {
	if src == nil {
		return tools.DefaultPermissionConfig()
	}
	data, err := json.Marshal(src)
	if err != nil {
		return tools.DefaultPermissionConfig()
	}
	var dst tools.PermissionConfig
	if err := json.Unmarshal(data, &dst); err != nil {
		return tools.DefaultPermissionConfig()
	}
	return dst
}

// loadProjectPermissionConfig reads .swarmos/permissions.json from the workspace.
func loadProjectPermissionConfig(workspaceRoot string) (tools.PermissionConfig, error) {
	if workspaceRoot == "" {
		return tools.PermissionConfig{}, nil
	}
	path := filepath.Join(workspaceRoot, ".swarmos", "permissions.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return tools.PermissionConfig{}, nil
		}
		return tools.PermissionConfig{}, err
	}
	cfg, err := tools.DecodePermissionConfigStrict(data)
	if err != nil {
		return tools.PermissionConfig{}, err
	}
	return cfg, nil
}

// refreshHooks loads shell hooks from global and project config and registers them.
func refreshHooks(
	hm *chathooks.HooksManager,
	workspaceRoot, configDir string,
	logDebug func(string, ...any),
) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	globalPath := filepath.Join(home, ".swarmos", "hooks.json")
	projectPath := filepath.Join(workspaceRoot, ".swarmos", "hooks.json")

	globalHooks, err := readShellHookConfigs(globalPath)
	if err != nil {
		logDebug("warning: failed to read global hooks: %v", err)
	}
	projectHooks, err := readShellHookConfigs(projectPath)
	if err != nil {
		logDebug("warning: failed to read project hooks: %v", err)
	}

	merged := make(map[string]*sdkhooks.ShellHookConfig, len(globalHooks)+len(projectHooks))
	for _, h := range globalHooks {
		if h != nil && h.Name != "" {
			merged[h.Name] = h
		}
	}
	for _, h := range projectHooks {
		if h != nil && h.Name != "" {
			merged[h.Name] = h
		}
	}

	enabled := make([]*sdkhooks.ShellHookConfig, 0, len(merged))
	for _, h := range merged {
		if h.Enabled {
			enabled = append(enabled, h)
		}
	}
	sort.Slice(enabled, func(i, j int) bool {
		return enabled[i].Priority > enabled[j].Priority
	})

	hm.GetManager().Clear()
	for _, h := range enabled {
		sh, err := h.ToShellHook()
		if err != nil {
			logDebug("warning: bad shell hook %q: %v", h.Name, err)
			continue
		}
		if err := hm.RegisterCustomHook(sh); err != nil {
			logDebug("warning: register shell hook %q: %v", h.Name, err)
		}
	}
	logDebug("refreshed %d shell hooks", len(enabled))
	return nil
}

func readShellHookConfigs(path string) ([]*sdkhooks.ShellHookConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var swarmos struct {
		CustomHooks []*sdkhooks.ShellHookConfig `json:"custom_hooks"`
	}
	if err := json.Unmarshal(data, &swarmos); err == nil && swarmos.CustomHooks != nil {
		return swarmos.CustomHooks, nil
	}
	var legacy struct {
		Hooks []*sdkhooks.ShellHookConfig `json:"hooks"`
	}
	if err := json.Unmarshal(data, &legacy); err == nil && legacy.Hooks != nil {
		return legacy.Hooks, nil
	}
	return nil, fmt.Errorf("unrecognised hooks.json format: %s", path)
}

// ── ACP enrichment helpers ────────────────────────────────────────────────────

// registeredToolNames returns all tool names known to the given registry.
func registeredToolNames(reg *tools.SimpleRegistry) []string {
	if reg == nil {
		return nil
	}
	names := reg.ListAll()
	sort.Strings(names)
	return names
}

// buildAgentOptions converts agent profiles to ACP agent options.
func buildAgentOptions(ps *profile.ProfileStore) []acp.AgentOption {
	if ps == nil {
		return nil
	}
	cfg, err := ps.LoadConfig()
	if err != nil || cfg == nil {
		return nil
	}
	opts := make([]acp.AgentOption, 0, len(cfg.Profiles))
	for _, p := range cfg.Profiles {
		opts = append(opts, acp.AgentOption{
			ID:          p.ID,
			Name:        p.Name,
			Description: p.Description,
		})
	}
	return opts
}

// providerNames returns a de-duplicated, ordered list of provider names.
func providerNames(providers []core.ProviderConfig) []string {
	seen := make(map[string]bool)
	var names []string
	// Hardcoded well-known providers first.
	for _, n := range []string{"anthropic", "openai", "gemini", "cerebras", "openrouter", "fireworks", "groq", "together", "deepseek", "perplexity"} {
		seen[n] = true
		names = append(names, n)
	}
	for _, p := range providers {
		key := strings.ToLower(strings.TrimSpace(p.Name))
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		names = append(names, key)
	}
	return names
}

// providerModelMap builds a map of provider name → ordered model list from config.
func providerModelMap(providers []core.ProviderConfig) map[string][]acp.ModelOptionValue {
	out := make(map[string][]acp.ModelOptionValue)
	for _, p := range providers {
		if len(p.Models) == 0 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(p.Name))
		if key == "" {
			continue
		}
		models := make([]acp.ModelOptionValue, 0, len(p.Models))
		for _, m := range p.Models {
			name := m.Name
			if name == "" {
				name = m.ID
			}
			models = append(models, acp.ModelOptionValue{ID: m.ID, Name: name})
		}
		out[key] = models
	}
	return out
}

// ── Noop implementations ──────────────────────────────────────────────────────

type nativeFS struct{}

func (f *nativeFS) Read(_ context.Context, path string) ([]byte, error) {
	return os.ReadFile(path)
}
func (f *nativeFS) Write(_ context.Context, path string, data []byte) error {
	return os.WriteFile(path, data, 0644)
}
func (f *nativeFS) Exists(_ context.Context, path string) (bool, error) {
	_, err := os.Stat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	return err == nil, err
}
func (f *nativeFS) MkdirAll(_ context.Context, path string) error {
	return os.MkdirAll(path, 0755)
}
func (f *nativeFS) Rename(_ context.Context, oldPath, newPath string) error {
	return os.Rename(oldPath, newPath)
}
func (f *nativeFS) Remove(_ context.Context, path string) error {
	return os.Remove(path)
}

type nopTracer struct{}

func (t *nopTracer) StartSpan(ctx context.Context, _ string) (context.Context, observability.Span) {
	return ctx, &nopSpan{}
}
func (t *nopTracer) StartSpanWithOptions(ctx context.Context, _ string, _ observability.SpanOptions) (context.Context, observability.Span) {
	return ctx, &nopSpan{}
}
func (t *nopTracer) SpanFromContext(_ context.Context) observability.Span       { return &nopSpan{} }
func (t *nopTracer) InjectContext(_ context.Context, _ map[string]string) error { return nil }
func (t *nopTracer) ExtractContext(_ map[string]string) (context.Context, error) {
	return context.Background(), nil
}

type nopSpan struct{}

func (s *nopSpan) End()                                           {}
func (s *nopSpan) SetAttribute(_ string, _ any)                   {}
func (s *nopSpan) SetAttributes(_ map[string]any)                 {}
func (s *nopSpan) SetStatus(_ observability.StatusCode, _ string) {}
func (s *nopSpan) RecordError(_ error)                            {}
func (s *nopSpan) SpanID() string                                 { return "" }
func (s *nopSpan) TraceID() string                                { return "" }
func (s *nopSpan) Context() context.Context                       { return context.Background() }

type nopAuditor struct{}

func (a *nopAuditor) Record(_ context.Context, _ observability.AuditEvent) error { return nil }
func (a *nopAuditor) Query(_ context.Context, _ observability.AuditCriteria) ([]observability.AuditEvent, error) {
	return nil, nil
}
