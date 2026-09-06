# Headless Test Client

A command-line interface for testing the SwarmOS SDK. This client demonstrates core functionality including conversation management, resuming, branching, tool execution, and MCP integration.

## Features

- ✅ **Conversation Management**: Create, resume, list, and delete conversations
- ✅ **Message Exchange**: Send messages and receive AI responses
- ✅ **Conversation Branching**: Fork conversations at any message point
- ✅ **Export/Import**: Export conversations in multiple formats (JSON, Markdown, HTML, JSONL)
- ✅ **Tool Integration**: Built-in tools (FileRead, FileWrite, Bash, Grep)
- ✅ **MCP Support**: Connect to MCP servers for extended functionality
- ✅ **Multi-Provider**: Support for Anthropic Claude and OpenAI GPT models
- ✅ **Observability**: Full logging and tracing support

## Installation

```bash
# From the SDK root
cd cmd/headless
go build -o headless

# Or use make
make build-headless
```

## Quick Start

### 1. Set up API keys

```bash
# For Anthropic
export ANTHROPIC_API_KEY="your-api-key"

# For OpenAI
export OPENAI_API_KEY="your-api-key"
```

### 2. Create a conversation

```bash
./headless new -mode interactive
# Output: Created conversation: conv_1733108400123456789
```

### 3. Send a message

```bash
./headless send -id conv_1733108400123456789 -message "What is the capital of France?"
```

### 4. Resume conversation

```bash
./headless resume -id conv_1733108400123456789
```

## Command Reference

### `new` - Create New Conversation

Create a new conversation with specified mode.

```bash
headless new [options]

Options:
  -mode string         Conversation mode (default: "interactive")
  -provider string     Provider to use: anthropic, openai (default: "anthropic")
  -model string        Model to use (uses provider default if not specified)
  -storage string      Path to conversation storage (default: ".headless_conversations")
  -verbose            Enable verbose logging
```

**Examples:**
```bash
# Create with default settings
./headless new

# Create with specific provider and model
./headless new -provider openai -model gpt-4

# Create with custom mode
./headless new -mode code_review
```

### `resume` - Resume Conversation

Resume an existing conversation and view its details.

```bash
headless resume -id <conversation-id> [options]

Options:
  -id string          Conversation ID (required)
  -storage string     Path to conversation storage
  -verbose           Enable verbose logging
```

**Examples:**
```bash
# Resume and view conversation details
./headless resume -id conv_1733108400123456789

# Output shows:
# - Conversation metadata
# - Message count
# - Token usage
# - Recent messages (last 5)
```

### `list` - List Conversations

List all stored conversations.

```bash
headless list [options]

Options:
  -storage string     Path to conversation storage
  -verbose           Enable verbose logging
```

**Examples:**
```bash
./headless list
```

### `send` - Send Message

Send a message to an existing conversation and get AI response.

```bash
headless send -id <conversation-id> [options]

Options:
  -id string           Conversation ID (required)
  -message string      Message to send
  -file string         Read message from file
  -provider string     Provider to use (default: "anthropic")
  -model string        Model to use
  -api-key string      API key (or use environment variable)
  -storage string      Path to conversation storage
  -verbose            Enable verbose logging
```

**Examples:**
```bash
# Send inline message
./headless send -id conv_123 -message "Explain quantum computing"

# Send message from file
./headless send -id conv_123 -file prompt.txt

# Use specific model
./headless send -id conv_123 -message "Hello" -model claude-3-opus-20240229
```

### `export` - Export Conversation

Export a conversation in various formats.

```bash
headless export -id <conversation-id> [options]

Options:
  -id string          Conversation ID (required)
  -format string      Export format: json, markdown, html, jsonl (default: "json")
  -output string      Output file (prints to stdout if not specified)
  -storage string     Path to conversation storage
  -verbose           Enable verbose logging
```

**Examples:**
```bash
# Export to JSON (stdout)
./headless export -id conv_123 -format json

# Export to file
./headless export -id conv_123 -format markdown -output conversation.md

# Export to HTML
./headless export -id conv_123 -format html -output conversation.html

# Export to JSONL (streaming format)
./headless export -id conv_123 -format jsonl -output messages.jsonl
```

### `branch` - Branch Conversation

Create a new conversation branching from an existing one at a specific message.

```bash
headless branch -id <conversation-id> -at <message-index> [options]

Options:
  -id string          Conversation ID (required)
  -at int            Branch at message index, 0-based (required)
  -storage string     Path to conversation storage
  -verbose           Enable verbose logging
```

**Examples:**
```bash
# Branch at message 3
./headless branch -id conv_123 -at 3

# Output:
# Branched conversation at message 3
# Original: conv_123
# Branch:   conv_456
# Copied 4 message(s)
```

**Use Cases:**
- Test different conversation paths
- Explore alternative responses
- Debug specific conversation states
- A/B testing prompts

### `tools` - List Available Tools

List all registered tools and their capabilities.

```bash
headless tools [options]

Options:
  -category string    Filter by category
  -verbose           Enable verbose logging
```

