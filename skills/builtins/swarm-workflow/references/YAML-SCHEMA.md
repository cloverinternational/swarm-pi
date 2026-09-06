# Swarm Workflow YAML Schema — Complete Reference

## Top-Level Fields

```yaml
id: string                    # Required. Unique workflow identifier. snake_case recommended.
                              #   Must be unique within its workflows directory.
name: string                  # Required. Human-readable display name.
version: string               # Required. Semver string: "1.0.0"
description: string           # Recommended. What this workflow does and when to use it.

config:                       # Optional. Workflow-level execution config.
  max_duration: string        #   Total wall-clock timeout. Format: "30m", "1h", "2h30m". Default: 30m.
  allow_human_intervention: bool  # Allow steering blocks to pause for human input. Default: true.
  fail_on_steering_block: bool    # If true, a gate block fails the whole workflow. Default: false.
  max_retries: int            #   Max retry attempts for failed groups. Default: 3.
  timeout_behavior: string    #   "partial" (return partial results) | "fail" | "continue". Default: "partial".

groups: []                    # Required. List of AgentGroup definitions. At least one required.
parameters: []                # Optional. Interactive user inputs collected before execution starts.
steering: {}                  # Optional. Global meta-agent / rules for workflow-level decisions.
metadata: {}                  # Optional. Arbitrary key-value metadata (author, category, tags, etc.)
```

---

## AgentGroup Fields

```yaml
- id: string                  # Required. Unique within this workflow. snake_case recommended.
  name: string                # Required. Display name.
  description: string         # Optional. What this group does.
  execution: string           # Required. "parallel" | "sequential" | "adversarial"
  timeout: string             # Optional. Group-level timeout: "5m", "30s". Overrides config.max_duration for this group.

  depends_on: [string]        # Optional. List of group IDs that must complete before this group starts.
                              #   Empty or absent = starts immediately (layer 0).

  completion:                 # Optional. When is this group considered done?
    type: string              #   "all" (default) | "first" | "consensus"
    threshold: float          #   For consensus: 0.0–1.0. For all: ignored. Default: 1.0.
    min_agents: int           #   Minimum agents that must succeed. Default: 1.
    max_failures: int         #   How many agents may fail before group fails. Default: 0.

  output_strategy: string     # Optional. How agent outputs are combined.
                              #   "raw" (default) | "synthesize" | "first"
                              #   "synthesize" requires a coordinator agent to be defined.

  coordinator:                # Optional. Agent that synthesizes outputs when output_strategy: synthesize.
    (same fields as agent definition below)

  steering:                   # Optional. Group-level steering config.
    synthesis_strategy: string
    conflict_resolution: string
    validate_plan:
      type: string            #   "rule" | "llm"
      prompt: string
      rules: [string]
      min_confidence: float

  agents: []                  # Required. List of agent definitions.
```

---

## Agent Definition Fields

```yaml
- id: string                  # Optional. Auto-generated from name+model if absent.
  name: string                # Required. Display name for this agent.
  description: string         # Optional.

  provider: string            # Required. Provider name OR special value.
                              #   Special values: "@current", "@profile"
                              #   Literal: "anthropic", "openai", "gemini", "openrouter"

  model: string               # Required. Model name OR special value.
                              #   Special values: "@current", "@main", "@steering", "@sub_agent"
                              #   Literal: "claude-sonnet-4", "gpt-4o", etc.

  system_prompt: string       # Required (or system_prompt_template). Agent instructions.
                              #   Use YAML block scalar (|-) for multi-line:
                              #   system_prompt: |-
                              #     You are a ...
                              #     1. Do this
                              #     2. Do that

  system_prompt_template: string  # Alternative: a named template (resolved at runtime).
  prompt_variables: {}            # Variables injected into system_prompt_template.

  tools: [string]             # Optional. Canonical tool names from the SDK registry.
                              #   [] or absent = no tools. Use canonical names (case-sensitive).
                              #   Common: Bash, Read, Write, Edit, Grep, Task, BackgroundTask,
                              #           ReadBackgroundCommand, TodoRead, TodoWrite

  context_sources: [string]   # Optional. References to prior group outputs as agent context.
                              #   Format: "groups.<group_id>.output"
                              #   Example: context_sources: ["groups.analysis.output"]

  capabilities:               # Optional. LLM inference settings.
    max_tokens: int           #   Max output tokens. Default: provider default.
    temperature: float        #   0.0 (deterministic) – 1.0 (creative). Default: 0.7.
    max_turns: int            #   Max agentic turns before agent stops. Default: unlimited.
    timeout_seconds: int      #   Per-agent timeout in seconds.

  provider_config: {}         # Optional. Provider-specific settings (base_url, etc.)
```

---

## Parameters Fields

