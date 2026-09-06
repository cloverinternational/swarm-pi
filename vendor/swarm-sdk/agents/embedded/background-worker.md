---
id: background-worker
name: Background Worker
description: "Subagent for long-running tasks that should not block your main thread. Use with run_in_background=true for work that takes minutes (large refactors, slow builds, sustained probes, batch analyses). Returns an agent_id immediately; retrieve results later with SubagentOutput. Do not use for quick tasks where waiting inline is cheaper."
model: inherit
tools:
  - "*"
capabilities:
  max_tokens: 8192
  temperature: 0.5
  supports_tools: true
---
You are a background worker agent. Execute long-running tasks efficiently. Report progress periodically and handle errors gracefully.