**Examples:**
```bash
# List all tools
./headless tools

# Output shows:
# - Tool name
# - Description
# - Idempotent status
# - Required permissions
```

### `mcp` - Connect to MCP Server

Connect to an MCP (Model Context Protocol) server and list available tools.

```bash
headless mcp -server <name> -command <cmd> [options]

Options:
  -server string      MCP server name (required)
  -command string     MCP server command (required)
  -args string        Comma-separated arguments
  -verbose           Enable verbose logging
```

**Examples:**
```bash
# Connect to filesystem server
./headless mcp \
  -server filesystem \
  -command npx \
  -args "@modelcontextprotocol/server-filesystem,/tmp"

# Connect to GitHub server
./headless mcp \
  -server github \
  -command mcp-server-github \
  -args "--token,${GITHUB_TOKEN}"
```

## Workflows

### Complete Conversation Flow

```bash
# 1. Create conversation
CONV_ID=$(./headless new -mode interactive | grep "Created conversation:" | awk '{print $3}')

# 2. Send initial message
./headless send -id $CONV_ID -message "What are the benefits of Go?"

# 3. Continue conversation
./headless send -id $CONV_ID -message "Can you give me a code example?"

# 4. Export for review
./headless export -id $CONV_ID -format markdown -output review.md

# 5. View conversation details
./headless resume -id $CONV_ID
```

### Conversation Branching Workflow

```bash
# 1. Create and populate conversation
CONV_ID=$(./headless new | grep "Created" | awk '{print $3}')
./headless send -id $CONV_ID -message "Explain async/await"
./headless send -id $CONV_ID -message "Show me an example"

# 2. Branch at different points to explore alternatives
BRANCH_1=$(./headless branch -id $CONV_ID -at 0 | grep "Branch:" | awk '{print $2}')
BRANCH_2=$(./headless branch -id $CONV_ID -at 1 | grep "Branch:" | awk '{print $2}')

# 3. Continue each branch differently
./headless send -id $BRANCH_1 -message "Show async/await in Python"
./headless send -id $BRANCH_2 -message "Show async/await in JavaScript"

# 4. Compare results
./headless export -id $BRANCH_1 -format json -output branch1.json
./headless export -id $BRANCH_2 -format json -output branch2.json
```

### Testing with Different Providers

```bash
# Test same prompt with different providers
PROMPT="Explain the CAP theorem"

# Anthropic Claude
CONV_CLAUDE=$(./headless new -provider anthropic | grep "Created" | awk '{print $3}')
./headless send -id $CONV_CLAUDE -message "$PROMPT"

# OpenAI GPT
CONV_GPT=$(./headless new -provider openai | grep "Created" | awk '{print $3}')
./headless send -id $CONV_GPT -message "$PROMPT"

# Compare responses
diff <(./headless export -id $CONV_CLAUDE -format markdown) \
     <(./headless export -id $CONV_GPT -format markdown)
```

## Configuration

### Storage

By default, conversations are stored in memory (`.headless_conversations`). You can specify a custom path:

```bash
./headless new -storage /path/to/custom/storage
```

### Provider Configuration

Set default provider and model:

```bash
# Use Anthropic's Claude Sonnet
./headless send -id $CONV -message "Hello" -provider anthropic -model claude-3-5-sonnet-20241022

# Use OpenAI's GPT-4
./headless send -id $CONV -message "Hello" -provider openai -model gpt-4
```

### API Keys

API keys can be provided via:
1. Environment variables (recommended)
2. Command-line flags (for testing)

```bash
# Environment variables
export ANTHROPIC_API_KEY="sk-ant-..."
export OPENAI_API_KEY="sk-..."

# Or via flags (not recommended for production)
./headless send -id $CONV -message "Hello" -api-key "sk-ant-..."
```

## Architecture

The headless client demonstrates the SDK's Ring architecture:

- **Ring 0**: Core interfaces (Provider, Tool, Storage)
- **Ring 1**: Implementations (Anthropic, OpenAI, MemoryStorage)
- **Ring 2**: Agent runtime (conversation management, tool execution)
- **Ring 4**: Integration layer (this CLI)

```
CLI Command → Manager → Provider → API
                  ↓
            Storage ← Conversation
                  ↓
            Tools → Registry
```

## Troubleshooting

### "API key required"

Set the appropriate environment variable:
```bash
export ANTHROPIC_API_KEY="your-key"
# or
export OPENAI_API_KEY="your-key"
```

### "Conversation not found"

Check available conversations:
```bash
./headless list
```

### "Tool not found"

List available tools:
```bash
./headless tools
```

### Verbose Logging

Enable verbose mode to see detailed execution:
```bash
./headless send -id $CONV -message "Test" -verbose
```

## Examples

See the `examples/` directory for:
- Complete conversation scripts
- MCP integration examples
- Branching workflows
- Multi-provider testing

## Contributing

When adding new features to the headless client:

1. Follow the SDK's coding conventions (see `AGENTS.md`)
2. Add tests for new commands
3. Update this README with new functionality
4. Use TDD approach (write tests first)

## License

See the SDK root LICENSE file.
