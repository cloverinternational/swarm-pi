// Package main provides a standalone CLI for the swarm tool.
// Uses the SDK's config system for provider credentials.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/a2a"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/config"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability/noop"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/paths"
	provPackage "github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider/anthropic"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider/openai"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/swarm"
)

// Config holds CLI configuration
type Config struct {
	Action       string
	Handle       string
	Name         string
	Description  string
	SystemPrompt string
	Model        string
	Provider     string
	Message      string
	TargetHandle string
	Workspace    string
	Debug        bool
	A2AHandle    string
	A2AListen    string
	LogFile      string
	SwarmName    string
}

func listPeers(swarmName string) (string, error) {
	if swarmName == "" {
		swarmName = "default"
	}

	peersDir := paths.In("swarms", swarmName, "peers")
	entries, err := os.ReadDir(peersDir)
	if err != nil {
		return "", fmt.Errorf("failed to read peers directory: %w", err)
	}

	if len(entries) == 0 {
		return "No peers found in swarm: " + swarmName, nil
	}

	var output strings.Builder
	output.WriteString(fmt.Sprintf("Peers in swarm '%s':\n", swarmName))
	output.WriteString(strings.Repeat("-", 60) + "\n")

	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".json") {
			handle := strings.TrimSuffix(entry.Name(), ".json")
			data, err := os.ReadFile(filepath.Join(peersDir, entry.Name()))
			if err != nil {
				output.WriteString(fmt.Sprintf("  %s: (error reading: %v)\n", handle, err))
				continue
			}
			var peer a2a.PeerPresence
			if err := json.Unmarshal(data, &peer); err != nil {
				output.WriteString(fmt.Sprintf("  %s: (error parsing: %v)\n", handle, err))
				continue
			}
			status := peer.Status
			if status == "" {
				status = "idle"
			}
			output.WriteString(fmt.Sprintf("  @%s - %s\n", handle, status))
			output.WriteString(fmt.Sprintf("     Endpoint: %s\n", peer.EndpointURL))
			output.WriteString(fmt.Sprintf("     Last seen: %s\n", peer.LastSeenAt.Format(time.RFC3339)))
		}
	}

	return output.String(), nil
}

func showStatus(swarmName string) (string, error) {
	return listPeers(swarmName)
}

func main() {
	cfg := parseFlags()

	if cfg.Debug {
		fmt.Fprintf(os.Stderr, "[DEBUG] Config: %+v\n", cfg)
	}

	ctx := context.Background()

	// Handle simple discovery operations that don't need a full agent
	switch cfg.Action {
	case "list":
		result, err := listPeers(cfg.SwarmName)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(result)
		os.Exit(0)
	case "status":
		result, err := showStatus(cfg.SwarmName)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(result)
		os.Exit(0)
	}

	// Initialize agent with A2A (for more complex operations)
	agt, err := initAgent(ctx, cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error initializing agent: %v\n", err)
		os.Exit(1)
	}
	defer agt.Close()

	// Enable A2A if not already enabled
	if agt.A2ARuntime() == nil {
		handle := cfg.A2AHandle
		if handle == "" {
			handle = cfg.Handle
		}
		if handle == "" {
			handle = "cli-agent"
		}
		if err := agt.EnableA2A(ctx, handle); err != nil {
			fmt.Fprintf(os.Stderr, "Error enabling A2A: %v\n", err)
			os.Exit(1)
		}
		if cfg.Debug {
			fmt.Fprintf(os.Stderr, "[DEBUG] A2A enabled with handle: %s\n", handle)
		}
	}

	// Execute the requested action
	if cfg.Action == "agent" {
		// Run as a persistent agent - keep alive for A2A
		if err := runAgentMode(ctx, agt, cfg); err != nil {
			fmt.Fprintf(os.Stderr, "Error in agent mode: %v\n", err)
			os.Exit(1)
		}
		return
	}

	result, err := executeAction(ctx, agt, cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error executing action: %v\n", err)
		os.Exit(1)
	}

	fmt.Println(result)
}

