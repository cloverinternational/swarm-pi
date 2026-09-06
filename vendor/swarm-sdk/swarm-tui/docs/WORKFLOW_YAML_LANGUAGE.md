# Workflow YAML Language Guide

This guide explains the **workflow YAML language** itself - the structure, syntax, and semantics of workflow definition files. This guide is **project and directory agnostic** and works for any workflow system using this YAML format.

---

## Overview

A workflow YAML file defines how multiple agents coordinate to solve problems. The file structure defines:
- **What agents are involved** (their roles, models, instructions)
- **How they work** (sequentially, parallel, adversarial)
- **What they can do** (which tools they have access to)
- **When they finish** (completion criteria and dependencies)

---

## File Structure

Every workflow YAML file has this basic structure:

```yaml
# Workflow identification and metadata
id: unique_identifier
name: Human-Readable Name
description: What this workflow does
version: 1.0.0

# Global configuration
config:
  max_duration: timespan
  allow_human_intervention: boolean
  max_retries: integer
  timeout_behavior: string

# Group definitions (execution units)
groups:
  - id: group_id
    name: Group Name
    execution: execution_type
    timeout: timespan
    completion:
      type: completion_type
      threshold: number
    agents:
      - agent configuration

# Optional: Steering/oversight rules
steering:
  type: steering_type
  rules: [...]

# Optional: Metadata
metadata:
  author: name
  category: category
  tags: [list]
  use_cases: [list]
```

---

## Top-Level Fields

### `id` (required)
Unique identifier for the workflow. Used for referencing, logging, and lookup.

```yaml
id: my_workflow_id
```

**Rules:**
- Must be unique within your workflows directory
- Use lowercase letters, numbers, underscores
- No spaces, hyphens, or special characters
- Typically descriptive (e.g., `code_review`, `data_analysis`, `research_synthesis`)

### `name` (required)
Human-readable title shown in UI and logs.

```yaml
name: My Awesome Workflow
```

### `description` (recommended)
Detailed explanation of what the workflow does, its purpose, and typical use cases.

```yaml
description: |
  This workflow analyzes code changes by having multiple experts
  review different aspects in parallel, then synthesizes findings
  into a unified recommendation.
```

### `version` (recommended)
Semantic versioning for the workflow. Helps track changes over time.

```yaml
version: 1.0.0
version: 2.1.3
```

---

## Configuration Section (`config`)

Global settings that apply to the entire workflow execution.

```yaml
config:
  max_duration: "30m"
  allow_human_intervention: true
  fail_on_steering_block: false
  max_retries: 2
  timeout_behavior: "partial"
```

### `max_duration`
Maximum time the entire workflow can run before timeout.

```yaml
max_duration: "10m"      # 10 minutes
max_duration: "1h"       # 1 hour
max_duration: "45m"      # 45 minutes
max_duration: "2h30m"    # 2 hours 30 minutes
```

### `allow_human_intervention`
Whether to pause execution and ask humans for input/decisions at certain points.

```yaml
allow_human_intervention: true    # Allow pauses for human input
allow_human_intervention: false   # Run completely autonomously
```

### `fail_on_steering_block`
What to do if steering rules block/reject the workflow.

```yaml
fail_on_steering_block: true   # Stop execution if steering blocks
fail_on_steering_block: false  # Continue despite steering blocks
```

### `max_retries`
Number of times to retry a failed group before giving up.

```yaml
max_retries: 0   # No retries, fail immediately
max_retries: 2   # Retry twice (3 total attempts)
max_retries: 5   # Retry five times
```

### `timeout_behavior`
What to do when groups timeout.

```yaml
timeout_behavior: "hard"     # Stop immediately on timeout
timeout_behavior: "partial"  # Return partial results from completed agents
timeout_behavior: "graceful" # Give agents time to finish
```

---

## Groups Section

A group is a collection of agents that execute together with a specific strategy.

```yaml
groups:
  - id: analysis
    name: Analysis Stage
    description: Multiple specialists analyze the problem
    execution: parallel
    timeout: "10m"
    depends_on: ["input_validation"]
    
    completion:
      type: consensus
      threshold: 0.8
      min_agents: 2
      max_failures: 1
    
    agents:
      - id: agent1
        # ... agent config
      - id: agent2
        # ... agent config
```

### Group Fields

#### `id` (required)
Unique identifier for this group within the workflow.

```yaml
id: expert_panel
id: analysis_stage_1
id: final_review
```

**Rules:**
- Unique within the workflow
- Referenced by other groups via `depends_on`
- Lowercase, no spaces

#### `name` (required)
Human-readable group name.

```yaml
name: Expert Panel Review
name: Initial Analysis Stage
```

#### `description` (recommended)
What this group does and why.

```yaml
description: Multiple domain experts analyze from different perspectives
```

