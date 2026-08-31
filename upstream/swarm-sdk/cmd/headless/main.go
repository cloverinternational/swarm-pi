// Package main provides a headless CLI client for testing the SwarmOS SDK.
// This demonstrates conversation management, resuming, branching, tool execution, and MCP integration.
package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/Swarm-Code/mono/swarm-core/core"
	clientpkg "github.com/Swarm-Code/mono/swarm-sdk/client"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/a2a"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/analytics"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/compaction"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/configmigrate"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation/manager"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation/storage"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/paths"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider/anthropic"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider/openai"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/builtin"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/forge"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/mcp"
	swarmtool "github.com/Swarm-Code/mono/swarm-sdk/internal/tools/swarm"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/web_fetch"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/websearch"
	"github.com/Swarm-Code/mono/swarm-sdk/serve"
	"github.com/Swarm-Code/mono/swarm-sdk/tests/testconfig"
)

// Command represents a CLI command.
type Command string

const (
	CmdNew          Command = "new"           // Create new conversation
	CmdResume       Command = "resume"        // Resume existing conversation
	CmdList         Command = "list"          // List conversations
	CmdSend         Command = "send"          // Send message to conversation
	CmdExport       Command = "export"        // Export conversation
	CmdImportClaude Command = "import-claude" // Import from Claude Code
	CmdExportClaude Command = "export-claude" // Export to Claude Code
	CmdBranch       Command = "branch"        // Branch conversation at message
	CmdTools        Command = "tools"         // List available tools
	CmdMCP          Command = "mcp"           // Manage MCP servers
	CmdConfig       Command = "config"        // Manage configuration
	CmdTest         Command = "test"          // Test configuration
	CmdHelpAI       Command = "help-ai"       // AI-powered interactive help
	CmdHelp         Command = "help"          // Show help
	CmdPeer         Command = "peer"          // Start A2A peer (headless agent)
)

// Config holds CLI configuration.
type Config struct {
	// Provider settings
	Provider      string
	Model         string
	APIKey        string
	BaseURL       string
	UseTestConfig bool

	// Conversation settings
	ConversationID string
	Mode           string

	// Message settings
	Message     string
	MessageFile string

	// Export settings
	ExportFormat string
	OutputFile   string

	// Import settings
	InputFile     string
	ProjectPath   string
	WorkspacePath string

	// Branch settings
	BranchAtMessage int

	// MCP settings
	MCPServer  string
	MCPCommand string
	MCPArgs    string
	MCPServers string // Comma-separated list for send command

	// Tool settings
	ToolCategory string

	// Beta feature settings
	EnableThinking  bool
	ThinkingBudget  int
	ThinkingEffort  string // "low", "medium", "high", "max" (Opus 4.6+)
	EnableCaching   bool
	CacheTTL        string // "5m" or "1h"; empty means 5m (Anthropic default)
	EnableCitations bool

	// Vision settings
	ImagePath string
	ImageURL  string
	PDFPath   string

	// Config command settings
	ConfigSubcommand string   // providers, models, mcp, tools, permissions, profiles, hooks, show, set, get
	ConfigAction     string   // list, add, remove, set, get, create, delete, use, export, import, show
	ConfigArgs       []string // remaining arguments for the config command
	ConfigKeyValue   map[string]string

	// General settings
	Verbose     bool
	StoragePath string
	ConfigDir   string // Configuration directory path
	TraceMode   string // noop|local
	TracePath   string // JSONL path for local tracing

	// Peer command settings
	A2AHandle        string
	A2AListenAddress string
	SystemPrompt     string
	PeerName         string
	PeerDescription  string
	PeerToolsStr     string   // Comma-separated list of tools
	PeerTools        []string // Parsed tools
	PeerInteractive  bool
	PeerTimeout      time.Duration
	A2ASwarm         string // Swarm name for peer discovery (default: "default")
	SwarmScope       string // Swarm scope ID for peer grouping
}

func main() {
	// One-time migration of any legacy config root into the canonical ~/.swarm
	// root. Idempotent, best-effort, and must run BEFORE any config load so
	// first-run installs pick up migrated credentials/providers/etc.
	configmigrate.Run()

	// One-shot mode: swarm -p '<prompt>'
	// Must be checked before the generic command parser so -p is not treated as a command.
	if len(os.Args) >= 2 && os.Args[1] == "-p" {
		prompt := strings.Join(os.Args[2:], " ")
		if prompt == "" {
			fmt.Fprintln(os.Stderr, "Error: -p requires a prompt argument")
			os.Exit(1)
		}
		ctx := context.Background()
		logger := newConsoleLogger(false)
		if err := runOneShot(ctx, prompt, logger); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// Parse command from arguments
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	cmd := Command(os.Args[1])

	// Create flag set for remaining arguments
	fs := flag.NewFlagSet(string(cmd), flag.ExitOnError)
	config := parseFlags(fs, cmd)

	// Parse command-specific flags
	if err := fs.Parse(os.Args[2:]); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing flags: %v\n", err)
		os.Exit(1)
	}

	// Initialize observability
	logger := newConsoleLogger(config.Verbose)
	tracer, cleanupTracer, err := initializeTracer(config, logger)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error initializing tracer: %v\n", err)
		os.Exit(1)
	}
	defer cleanupTracer()

	// Run command
	ctx := context.Background()
	if err := runCommand(ctx, cmd, config, logger, tracer); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func parseFlags(fs *flag.FlagSet, cmd Command) *Config {
	config := &Config{
		UseTestConfig:  true,
		ThinkingBudget: 2048,
	}

	// Common flags
	fs.StringVar(&config.Provider, "provider", "anthropic", "Provider to use (anthropic, openai, glm)")
	fs.StringVar(&config.Model, "model", "", "Model to use (defaults to provider default)")
	fs.StringVar(&config.APIKey, "api-key", "", "API key (or use env var or test config)")
	fs.StringVar(&config.BaseURL, "base-url", "", "Custom base URL")
	fs.BoolVar(&config.UseTestConfig, "test-config", true, "Use tests/config.toml for credentials (default: true)")
	fs.BoolVar(&config.Verbose, "verbose", false, "Enable verbose logging")
	fs.StringVar(&config.StoragePath, "storage", "", "Path to conversation storage (default: ~/.swarm/conversations)")
	fs.StringVar(&config.ConfigDir, "config", "", "Configuration directory (default: ~/.swarm)")
	fs.StringVar(&config.TraceMode, "trace-mode", "noop", "Tracer mode: noop|local")
	fs.StringVar(&config.TracePath, "trace-path", "", "Path to local JSONL trace file (used when -trace-mode=local)")

	// Command-specific flags
	switch cmd {
	case CmdNew:
		fs.StringVar(&config.Mode, "mode", "interactive", "Conversation mode")

	case CmdResume:
		fs.StringVar(&config.ConversationID, "id", "", "Conversation ID (required)")

	case CmdSend:
		fs.StringVar(&config.ConversationID, "id", "", "Conversation ID (required)")
		fs.StringVar(&config.Message, "message", "", "Message to send")
		fs.StringVar(&config.MessageFile, "file", "", "Read message from file")
		fs.StringVar(&config.MCPServers, "mcp-servers", "", "MCP servers to connect (comma-separated commands)")

		// Beta features
		fs.BoolVar(&config.EnableThinking, "thinking", false, "Enable extended thinking")
		fs.IntVar(&config.ThinkingBudget, "thinking-budget", 2048, "Thinking token budget (1024-100000)")
		fs.StringVar(&config.ThinkingEffort, "thinking-effort", "", "Thinking effort level: low, medium, high, max (Opus 4.6+)")
		fs.BoolVar(&config.EnableCaching, "cache", false, "Enable prompt caching")
		fs.StringVar(&config.CacheTTL, "cache-ttl", "1h", "Prompt cache TTL: 5m or 1h")
		fs.BoolVar(&config.EnableCitations, "citations", false, "Enable citations")

		// Vision/multimodal
		fs.StringVar(&config.ImagePath, "image", "", "Path to image file")
		fs.StringVar(&config.ImageURL, "image-url", "", "URL to image")
		fs.StringVar(&config.PDFPath, "pdf", "", "Path to PDF file")

	case CmdExport:
		fs.StringVar(&config.ConversationID, "id", "", "Conversation ID (required)")
		fs.StringVar(&config.ExportFormat, "format", "json", "Export format (json, markdown, html, jsonl)")
		fs.StringVar(&config.OutputFile, "output", "", "Output file (stdout if not specified)")

	case CmdImportClaude:
		fs.StringVar(&config.InputFile, "file", "", "Claude conversation JSON file to import")
		fs.StringVar(&config.ProjectPath, "project", "", "Claude project path to import all conversations from")

	case CmdExportClaude:
		fs.StringVar(&config.ConversationID, "id", "", "Conversation ID to export")
		fs.StringVar(&config.OutputFile, "output", "", "Output file for single conversation")
		fs.StringVar(&config.WorkspacePath, "workspace", "", "Export all conversations for this workspace")
		fs.StringVar(&config.ProjectPath, "project", "", "Target project path for batch export")

	case CmdBranch:
		fs.StringVar(&config.ConversationID, "id", "", "Conversation ID (required)")
		fs.IntVar(&config.BranchAtMessage, "at", -1, "Branch at message index (0-based)")

	case CmdMCP:
		fs.StringVar(&config.MCPServer, "server", "", "MCP server name")
		fs.StringVar(&config.MCPCommand, "command", "", "MCP server command")
		fs.StringVar(&config.MCPArgs, "args", "", "MCP server arguments (comma-separated)")

	case CmdTools:
		fs.StringVar(&config.ToolCategory, "category", "", "Filter by category")

	case CmdConfig:
		// Config subcommand and action are parsed from remaining args
		// No flags for now, handled in cmdConfig
		config.ConfigKeyValue = make(map[string]string)

	case CmdPeer:
		// Peer command: start an A2A-enabled headless agent
		fs.StringVar(&config.ConversationID, "id", "", "Conversation ID (optional, creates new if not set)")
		fs.StringVar(&config.A2AHandle, "handle", "", "A2A handle for this peer (required)")
		fs.StringVar(&config.A2AListenAddress, "listen", "localhost:0", "Listen address for A2A (default: localhost with auto-assign port)")
		fs.StringVar(&config.SystemPrompt, "system-prompt", "", "System prompt for the peer agent")
		fs.StringVar(&config.PeerName, "name", "", "Human-readable name for the peer")
		fs.StringVar(&config.PeerDescription, "description", "", "Description of what this peer does")
		fs.StringVar(&config.PeerToolsStr, "tools", "", "Tools the peer can use (comma-separated)")
		fs.BoolVar(&config.PeerInteractive, "interactive", false, "Run in interactive mode (receive and respond to messages)")
		fs.DurationVar(&config.PeerTimeout, "timeout", 0, "Auto-shutdown after this duration (0 = run until killed, min: 10m, max: 30m)")
		fs.StringVar(&config.A2ASwarm, "swarm", "default", "Swarm name for peer discovery")
		fs.StringVar(&config.SwarmScope, "swarm-scope", "", "Swarm scope ID for peer grouping (inherits from SWARM_SWARM_SCOPE_ID env var if not set)")
	}

	return config
}