func parseFlags() Config {
	var cfg Config

	flag.StringVar(&cfg.Action, "action", "list", "Action: create, join, list, send, broadcast, status, sync, agent")
	flag.StringVar(&cfg.Handle, "handle", "", "Peer handle")
	flag.StringVar(&cfg.Name, "name", "", "Peer name")
	flag.StringVar(&cfg.Description, "description", "", "Peer description")
	flag.StringVar(&cfg.SystemPrompt, "system-prompt", "", "System prompt")
	flag.StringVar(&cfg.Model, "model", "claude-sonnet-4-20250514", "Model")
	flag.StringVar(&cfg.Provider, "provider", "anthropic", "Provider (anthropic, openai)")
	flag.StringVar(&cfg.Message, "message", "", "Message to send")
	flag.StringVar(&cfg.TargetHandle, "target", "", "Target peer handle")
	flag.StringVar(&cfg.Workspace, "workspace", "", "Workspace (default: pwd)")
	flag.BoolVar(&cfg.Debug, "debug", false, "Enable debug")
	flag.StringVar(&cfg.A2AHandle, "a2a-handle", "cli-agent", "A2A handle")
	flag.StringVar(&cfg.A2AListen, "a2a-listen", "localhost:0", "A2A listen address")
	flag.StringVar(&cfg.LogFile, "log-file", "", "Log file path (for agent mode)")
	flag.StringVar(&cfg.SwarmName, "swarm", "default", "Swarm name for peer discovery")

	flag.Parse()

	if cfg.Workspace == "" {
		cfg.Workspace, _ = os.Getwd()
	}

	return cfg
}

func loadProviderFromConfig(providerName string) (*config.ProviderConfig, error) {
	mgr, err := config.NewProviderConfigManager()
	if err != nil {
		return nil, fmt.Errorf("failed to create config manager: %w", err)
	}

	if err := mgr.Load(); err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}

	cfg, ok := mgr.GetProvider(providerName)
	if !ok {
		// Fallback: try to create from environment variables
		if providerName == "anthropic" {
			apiKey := os.Getenv("ANTHROPIC_API_KEY")
			if apiKey != "" {
				return &config.ProviderConfig{
					Name:   "anthropic",
					Type:   "anthropic",
					APIKey: apiKey,
				}, nil
			}
		}
		return nil, fmt.Errorf("provider %q not found in %s (and no ANTHROPIC_API_KEY env var)", providerName, paths.ProvidersFile())
	}

	return &cfg, nil
}

func initAgent(ctx context.Context, cfg Config) (*agent.Agent, error) {
	if cfg.Debug {
		fmt.Fprintf(os.Stderr, "[DEBUG] Loading provider config for: %s\n", cfg.Provider)
	}

	// Load provider if:
	// 1. Not in agent mode, OR
	// 2. In agent mode with a specific provider requested (not default "anthropic")
	loadProvider := cfg.Action != "agent" || (cfg.Action == "agent" && cfg.Provider != "anthropic")

	var prov provPackage.Provider

	if loadProvider {
		// Load provider from SDK config system
		provCfg, err := loadProviderFromConfig(cfg.Provider)
		if err != nil {
			// For agent mode with invalid provider, fall back to noop
			if cfg.Action == "agent" {
				fmt.Fprintf(os.Stderr, "[WARN] Provider %s not found, using noop provider\n", cfg.Provider)
			} else {
				return nil, err
			}
		} else {
			if cfg.Debug {
				fmt.Fprintf(os.Stderr, "[DEBUG] Provider config loaded: type=%s\n", provCfg.Type)
			}

			// Create provider based on type
			switch provCfg.Type {
			case "anthropic":
				anthroCfg := anthropic.Config{
					APIKey:       provCfg.APIKey,
					DefaultModel: cfg.Model,
				}
				if provCfg.BaseURL != "" {
					anthroCfg.BaseURL = provCfg.BaseURL
				}
				prov, err = anthropic.New(anthroCfg)
				if err != nil {
					return nil, fmt.Errorf("failed to create anthropic provider: %w", err)
				}
			case "openai":
				// Fireworks uses OpenAI-compatible API
				openaiCfg := openai.Config{
					APIKey: provCfg.APIKey,
					Name:   "fireworks",
				}
				if provCfg.BaseURL != "" {
					openaiCfg.BaseURL = provCfg.BaseURL
				}
				prov, err = openai.New(openaiCfg)
				if err != nil {
					return nil, fmt.Errorf("failed to create openai provider: %w", err)
				}
			default:
				return nil, fmt.Errorf("unsupported provider type: %s", provCfg.Type)
			}
		}
	}

	// Create agent definition
	providerName := cfg.Provider
	if cfg.Action == "agent" && prov == nil {
		providerName = "noop"
	}
	def := &agent.Definition{
		ID:          cfg.A2AHandle,
		Name:        cfg.A2AHandle,
		Description: "CLI swarm agent",
		Provider:    providerName,
		Model:       cfg.Model,
	}

	// Create A2A config
	a2aCfg := &a2a.Config{
		Handle:        cfg.A2AHandle,
		WorkspacePath: cfg.Workspace,
		SwarmName:     cfg.SwarmName,
		ListenAddress: cfg.A2AListen,
		Descriptor: a2a.AgentDescriptor{
			Name:               cfg.A2AHandle,
			Description:        "CLI swarm agent",
			DefaultInputModes:  []string{"text", "text/plain"},
			DefaultOutputModes: []string{"text", "text/plain"},
		},
	}

	// Create tool registry
	toolRegistry := tools.NewRegistry()

	// Create agent with A2A
	agentCfg := agent.Config{
		Definition:    def,
		ToolRegistry:  toolRegistry,
		WorkspacePath: cfg.Workspace,
		A2A:           a2aCfg,
		Logger:        noop.NewLogger(),
		Tracer:        noop.NewTracer(),
	}

	// Only set provider if available (not required for A2A agent mode)
	if prov != nil {
		agentCfg.Provider = prov
	} else if cfg.Action == "agent" {
		// Use no-op provider for A2A relay mode
		agentCfg.Provider = &NoOpProvider{}
	}

	agt, err := agent.New(agentCfg)

	if err != nil {
		return nil, fmt.Errorf("failed to create agent: %w", err)
	}

	if cfg.Debug {
		fmt.Fprintf(os.Stderr, "[DEBUG] Agent created with A2A runtime\n")
	}

	// Wire up the swarm communicator
	swarmToolInterface, err := toolRegistry.Get("swarm")
	if err == nil {
		if swarmToolImpl, ok := swarmToolInterface.(*swarm.SwarmTool); ok {
			swarmToolImpl.SetCommunicator(&communicatorWrapper{agt: agt, handle: cfg.A2AHandle})
		}
	}

	return agt, nil
}