#### `execution` (required)
How agents in this group work together.

```yaml
execution: sequential   # One agent runs, then the next
execution: parallel     # All agents run at the same time
execution: adversarial  # Agents debate/discuss until consensus
```

**Sequential**: Agents run one after another in order. Output from one becomes input to the next.

```yaml
execution: sequential
# Agent 1 runs → finishes → Agent 2 runs → finishes → Agent 3 runs
```

**Parallel**: All agents run simultaneously. Useful for independent analysis.

```yaml
execution: parallel
# Agent 1, Agent 2, Agent 3 all run at the same time
```

**Adversarial**: Agents iteratively discuss/debate until reaching consensus. Good for thorough review.

```yaml
execution: adversarial
# Agents take turns presenting arguments/evidence
# Process continues until consensus threshold reached
```

#### `timeout`
Maximum time for this group to complete.

```yaml
timeout: "5m"   # 5 minutes
timeout: "30m"  # 30 minutes
timeout: "2h"   # 2 hours
```

#### `depends_on` (optional)
Which groups must complete before this one starts.

```yaml
depends_on: ["input_preparation"]
depends_on: ["stage1", "stage2"]  # Can depend on multiple groups
```

**Important**: Declares dependencies in the execution DAG (directed acyclic graph).

```yaml
groups:
  - id: planning
    # ...
  
  - id: execution
    depends_on: ["planning"]  # execution runs after planning completes
    # ...
```

#### `completion` (required)
When is the group considered complete/successful?

```yaml
completion:
  type: all                 # All agents must succeed
  threshold: 1.0           # 100% success rate
  min_agents: 2            # Minimum 2 agents required
  max_failures: 1          # Allow 1 agent to fail
```

**Completion Types:**

```yaml
type: all           # Every agent must complete successfully
type: consensus     # Agents must agree (use with threshold)
type: majority      # More than half must succeed
type: first         # First successful agent completes group
type: quality       # Output must meet quality threshold
```

**Common Patterns:**

```yaml
# All agents must succeed
completion:
  type: all
  threshold: 1.0

# Agents must reach agreement (70%+)
completion:
  type: consensus
  threshold: 0.7
  min_agents: 2

# Allow some failures
completion:
  type: all
  threshold: 0.9
  max_failures: 1

# Any agent succeeding is enough
completion:
  type: first
```

---

## Agents Section

Individual AI agents within a group. Each agent is an autonomous worker with specific role, model, and capabilities.

```yaml
agents:
  - id: security_reviewer
    name: Security Specialist
    provider: openai
    model: gpt-4
    
    system_prompt: |
      You are a security specialist. Review code for vulnerabilities...
    
    tools:
      - Bash
      - Read
      - Grep
    
    capabilities:
      max_tokens: 4000
      temperature: 0.5
    
    context_sources:
      - "groups.planning.output"
```

### Agent Fields

#### `id` (required)
Unique identifier for this agent within its group.

```yaml
id: security_reviewer
id: technical_analyst
id: business_analyst
```

**Rules:**
- Unique within the group
- Lowercase, no spaces
- Descriptive of agent's role

#### `name` (required)
Human-readable agent name.

```yaml
name: Security Specialist
name: Technical Architect
name: Business Analyst
```

#### `provider` (required)
Which AI model provider to use.

```yaml
provider: openai           # OpenAI models
provider: anthropic        # Anthropic Claude models
provider: local_llm        # Local model server
provider: custom           # Custom provider
```

#### `model` (required)
Which specific model from that provider.

```yaml
model: gpt-4              # Example OpenAI model
model: claude-opus        # Example Anthropic model (use descriptive names, not IDs)
```

**Note:** Use descriptive model names/tiers rather than version IDs:
- Use: `claude-opus`, `gpt-4`, `llama-3`
- Don't: `gpt-4-20240314`, `claude-3-opus-20240229`

#### `system_prompt` (required)
Detailed instructions for this agent. This is the "personality" and task definition.

```yaml
system_prompt: |
  You are a code security specialist. Your role is to:
  1. Analyze code for security vulnerabilities
  2. Identify potential attack vectors
  3. Rate severity of findings
  4. Suggest mitigations
  
  Be thorough but concise in your analysis.
```

**Best Practices:**
- Be specific about the agent's role
- Explain what they should focus on
- Provide output format guidance
- Include any special instructions or constraints

#### `tools` (required)
Which tools/functions this agent can use.

```yaml
tools:
  - Bash
  - Read
  - Grep
  - TodoRead
  - TodoWrite
```

**Common Tools:**
- `Bash` - Execute shell commands
- `Read` - Read file contents
- `Grep` - Search for patterns in files
- `Write` - Write to files
- `TodoRead` - Read todo list
- `TodoWrite` - Update todo list
- `ReadBackgroundCommand` - Check background process status