func runCommand(ctx context.Context, cmd Command, config *Config, logger observability.Logger, tracer observability.Tracer) error {
	switch cmd {
	case CmdNew:
		return cmdNew(ctx, config, logger, tracer)
	case CmdResume:
		return cmdResume(ctx, config, logger, tracer)
	case CmdList:
		return cmdList(ctx, config, logger, tracer)
	case CmdSend:
		return cmdSend(ctx, config, logger, tracer)
	case CmdExport:
		return cmdExport(ctx, config, logger, tracer)
	case CmdImportClaude:
		return cmdImportClaude(ctx, config, logger, tracer)
	case CmdExportClaude:
		return cmdExportClaude(ctx, config, logger, tracer)
	case CmdBranch:
		return cmdBranch(ctx, config, logger, tracer)
	case CmdTools:
		return cmdTools(ctx, config, logger, tracer)
	case CmdMCP:
		return cmdMCP(ctx, config, logger, tracer)
	case CmdConfig:
		return cmdConfig(ctx, config, logger, tracer)
	case CmdTest:
		return cmdTest(ctx, config, logger, tracer)
	case CmdHelpAI:
		return cmdHelpAI(ctx, config, logger, tracer)
	case CmdHelp:
		printHelp()
		return nil
	case CmdPeer:
		return cmdPeer(ctx, config, logger, tracer)
	case "runtime-demo":
		cmdRuntimeDemo()
		return nil
	default:
		printUsage()
		return fmt.Errorf("unknown command: %s", cmd)
	}
}

// ============================================================================
// Command Implementations
// ============================================================================

func cmdNew(ctx context.Context, config *Config, logger observability.Logger, tracer observability.Tracer) error {
	// Create storage
	store, err := createStorage(config.StoragePath)
	if err != nil {
		return fmt.Errorf("failed to create storage: %w", err)
	}

	// Create manager
	mgr, err := manager.NewManager(manager.Config{
		Storage: store,
		Logger:  logger,
		Tracer:  tracer,
	})
	if err != nil {
		return fmt.Errorf("failed to create manager: %w", err)
	}

	// Create conversation
	conv, err := mgr.Create(ctx, manager.CreateOptions{
		Mode: config.Mode,
	})
	if err != nil {
		return fmt.Errorf("failed to create conversation: %w", err)
	}

	fmt.Printf("Created conversation: %s\n", conv.ID)
	fmt.Printf("Mode: %s\n", conv.Mode)
	fmt.Printf("Status: %s\n", conv.Status)

	return nil
}

func cmdResume(ctx context.Context, config *Config, logger observability.Logger, tracer observability.Tracer) error {
	if config.ConversationID == "" {
		return fmt.Errorf("conversation ID required (use -id)")
	}

	// Create storage
	store, err := createStorage(config.StoragePath)
	if err != nil {
		return fmt.Errorf("failed to create storage: %w", err)
	}

	// Create manager
	mgr, err := manager.NewManager(manager.Config{
		Storage: store,
		Logger:  logger,
		Tracer:  tracer,
	})
	if err != nil {
		return fmt.Errorf("failed to create manager: %w", err)
	}

	// Resume conversation
	conv, err := mgr.Resume(ctx, config.ConversationID)
	if err != nil {
		return fmt.Errorf("failed to resume conversation: %w", err)
	}

	fmt.Printf("Resumed conversation: %s\n", conv.ID)
	fmt.Printf("Mode: %s\n", conv.Mode)
	fmt.Printf("Status: %s\n", conv.Status)
	fmt.Printf("Messages: %d\n", len(conv.Messages))
	fmt.Printf("Total tokens: %d\n", conv.TotalTokens)

	// Print recent messages
	if len(conv.Messages) > 0 {
		fmt.Println("\nRecent messages:")
		start := len(conv.Messages) - 5
		if start < 0 {
			start = 0
		}
		for i := start; i < len(conv.Messages); i++ {
			msg := conv.Messages[i]
			content := msg.Content
			if len(content) > 80 {
				content = content[:77] + "..."
			}
			fmt.Printf("  [%d] %s: %s\n", i, msg.Role, content)
		}
	}

	return nil
}

func cmdList(ctx context.Context, config *Config, logger observability.Logger, tracer observability.Tracer) error {
	// Create storage
	store, err := createStorage(config.StoragePath)
	if err != nil {
		return fmt.Errorf("failed to create storage: %w", err)
	}

	// List conversations
	conversations, err := store.List(ctx)
	if err != nil {
		return fmt.Errorf("failed to list conversations: %w", err)
	}

	if len(conversations) == 0 {
		fmt.Println("No conversations found")
		return nil
	}

	fmt.Printf("Found %d conversation(s):\n\n", len(conversations))
	for _, conv := range conversations {
		fmt.Printf("ID: %s\n", conv.ID)
		fmt.Printf("  Mode: %s\n", conv.Mode)
		fmt.Printf("  Status: %s\n", conv.Status)
		fmt.Printf("  Messages: %d\n", len(conv.Messages))
		fmt.Printf("  Tokens: %d\n", conv.TotalTokens)
		fmt.Printf("  Created: %s\n", conv.CreatedAt.Format("2006-01-02 15:04:05"))
		fmt.Printf("  Updated: %s\n\n", conv.UpdatedAt.Format("2006-01-02 15:04:05"))
	}

	return nil
}

