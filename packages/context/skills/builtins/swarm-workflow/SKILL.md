---
name: swarm-workflow
description: Teaches agents how to author, validate, and run Swarm multi-agent workflows. Covers the YAML schema, group execution strategies (parallel/sequential/adversarial), DAG dependency wiring, profile/provider resolution (@current, @profile), parameters, steering, gates, and where to place workflow files. Use this when creating workflows, designing agent pipelines, or debugging workflow YAML.
version: 1.0.0
author: Swarm SDK
category: workflows
tags:
  - workflows
  - multi-agent
  - yaml
  - orchestration
  - dag
  - parallel
  - sequential
  - adversarial
enabled: true
priority: 90
allowed-tools: Write Edit Read Bash
triggers:
  - type: keyword
    pattern: create workflow
  - type: keyword
    pattern: workflow yaml
  - type: keyword
    pattern: agent group
  - type: keyword
    pattern: multi-agent workflow
  - type: keyword
    pattern: parallel agents
  - type: keyword
    pattern: sequential agents
  - type: keyword
    pattern: adversarial agents
  - type: keyword
    pattern: depends_on
  - type: keyword
    pattern: workflow execution
  - type: keyword
    pattern: swarm workflow
  - type: file_pattern
    pattern: "workflows/*.yaml"
  - type: file_pattern
    pattern: ".workflows/*.yaml"
---
# Swarm Workflow Authoring

A **workflow** (also called a `Mode`) is a YAML file that defines a multi-agent pipeline as a DAG (directed acyclic graph) of **agent groups**. The workflow engine executes groups in dependency order, resolving which groups can run in parallel at each layer automatically.

Workflow files live in:
- `./workflows/*.yaml` — project-local (checked into repo)
- `./.workflows/*.yaml` — project-local (hidden directory)
- `~/.swarm/workflows/*.yaml` — user-global (available in every session)

## Minimal Working Example

```yaml
id: my_workflow
name: My Workflow
version: 1.0.0
description: What this workflow does

groups:
  - id: analysis
    name: Analysis
    execution: parallel
    agents:
      - id: analyst
        name: Analyst
        provider: "@current"
        model: "@current"
        system_prompt: "Analyze the codebase and report findings."
        tools: [Bash, Read, Grep]
        capabilities:
          max_tokens: 8000
          temperature: 0.3
```

## Execution Strategies

Every group has an `execution` field. Choose based on what the group's agents need to do:

| Strategy | When to use |
|---|---|
| `parallel` | Independent tasks that don't need each other's output |
| `sequential` | Step-by-step pipeline where each agent builds on the previous |
| `adversarial` | Debate/critique loops — agents argue until consensus |

## DAG Dependencies

Use `depends_on` to wire groups into a pipeline. Groups without `depends_on` start simultaneously (layer 0). The engine resolves layers automatically using Kahn's algorithm.

```yaml
groups:
  - id: gather          # starts immediately (layer 0)
    ...
  - id: analyze
    depends_on: [gather]   # starts after gather completes (layer 1)
    ...
  - id: report
    depends_on: [analyze]  # starts after analyze (layer 2)
    ...
```

Multiple groups can depend on the same upstream group — they all start in the same layer once that upstream group completes.

## Provider / Model Resolution

Agents in workflows resolve provider and model through three mechanisms:

1. **`@current`** — uses whatever provider/model the user has active right now:
   ```yaml
   provider: "@current"
   model: "@current"
   ```

2. **Profile role aliases** — uses a named role from the user's active profile:
   ```yaml
   provider: "@profile"
   model: "@main"        # or "@steering", "@sub_agent", etc.
   ```

3. **Literal** — hardcodes a specific provider and model:
   ```yaml
   provider: anthropic
   model: claude-sonnet-4
   ```

Prefer `@current` for single-model workflows. Use `@profile` role aliases when a workflow needs different models for different roles (e.g., a powerful model for planning, a fast model for search).

## Tool Names

Use the canonical SDK tool names. The most common ones:

```yaml
tools: [Bash, Read, Write, Edit, Grep, Undo, Shell, apply_patch, Task, BackgroundTask, ReadBackgroundCommand, TodoRead, TodoWrite]
```

The workflow engine resolves common aliases (e.g. `file_read` → `Read`, `bash` → `Bash`) but always prefer canonical names to avoid silent failures.

## Validating a Workflow

Before writing a workflow to disk, validate it with the bundled script:

```bash
bash scripts/validate-workflow.sh ./workflows/my_workflow.yaml
```

The script checks: YAML syntax, required fields, group ID uniqueness, `depends_on` references, circular dependencies, and agent tool names.

## Scaffolding a New Workflow

Use the scaffold script to generate a starter YAML:

```bash
bash scripts/new-workflow.sh my_workflow "My Workflow" ./workflows/
```

This creates `./workflows/my_workflow.yaml` with a 2-group parallel→sequential template.

## Key Rules

1. Every `id` field must be unique within its scope (workflow IDs unique in their dir; group IDs unique within a workflow; agent IDs unique within a group).
2. `depends_on` values must reference group IDs that exist in the same workflow — the validator will catch missing references.
3. `execution: adversarial` requires at least 2 agents and a `completion.type: consensus` with a `threshold`.
4. Tool names in `tools:` must match registry names exactly (case-sensitive). Use the canonical list above.
5. Never create circular dependencies — group A cannot depend on group B if B depends on A directly or transitively.
6. Always set `version: 1.0.0` (semver) and `id:` explicitly — don't rely on auto-generation from `name`.

## Deep Reference

- Full YAML schema with every field: `references/YAML-SCHEMA.md`
- Execution strategy decision guide: `references/EXECUTION-STRATEGIES.md`
- Profile/provider resolution: `references/PROFILE-INTEGRATION.md`
- Annotated real-world examples: `references/EXAMPLES.md`
- Common errors and fixes: `references/TROUBLESHOOTING.md`
