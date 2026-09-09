#!/usr/bin/env python3
"""Validate that Pi's native mapping is present before a tmux save completes."""
import json
import os
from pathlib import Path

state = Path(os.environ.get("XDG_STATE_HOME", Path.home() / ".local/state")) / "pi-tmux/sessions.json"
if state.is_file():
    try:
        data = json.loads(state.read_text())
        if not isinstance(data, dict):
            raise ValueError("mapping must be an object")
    except (OSError, ValueError) as exc:
        print(f"pi-tmux-save: invalid mapping (tmux layout remains saved): {exc}", flush=True)