func cmdSend(ctx context.Context, config *Config, logger observability.Logger, tracer observability.Tracer) error {
	if config.ConversationID == "" {
		return fmt.Errorf("conversation ID required (use -id)")
	}

	// Get message content
	message := config.Message
	if config.MessageFile != "" {
		content, err := os.ReadFile(config.MessageFile)
		if err != nil {
			return fmt.Errorf("failed to read message file: %w", err)
		}
		message = string(content)
	}

	if message == "" && config.ImagePath == "" && config.ImageURL == "" && config.PDFPath == "" {
		return fmt.Errorf("message required (use -message, -file, -image, -image-url, or -pdf)")
	}

	// Create storage
	store, err := createStorage(config.StoragePath)
	if err != nil {
		return fmt.Errorf("failed to create storage: %w", err)
	}

	// Create manager
	mgr, err := manager.NewManager(manager.Config{
		Storage: store,
		Logger:  logger,
		Tracer:  tracer,
	})
	if err != nil {
		return fmt.Errorf("failed to create manager: %w", err)
	}

	// Load conversation
	conv, err := mgr.Resume(ctx, config.ConversationID)
	if err != nil {
		return fmt.Errorf("failed to load conversation: %w", err)
	}

	// Create provider
	prov, err := createProvider(config, logger, tracer)
	if err != nil {
		return fmt.Errorf("failed to create provider: %w", err)
	}

	// Create tool registry with builtin tools + MCP tools
	registry, mcpClients, err := createToolRegistryWithMCP(ctx, config, logger, tracer)
	if err != nil {
		return fmt.Errorf("failed to create tool registry: %w", err)
	}

	// Ensure MCP clients are closed at the end
	defer func() {
		for _, client := range mcpClients {
			client.Close()
		}
	}()

	// Add user message with multimodal content
	userMsg, err := createUserMessage(config, message)
	if err != nil {
		return fmt.Errorf("failed to create user message: %w", err)
	}

	if err := mgr.AddMessage(ctx, config.ConversationID, userMsg); err != nil {
		return fmt.Errorf("failed to add user message: %w", err)
	}

	// Display user message
	if message != "" {
		fmt.Printf("User: %s\n", message)
	}
	if config.ImagePath != "" {
		fmt.Printf("      [Image: %s]\n", config.ImagePath)
	}
	if config.ImageURL != "" {
		fmt.Printf("      [Image URL: %s]\n", config.ImageURL)
	}
	if config.PDFPath != "" {
		fmt.Printf("      [PDF: %s]\n", config.PDFPath)
	}
	fmt.Println()

	// Tool calling loop - handle up to 10 iterations
	maxIterations := 10
	for iteration := 0; iteration < maxIterations; iteration++ {
		// Reload conversation to get updated messages
		conv, err = mgr.Resume(ctx, config.ConversationID)
		if err != nil {
			return fmt.Errorf("failed to reload conversation: %w", err)
		}

		// Prepare request with tools and beta features
		req := provider.ChatRequest{
			Messages: conv.Messages,
			Model:    getModel(config),
			Metadata: make(map[string]any),
		}

		// Enable beta features
		if config.EnableThinking {
			req.Metadata["thinking_enabled"] = true
			req.Metadata["thinking_budget"] = config.ThinkingBudget

			// Add effort parameter if specified
			if config.ThinkingEffort != "" {
				req.Metadata["thinking_effort"] = config.ThinkingEffort
				fmt.Printf("[Beta] Extended thinking enabled (budget: %d tokens, effort: %s)\n",
					config.ThinkingBudget, config.ThinkingEffort)
			} else {
				fmt.Printf("[Beta] Extended thinking enabled (budget: %d tokens)\n", config.ThinkingBudget)
			}
		}

		if config.EnableCaching {
			// Mark system prompt, tools, AND the conversation tail for caching.
			//
			// The message breakpoint is what makes the growing history cacheable;
			// without it only the static system+tools prefix is cached and every
			// turn re-reads the whole conversation uncached.
			//
			// An explicit ttl is required too: omitting it silently selects the 5
			// minute default, so any tool call or pause longer than that recreates
			// the full prefix. The TUI already defaults to 1h; headless now matches.
			cacheTTL := config.CacheTTL
			if cacheTTL != "5m" && cacheTTL != "1h" {
				cacheTTL = "1h"
			}
			cacheControl := map[string]string{"type": "ephemeral", "ttl": cacheTTL}
			req.Metadata["system_cache_control"] = cacheControl
			req.Metadata["tool_cache_control"] = cacheControl
			req.Metadata["message_cache_control"] = cacheControl
			fmt.Println("[Beta] Prompt caching enabled")
		}

		if config.EnableCitations {
			req.Metadata["enable_citations"] = true
			fmt.Println("[Beta] Citations enabled")
		}

		// Add tools to request
		toolList := registry.List()
		req.Tools = make([]provider.Tool, len(toolList))
		for i, toolName := range toolList {
			tool, _ := registry.Get(toolName)
			req.Tools[i] = provider.Tool{
				Name:        tool.Name(),
				Description: tool.Description(),
				Parameters:  tool.Parameters(),
			}
		}

		// Send to provider
		resp, err := prov.Chat(ctx, req)
		if err != nil {
			return fmt.Errorf("provider chat failed: %w", err)
		}

		// Add assistant response
		if err := mgr.AddMessage(ctx, config.ConversationID, resp.Message); err != nil {
			return fmt.Errorf("failed to add assistant message: %w", err)
		}

		// Display thinking content if present
		if resp.Message.Metadata != nil {
			if thinking, ok := resp.Message.Metadata["thinking"].(string); ok && thinking != "" {
				fmt.Printf("\n[Thinking]\n%s\n\n", thinking)
			}
		}

		// Check if assistant wants to use tools
		if len(resp.Message.ToolCalls) > 0 {
			fmt.Printf("Assistant requested %d tool(s):\n", len(resp.Message.ToolCalls))

			// Execute each tool call
			toolResults := make([]conversation.ToolResult, 0, len(resp.Message.ToolCalls))
			for _, toolCall := range resp.Message.ToolCalls {
				fmt.Printf("  - %s (ID: %s)\n", toolCall.Name, toolCall.ID)

				// Get tool from registry
				_, err := registry.Get(toolCall.Name)
				if err != nil {
					// Tool not found
					toolResults = append(toolResults, conversation.ToolResult{
						CallID: toolCall.ID,
						Name:   toolCall.Name,
						Output: "",
						Error: &conversation.ToolError{
							Type:    "tool.not_found",
							Message: fmt.Sprintf("Tool '%s' not found", toolCall.Name),
						},
					})
					continue
				}

				// Execute tool
				var result *tools.ToolResult
				toolImpl, toolErr := registry.Get(toolCall.Name)
				if toolErr == nil {
					result, err = toolImpl.Execute(ctx, toolCall.Parameters)
				} else {
					err = toolErr
				}
				if err != nil {
					// Tool execution failed
					toolResults = append(toolResults, conversation.ToolResult{
						CallID: toolCall.ID,
						Name:   toolCall.Name,
						Output: "",
						Error: &conversation.ToolError{
							Type:    "tool.execution_failed",
							Message: err.Error(),
						},
					})
				} else {
					// Tool execution succeeded
					fmt.Printf("    Result: %s\n", truncateString(result.Output, 100))
					toolResults = append(toolResults, conversation.ToolResult{
						CallID: toolCall.ID,
						Name:   toolCall.Name,
						Output: result.Output,
					})
				}
			}

			// Create user message with tool results
			toolResultMsg := newToolResultMessage(toolResults)
			if err := mgr.AddMessage(ctx, config.ConversationID, toolResultMsg); err != nil {
				return fmt.Errorf("failed to add tool results: %w", err)
			}

			fmt.Println()
			// Continue loop to get assistant's response to tool results
			continue
		}

		// No tool calls - print final response
		fmt.Printf("Assistant: %s\n\n", resp.Message.Content)

		// Display citations if present
		if resp.Message.Metadata != nil {
			if citations, ok := resp.Message.Metadata["citations"].([]any); ok && len(citations) > 0 {
				fmt.Printf("[Citations] %d citation(s) found\n", len(citations))
			}
		}

		if resp.Usage != nil {
			fmt.Printf("Tokens - Input: %d, Output: %d, Total: %d\n",
				resp.Usage.Input,
				resp.Usage.Output,
				resp.Usage.Total)
		}

		// Done - break out of loop
		break
	}

	return nil
}

func cmdExport(ctx context.Context, config *Config, logger observability.Logger, tracer observability.Tracer) error {
	if config.ConversationID == "" {
		return fmt.Errorf("conversation ID required (use -id)")
	}

	// Create storage
	store, err := createStorage(config.StoragePath)
	if err != nil {
		return fmt.Errorf("failed to create storage: %w", err)
	}

	// Create manager
	mgr, err := manager.NewManager(manager.Config{
		Storage: store,
		Logger:  logger,
		Tracer:  tracer,
	})
	if err != nil {
		return fmt.Errorf("failed to create manager: %w", err)
	}

	// Export conversation
	format := manager.ExportFormat(config.ExportFormat)
	data, err := mgr.Export(ctx, config.ConversationID, format)
	if err != nil {
		return fmt.Errorf("failed to export conversation: %w", err)
	}

	// Write to output
	if config.OutputFile != "" {
		if err := os.WriteFile(config.OutputFile, data, 0644); err != nil {
			return fmt.Errorf("failed to write output file: %w", err)
		}
		fmt.Printf("Exported conversation to %s\n", config.OutputFile)
	} else {
		fmt.Println(string(data))
	}

	return nil
}

func cmdBranch(ctx context.Context, config *Config, logger observability.Logger, tracer observability.Tracer) error {
	if config.ConversationID == "" {
		return fmt.Errorf("conversation ID required (use -id)")
	}

	if config.BranchAtMessage < 0 {
		return fmt.Errorf("branch point required (use -at)")
	}

	// Create storage
	store, err := createStorage(config.StoragePath)
	if err != nil {
		return fmt.Errorf("failed to create storage: %w", err)
	}

	// Create manager
	mgr, err := manager.NewManager(manager.Config{
		Storage: store,
		Logger:  logger,
		Tracer:  tracer,
	})
	if err != nil {
		return fmt.Errorf("failed to create manager: %w", err)
	}

	// Load original conversation
	original, err := mgr.Resume(ctx, config.ConversationID)
	if err != nil {
		return fmt.Errorf("failed to load conversation: %w", err)
	}

	if config.BranchAtMessage >= len(original.Messages) {
		return fmt.Errorf("invalid branch point: %d (conversation has %d messages)",
			config.BranchAtMessage, len(original.Messages))
	}

	// Create branched conversation
	branched, err := mgr.Create(ctx, manager.CreateOptions{
		Mode: original.Mode,
		Metadata: &conversation.ConversationMetadata{
			UserID:    original.Metadata.UserID,
			ProjectID: original.Metadata.ProjectID,
			Tags:      append(original.Metadata.Tags, "branched"),
			Custom: map[string]any{
				"branched_from": original.ID,
				"branch_point":  config.BranchAtMessage,
			},
		},
	})
	if err != nil {
		return fmt.Errorf("failed to create branched conversation: %w", err)
	}

	// Copy messages up to branch point
	for i := 0; i <= config.BranchAtMessage; i++ {
		msg := original.Messages[i]
		if err := mgr.AddMessage(ctx, branched.ID, msg); err != nil {
			return fmt.Errorf("failed to copy message: %w", err)
		}
	}

	fmt.Printf("Created branched conversation: %s\n", branched.ID)
	fmt.Printf("Original: %s\n", original.ID)
	fmt.Printf("Branch point: message %d\n", config.BranchAtMessage)
	fmt.Printf("Copied %d messages\n", config.BranchAtMessage+1)

	return nil
}

