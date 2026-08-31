#!/bin/zsh
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")" && pwd)"
repo_root="$(git -C "$script_dir/.." rev-parse --show-toplevel)"
cd "$repo_root"

paths=(
  "go.work"
  "swarm-sdk/go.mod"
  "swarm-tui/go.mod"
  "swarm-tui/internal/chat/commands/config.go"
  "swarm-tui/internal/chat/commands/model.go"
  "swarm-tui/internal/chat/app.go"
  "swarm-tui/.idea/runConfigurations/Go_Test.xml"
)

echo "Staging selected files..."
for path in "${paths[@]}"; do
  if [ -e "$path" ]; then
    git add "$path"
  fi
done

echo "Running uvx gac..."
uvx gac

echo "Done."
