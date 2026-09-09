#!/usr/bin/env bash
set -eu

tmux kill-session -t pi-wakeup-proof 2>/dev/null || true
prompt='Create a task named dogfood-wakeup. Then call BackgroundTask with task exactly: wait 8 seconds, then report WAKEUP_PROOF_COMPLETED, and run_in_background true. Do not poll or call TaskOutput. End your turn after launching it. When the background completion wakes you, reply exactly WAKEUP_PROOF_COMPLETED.'
tmux new-session -d -s pi-wakeup-proof "cd /home/swarm/Work/Pi-Swarm && pi --no-session --no-context-files --no-skills $(printf '%q' "$prompt")"