func cmdTools(ctx context.Context, config *Config, logger observability.Logger, tracer observability.Tracer) error {
	registry := createToolRegistry(logger, tracer)

	toolNames := registry.List()
	if len(toolNames) == 0 {
		fmt.Println("No tools registered")
		return nil
	}

	fmt.Printf("Available tools (%d):\n\n", len(toolNames))
	for _, name := range toolNames {
		tool, err := registry.Get(name)
		if err != nil {
			continue
		}

		fmt.Printf("• %s\n", tool.Name())
		fmt.Printf("  %s\n", tool.Description())
		fmt.Println()
	}

	return nil
}

func cmdMCP(ctx context.Context, config *Config, logger observability.Logger, tracer observability.Tracer) error {
	if config.MCPCommand == "" {
		return fmt.Errorf("MCP command required (use -command)")
	}

	// Parse arguments
	var args []string
	if config.MCPArgs != "" {
		args = strings.Split(config.MCPArgs, ",")
	}

	// Create MCP transport config
	transportCfg := &mcp.TransportConfig{
		Type:    mcp.TransportStdio,
		Command: config.MCPCommand,
		Args:    args,
	}

	// Create transport and client
	transport := mcp.NewStdioTransport(transportCfg, logger)
	client := mcp.NewClient(transport, logger, tracer)

	// Connect to MCP server
	if err := client.Connect(ctx); err != nil {
		return fmt.Errorf("failed to connect to MCP server: %w", err)
	}
	defer client.Close()

	// List available tools
	toolsList := client.ListTools()

	serverName := config.MCPServer
	if serverName == "" {
		serverName = config.MCPCommand
	}

	fmt.Printf("MCP Server: %s\n", serverName)
	fmt.Printf("Available tools: %d\n\n", len(toolsList))

	for _, tool := range toolsList {
		fmt.Printf("• %s\n", tool.Name)
		if tool.Description != "" {
			fmt.Printf("  %s\n", tool.Description)
		}
		fmt.Println()
	}

	return nil
}

// ============================================================================
// Helper Functions
// ============================================================================

func createStorage(storagePath string) (storage.Storage, error) {
	// Determine storage path
	path := storagePath
	if path == "" {
		path = paths.ConversationsDir()
	}

	// Create directory if it doesn't exist
	if err := os.MkdirAll(path, 0755); err != nil {
		return nil, fmt.Errorf("failed to create storage directory: %w", err)
	}

	// Create file storage
	return storage.NewFileStorage(storage.FileStorageConfig{
		BaseDir: path,
	})
}

func initializeTracer(config *Config, logger observability.Logger) (observability.Tracer, func(), error) {
	mode := strings.ToLower(strings.TrimSpace(config.TraceMode))
	switch mode {
	case "", "noop":
		return observability.NewNoopTracer(), func() {}, nil
	case "local":
		tracePath := strings.TrimSpace(config.TracePath)
		if tracePath == "" {
			configDir, err := getConfigDir(config.ConfigDir)
			if err != nil {
				return nil, nil, err
			}
			tracePath = filepath.Join(configDir, "diagnostics", "trace-events.jsonl")
		}

		tracer, _, sink, err := observability.NewLocalJSONLTracer(tracePath)
		if err != nil {
			return nil, nil, err
		}
		logger.Info(context.Background(), "headless.local_tracer_enabled",
			observability.F("trace_path", tracePath))
		return tracer, func() { _ = sink.Close() }, nil
	default:
		return nil, nil, fmt.Errorf("invalid trace mode %q (expected noop|local)", config.TraceMode)
	}
}

func createProvider(config *Config, logger observability.Logger, tracer observability.Tracer) (provider.Provider, error) {
	// Get config directory
	configDir, err := getConfigDir(config.ConfigDir)
	if err != nil {
		logger.Warn(context.Background(), "failed to get config dir", observability.F("error", err.Error()))
	}

	// Try to load from our config system FIRST
	apiKey := config.APIKey
	baseURL := config.BaseURL
	model := config.Model

	if configDir != "" && apiKey == "" {
		// Load configuration
		ctx := context.Background()
		configMgr := core.NewFileConfigManager(configDir, &nativeFS{})
		if loadErr := configMgr.Load(ctx); loadErr == nil {
			// Get credentials for this provider
			creds := configMgr.Credentials()
			if creds != nil && creds.Providers != nil {
				// Try exact match first
				if provCred, ok := creds.Providers[config.Provider]; ok && provCred.APIKey != "" {
					apiKey = provCred.APIKey
					logger.Info(ctx, "loaded API key from config",
						observability.F("provider", config.Provider),
						observability.F("config_dir", configDir))
				}
				// Try case-insensitive match (e.g., "cerebras" vs "Cerebras")
				if apiKey == "" {
					for provName, provCred := range creds.Providers {
						if strings.EqualFold(provName, config.Provider) && provCred.APIKey != "" {
							apiKey = provCred.APIKey
							logger.Info(ctx, "loaded API key from config (case-insensitive match)",
								observability.F("provider", config.Provider),
								observability.F("matched_name", provName),
								observability.F("config_dir", configDir))
							break
						}
					}
				}
			}

			// Get provider config for base URL and model defaults
			providers := configMgr.GetProviders()
			for _, p := range providers {
				if strings.EqualFold(p.Name, config.Provider) {
					if baseURL == "" && p.BaseURL != "" {
						baseURL = p.BaseURL
					}
					if model == "" && len(p.Models) > 0 {
						// Find default model
						for _, m := range p.Models {
							if m.Default {
								model = m.ID
								break
							}
						}
						// Fall back to first model
						if model == "" {
							model = p.Models[0].ID
						}
					}
					break
				}
			}
		}
	}

	// Try to load from test config second
	if config.UseTestConfig && apiKey == "" {
		testCfg, err := testconfig.Load()
		if err == nil {
			providerCfg, ok := testCfg.GetProviderConfig(config.Provider)
			if ok {
				if apiKey == "" {
					apiKey = providerCfg.APIKey
				}
				if baseURL == "" {
					baseURL = providerCfg.BaseURL
				}
				if model == "" {
					model = providerCfg.GetDefaultModel()
				}

				logger.Info(context.Background(), "loaded credentials from test config",
					observability.F("provider", config.Provider),
					observability.F("model", model))
			}
		}
	}

	// Fall back to the canonical SDK credential resolver:
	// env vars 	 ~/.swarm/config/credentials.json 	 OAuth token.
	if apiKey == "" {
		var resolvedBase string
		apiKey, resolvedBase = clientpkg.ResolveCredentials(config.Provider)
		if baseURL == "" && resolvedBase != "" {
			baseURL = resolvedBase
		}
	}

	if apiKey == "" {
		return nil, fmt.Errorf("API key required (use -api-key, environment variable, OAuth token, or -test-config)")
	}

	switch config.Provider {
	case "anthropic":
		// Detect if this is an OAuth token
		isOAuth := anthropic.IsOAuthToken(apiKey)

		cfg := anthropic.Config{
			APIKey:       apiKey,
			DefaultModel: model,
			BaseURL:      baseURL,
			IsOAuth:      isOAuth,
			Logger:       logger,
			Tracer:       tracer,
		}
		return anthropic.New(cfg)

	case "openai":
		cfg := openai.Config{
			APIKey:  apiKey,
			BaseURL: baseURL,
			Logger:  logger,
			Tracer:  tracer,
		}
		return openai.New(cfg)

	case "glm":
		// GLM uses OpenAI-compatible API
		cfg := openai.Config{
			APIKey:  apiKey,
			BaseURL: baseURL,
			Logger:  logger,
			Tracer:  tracer,
		}
		return openai.New(cfg)

	case "cerebras", "cerebrasv2":
		// Cerebras uses OpenAI-compatible API
		if baseURL == "" {
			baseURL = "https://api.cerebras.ai/v1"
		}
		cfg := openai.Config{
			APIKey:  apiKey,
			BaseURL: baseURL,
			Logger:  logger,
			Tracer:  tracer,
		}
		return openai.New(cfg)

	default:
		// First check if this is a known OpenAI-compatible profile
		factory, err := openai.NewFactory()
		if err == nil {
			cfg := openai.FactoryConfig{
				Profile: config.Provider,
				APIKey:  apiKey,
				Logger:  logger,
				Tracer:  tracer,
			}
			if prov, err := factory.CreateFromProfile(cfg); err == nil {
				return prov, nil
			}
		}

		// Try as OpenAI-compatible provider with custom base URL
		if baseURL != "" {
			cfg := openai.Config{
				APIKey:  apiKey,
				BaseURL: baseURL,
				Name:    config.Provider,
				Logger:  logger,
				Tracer:  tracer,
			}
			return openai.New(cfg)
		}
		return nil, fmt.Errorf("unsupported provider: %s (supported: anthropic, openai, glm, cerebras, or any OpenAI-compatible with base URL)", config.Provider)
	}
}

