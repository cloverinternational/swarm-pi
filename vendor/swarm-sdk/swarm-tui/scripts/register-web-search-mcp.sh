#!/bin/bash

# Register the anthropic-web-search MCP server with Claude Code
# This replaces the Claude Code managed swarm_anthropic_web_search tool
# with our local OAuth-authenticated version

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Paths
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
BINARY_PATH="$PROJECT_ROOT/bin/anthropic-web-search"
CLAUDE_CONFIG="$HOME/.claude.json"

echo "================================================"
echo "Anthropic Web Search MCP Server Registration"
echo "================================================"
echo ""

# Check if binary exists, build if not
if [ ! -f "$BINARY_PATH" ]; then
    echo -e "${YELLOW}Building anthropic-web-search binary...${NC}"
    mkdir -p "$(dirname "$BINARY_PATH")"
    cd "$PROJECT_ROOT"
    go build -o "$BINARY_PATH" ./cmd/anthropic-web-search
    echo -e "${GREEN}✓ Binary built successfully${NC}"
else
    echo -e "${GREEN}✓ Binary already exists${NC}"
fi

# Check if Claude Code is installed
if [ ! -f "$CLAUDE_CONFIG" ]; then
    echo -e "${RED}✗ Claude Code not found. Please run 'claude login' first.${NC}"
    exit 1
fi

echo ""
echo "Registering MCP server with Claude Code..."
echo ""

# Use the claude CLI to register the MCP server
# The binary will be run with stdio transport, which means Claude Code
# will communicate with it using JSON-RPC over stdin/stdout

claude mcp add \
    --name "anthropic-web-search" \
    --type "stdio" \
    --command "$BINARY_PATH"

if [ $? -eq 0 ]; then
    echo ""
    echo -e "${GREEN}✓ MCP server registered successfully!${NC}"
    echo ""
    echo "The anthropic-web-search tool is now available in Claude Code."
    echo "This is a local, OAuth-authenticated alternative to swarm_anthropic_web_search."
    echo ""
    echo "Features:"
    echo "  • Uses your Claude Code OAuth credentials from ~/.swarmos/oauth.json"
    echo "  • Proper Bearer token authentication"
    echo "  • Automatic token refresh on expiry"
    echo "  • Rate limiting and usage tracking"
    echo ""
    exit 0
else
    echo ""
    echo -e "${RED}✗ Failed to register MCP server${NC}"
    echo ""
    echo "Troubleshooting:"
    echo "  1. Make sure Claude Code is installed: which claude"
    echo "  2. Make sure you're logged in: claude login"
    echo "  3. Check that the binary is executable: chmod +x $BINARY_PATH"
    echo ""
    exit 1
fi
