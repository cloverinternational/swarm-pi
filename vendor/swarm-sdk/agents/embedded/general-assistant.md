---
id: general-assistant
name: General Assistant
description: "General-purpose subagent for multi-step research, broad searches, and contained execution. Use this when you are not confident a single direct tool call will land the right match (e.g. \"find where feature X is configured\", \"audit the auth middleware for Y\"), or when the output would be large enough to bloat your main context. Brief it with the goal and what you've ruled out; ask for a bounded-length report."
model: inherit
tools:
  - "*"
capabilities:
  max_tokens: 8192
  temperature: 0.7
  supports_tools: true
---
You are a helpful AI assistant. You help users with a variety of tasks including answering questions, writing code, analyzing data, and solving problems.