Collected from the user via interactive prompts before workflow execution begins.

```yaml
parameters:
  - id: string                # Required. Unique parameter ID.
    question: string          # Required. Text shown to the user.
    description: string       # Optional. Extra help text.
    type: string              # Required. "text" | "number" | "choice" | "multi_choice" | "confirm"
    required: bool            # Default: false.
    default: any              # Optional. Default value.
    choices: [string]         # Required for type: choice and multi_choice.
    validation:               # Optional. Input validation rules.
      min_length: int         #   For text
      max_length: int         #   For text
      pattern: string         #   Regex for text validation
      min: float              #   For number
      max: float              #   For number
      step: float             #   For number
```

---

## Steering Config Fields

```yaml
steering:
  type: string                # "none" | "rule" | "llm" | "hybrid"

  llm_meta_agent:             # Required for type: llm or hybrid.
    name: string
    provider: string
    model: string
    system_prompt: string
    capabilities:
      max_tokens: int
      temperature: float

  rules:                      # Required for type: rule or hybrid.
    - id: string
      condition: string       # Condition expression (e.g. "group.confidence < 0.7")
      action: string          # Action to take (e.g. "retry", "escalate_to_human", "skip_group")
      priority: int           # Higher = evaluated first. Default: 0.
      parameters: {}          # Optional action parameters.
```

---

## Metadata Fields

Free-form metadata. The TUI uses these for display and filtering:

```yaml
metadata:
  author: string
  category: string            # "development" | "ops" | "research" | "data" | etc.
  created_at: string          # ISO 8601 datetime string
  tags: [string]
  use_cases: [string]
```

---

## Complete Annotated Example

```yaml
id: full_example_workflow
name: Full Example Workflow
version: 1.0.0
description: Demonstrates all major YAML fields

config:
  max_duration: 45m
  allow_human_intervention: true
  fail_on_steering_block: false
  max_retries: 2
  timeout_behavior: partial

parameters:
  - id: target_env
    question: "Which environment should this target?"
    type: choice
    required: true
    choices: [dev, staging, production]
    default: dev
  - id: dry_run
    question: "Run in dry-run mode (no writes)?"
    type: confirm
    default: false

groups:
  - id: discovery
    name: Discovery
    execution: parallel
    timeout: 8m
    completion:
      type: all
      threshold: 1
    agents:
      - id: file_scanner
        name: File Scanner
        provider: "@current"
        model: "@current"
        system_prompt: |-
          Scan the repository structure and identify relevant files.
          Report: total file count, key directories, detected language/framework.
        tools: [Bash, Grep, Read]
        capabilities:
          max_tokens: 4000
          temperature: 0.1

      - id: dep_scanner
        name: Dependency Scanner
        provider: "@profile"
        model: "@sub_agent"
        system_prompt: |-
          Analyze all dependency files (package.json, go.mod, requirements.txt, etc.)
          Report: dependency count, outdated packages, security concerns.
        tools: [Bash, Read, Grep]
        capabilities:
          max_tokens: 4000
          temperature: 0.1

  - id: analysis
    name: Analysis
    execution: sequential
    depends_on: [discovery]
    timeout: 15m
    completion:
      type: all
      threshold: 1
    context_sources:
      - groups.discovery.output
    agents:
      - id: analyzer
        name: Analyzer
        provider: "@profile"
        model: "@main"
        system_prompt: |-
          Review the discovery results and produce a structured analysis.
          Identify the top 5 areas requiring attention.
        tools: [Read, Grep, Bash]
        context_sources:
          - groups.discovery.output
        capabilities:
          max_tokens: 16000
          temperature: 0.2

  - id: report
    name: Report
    execution: sequential
    depends_on: [analysis]
    timeout: 10m
    output_strategy: synthesize
    coordinator:
      id: report_coordinator
      name: Report Coordinator
      provider: "@current"
      model: "@current"
      system_prompt: |-
        Synthesize all agent outputs into a single cohesive final report.
        Format as Markdown with an executive summary at the top.
    agents:
      - id: writer
        name: Writer
        provider: anthropic
        model: claude-sonnet-4
        system_prompt: |-
          Write the detailed findings section of the report.
        tools: [Write, Read]
        capabilities:
          max_tokens: 8000
          temperature: 0.3

steering:
  type: rule
  rules:
    - id: low_confidence
      condition: "group.confidence < 0.6"
      action: "retry"
      priority: 10
    - id: timeout_reached
      condition: "elapsed_time > 40m"
      action: "escalate_to_human"
      priority: 5

metadata:
  author: Swarm SDK
  category: analysis
  created_at: "2026-01-01T00:00:00Z"
  tags: [analysis, discovery, reporting]
  use_cases: [codebase audit, dependency review, automated reporting]
```
