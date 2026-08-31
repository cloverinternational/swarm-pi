package main

import (
	"context"
	"fmt"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation/manager"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
)

// cmdTest provides an interactive testing workflow for configurations
func cmdTest(ctx context.Context, config *Config, logger observability.Logger, tracer observability.Tracer) error {
	fmt.Print(`
╔══════════════════════════════════════════════════════════════════════════╗
║                    SwarmOS Configuration Tester                          ║
║                   Test providers, models, MCPs & more                    ║
╚══════════════════════════════════════════════════════════════════════════╝

This command helps you test your SwarmOS configuration by creating a
temporary conversation and sending a test message.

Usage Patterns:

1. Test Current Configuration:
   headless test
   → Uses your active profile and default provider/model

2. Test Specific Provider:
   headless test --provider anthropic
   headless test --provider openai --model gpt-4-turbo

3. Test with MCP Servers:
   headless test --mcp-servers "npx -y @modelcontextprotocol/server-filesystem /tmp"

4. Test with Features:
   headless test --thinking --cache --citations

5. Quick Test Message:
   headless test --message "What is 2+2?"

────────────────────────────────────────────────────────────────────────────

Step-by-Step Testing Guide:

Step 1: Configure what you want to test
────────────────────────────────────────

  # Test a new provider
  headless config providers add openrouter \
    --api-type openai-compatible \
    --base-url https://openrouter.ai/api/v1

  # Add a model
  headless config models add openrouter deepseek/deepseek-chat \
    --name "DeepSeek Chat"

  # Set up MCP server
  headless config mcp add filesystem npx \
    -y @modelcontextprotocol/server-filesystem /tmp

  # Create a test profile
  headless config profiles create test-profile \
    --description "Testing configuration" \
    --tools "FileRead,Bash,Grep"

Step 2: Test the configuration
───────────────────────────────

  # Test with explicit provider/model
  headless test --provider openrouter --model deepseek/deepseek-chat

  # Test with profile
  headless config profiles use test-profile
  headless test

  # Test with custom message
  headless test --message "List files in /tmp using MCP"

Step 3: Interactive testing
────────────────────────────

  # Start a test conversation
  CONV_ID=$(headless new --provider openrouter | grep "Created" | awk '{print $3}')

  # Send test messages
  headless send -id $CONV_ID -message "Hello, testing provider"
  headless send -id $CONV_ID -message "Can you read files?"
  headless send -id $CONV_ID -message "Execute: echo 'test'"

  # Test with MCP
  headless send -id $CONV_ID \
    -mcp-servers "npx -y @modelcontextprotocol/server-filesystem /tmp" \
    -message "List files in /tmp"

────────────────────────────────────────────────────────────────────────────

Common Testing Scenarios:

🧪 Test New Provider
  headless config providers add cerebras --api-type openai-compatible \
    --base-url https://api.cerebras.ai/v1
  headless config models add cerebras llama-3.3-70b
  headless test --provider cerebras --model llama-3.3-70b \
    --message "Tell me a joke"

🔧 Test MCP Server
  headless config mcp add github npx -y @modelcontextprotocol/server-github
  headless test --mcp-servers "npx -y @modelcontextprotocol/server-github" \
    --message "Search GitHub for 'anthropic/claude-code'"

🎯 Test Thinking Feature
  headless test --provider anthropic \
    --model claude-opus-4.5-20250514 \
    --thinking --thinking-budget 5000 \
    --message "Solve this: What is the 100th prime number?"

📦 Test Multiple Features Together
  headless test --provider anthropic \
    --cache --thinking --citations \
    --mcp-servers "npx -y @modelcontextprotocol/server-filesystem /tmp" \
    --message "Read /tmp/test.txt and summarize it"

🔐 Test Permissions
  headless config permissions level always_ask
  headless test --message "Write 'test' to /tmp/test.txt"
  # You'll be prompted for permission

────────────────────────────────────────────────────────────────────────────

Debugging Tips:

1. Check Configuration:
   headless config show
   headless config providers list
   headless config mcp list

2. Verbose Mode:
   headless test --verbose --message "Test message"

3. Check Logs:
   tail -f ~/.swarm/logs/headless.log

4. Verify API Keys:
   headless config get defaultProvider
   env | grep API_KEY

5. List Available Tools:
   headless config tools list

────────────────────────────────────────────────────────────────────────────

Quick Start Testing:

# 1. Test default setup
headless test --message "Hello, world!"

# 2. Test with thinking
headless test --thinking --message "Explain quantum computing"

# 3. Test file operations
headless test --message "Read the contents of $(pwd)/README.md"

# 4. Test bash execution
headless test --message "Execute: ls -la /tmp"

# 5. Test MCP filesystem
headless config mcp add fs npx -y @modelcontextprotocol/server-filesystem /tmp
headless test --mcp-servers "npx -y @modelcontextprotocol/server-filesystem /tmp" \
  --message "List all files in /tmp"

────────────────────────────────────────────────────────────────────────────

For more help:
  headless help           - General help
  headless help-ai        - AI-powered interactive help
  headless config --help  - Configuration help

Documentation:
  ./sdk/cmd/headless/CONFIG_GUIDE.md   - Complete configuration guide
  ./sdk/cmd/headless/README.md         - Headless command reference
`)

	// If message provided, run actual test
	if config.Message != "" {
		fmt.Print("\n🧪 Running test with provided message...\n\n")

		// Create test conversation
		fmt.Println("Creating test conversation...")
		store, err := createStorage(config.StoragePath)
		if err != nil {
			return fmt.Errorf("failed to create storage: %w", err)
		}

		mgr, err := manager.NewManager(manager.Config{
			Storage: store,
			Logger:  logger,
			Tracer:  tracer,
		})
		if err != nil {
			return fmt.Errorf("failed to create manager: %w", err)
		}

		conv, err := mgr.Create(ctx, manager.CreateOptions{
			Mode: "interactive",
		})
		if err != nil {
			return fmt.Errorf("failed to create conversation: %w", err)
		}

		fmt.Printf("✓ Created test conversation: %s\n\n", conv.ID)

		// Send test message using existing send logic
		config.ConversationID = conv.ID
		fmt.Printf("Sending test message: %q\n\n", config.Message)

		return cmdSend(ctx, config, logger, tracer)
	}

	return nil
}

