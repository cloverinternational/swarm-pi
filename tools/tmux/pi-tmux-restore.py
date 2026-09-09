#!/usr/bin/env python3
"""Restore exact Pi JSONL sessions into panes recreated by tmux-resurrect."""
import json
import os
import shlex
import subprocess
import sys
import time
from pathlib import Path

STATE = Path(os.environ.get("XDG_STATE_HOME", Path.home() / ".local/state")) / "pi-tmux/sessions.json"

def tmux(*args: str) -> str:
    socket = os.environ.get("PI_TMUX_SOCKET")
    command = ["tmux"] + (["-L", socket] if socket else []) + list(args)
    return subprocess.check_output(command, text=True).strip()

def main() -> int:
    if not STATE.is_file():
        return 0
    try:
        state = json.loads(STATE.read_text())
    except (OSError, ValueError) as exc:
        print(f"pi-tmux-restore: invalid state: {exc}", file=sys.stderr)
        return 0
    time.sleep(1)
    panes = {}
    output = tmux("list-panes", "-a", "-F", "#{session_name}\t#{window_index}\t#{pane_index}\t#{pane_id}\t#{pane_current_path}")
    for line in output.splitlines():
        parts = line.split("\t", 4)
        if len(parts) == 5:
            panes[":".join(parts[:2]) + "." + parts[2]] = parts
    for key, entry in state.items():
        if key not in panes:
            print(f"pi-tmux-restore: pane not found: {key}", file=sys.stderr)
            continue
        session_file = entry.get("sessionFile", "")
        if not session_file or not Path(session_file).is_file():
            print(f"pi-tmux-restore: session file missing for {key}: {session_file}", file=sys.stderr)
            continue
        pane_id = panes[key][3]
        current_cwd = os.path.realpath(panes[key][4])
        expected_cwd = os.path.realpath(entry.get("cwd", ""))
        if current_cwd != expected_cwd:
            print(f"pi-tmux-restore: cwd mismatch for {key}; refusing automatic resume", file=sys.stderr)
            continue
        command = "cd -- %s && exec pi --session %s" % (shlex.quote(expected_cwd), shlex.quote(session_file))
        socket = os.environ.get("PI_TMUX_SOCKET")
        tmux_command = ["tmux"] + (["-L", socket] if socket else []) + ["send-keys", "-t", pane_id, command, "Enter"]
        subprocess.run(tmux_command, check=False)
    return 0

if __name__ == "__main__":
    raise SystemExit(main())