func executeAction(ctx context.Context, agt *agent.Agent, cfg Config) (string, error) {
	if cfg.Debug {
		fmt.Fprintf(os.Stderr, "[DEBUG] Executing action: %s\n", cfg.Action)
	}

	// Get the swarm tool
	tool, err := agt.ToolRegistry().Get("swarm")
	if err != nil {
		if cfg.Debug {
			fmt.Fprintf(os.Stderr, "[DEBUG] Swarm tool not found, available tools:\n")
			for _, name := range agt.ToolRegistry().List() {
				fmt.Fprintf(os.Stderr, "  - %s\n", name)
			}
		}
		return "", fmt.Errorf("swarm tool not found in registry: %w", err)
	}

	// Build parameters
	params := map[string]any{
		"action": cfg.Action,
	}

	switch cfg.Action {
	case "create":
		params["handle"] = cfg.Handle
		params["name"] = cfg.Name
		params["description"] = cfg.Description
		params["system_prompt"] = cfg.SystemPrompt
		params["model"] = cfg.Model

	case "send":
		params["target"] = cfg.TargetHandle
		params["message"] = cfg.Message

	case "broadcast":
		params["message"] = cfg.Message

	case "join":
		params["handle"] = cfg.Handle

	case "list", "status", "sync":
		// No additional params

	default:
		return "", fmt.Errorf("unknown action: %s", cfg.Action)
	}

	if cfg.Debug {
		paramsJSON, _ := json.MarshalIndent(params, "", "  ")
		fmt.Fprintf(os.Stderr, "[DEBUG] Tool params:\n%s\n", string(paramsJSON))
	}

	// Execute the tool
	result, err := tool.Execute(ctx, params)
	if err != nil {
		return "", fmt.Errorf("tool execution failed: %w", err)
	}

	// Format result
	if result == nil {
		return "Action completed (no result)", nil
	}

	// Extract text content from result
	var output strings.Builder
	for _, block := range result.Content {
		if block.Type == "text" {
			output.WriteString(block.Text)
		}
	}

	if output.Len() == 0 {
		return "Action completed (no text output)", nil
	}

	return output.String(), nil
}