func createToolRegistry(logger observability.Logger, tracer observability.Tracer) tools.Registry {
	registry := tools.NewSimpleRegistry(logger, tracer)

	// Register builtin tools directly (they implement tools.Tool interface)
	registry.Register(forge.NewFSRead(""))
	registry.Register(forge.NewFSWrite(""))
	registry.Register(forge.NewFSPatch(""))
	registry.Register(builtin.NewBashTool())
	registry.Register(builtin.NewAnnoyedTool())
	registry.Register(builtin.NewAgentBrowserTool())

	// Register websearch tool (auto-detects Anthropic OAuth or Exa via EXA_API_KEY)
	if websearch.IsAuthConfigured() {
		registry.Register(websearch.New())
	}

	// Register web fetch tool (always available — no auth required)
	registry.Register(web_fetch.New())

	return registry
}

func createToolRegistryWithMCP(ctx context.Context, config *Config, logger observability.Logger, tracer observability.Tracer) (tools.Registry, []*mcp.Client, error) {
	registry := createToolRegistry(logger, tracer)
	mcpClients := make([]*mcp.Client, 0)

	// Connect to MCP servers if specified
	if config.MCPServers != "" {
		serverCommands := strings.Split(config.MCPServers, ",")

		for _, serverCmd := range serverCommands {
			serverCmd = strings.TrimSpace(serverCmd)
			if serverCmd == "" {
				continue
			}

			// Parse command (format: "command arg1 arg2")
			parts := strings.Fields(serverCmd)
			if len(parts) == 0 {
				continue
			}

			command := parts[0]
			args := parts[1:]

			// Create MCP client
			transportCfg := &mcp.TransportConfig{
				Type:    mcp.TransportStdio,
				Command: command,
				Args:    args,
			}

			transport := mcp.NewStdioTransport(transportCfg, logger)
			client := mcp.NewClient(transport, logger, tracer)

			// Connect to server
			if err := client.Connect(ctx); err != nil {
				logger.Warn(ctx, "failed to connect to MCP server",
					observability.F("command", command),
					observability.F("error", err.Error()))
				continue
			}

			// Wrap and register MCP tools
			mcpTools := mcp.WrapMCPTools(client)
			for _, tool := range mcpTools {
				registry.Register(tool)
			}

			mcpClients = append(mcpClients, client)

			logger.Info(ctx, "connected to MCP server",
				observability.F("command", command),
				observability.F("tools", len(mcpTools)))
		}
	}

	return registry, mcpClients, nil
}

func createUserMessage(config *Config, textContent string) (*conversation.Message, error) {
	msg := &conversation.Message{
		ID:        fmt.Sprintf("msg_%d", time.Now().UnixNano()),
		Timestamp: time.Now(),
		Role:      conversation.RoleUser,
		Content:   textContent,
		Metadata:  make(map[string]any),
	}

	// Handle multimodal content via metadata
	// This will be translated by provider-specific code
	hasMultimodal := config.ImagePath != "" || config.ImageURL != "" || config.PDFPath != ""

	if hasMultimodal {
		multimodalContent := make([]map[string]any, 0)

		// Add text content if present
		if textContent != "" {
			multimodalContent = append(multimodalContent, map[string]any{
				"type": "text",
				"text": textContent,
			})
		}

		// Add image from file
		if config.ImagePath != "" {
			imageData, err := os.ReadFile(config.ImagePath)
			if err != nil {
				return nil, fmt.Errorf("failed to read image file: %w", err)
			}

			// Encode as base64
			imageB64 := base64.StdEncoding.EncodeToString(imageData)

			// Detect MIME type from extension
			mimeType := "image/jpeg"
			if strings.HasSuffix(strings.ToLower(config.ImagePath), ".png") {
				mimeType = "image/png"
			} else if strings.HasSuffix(strings.ToLower(config.ImagePath), ".gif") {
				mimeType = "image/gif"
			} else if strings.HasSuffix(strings.ToLower(config.ImagePath), ".webp") {
				mimeType = "image/webp"
			}

			multimodalContent = append(multimodalContent, map[string]any{
				"type": "image",
				"source": map[string]any{
					"type":       "base64",
					"media_type": mimeType,
					"data":       imageB64,
				},
			})
		}

		// Add image from URL
		if config.ImageURL != "" {
			multimodalContent = append(multimodalContent, map[string]any{
				"type": "image",
				"source": map[string]any{
					"type": "url",
					"url":  config.ImageURL,
				},
			})
		}

		// Add PDF
		if config.PDFPath != "" {
			pdfData, err := os.ReadFile(config.PDFPath)
			if err != nil {
				return nil, fmt.Errorf("failed to read PDF file: %w", err)
			}

			// Encode as base64
			pdfB64 := base64.StdEncoding.EncodeToString(pdfData)

			multimodalContent = append(multimodalContent, map[string]any{
				"type": "document",
				"source": map[string]any{
					"type":       "base64",
					"media_type": "application/pdf",
					"data":       pdfB64,
				},
			})
		}

		// Store multimodal content in metadata for provider translation
		msg.Metadata["content_blocks"] = multimodalContent
	}

	return msg, nil
}

func getModel(config *Config) string {
	if config.Model != "" {
		return config.Model
	}

	// Try to get from test config
	if config.UseTestConfig {
		testCfg, err := testconfig.Load()
		if err == nil {
			providerCfg, ok := testCfg.GetProviderConfig(config.Provider)
			if ok {
				return providerCfg.GetDefaultModel()
			}
		}
	}

	// Provider defaults
	switch config.Provider {
	case "anthropic":
		return "claude-3-5-sonnet-20241022"
	case "openai":
		return "gpt-4"
	case "glm":
		return "GLM-4-Plus"
	default:
		return ""
	}
}

func newToolResultMessage(results []conversation.ToolResult) *conversation.Message {
	return &conversation.Message{
		ID:          fmt.Sprintf("msg_%d", time.Now().UnixNano()),
		Timestamp:   time.Now(),
		Role:        conversation.RoleUser,
		Content:     "", // Tool results don't have text content
		ToolResults: results,
	}
}

func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

func printUsage() {
	fmt.Println("Usage: headless <command> [options]")
	fmt.Println()
	fmt.Println("Commands:")
	fmt.Println("  new           Create a new conversation")
	fmt.Println("  resume        Resume an existing conversation")
	fmt.Println("  list          List all conversations")
	fmt.Println("  send          Send a message to a conversation")
	fmt.Println("  export        Export a conversation")
	fmt.Println("  import-claude Import conversations from Claude Code")
	fmt.Println("  export-claude Export conversations to Claude Code")
	fmt.Println("  branch        Branch a conversation at a specific message")
	fmt.Println("  tools         List available tools")
	fmt.Println("  mcp           Connect to an MCP server")
	fmt.Println("  help          Show detailed help")
	fmt.Println()
	fmt.Println("Use 'headless <command> -h' for command-specific help")
}

func printHelp() {
	fmt.Println("SwarmOS SDK Headless CLI Client")
	fmt.Println()
	fmt.Println("This tool demonstrates the SwarmOS SDK capabilities:")
	fmt.Println("  • Conversation management (create, resume, branch)")
	fmt.Println("  • Multi-provider support (Anthropic, OpenAI, GLM)")
	fmt.Println("  • Tool execution (builtin and MCP)")
	fmt.Println("  • Beta features (thinking, caching, citations)")
	fmt.Println("  • Vision and multimodal support")
	fmt.Println("  • Export to multiple formats")
	fmt.Println("  • Import/Export with Claude Code")
	fmt.Println()
	fmt.Println("Examples:")
	fmt.Println()
	fmt.Println("  # Create conversation")
	fmt.Println("  headless new -provider anthropic")
	fmt.Println()
	fmt.Println("  # Send message with extended thinking")
	fmt.Println("  headless send -id <conv_id> -message \"Explain quantum computing\" -thinking")
	fmt.Println()
	fmt.Println("  # Send with adaptive thinking and effort (Opus 4.6+)")
	fmt.Println("  headless send -id <conv_id> -model claude-opus-4-6 \\")
	fmt.Println("    -message \"Prove the Riemann Hypothesis\" -thinking -thinking-effort max")
	fmt.Println()
	fmt.Println("  # Send with image")
	fmt.Println("  headless send -id <conv_id> -message \"What's in this image?\" -image photo.jpg")
	fmt.Println()
	fmt.Println("  # Connect MCP servers")
	fmt.Println("  headless send -id <conv_id> -message \"List files\" \\")
	fmt.Println("    -mcp-servers \"npx @modelcontextprotocol/server-filesystem /tmp\"")
	fmt.Println()
	fmt.Println("  # Enable all beta features")
	fmt.Println("  headless send -id <conv_id> -message \"Research AI safety\" \\")
	fmt.Println("    -thinking -cache -citations")
	fmt.Println()
	fmt.Println("  # Import from Claude Code")
	fmt.Println("  headless import-claude -file ~/.claude/projects/-home-user-proj/conversations/conv-123.json")
	fmt.Println("  headless import-claude -project /home/user/myproject")
	fmt.Println()
	fmt.Println("  # Export to Claude Code")
	fmt.Println("  headless export-claude -id <conv_id> -output conv-claude.json")
	fmt.Println("  headless export-claude -workspace /home/user/myproject")
	fmt.Println("    -thinking -thinking-effort high -cache -citations")
	fmt.Println()
	fmt.Println("See README.md for full documentation")
}