// cmdHelpAI provides AI-powered interactive help
func cmdHelpAI(ctx context.Context, config *Config, logger observability.Logger, tracer observability.Tracer) error {
	fmt.Print(`
╔══════════════════════════════════════════════════════════════════════════╗
║                  SwarmOS AI-Powered Help System                          ║
║              Get personalized help for your configuration                ║
╚══════════════════════════════════════════════════════════════════════════╝

This creates an interactive AI conversation to help you with SwarmOS
configuration, testing, and troubleshooting.

Quick Start:
───────────

  1. Start AI help session:
     headless help-ai

  2. This will create a conversation with an AI assistant that knows
     about SwarmOS configuration and can guide you interactively.

What Can AI Help With?
──────────────────────

✓ Setting up providers (Anthropic, OpenAI, Cerebras, etc.)
✓ Configuring models and their capabilities
✓ Creating and managing profiles
✓ Setting up MCP servers
✓ Debugging connection issues
✓ Understanding permission systems
✓ Testing configurations
✓ Workflow recommendations
✓ Best practices

Example Conversations:
─────────────────────

"How do I add OpenRouter as a provider?"
"Help me set up an MCP server for GitHub"
"My API calls are failing, how do I debug?"
"What's the best profile setup for code review?"
"How do I test if my MCP server is working?"
"Configure permissions to always ask for bash commands"
"Show me how to use thinking with Claude Opus"

────────────────────────────────────────────────────────────────────────────
`)

	// Create a conversation with AI help
	fmt.Print("🤖 Starting AI help session...\n\n")

	store, err := createStorage(config.StoragePath)
	if err != nil {
		return fmt.Errorf("failed to create storage: %w", err)
	}

	mgr, err := manager.NewManager(manager.Config{
		Storage: store,
		Logger:  logger,
		Tracer:  tracer,
	})
	if err != nil {
		return fmt.Errorf("failed to create manager: %w", err)
	}

	conv, err := mgr.Create(ctx, manager.CreateOptions{
		Mode: "interactive",
	})
	if err != nil {
		return fmt.Errorf("failed to create conversation: %w", err)
	}

	fmt.Printf("✓ Created AI help conversation: %s\n\n", conv.ID)

	// Send initial help message
	config.ConversationID = conv.ID
	config.Message = `I need help with SwarmOS headless configuration. I want to learn how to:
- Set up and test different AI providers
- Configure models and their capabilities  
- Set up MCP servers for extended functionality
- Create profiles for different workflows
- Test my configurations

Can you guide me through these topics interactively? Ask me what I want to start with.`

	fmt.Print("Sending initial help request to AI...\n\n")
	fmt.Print("───────────────────────────────────────────────────────────────────────────\n\n")

	if err := cmdSend(ctx, config, logger, tracer); err != nil {
		return err
	}

	fmt.Println("\n───────────────────────────────────────────────────────────────────────────")
	fmt.Printf(`
Continue the conversation with:

  headless send -id %s -message "Your question here"

Or test something the AI suggests:

  headless test --message "Your test message"

End the help session by simply stopping (Ctrl+C) or:

  headless export -id %s -format markdown -output ai-help-session.md
`, conv.ID, conv.ID)

	return nil
}
