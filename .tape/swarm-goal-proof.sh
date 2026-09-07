#!/usr/bin/env bash
set -eu
root=$(cd "$(dirname "$0")/.." && pwd)
name=pi-swarm-goal-proof
if tmux has-session -t "$name" 2>/dev/null; then
  echo "Proof session already exists; inspect it before starting another." >&2
  exit 1
fi
prompt='Test the scheduler tool. First create a task for this test if required. Call scheduler with action create, delay 15s, and prompt exactly: Reply with SCHEDULER_RESUMED_AUTOMATICALLY. Then end your turn saying SCHEDULER_ARMED. Do not poll, wait, call other scheduling tools, or modify files. When the scheduled message arrives respond as requested.'
tmux new-session -d -s "$name" -x 120 -y 36 -c "$root" "pi --approve --session-dir '$root/.tape/goal-proof-sessions' -- $(printf '%q' "$prompt")"
exec tmux attach-session -t "$name"