// ============================================================================
// Tool Adapter - Wraps legacy builtin tools

// ============================================================================
// Console Logger Implementation
// ============================================================================

type consoleLogger struct {
	verbose bool
}

func newConsoleLogger(verbose bool) *consoleLogger {
	return &consoleLogger{verbose: verbose}
}

func (l *consoleLogger) Log(ctx context.Context, level observability.Level, message string, fields ...observability.Field) {
	if !l.verbose && level < observability.LevelInfo {
		return
	}

	prefix := ""
	switch level {
	case observability.LevelTrace:
		prefix = "[TRACE] "
	case observability.LevelDebug:
		prefix = "[DEBUG] "
	case observability.LevelInfo:
		prefix = "[INFO] "
	case observability.LevelWarn:
		prefix = "[WARN] "
	case observability.LevelError:
		prefix = "[ERROR] "
	case observability.LevelFatal:
		prefix = "[FATAL] "
	}

	fmt.Fprintf(os.Stderr, "%s%s", prefix, message)
	if len(fields) > 0 {
		fmt.Fprintf(os.Stderr, " {")
		for i, f := range fields {
			if i > 0 {
				fmt.Fprintf(os.Stderr, ", ")
			}
			fmt.Fprintf(os.Stderr, "%s: %v", f.Key, f.Value)
		}
		fmt.Fprintf(os.Stderr, "}")
	}
	fmt.Fprintln(os.Stderr)
}

func (l *consoleLogger) Trace(ctx context.Context, message string, fields ...observability.Field) {
	l.Log(ctx, observability.LevelTrace, message, fields...)
}

func (l *consoleLogger) Debug(ctx context.Context, message string, fields ...observability.Field) {
	l.Log(ctx, observability.LevelDebug, message, fields...)
}

func (l *consoleLogger) Info(ctx context.Context, message string, fields ...observability.Field) {
	l.Log(ctx, observability.LevelInfo, message, fields...)
}

func (l *consoleLogger) Warn(ctx context.Context, message string, fields ...observability.Field) {
	l.Log(ctx, observability.LevelWarn, message, fields...)
}

func (l *consoleLogger) Error(ctx context.Context, message string, fields ...observability.Field) {
	l.Log(ctx, observability.LevelError, message, fields...)
}

func (l *consoleLogger) Fatal(ctx context.Context, message string, fields ...observability.Field) {
	l.Log(ctx, observability.LevelFatal, message, fields...)
	os.Exit(1)
}

func (l *consoleLogger) WithFields(fields ...observability.Field) observability.Logger {
	return l
}

func (l *consoleLogger) SetLevel(level observability.Level) {
	// Not implemented for console logger
}

// runOneShot runs the agent loop for a single prompt without persistent storage.
// This is the benchmark-friendly execution path: swarm -p '<prompt>'
//
// Environment variables:
//
//	SWARM_PROVIDER / SWARM_OVERRIDE_PROVIDER  – provider name (default: anthropic)
//	SWARM_MODEL    / SWARM_OVERRIDE_MODEL     – model name
//	ANTHROPIC_API_KEY / OPENAI_API_KEY        – provider API key
//	SWARM_SYSTEM_PROMPT                       – optional custom system prompt
//	SWARM_DEBUG_REQUESTS                      – if set, write full JSON dump to this path
func runOneShot(ctx context.Context, prompt string, logger observability.Logger) error {
	// Build config from environment variables.
	// SWARM_OVERRIDE_* takes precedence (injected by benchmark harness).
	providerName := envOrDefault("SWARM_OVERRIDE_PROVIDER", envOrDefault("SWARM_PROVIDER", "anthropic"))
	modelName := envOrDefault("SWARM_OVERRIDE_MODEL", envOrDefault("SWARM_MODEL", ""))
	apiKey := ""
	switch providerName {
	case "anthropic":
		apiKey = os.Getenv("ANTHROPIC_API_KEY")
	case "openai":
		apiKey = os.Getenv("OPENAI_API_KEY")
	}

	cfg := &Config{
		Provider:      providerName,
		Model:         modelName,
		APIKey:        apiKey,
		UseTestConfig: false,
	}

	// Create provider.
	tracer := observability.NewNoopTracer()
	prov, err := createProvider(cfg, logger, tracer)
	if err != nil {
		return fmt.Errorf("failed to create provider: %w", err)
	}

	// Create tool registry (builtin tools only; no MCP for one-shot).
	registry := createToolRegistry(logger, tracer)

	// Build in-memory message list.
	systemPrompt := envOrDefault("SWARM_SYSTEM_PROMPT", defaultOneShotSystemPrompt())
	messages := []*conversation.Message{
		{
			ID:        fmt.Sprintf("msg_%d", time.Now().UnixNano()),
			Timestamp: time.Now(),
			Role:      conversation.RoleUser,
			Content:   prompt,
		},
	}

	fmt.Printf("User: %s\n\n", prompt)
	// Micro-compactor: trims old heavy tool results when context grows large.
	// This mirrors Forge's "always-on compaction" — fires after every tool-result
	// message rather than waiting for a hard context-limit error.
	microCompactor := compaction.NewMicroCompactor()
	// microCompactThreshold: start trimming when total input tokens > 30k.
	// Adjustable via SWARM_MICRO_COMPACT_THRESHOLD (integer, tokens).
	microCompactThreshold := 30000
	if v := os.Getenv("SWARM_MICRO_COMPACT_THRESHOLD"); v != "" {
		fmt.Sscanf(v, "%d", &microCompactThreshold)
	}
	totalInputTokens := 0

	// Agent loop — runs until no more tool calls or maxIterations.
	const maxIterations = 50
	for i := 0; i < maxIterations; i++ {
		// Build tool list.
		toolList := registry.List()
		reqTools := make([]provider.Tool, len(toolList))
		for j, name := range toolList {
			t, _ := registry.Get(name)
			reqTools[j] = provider.Tool{
				Name:        t.Name(),
				Description: t.Description(),
				Parameters:  t.Parameters(),
			}
		}

		req := provider.ChatRequest{
			Messages:     messages,
			Model:        getModel(cfg),
			SystemPrompt: systemPrompt,
			Tools:        reqTools,
			Metadata:     make(map[string]any),
		}

		resp, err := prov.Chat(ctx, req)
		if err != nil {
			return fmt.Errorf("provider chat failed: %w", err)
		}

		// Append assistant message.
		messages = append(messages, resp.Message)
		// Track input token count for compaction heuristic.
		if resp.Usage != nil {
			totalInputTokens = resp.Usage.Input
		}

		if len(resp.Message.ToolCalls) == 0 {
			// Final response — print and exit loop.
			fmt.Printf("Assistant: %s\n\n", resp.Message.Content)
			if resp.Usage != nil {
				fmt.Printf("Tokens — Input: %d, Output: %d, Total: %d\n",
					resp.Usage.Input, resp.Usage.Output, resp.Usage.Total)
			}
			break
		}

		// Execute tool calls.
		fmt.Printf("Assistant requested %d tool(s):\n", len(resp.Message.ToolCalls))
		toolResults := make([]conversation.ToolResult, 0, len(resp.Message.ToolCalls))
		for _, tc := range resp.Message.ToolCalls {
			fmt.Printf("  - %s (ID: %s)\n", tc.Name, tc.ID)
			var result *tools.ToolResult
			var execErr error
			if toolImpl, getErr := registry.Get(tc.Name); getErr != nil {
				execErr = getErr
			} else {
				result, execErr = toolImpl.Execute(ctx, tc.Parameters)
			}
			if execErr != nil {
				toolResults = append(toolResults, conversation.ToolResult{
					CallID: tc.ID,
					Name:   tc.Name,
					Output: "",
					Error: &conversation.ToolError{
						Type:    "tool.execution_failed",
						Message: execErr.Error(),
					},
				})
			} else {
				fmt.Printf("    Result: %s\n", truncateString(result.Output, 100))
				toolResults = append(toolResults, conversation.ToolResult{
					CallID: tc.ID,
					Name:   tc.Name,
					Output: result.Output,
				})
			}
		}
		fmt.Println()

		// Append tool result message and continue.
		messages = append(messages, newToolResultMessage(toolResults))

		// Forge-style micro-compaction: trim old heavy tool results when context grows.
		// Fires after every tool turn (always-on), matching Forge's CompactionHandler.
		if totalInputTokens >= microCompactThreshold {
			compacted, saved := microCompactor.Process(messages)
			if saved > 0 {
				messages = compacted
				fmt.Printf("[compaction] micro-compacted %d tokens (input was %d)\n", saved, totalInputTokens)
			}
		}
	}

	// Write debug dump if requested.
	if debugPath := os.Getenv("SWARM_DEBUG_REQUESTS"); debugPath != "" {
		if err := writeDebugDump(debugPath, messages); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to write debug dump: %v\n", err)
		}
	}

	return nil
}

