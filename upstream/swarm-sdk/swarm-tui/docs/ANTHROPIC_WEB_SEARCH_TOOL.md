# Anthropic Web Search Tool - Local Implementation

## Overview

This is a **local, OAuth-authenticated** web search tool that replaces the Claude Code managed `swarm_anthropic_web_search` tool. It uses **server-side web search** powered by Anthropic's API with the `web-search-2025-03-05` beta feature.

## Key Differences from `swarm_anthropic_web_search`

| Feature | Local Tool | Claude Code Tool |
|---------|-----------|------------------|
| **Authentication** | OAuth Bearer token from `~/.swarmos/oauth.json` | Claude Code managed OAuth |
| **Management** | Fully local, under your control | Managed by Anthropic's Claude Code infrastructure |
| **Web Search Method** | Server-side API beta feature | Claude Code infrastructure |
| **API Headers** | Proper Bearer auth + OAuth headers | Claude Code headers |
| **Token Handling** | Automatic refresh from local storage | Claude Code managed |
| **Customization** | Full source control | No customization available |

## Architecture

### How Server-Side Web Search Works

The web search tool uses **Anthropic's server-side web search feature** enabled by the `web-search-2025-03-05` beta flag:

```
┌─────────────────────────────────────────────────────┐
│  Web Search Tool (anthropic_web_search.Tool)        │
│  - Validates OAuth token                            │
│  - Builds API request with:                         │
│    • Authorization: Bearer {token}                  │
│    • anthropic-beta: web-search-2025-03-05          │
│    • Simple search query message                    │
└────────────────┬────────────────────────────────────┘
                 │
                 ▼
┌─────────────────────────────────────────────────────┐
│  Anthropic Messages API (v1/messages)               │
│  - Receives message with web search beta enabled   │
│  - **SERVER** performs web search automatically     │
│  - Returns results in usage.server_tool_use field   │
└────────────────┬────────────────────────────────────┘
                 │
                 ▼
┌─────────────────────────────────────────────────────┐
│  Tool Result                                        │
│  - Text content with search results                 │
│  - Metadata: web_search_requests count              │
│  - Token usage statistics                           │
└─────────────────────────────────────────────────────┘
```

### Key Discovery from Reverse Engineering

From the Claude Code v2.1.25 binary analysis:
- Web search is **NOT** a client-side tool that spawns agents
- Web search IS a **SERVER-SIDE tool** enabled via the `web-search-2025-03-05` beta flag
- When enabled, the API automatically handles web search execution
- Results are returned via `usage.server_tool_use.web_search_requests` field

## Authentication

The tool uses OAuth tokens from Claude Code stored at `~/.swarmos/oauth.json`:

```json
{
  "token": {
    "access_token": "sk-ant-oat01-...",
    "token_type": "Bearer",
    "expires_in": 28800,
    "refresh_token": "sk-ant-ort01-...",
    "scope": "user:inference user:profile",
    "expiry": 1769832315
  }
}
```

### Authentication Headers

The tool sets the correct OAuth authentication headers:

```go
// For OAuth tokens:
Authorization: Bearer {token.AccessToken}
anthropic-beta: oauth-2025-04-20,web-search-2025-03-05
x-app: cli
```

### Token Refresh

Tokens are automatically refreshed if expired before making API calls.

## Installation

### 1. Build the Binary

```bash
cd /home/swarm/SwarmCode/TUI
go build -o bin/anthropic-web-search ./cmd/anthropic-web-search
```

### 2. Ensure OAuth is Configured

```bash
# Check if you're logged in
if [ -f ~/.swarmos/oauth.json ]; then
    echo "✓ OAuth token found"
else
    echo "✗ OAuth not configured"
    # Run in Claude Code or use: claude login
fi
```

### 3. Test the Tool

```bash
# Test if authentication is working
./bin/anthropic-web-search --socket /tmp/test.sock

# In another terminal, test the MCP server (if registered)
# The server listens on a Unix socket for MCP client connections
```

## Usage

### As an SDK Tool

```go
import "github.com/Swarm-Code/mono/swarm-sdk/tools/anthropic_web_search"

// Create the tool
tool := anthropic_web_search.New()

// Check if auth is configured
if !anthropic_web_search.IsAuthConfigured() {
    log.Fatal("OAuth not configured")
}

// Execute a search
result, err := tool.Execute(ctx, map[string]interface{}{
    "query":       "Go programming language features 2025",
    "max_results": 10,
})

// Access results
fmt.Println(result.Output)
fmt.Println(result.Metadata)
```