#### `capabilities` (optional)
Token limits and model parameters for this agent.

```yaml
capabilities:
  max_tokens: 4000
  temperature: 0.5
```

**`max_tokens`**: Maximum output tokens the agent can generate.

```yaml
max_tokens: 1000    # Short responses
max_tokens: 4000    # Standard responses
max_tokens: 8000    # Long detailed responses
```

**`temperature`**: Controls randomness/creativity of responses.

```yaml
temperature: 0.0    # Deterministic, precise (use for analysis, code)
temperature: 0.5    # Balanced (use for mixed tasks)
temperature: 0.9    # Creative, varied (use for brainstorming)
```

#### `context_sources` (optional)
Where this agent gets input from previous groups.

```yaml
context_sources:
  - "groups.planning.output"
  - "groups.research.agents.technical_researcher.output"
```

**Formats:**
- `groups.{group_id}.output` - All output from a group
- `groups.{group_id}.agents.{agent_id}.output` - Specific agent output

Without `context_sources`, agent only gets the initial workflow input.

---

## Steering Section (Optional)

Quality control and oversight layer. Steering rules can:
- Block workflows that don't meet quality standards
- Automatically route to different paths based on results
- Escalate to human review
- Trigger retries with more context

```yaml
steering:
  type: hybrid
  llm_meta_agent:
    id: overseer
    name: Workflow Overseer
    provider: openai
    model: gpt-4
    system_prompt: |
      You oversee workflow execution. Monitor quality...
  
  rules:
    - id: quality_check
      condition: "output.confidence < 0.7"
      action: "escalate_to_human"
      priority: 20
    
    - id: critical_issues
      condition: "output contains 'CRITICAL'"
      action: "route_to_group"
      target_group: "deep_dive"
      priority: 30
```

### Steering Types

```yaml
type: none          # No oversight, auto-approve everything
type: rule_based    # Use predefined rules
type: llm_based     # LLM meta-agent makes decisions
type: hybrid        # Combination of rules and LLM
```

### Rules

```yaml
rules:
  - id: rule_identifier
    condition: "condition_expression"
    action: "action_to_take"
    priority: 10          # Higher = evaluated first
    target_group: "group_id"  # For routing actions
```

**Common Actions:**
- `approve` - Allow workflow to continue
- `reject` - Block workflow execution
- `retry` - Retry the group
- `escalate_to_human` - Pause and ask human
- `route_to_group` - Jump to different group

---

## Metadata Section (Optional)

Information about the workflow itself (not used in execution).

```yaml
metadata:
  author: Team Name
  category: code_review
  tags:
    - security
    - quality
    - parallel
  use_cases:
    - Pull request review
    - Security audit
  version: 1.0.0
  features:
    - Parallel analysis
    - Consensus-based
```

---

## Template Variables (Optional)

Some fields support template variables for dynamic behavior:

```yaml
# Reference user inputs
system_prompt: |
  Analyze the following topic: ${inputs.topic}
  Use this depth level: ${inputs.depth}

# Conditional logic
max_tokens: |
  ${inputs.depth === 'deep' ? 8000 : 4000}

# List operations
agents:
  - condition: |
      ${inputs.perspectives.includes('technical')}
```

---

## Complete Minimal Example

Here's the simplest valid workflow:

```yaml
id: minimal_workflow
name: Minimal Workflow
version: 1.0.0

config:
  max_duration: "10m"

groups:
  - id: main
    execution: sequential
    timeout: "8m"
    completion:
      type: all
    agents:
      - id: worker
        name: Worker
        provider: openai
        model: gpt-4
        system_prompt: "Complete the assigned task."
        tools: [Bash, Read]
        capabilities:
          max_tokens: 2000
          temperature: 0.5
```

---

## Complete Medium Example

A realistic multi-stage workflow:

```yaml
id: code_review_workflow
name: Code Review Process
description: Multi-stage code review with parallel experts and synthesis
version: 1.0.0

config:
  max_duration: "30m"
  allow_human_intervention: true
  max_retries: 2
  timeout_behavior: "partial"

groups:
  # Stage 1: Parallel analysis
  - id: analysis
    name: Code Analysis
    description: Multiple experts analyze code in parallel
    execution: parallel
    timeout: "10m"
    
    completion:
      type: consensus
      threshold: 0.7
      min_agents: 2
    
    agents:
      - id: security_expert
        name: Security Expert
        provider: anthropic
        model: claude-opus
        system_prompt: |
          You are a security expert. Analyze the code for:
          - Security vulnerabilities
          - Authentication issues
          - Data protection concerns
          
          Rate severity from 0-10.
        tools: [Read, Grep]
        capabilities:
          max_tokens: 4000
          temperature: 0.3
      
      - id: performance_expert
        name: Performance Expert
        provider: anthropic
        model: claude-opus
        system_prompt: |
          You are a performance specialist. Analyze for:
          - Algorithmic efficiency
          - Resource usage
          - Scalability concerns
          
          Suggest optimizations.
        tools: [Read, Grep]
        capabilities:
          max_tokens: 4000
          temperature: 0.3
  
  # Stage 2: Synthesis
  - id: synthesis
    name: Synthesis
    description: Combine expert findings
    execution: sequential
    depends_on: ["analysis"]
    timeout: "5m"
    
    completion:
      type: all
    
    agents:
      - id: synthesizer
        name: Review Synthesizer
        provider: anthropic
        model: claude-opus
        system_prompt: |
          Synthesize the expert reviews into a unified report.
          Prioritize critical issues first.
        tools: [Read, Write]
        context_sources:
          - "groups.analysis.output"
        capabilities:
          max_tokens: 4000
          temperature: 0.4

metadata:
  author: Development Team
  category: code_review
  tags:
    - security
    - performance
    - quality
  use_cases:
    - Pull request review
    - Code audit
    - Quality gates
```

---

## Common Patterns

### Pattern: Sequential Processing
One agent after another, each using previous output.

```yaml
groups:
  - id: stage1
    execution: sequential
    agents: [agent1, agent2, agent3]
    completion:
      type: all
  
  - id: stage2
    depends_on: ["stage1"]
    execution: sequential
    agents: [agent4]
```

### Pattern: Parallel Specialists
Multiple independent experts, consensus required.

```yaml
groups:
  - id: experts
    execution: parallel
    agents: [technical_expert, business_expert, security_expert]
    completion:
      type: consensus
      threshold: 0.7
      min_agents: 2
```

### Pattern: Debate/Adversarial
Agents discuss and refine until consensus.

```yaml
groups:
  - id: debate
    execution: adversarial
    agents: [proposer, critic, advocate]
    completion:
      type: consensus
      threshold: 0.8
```

### Pattern: Quality Gates
Stage 1 analysis → Steering decision → Different paths.

```yaml
groups:
  - id: initial
    execution: parallel
    agents: [analyst1, analyst2]
    completion:
      type: consensus

steering:
  rules:
    - condition: "quality < 0.7"
      action: "route_to_group"
      target_group: "deep_dive"
    - condition: "quality >= 0.7"
      action: "route_to_group"
      target_group: "finalize"

groups:
  - id: deep_dive
    depends_on: ["initial"]
    agents: [expert]
  
  - id: finalize
    depends_on: ["initial"]
    agents: [finalizer]
```

---

## Execution Flow Visualization

### Sequential Group
```
Agent 1 ▶ Agent 2 ▶ Agent 3
(complete) (complete) (complete)
```

### Parallel Group
```
Agent 1 ─┐
Agent 2 ─┼ (all run simultaneously)
Agent 3 ─┘
```

### Adversarial Group
```
Agent 1: "Proposal: X"
Agent 2: "I disagree because: Y"
Agent 1: "But consider: Z"
Agent 2: "Fair point, I agree on modified X'"
(Consensus reached)
```

---

## Best Practices

### Naming
- Use descriptive, self-documenting names
- Use lowercase with underscores for IDs
- Use readable names for display fields

### Timeouts
- Set realistic timeouts based on task complexity
- Group timeout < workflow timeout
- Budget extra time for parallel agents

### Models
- Use tier-based naming (opus, sonnet, haiku, gpt-4, gpt-3.5)
- Don't hardcode version IDs
- Match model capability to task

### Tools
- Only grant tools that agents actually need
- Be explicit (don't use wildcard tools)
- Security: restrict file access to needed paths

### Prompts
- Clear role definition
- Specific task instructions
- Output format guidance
- Examples or constraints

### Temperature
- 0.0-0.3: Analysis, code, factual tasks
- 0.4-0.6: Balanced tasks, creative analysis
- 0.7-1.0: Brainstorming, creative ideation

---

## Validation Checklist

- [ ] All required fields present
- [ ] Unique IDs within scope (workflow, groups, agents)
- [ ] No circular dependencies
- [ ] All `depends_on` reference existing groups
- [ ] All `context_sources` reference valid paths
- [ ] YAML syntax is valid
- [ ] Timeouts are reasonable
- [ ] System prompts are non-empty and clear
- [ ] Tools arrays are non-empty

---

## File Format Rules

- YAML format (`.yaml` extension recommended)
- Use 2-space indentation
- No tabs
- Proper quoting for strings with special characters
- Use `|` for multi-line strings (preserves newlines)

---

Done! This guide focuses entirely on the **YAML language itself** - structure, semantics, and best practices - without being tied to any specific project or outdated model IDs.