// runAgentMode runs the CLI as a persistent agent for A2A communication
func runAgentMode(ctx context.Context, agt *agent.Agent, cfg Config) error {
	runtime := agt.A2ARuntime()
	if runtime == nil {
		return fmt.Errorf("A2A runtime not configured")
	}

	logFile := cfg.LogFile
	if logFile == "" {
		logFile = "/tmp/swarm-cli-agent.log"
	}
	log, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("failed to open log file: %w", err)
	}
	defer log.Close()

	handle := cfg.A2AHandle
	if handle == "" {
		handle = "cli-agent"
	}

	fmt.Fprintf(log, "[%s] Agent %s started - A2A listening on %s\n", time.Now().Format(time.RFC3339), handle, runtime.Address())
	fmt.Fprintf(log, "[%s] Agent is running in persistent mode. Press Ctrl+C to exit.\n", time.Now().Format(time.RFC3339))

	if cfg.Debug {
		fmt.Fprintf(os.Stderr, "[DEBUG] Agent %s running - A2A: %s, Log: %s\n", handle, runtime.Address(), logFile)
	}

	// Install A2A request handler to process incoming messages through agent execution
	if agt.A2ARuntime() != nil {
		agt.A2ARuntime().SetRequestHandler(func(ctx context.Context, req *a2a.InboundRequest) (*conversation.Message, error) {
			if req == nil || req.ProjectedMessage == nil {
				return nil, fmt.Errorf("inbound request missing projected message")
			}

			// Create conversation for this peer interaction
			convID := fmt.Sprintf("a2a:%s", req.Peer.Handle)

			// Clone and annotate the peer message
			peerMsg := req.ProjectedMessage.Clone()
			if peerMsg.Metadata == nil {
				peerMsg.Metadata = make(map[string]any)
			}
			peerMsg.Metadata["conversation_id"] = convID
			peerMsg.Metadata[conversation.A2APersistedMetadataKey] = true

			fmt.Fprintf(log, "[%s] Received message from %s: %s\n", time.Now().Format(time.RFC3339), req.Peer.Handle, peerMsg.Content)

			// Enqueue to A2A runtime
			agt.A2ARuntime().EnqueueMessage(peerMsg)

			// Trigger agent execution
			go func() {
				execCtx := context.Background()
				_, err := agt.ExecuteWhenIdle(execCtx, agent.ExecuteRequest{
					ConversationID: convID,
				})
				if err != nil {
					fmt.Fprintf(log, "[%s] Execution error: %v\n", time.Now().Format(time.RFC3339), err)
				}
			}()

			// Return nil - response comes through normal agent execution flow
			return nil, nil
		})
	}

	// Set up message callback to capture responses
	agt.SetMessageCallback(func(ctx context.Context, msg *conversation.Message) error {
		if msg == nil {
			return nil
		}
		if msg.Role == conversation.RoleAssistant && msg.Content != "" {
			fmt.Fprintf(log, "[%s] Generated response: %s\n", time.Now().Format(time.RFC3339), msg.Content)
		}
		return nil
	})

	// Start background processing goroutine
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				// Periodic health check
				fmt.Fprintf(log, "[%s] [HEARTBEAT] Agent %s alive\n", time.Now().Format(time.RFC3339), handle)
			}
		}
	}()

	// Process incoming A2A messages (only if using real provider)
	go func() {
		// Check for messages in inbox and process them
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				// Process any pending A2A messages
				// Note: Message processing would require ExecuteWhenIdle
				// This is a placeholder for future implementation
			}
		}
	}()

	// Keep main goroutine alive
	fmt.Printf("Agent %s is running (A2A: %s, Log: %s)\nPress Ctrl+C to exit.\n", handle, runtime.Address(), logFile)
	<-ctx.Done()

	fmt.Fprintf(log, "[%s] Agent %s shutting down\n", time.Now().Format(time.RFC3339), handle)
	return nil
}

// communicatorWrapper wraps the A2A Runtime to implement the swarm.SwarmCommunicator interface
type communicatorWrapper struct {
	agt    *agent.Agent
	handle string
}

func (c *communicatorWrapper) CreatePeer(ctx context.Context, config swarm.PeerConfig) (*swarm.PeerInfo, error) {
	return nil, fmt.Errorf("CreatePeer not implemented in CLI communicator")
}