// cmdPeer starts an A2A-enabled headless agent (peer process).
// This command runs the agent in peer mode, enabling it to communicate
// with other agents in the swarm via the A2A protocol.
func cmdPeer(ctx context.Context, config *Config, logger observability.Logger, tracer observability.Tracer) error {
	// Validate required parameters
	if config.A2AHandle == "" {
		return fmt.Errorf("--handle is required for peer command")
	}

	// Validate and enforce timeout constraints
	// Minimum: 10 minutes, Maximum: 30 minutes
	// 0 means run until killed (no timeout)
	const (
		minTimeout = 10 * time.Minute
		maxTimeout = 30 * time.Minute
	)
	if config.PeerTimeout > 0 && config.PeerTimeout < minTimeout {
		fmt.Printf("⚠️  Timeout %s is below minimum of 10 minutes. Using 10 minutes.\n", config.PeerTimeout)
		config.PeerTimeout = minTimeout
	}
	if config.PeerTimeout > maxTimeout {
		fmt.Printf("⚠️  Timeout %s exceeds maximum of 30 minutes. Using 30 minutes.\n", config.PeerTimeout)
		config.PeerTimeout = maxTimeout
	}

	// Parse tools from comma-separated string
	if config.PeerToolsStr != "" {
		config.PeerTools = strings.Split(config.PeerToolsStr, ",")
		for i := range config.PeerTools {
			config.PeerTools[i] = strings.TrimSpace(config.PeerTools[i])
		}
	}

	logger.Info(ctx, "peer.starting",
		observability.F("handle", config.A2AHandle),
		observability.F("listen", config.A2AListenAddress),
		observability.F("tools", config.PeerTools))

	// Create provider using existing helper
	prov, err := createProvider(config, logger, tracer)
	if err != nil {
		return fmt.Errorf("failed to create provider: %w", err)
	}

	// Create storage
	storagePath := config.StoragePath
	if storagePath == "" {
		storagePath = filepath.Join(os.TempDir(), "swarm-peer", config.A2AHandle)
	}
	store, err := createStorage(storagePath)
	if err != nil {
		return fmt.Errorf("failed to create storage: %w", err)
	}

	// Create manager
	mgr, err := manager.NewManager(manager.Config{
		Storage: store,
		Logger:  logger,
		Tracer:  tracer,
	})
	if err != nil {
		return fmt.Errorf("failed to create manager: %w", err)
	}

	// Load or create conversation
	var conv *conversation.Conversation
	if config.ConversationID != "" {
		conv, err = mgr.Resume(ctx, config.ConversationID)
		if err != nil {
			return fmt.Errorf("failed to load conversation: %w", err)
		}
	} else {
		conv, err = mgr.Create(ctx, manager.CreateOptions{
			Mode: "peer",
		})
		if err != nil {
			return fmt.Errorf("failed to create conversation: %w", err)
		}
		config.ConversationID = conv.ID
	}

	// Create agent definition
	def := agent.Definition{
		ID:          config.A2AHandle,
		Name:        config.PeerName,
		Provider:    config.Provider,
		Model:       config.Model,
		Description: config.PeerDescription,
	}
	// SWA-19: stamp client type + machine ID so executions are segmentable.
	def.ClientType = "headless"
	def.MachineIDHash, _ = analytics.DeviceIDHash("swarm.sdk.v1")
	if def.Name == "" {
		def.Name = config.A2AHandle
	}
	if def.Provider == "" {
		def.Provider = "anthropic"
	}
	if def.Model == "" {
		def.Model = "claude-sonnet-4-5"
	}

	// Create tool registry using helper
	toolReg := createToolRegistry(logger, tracer)

	// Build system prompt
	systemPrompt := config.SystemPrompt
	if systemPrompt == "" {
		systemPrompt = fmt.Sprintf(`You are an AI peer agent named "%s" in a multi-agent swarm.
Your role: %s

You can receive messages from other agents and respond to them.
Use the swarm tool to communicate with other peers.
`, def.Name, def.Description)
	}

	// Create A2A config - uses global registry automatically
	a2aCfg := &a2a.Config{
		Handle:        config.A2AHandle,
		SessionID:     config.ConversationID,
		WorkspacePath: storagePath,
		ProjectID:     config.SwarmScope, // Use SwarmScope as ProjectID for scope key
		SwarmName:     config.A2ASwarm,   // Swarm name for peer discovery
		ListenAddress: config.A2AListenAddress,
		Descriptor: a2a.AgentDescriptor{
			Name:               def.Name,
			Description:        def.Description,
			DefaultInputModes:  []string{"text", "text/plain"},
			DefaultOutputModes: []string{"text", "text/plain"},
		},
	}

	// Create agent
	agt, err := agent.New(agent.Config{
		Definition:    &def,
		Provider:      prov,
		ToolRegistry:  toolReg,
		Logger:        logger,
		Tracer:        tracer,
		A2A:           a2aCfg,
		StoragePath:   storagePath,
		WorkspacePath: storagePath,
	})
	if err != nil {
		return fmt.Errorf("failed to create agent: %w", err)
	}

	// Initialize agent
	if err := agt.Initialize(); err != nil {
		return fmt.Errorf("failed to initialize agent: %w", err)
	}

	// Wire up the swarm tool communicator
	if tool, err := toolReg.Get("swarm"); err == nil {
		if swarmTool, ok := tool.(*swarmtool.SwarmTool); ok {
			communicator := NewHeadlessCommunicator(agt, config.A2AHandle, logger)
			swarmTool.SetCommunicator(communicator)
			logger.Info(ctx, "peer.swarm_tool_wired",
				observability.F("handle", config.A2AHandle))
		}
	}

	// ── Rich native attach path (serve.Mux) ──────────────────────────────────
	// Wrap this peer's agent in an SDK client and expose it over the standard
	// serve interface (POST /rpc for client.snapshot/getMessages/sendMessage,
	// GET /sse for the event stream). We advertise the resulting ServeURL on the
	// peer presence below so an attaching UI (e.g. swarm-desktop) can render this
	// headless peer's conversation natively and send it messages — not just the
	// A2A protocol endpoint, which does not speak the client.* methods.
	//
	// Best-effort: WithAgentInstance reuses the exact agent we just built, so
	// serve operates on the same execution/conversation state. A failure here
	// leaves the peer fully functional over A2A; only the rich native path is
	// unavailable.
	var serveURL string
	if cl, cerr := clientpkg.New(
		clientpkg.WithAgentInstance(agt),
		clientpkg.WithProviderInstance(prov),
		clientpkg.WithToolRegistry(toolReg),
		clientpkg.WithStorageDir(storagePath),
		clientpkg.WithWorkspace(storagePath),
		clientpkg.WithLogger(logger),
		clientpkg.WithTracer(tracer),
		clientpkg.WithoutAutoConfig(),
		clientpkg.WithClientType(clientpkg.ClientTypeHeadless),
	); cerr != nil {
		logger.Warn(ctx, "peer.serve_client_failed",
			observability.F("error", cerr.Error()))
	} else if serr := cl.Start(ctx); serr != nil {
		logger.Warn(ctx, "peer.serve_client_start_failed",
			observability.F("error", serr.Error()))
	} else {
		mux := serve.NewMux(cl)
		httpMux := http.NewServeMux()
		httpMux.Handle("/rpc", mux.HTTPHandler())
		httpMux.Handle("/sse", mux.SSEHandler())
		httpMux.Handle("/ws", mux.WebSocketHandler())
		if ln, lerr := net.Listen("tcp", "127.0.0.1:0"); lerr != nil {
			logger.Warn(ctx, "peer.serve_listen_failed",
				observability.F("error", lerr.Error()))
		} else {
			serveURL = "http://" + ln.Addr().String()
			srv := &http.Server{Handler: httpMux}
			go func() {
				if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
					logger.Warn(ctx, "peer.serve_error",
						observability.F("error", err.Error()))
				}
			}()
			defer srv.Close()
			fmt.Printf("   Serve:    %s/rpc  (events: %s/sse)\n", serveURL, serveURL)
		}
	}

	// Get A2A runtime info
	if agt.A2ARuntime() != nil {
		peer := agt.A2ARuntime().Peer()
		fmt.Printf("🌐 Peer started\n")
		fmt.Printf("   PID:      %d\n", os.Getpid())
		fmt.Printf("   Handle:   %s\n", peer.Handle)
		fmt.Printf("   Endpoint: %s\n", peer.EndpointURL)
		fmt.Printf("   Storage:  %s\n", storagePath)
		fmt.Printf("   ConvID:   %s\n", config.ConversationID)
		if config.PeerInteractive {
			fmt.Printf("   Mode:     Interactive\n")
		} else {
			fmt.Printf("   Mode:     Background (receive only)\n")
		}
		if config.PeerTimeout > 0 {
			fmt.Printf("   Timeout:  %s\n", config.PeerTimeout)
		}
		fmt.Println()

		// Also join swarm via filesystem-based discovery
		swarmName := config.A2ASwarm
		if swarmName == "" {
			swarmName = a2a.DefaultSwarmName
		}
		presence := a2a.PeerPresence{
			Handle:      config.A2AHandle,
			Name:        def.Name,
			EndpointURL: peer.EndpointURL,
			ServeURL:    serveURL,
			Workspace:   storagePath,
			Model:       config.Model,
			Status:      "idle",
			Type:        a2a.PeerTypeLocal,
		}
		if err := a2a.JoinSwarm(swarmName, presence); err != nil {
			logger.Warn(ctx, "peer.swarm_join_failed",
				observability.F("error", err.Error()))
		} else {
			logger.Info(ctx, "peer.swarm_joined",
				observability.F("swarm", swarmName),
				observability.F("handle", config.A2AHandle))
		}
	}

	// Run based on mode
	if config.PeerInteractive {
		return runPeerInteractive(ctx, agt, mgr, conv, config, logger, tracer)
	}

	return runPeerBackground(ctx, agt, config, logger)
}

