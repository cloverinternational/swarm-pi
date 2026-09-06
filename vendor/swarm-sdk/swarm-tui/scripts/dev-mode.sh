#!/bin/bash
# Toggle between local development and published SDK versions

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(git -C "$SCRIPT_DIR" rev-parse --show-toplevel)"
GO_WORK="$REPO_ROOT/go.work"
GO_WORK_BACKUP="$REPO_ROOT/go.work.backup"

MODE="${1:-status}"

case "$MODE" in
    local|dev)
        echo "📦 Switching to LOCAL development mode..."
        echo "   Using: monorepo sibling swarm-sdk (your edits)"

        if [ -f "$GO_WORK" ]; then
            echo "✅ go.work already present at repo root"
        elif [ -f "$GO_WORK_BACKUP" ]; then
            mv "$GO_WORK_BACKUP" "$GO_WORK"
            echo "✅ go.work restored from backup"
        else
            echo "⚠️  go.work is missing. Recreate it at repo root before building."
            exit 1
        fi
        echo "   Run: go build ./swarm-tui/cmd/swarmos"
        ;;

    published|prod)
        echo "📦 Switching to PUBLISHED version mode..."
        echo "   Using: github.com/Swarm-Code/mono/swarm-sdk v0.1.0 (published)"
        echo "   Note: swarm-tui/go.mod still has a local replace for ../swarm-sdk."
        echo "         Remove or comment the replace to fully use the published SDK."

        # Backup go.work
        if [ -f "$GO_WORK" ]; then
            mv "$GO_WORK" "$GO_WORK_BACKUP"
            echo "✅ go.work backed up to go.work.backup"
        fi

        echo "   To build: go build ./swarm-tui/cmd/swarmos"
        echo "   (go.mod will be used for published SDK only if local replace is removed)"
        ;;

    status)
        if [ -f "$GO_WORK" ]; then
            echo "📍 Current Mode: LOCAL DEVELOPMENT"
            echo "   Using: monorepo sibling swarm-sdk (local edits)"
            echo ""
            echo "   Switch to published: $REPO_ROOT/swarm-tui/scripts/dev-mode.sh published"
        else
            echo "📍 Current Mode: PUBLISHED"
            echo "   Using: github.com/Swarm-Code/mono/swarm-sdk v0.1.0 (go.work disabled)"
            echo "   Note: local replace in swarm-tui/go.mod may still apply."
            echo ""
            echo "   Switch to local: $REPO_ROOT/swarm-tui/scripts/dev-mode.sh local"
        fi
        ;;

    *)
        echo "Usage: $0 [local|published|status]"
        echo ""
        echo "  local     - Use monorepo sibling swarm-sdk (for development)"
        echo "  published - Use published v0.1.0 (go.mod)"
        echo "  status    - Show current mode (default)"
        exit 1
        ;;
esac
