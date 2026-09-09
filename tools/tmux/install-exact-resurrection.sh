#!/usr/bin/env bash
set -euo pipefail

TMUX_CONF="$HOME/.tmux.conf"
PLUGIN_DIR="$HOME/.tmux/plugins"
TPM="$PLUGIN_DIR/tpm"
RESURRECT="$PLUGIN_DIR/tmux-resurrect"
CONTINUUM="$PLUGIN_DIR/tmux-continuum"
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
RESTORE="$REPO_ROOT/tools/tmux/pi-tmux-restore.py"
MARKER="# >>> pi-swarm exact tmux resurrection >>>"

command -v tmux >/dev/null || { echo "tmux is required" >&2; exit 1; }
command -v git >/dev/null || { echo "git is required" >&2; exit 1; }
command -v python3 >/dev/null || { echo "python3 is required" >&2; exit 1; }

mkdir -p "$PLUGIN_DIR"
clone_or_update() {
  local url="$1" target="$2"
  if [ -d "$target/.git" ]; then git -C "$target" pull --ff-only; else git clone --depth 1 "$url" "$target"; fi
}
clone_or_update https://github.com/tmux-plugins/tpm "$TPM"
clone_or_update https://github.com/tmux-plugins/tmux-resurrect "$RESURRECT"
clone_or_update https://github.com/tmux-plugins/tmux-continuum "$CONTINUUM"

if ! grep -Fqx "$MARKER" "$TMUX_CONF" 2>/dev/null; then
  cat >>"$TMUX_CONF" <<EOF

$MARKER
set -g @plugin 'tmux-plugins/tpm'
set -g @plugin 'tmux-plugins/tmux-resurrect'
set -g @plugin 'tmux-plugins/tmux-continuum'
set -g @continuum-save-interval '5'
set -g @continuum-restore 'on'
set -g @resurrect-hook-post-save-all 'python3 $REPO_ROOT/tools/tmux/pi-tmux-save.py'
set -g @resurrect-hook-post-restore-all 'python3 $RESTORE'
run '$TPM/tpm'
# <<< pi-swarm exact tmux resurrection <<<
EOF
fi

"$TPM/bin/install_plugins"
echo "Installed exact Pi tmux resurrection. Save: prefix Ctrl-s; restore: prefix Ctrl-r."
