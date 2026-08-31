# Profile & Provider Integration

Workflows in Swarm are designed to be **portable** — the same YAML file can run with any provider the user has configured. This is achieved through provider/model resolution at execution time via the `WorkflowAgentFactory`.

---

## Resolution Hierarchy

When the workflow engine creates an agent, it resolves `provider` and `model` in this order:

1. **Profile metadata role alias** (set via agent `metadata.role_alias`)
2. **`@profile`** provider with a **role alias model** (e.g. `@main`, `@steering`)
3. **`@current`** provider/model (the user's actively selected provider and model)
4. **Literal** provider name and model name

---

## `@current` — Follow the User's Active Config

The simplest portable pattern. The agent uses whatever the user has selected in their active session.

```yaml
provider: "@current"
model: "@current"
```

**When to use:** Most workflows. If the user has `anthropic` + `claude-sonnet-4` active, that's what each agent will use. If they switch to `openai` + `gpt-4o`, all `@current` agents switch too.

**Fallback:** If no current provider is set, defaults to `anthropic` + `claude-3-5-sonnet-20241022`.

---

## `@profile` + Role Alias — Model Role Separation

When a workflow needs different capabilities for different roles, use profile role aliases. This lets the user configure once in their profile and all workflows that reference those roles pick up the right model.

```yaml
provider: "@profile"
model: "@main"       # → resolves to the "main" role in the user's active profile
```

```yaml
provider: "@profile"
model: "@steering"   # → resolves to the "steering" role
```

```yaml
provider: "@profile"
model: "@sub_agent"  # → resolves to the "sub_agent" role
```

**When to use:** Workflows where different groups need different capability levels:
- `@main` → powerful, high-context model for reasoning and writing
- `@sub_agent` → fast, cheap model for data gathering and search
- `@steering` → meta-agent for coordination and decision-making

**Fallback:** If the role alias is not found in the profile, falls back to `@current`.

---

## Literal Provider + Model — Pinned Configuration

For workflows that are model-specific and must run on a particular model:

```yaml
provider: anthropic
model: claude-sonnet-4
```

```yaml
provider: openai
model: gpt-4o
```

```yaml
provider: gemini
model: gemini-2.0-flash
```

```yaml
provider: openrouter
model: meta-llama/llama-3.1-70b-instruct
```

**When to use:** When benchmark reproducibility matters or the workflow was tuned for a specific model's behavior. Note that the credential for that provider must be available.

---

## Credential Resolution

The TUI resolves credentials automatically for every provider referenced in a workflow:

1. **OAuth token** (e.g. Claude Code auth)
2. **Account Registry** (provider accounts configured in settings)
3. **`credentials.json`** (local credentials file)
4. **Environment variables**: `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `GEMINI_API_KEY`, `OPENROUTER_API_KEY`

If a workflow uses a literal provider and the credential cannot be found, the workflow will fail at agent creation time with a `failed to get credentials for provider` error. Use `@current` or `@profile` to avoid hardcoding provider dependencies.

---

## Mixing Resolution Methods

A single workflow can mix resolution methods across groups:

```yaml
groups:
  - id: fast_search
    agents:
      - provider: "@profile"     # uses fast sub-agent role
        model: "@sub_agent"

  - id: deep_analysis
    agents:
      - provider: "@profile"     # uses powerful main role
        model: "@main"

  - id: coordination
    agents:
      - provider: "@current"     # just uses whatever's active
        model: "@current"
```

---

## Profile Role Alias Reference

These are the standard role aliases defined by Swarm profiles. Custom profiles may add others.

| Alias | Intended Use |
|---|---|
| `@main` | Primary reasoning, planning, writing agent |
| `@steering` | Meta-agent for workflow coordination and decisions |
| `@sub_agent` | Fast worker agents for tool-heavy tasks |
| `@background` | Long-running background task agents |

---

## Debugging Provider Resolution

If an agent is using the wrong model, check the workflow execution logs:
```
~/.swarm/logs/workflow-<id>-<timestamp>.log
```
The `WorkflowAgentFactory` logs resolved provider/model for every agent at `DEBUG` level. Look for:
```
workflow_factory.specific_tools_registered agent_id=<id> registered=N requested=N
```
If `registered < requested`, one or more tool names didn't match the registry — check canonical tool names.