func (c *communicatorWrapper) JoinSwarm(ctx context.Context, handle string, username string) error {
	runtime := c.agt.A2ARuntime()
	if runtime == nil {
		return fmt.Errorf("A2A runtime not available")
	}

	peer := runtime.Peer()

	// Convert PeerIdentity to PeerPresence
	presence := a2a.PeerPresence{
		Handle:      handle,
		Name:        peer.Handle,
		EndpointURL: peer.EndpointURL,
		CardURL:     peer.CardURL,
		Workspace:   peer.WorkspacePath,
	}

	// Register with discovery
	if err := a2a.JoinSwarm("default", presence); err != nil {
		return err
	}
	c.handle = handle
	return nil
}

func (c *communicatorWrapper) ListSwarmPeers(ctx context.Context) ([]swarm.PeerStatus, error) {
	runtime := c.agt.A2ARuntime()
	if runtime == nil {
		return nil, fmt.Errorf("A2A runtime not available")
	}
	result, err := runtime.ListPeers(ctx)
	if err != nil {
		return nil, err
	}
	statuses := make([]swarm.PeerStatus, len(result.Peers))
	for i, p := range result.Peers {
		statuses[i] = swarm.PeerStatus{
			Handle:      p.Handle,
			Name:        p.Handle,
			Status:      string(p.Status),
			CurrentTask: p.CurrentTask,
			Endpoint:    p.Endpoint,
		}
	}
	return statuses, nil
}

func (c *communicatorWrapper) SendMessage(ctx context.Context, targetHandle string, message string) error {
	runtime := c.agt.A2ARuntime()
	if runtime == nil {
		return fmt.Errorf("A2A runtime not available")
	}
	_, err := runtime.SendDM(ctx, targetHandle, message)
	return err
}

func (c *communicatorWrapper) Broadcast(ctx context.Context, message string) error {
	runtime := c.agt.A2ARuntime()
	if runtime == nil {
		return fmt.Errorf("A2A runtime not available")
	}
	_, err := runtime.Broadcast(ctx, message)
	return err
}

func (c *communicatorWrapper) UpdateStatus(ctx context.Context, status string, currentTask string) error {
	runtime := c.agt.A2ARuntime()
	if runtime == nil {
		return fmt.Errorf("A2A runtime not available")
	}
	swarmStatus := a2a.SwarmStatus(status)
	if swarmStatus == "" {
		swarmStatus = a2a.SwarmStatusIdle
	}
	_, err := runtime.UpdateStatus(ctx, swarmStatus, currentTask)
	return err
}

func (c *communicatorWrapper) SyncTasks(ctx context.Context, completedTasks []string) error {
	return nil // Not implemented
}

func (c *communicatorWrapper) GetLocalHandle() string {
	return c.handle
}

func (c *communicatorWrapper) GetLocalStatus() swarm.AgentStatus {
	runtime := c.agt.A2ARuntime()
	if runtime == nil {
		return swarm.AgentStatus{Status: "unknown"}
	}
	status := runtime.GetSwarmStatus()
	return swarm.AgentStatus{
		Status:      string(status.Status),
		CurrentTask: status.CurrentTask,
	}
}

func (c *communicatorWrapper) IsA2AEnabled() bool {
	return c.agt.A2ARuntime() != nil
}

// NoOpProvider is a minimal provider that returns empty responses.
// Used for A2A relay mode where no LLM calls are needed.
type NoOpProvider struct{}

func (n *NoOpProvider) Name() string { return "noop" }

func (n *NoOpProvider) Capabilities() provPackage.Capabilities {
	return provPackage.Capabilities{
		Streaming:        true,
		FunctionCalling:  true,
		MaxContextWindow: 100000,
		MaxOutputTokens:  4096,
	}
}

func (n *NoOpProvider) Chat(ctx context.Context, req provPackage.ChatRequest) (*provPackage.ChatResponse, error) {
	return &provPackage.ChatResponse{
		Message: &conversation.Message{
			Role:    conversation.RoleAssistant,
			Content: "A2A relay mode - no response generated",
		},
		FinishReason: provPackage.FinishReasonStop,
	}, nil
}

func (n *NoOpProvider) Stream(ctx context.Context, req provPackage.ChatRequest) (<-chan provPackage.StreamChunk, error) {
	ch := make(chan provPackage.StreamChunk, 1)
	ch <- provPackage.StreamChunk{
		Delta: "A2A relay mode - no response generated",
		Done:  true,
	}
	close(ch)
	return ch, nil
}