### As an MCP Server

The tool can be registered as an MCP (Model Context Protocol) server:

```bash
# Register with Claude Code
./scripts/register-web-search-mcp.sh

# Or manually:
claude mcp add \
    --name "anthropic-web-search" \
    --type "stdio" \
    --command "/full/path/to/bin/anthropic-web-search"
```

## API Request Example

When you search for "Go programming language":

```json
{
  "model": "claude-sonnet-4-20250514",
  "max_tokens": 4096,
  "system": "You are Swarm...",
  "messages": [
    {
      "role": "user",
      "content": "Search the web for: Go programming language"
    }
  ]
}
```

**Request Headers:**
```
Authorization: Bearer sk-ant-oat01-...
Content-Type: application/json
anthropic-version: 2023-06-01
anthropic-beta: claude-code-20250219,interleaved-thinking-2025-05-14,oauth-2025-04-20,web-search-2025-03-05
x-app: cli
```

**Response includes:**
```json
{
  "usage": {
    "input_tokens": 150,
    "output_tokens": 450,
    "server_tool_use": {
      "web_search_requests": 3
    }
  },
  "content": [
    {
      "type": "text",
      "text": "Here are the latest web search results for Go programming language..."
    }
  ]
}
```

## Configuration

### WebSearchConfig

```go
type WebSearchConfig struct {
    Enabled            bool          // Enable/disable web search
    MaxResultsPerQuery int           // Max results per search
    Timeout            time.Duration // Request timeout
    MaxPerSession      int           // Max searches per session
    MaxPerMinute       int           // Rate limit per minute
}

// Default:
// - Enabled: true
// - MaxResultsPerQuery: 10
// - Timeout: 30s
// - MaxPerSession: 100
// - MaxPerMinute: 10
```

## Files

```
sdk/tools/anthropic_web_search/
├── tool.go              # Main Tool implementation
├── web_search.go        # API client with web search support
├── mcp_server.go        # MCP server wrapper
├── registry.go          # Auth checks and tool registration
├── stats.go             # Usage statistics tracking
└── tool_test.go         # Tests

cmd/anthropic-web-search/
└── main.go              # CLI entry point for MCP server

scripts/
└── register-web-search-mcp.sh  # Registration script
```

## Commits

- `ca30597`: Add OAuth Bearer token authentication support for web search tool
- `d7da5c5`: Fix web search tool to use server-side web search API correctly

## Troubleshooting

### "OAuth authentication is not configured"

**Problem:** The tool can't find your OAuth token.

**Solution:**
```bash
# Check if you're logged in
ls -la ~/.swarmos/oauth.json

# If not found, authenticate
claude login
```

### "API error 401"

**Problem:** Bearer token authentication is failing.

**Solution:**
- Verify the token hasn't expired:
  ```bash
  python3 -c "
  import json, time
  with open('~/.swarmos/oauth.json') as f:
      data = json.load(f)
      expiry = data['token']['expiry']
      now = int(time.time())
      print(f'Expires in {expiry - now} seconds')
  "
  ```
- Refresh the token:
  ```bash
  claude login
  ```

### "web search request failed"

**Problem:** The API call failed.

**Possible causes:**
1. OAuth token expired
2. Network connectivity issue
3. Anthropic API unavailable
4. Rate limit exceeded

**Debug:**
```bash
# Check token validity
python3 -c "
import json
with open('~/.swarmos/oauth.json') as f:
    token = json.load(f)['token']
    print(f'Token valid: {bool(token[\"access_token\"])}')
    print(f'Token type: {token[\"token_type\"]}')
    print(f'Scope: {token[\"scope\"]}')
"

# Check network
curl -H "Authorization: Bearer \$(grep access_token ~/.swarmos/oauth.json | cut -d'\"' -f4)" \
  https://api.anthropic.com/v1/models \
  -H "anthropic-version: 2023-06-01"
```

## Related Documentation

- [WEB_SEARCH_TOOL_REVERSE_ENGINEERING.md](../docs/analysis-reports/WEB_SEARCH_TOOL_REVERSE_ENGINEERING.md) - Binary analysis details
- [OAuth Authentication Plan](../docs/aispam/CLAUDE_CODE_OAUTH_PLAN.md) - OAuth implementation
- [SDK Provider Documentation](../provider/anthropic/README.md) - Anthropic provider details