// runPeerInteractive runs the peer in interactive mode, processing messages.
func runPeerInteractive(ctx context.Context, agt *agent.Agent, mgr *manager.Manager, conv *conversation.Conversation, config *Config, logger observability.Logger, tracer observability.Tracer) error {
	fmt.Println("Interactive mode not yet fully implemented. Running in receive-only mode.")
	fmt.Println("Press Ctrl+C to stop.")
	return runPeerBackground(ctx, agt, config, logger)
}

// runPeerBackground runs the peer in background mode, just receiving messages.
func runPeerBackground(ctx context.Context, agt *agent.Agent, config *Config, logger observability.Logger) error {
	// Set up signal handling
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	// Set up timeout if configured
	var timeoutChan <-chan time.Time
	if config.PeerTimeout > 0 {
		timeoutChan = time.After(config.PeerTimeout)
	}

	fmt.Println("Peer running. Press Ctrl+C to stop.")
	if config.PeerTimeout > 0 {
		fmt.Printf("Auto-shutdown in %s\n", config.PeerTimeout)
	}

	// Wait for shutdown signal or timeout
	select {
	case <-sigChan:
		fmt.Println("\nShutting down peer...")
	case <-timeoutChan:
		fmt.Println("\nTimeout reached, shutting down...")
	case <-ctx.Done():
		fmt.Println("\nContext cancelled, shutting down...")
	}

	// Clean shutdown
	if err := agt.Close(); err != nil {
		return fmt.Errorf("error closing agent: %w", err)
	}

	return nil
}

// defaultOneShotSystemPrompt returns the system prompt used for one-shot mode.
// Equivalent to Forge's default prompt: workspace-aware, tool-rule-enforcing.
func defaultOneShotSystemPrompt() string {
	cwd, _ := os.Getwd()
	return fmt.Sprintf(`You are an AI coding assistant operating in a workspace.
Working directory: %s

Tool usage rules:
- Read files with file_read, not bash cat/head/tail.
- Search files with the grep tool, not bash grep/find.
- Explore directories with list_dir, not bash ls/find/tree.
- Edit existing files with str_replace, not file_write.
- Create new files with file_write.
- Use the cwd parameter instead of cd commands.
- Use task tools for multi-step work requiring 3 or more distinct steps.`, cwd)
}

// envOrDefault returns the value of an env var or a default if it's empty.
func envOrDefault(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}

// debugMessage is the JSON-serialisable form of a conversation message.
// It matches the OpenAI wire format so existing jq queries work against it.
type debugMessage struct {
	Role       string          `json:"role"`
	Content    string          `json:"content,omitempty"`
	ToolCalls  []debugToolCall `json:"tool_calls,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
	Name       string          `json:"name,omitempty"`
}

type debugToolCall struct {
	ID       string            `json:"id"`
	Type     string            `json:"type"`
	Function debugToolFunction `json:"function"`
}

type debugToolFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// writeDebugDump serialises messages into a jq-queryable JSON file.
// Format mirrors Forge's FORGE_DEBUG_REQUESTS output so the same tbench
// jq validations work unchanged.
func writeDebugDump(path string, messages []*conversation.Message) error {
	out := struct {
		Messages []debugMessage `json:"messages"`
	}{}

	for _, m := range messages {
		switch {
		case m.Role == conversation.RoleAssistant:
			dm := debugMessage{Role: "assistant", Content: m.Content}
			for _, tc := range m.ToolCalls {
				argsJSON, _ := json.Marshal(tc.Parameters)
				dm.ToolCalls = append(dm.ToolCalls, debugToolCall{
					ID:   tc.ID,
					Type: "function",
					Function: debugToolFunction{
						Name:      tc.Name,
						Arguments: string(argsJSON),
					},
				})
			}
			out.Messages = append(out.Messages, dm)

		case len(m.ToolResults) > 0:
			// Tool result messages — one debug message per result.
			for _, tr := range m.ToolResults {
				out.Messages = append(out.Messages, debugMessage{
					Role:       "tool",
					Content:    tr.Output,
					ToolCallID: tr.CallID,
					Name:       tr.Name,
				})
			}

		default:
			out.Messages = append(out.Messages, debugMessage{
				Role:    string(m.Role),
				Content: m.Content,
			})
		}
	}

	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// HeadlessCommunicator implements the swarm.SwarmCommunicator interface for the headless peer.
// It connects the swarm tool to the A2A runtime for peer-to-peer communication.
type HeadlessCommunicator struct {
	agent  *agent.Agent
	handle string
	status string
	task   string
	logger observability.Logger
}

// NewHeadlessCommunicator creates a new communicator for the headless peer.
func NewHeadlessCommunicator(agt *agent.Agent, handle string, logger observability.Logger) *HeadlessCommunicator {
	return &HeadlessCommunicator{
		agent:  agt,
		handle: handle,
		status: "idle",
		logger: logger,
	}
}

// IsA2AEnabled returns true if A2A runtime is available.
func (h *HeadlessCommunicator) IsA2AEnabled() bool {
	return h.agent != nil && h.agent.A2ARuntime() != nil
}

// SendMessage sends a direct message to a specific peer.
func (h *HeadlessCommunicator) SendMessage(ctx context.Context, targetHandle, message string) error {
	if !h.IsA2AEnabled() {
		return fmt.Errorf("A2A not enabled")
	}
	h.logger.Info(ctx, "headless.send_message",
		observability.F("target", targetHandle),
		observability.F("handle", h.handle))
	_, err := h.agent.A2ARuntime().SendDM(ctx, targetHandle, message)
	return err
}

// Broadcast sends a message to all peers in the swarm.
func (h *HeadlessCommunicator) Broadcast(ctx context.Context, message string) error {
	if !h.IsA2AEnabled() {
		return fmt.Errorf("A2A not enabled")
	}
	h.logger.Info(ctx, "headless.broadcast",
		observability.F("handle", h.handle))
	_, err := h.agent.A2ARuntime().Broadcast(ctx, message)
	return err
}

// ListSwarmPeers lists all peers in the swarm.
func (h *HeadlessCommunicator) ListSwarmPeers(ctx context.Context) ([]swarmtool.PeerStatus, error) {
	if !h.IsA2AEnabled() {
		return nil, fmt.Errorf("A2A not enabled")
	}
	result, err := h.agent.A2ARuntime().ListPeers(ctx)
	if err != nil {
		return nil, err
	}
	peers := make([]swarmtool.PeerStatus, 0, len(result.Peers))
	for _, p := range result.Peers {
		peers = append(peers, swarmtool.PeerStatus{
			Handle:      p.Handle,
			Name:        p.Model, // Use model as name
			Status:      string(p.Status),
			CurrentTask: p.CurrentTask,
			LastSeen:    p.RunningTime,
			Endpoint:    p.Endpoint,
		})
	}
	return peers, nil
}

// UpdateStatus updates the local peer's status in the swarm.
func (h *HeadlessCommunicator) UpdateStatus(ctx context.Context, status, currentTask string) error {
	if !h.IsA2AEnabled() {
		return fmt.Errorf("A2A not enabled")
	}
	h.status = status
	h.task = currentTask
	var a2aStatus a2a.SwarmStatus
	switch status {
	case "idle":
		a2aStatus = a2a.SwarmStatusIdle
	case "working":
		a2aStatus = a2a.SwarmStatusWorking
	case "busy":
		a2aStatus = a2a.SwarmStatusBusy
	case "away":
		a2aStatus = a2a.SwarmStatusAway
	default:
		a2aStatus = a2a.SwarmStatusIdle
	}
	_, err := h.agent.A2ARuntime().UpdateStatus(ctx, a2aStatus, currentTask)
	return err
}

// SyncTasks syncs completed tasks with the swarm.
func (h *HeadlessCommunicator) SyncTasks(ctx context.Context, completedTasks []string) error {
	h.logger.Info(ctx, "headless.sync_tasks",
		observability.F("count", len(completedTasks)))
	return nil
}

// GetLocalStatus returns the local peer's status.
func (h *HeadlessCommunicator) GetLocalStatus() swarmtool.AgentStatus {
	return swarmtool.AgentStatus{
		Handle:         h.handle,
		Status:         h.status,
		CurrentTask:    h.task,
		CompletedTasks: nil,
	}
}

// GetLocalHandle returns the local peer's handle.
func (h *HeadlessCommunicator) GetLocalHandle() string {
	return h.handle
}

// CreatePeer creates a new A2A peer (not supported in headless mode).
func (h *HeadlessCommunicator) CreatePeer(ctx context.Context, config swarmtool.PeerConfig) (*swarmtool.PeerInfo, error) {
	return nil, fmt.Errorf("CreatePeer not supported in headless mode - use TUI to spawn peers")
}

// JoinSwarm joins the swarm with the given handle (already joined via A2A config).
func (h *HeadlessCommunicator) JoinSwarm(ctx context.Context, handle, name string) error {
	h.handle = handle
	h.logger.Info(ctx, "headless.join_swarm",
		observability.F("handle", handle))
	return nil
}
