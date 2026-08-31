# Workflow Creation Checklist

Quick, practical checklist for creating and validating workflows.

---

## Pre-Creation Checklist

- [ ] I understand which workflow pattern I need:
  - [ ] Simple single-agent
  - [ ] Parallel specialists
  - [ ] Sequential multi-stage
  - [ ] Adversarial debate
  - [ ] Hybrid with steering
  - [ ] Interactive forms
  
- [ ] I have a clear task definition
- [ ] I know what agents are needed
- [ ] I know the execution flow

---

## File Creation

- [ ] Created YAML file in workflows directory
- [ ] File extension is `.yaml` (not `.yml`)
- [ ] File name is descriptive
- [ ] File name uses lowercase letters and underscores only

---

## YAML Structure Checklist

### Top-Level Fields
- [ ] `id:` is unique (lowercase, no spaces)
- [ ] `name:` is descriptive
- [ ] `description:` explains the workflow
- [ ] `version:` is set (e.g., 1.0.0)

### Config Section
- [ ] `max_duration:` is set to realistic time
- [ ] `allow_human_intervention:` is configured
- [ ] `max_retries:` is set appropriately
- [ ] `timeout_behavior:` is defined

### Groups Section
Each group should have:
- [ ] Unique `id:` (no spaces, lowercase)
- [ ] Descriptive `name:`
- [ ] Correct `execution:` type (sequential/parallel/adversarial)
- [ ] Realistic `timeout:`
- [ ] Valid `completion:` criteria with threshold
- [ ] `depends_on:` correctly references other groups (if applicable)
- [ ] At least one agent defined

### Agents Section
Each agent should have:
- [ ] Unique `id:` within its group
- [ ] Descriptive `name:`
- [ ] Valid `provider:` (anthropic, openai, etc.)
- [ ] Valid `model:` for that provider
- [ ] Non-empty `system_prompt:` (detailed and clear)
- [ ] Non-empty `tools:` array (agent needs at least one tool)
- [ ] `capabilities:` configured:
  - [ ] `max_tokens:` set to reasonable value (1000-8000)
  - [ ] `temperature:` set appropriately (0.0-1.0)
- [ ] `context_sources:` configured if agent needs previous outputs

---

## System Prompt Quality Checklist

For each agent's `system_prompt:`:
- [ ] Clear role definition ("You are a...")
- [ ] Specific task instructions
- [ ] Focus areas or constraints
- [ ] Output format guidance (if needed)
- [ ] Any special instructions
- [ ] NOT empty or vague

**Example Good Prompt:**
```yaml
system_prompt: |
  You are a security code reviewer.
  
  Analyze the provided code for:
  1. Security vulnerabilities
  2. Authentication/authorization issues
  3. Data protection concerns
  
  Rate each finding by severity (Low, Medium, High, Critical).
  Structure your response with findings grouped by category.
```

**Example Bad Prompt:**
```yaml
system_prompt: "Review the code."  # Too vague!
```

---

## Dependencies & Context Checklist

If using `depends_on:`:
- [ ] Referenced groups exist
- [ ] No circular dependencies (A→B→A)
- [ ] Logical sequence makes sense

If using `context_sources:`:
- [ ] Correct syntax: `groups.{id}.output` or `groups.{id}.agents.{id}.output`
- [ ] Referenced groups exist
- [ ] Referenced groups come before this agent

---

## Steering Checklist (if using steering)

- [ ] `steering.type:` is set (none, rule_based, llm_based, hybrid)
- [ ] If `llm_based` or `hybrid`:
  - [ ] `llm_meta_agent:` is configured
  - [ ] Meta-agent has valid provider and model
  - [ ] Meta-agent system prompt is clear
- [ ] Rules have:
  - [ ] Unique `id:`
  - [ ] Clear `condition:` expression
  - [ ] Valid `action:` (approve, reject, escalate_to_human, route_to_group, retry)
  - [ ] Appropriate `priority:` (higher = evaluated first)

---

## Validation & Testing

### Basic Validation
```bash
# Before running:
# 1. Check YAML syntax (use online YAML validator if needed)
# 2. Verify all IDs are unique within their scope
# 3. Check for spelling errors
```

### Logical Validation
- [ ] Execution flow makes sense
- [ ] Dependencies are in correct order
- [ ] Timeouts are realistic
- [ ] Agent specialties are distinct (if parallel)
- [ ] Completion thresholds are achievable

### Field Validation
- [ ] All required fields are present
- [ ] All IDs follow naming rules
- [ ] All file references are correct
- [ ] All model names are valid tier names (not version IDs)
- [ ] All tool names are correct

---

## Common Mistakes to Avoid

❌ **DON'T**: Use spaces or hyphens in `id:` fields
❌ **DON'T**: Leave `system_prompt:` empty or too vague
❌ **DON'T**: Create circular dependencies
❌ **DON'T**: Set unrealistic timeouts
❌ **DON'T**: Use version IDs for models (e.g., `gpt-4-20240314`)
❌ **DON'T**: Have agents with empty `tools:` arrays
❌ **DON'T**: Skip validation before execution
❌ **DON'T**: Use specific hardcoded model IDs that will become outdated

✅ **DO**: Use descriptive, self-documenting names
✅ **DO**: Write clear, specific system prompts
✅ **DO**: Use generic model tiers (gpt-4, claude-opus, llama-3)
✅ **DO**: Validate before executing
✅ **DO**: Test with realistic data first
✅ **DO**: Document your workflow in metadata

---

## Naming Conventions

### For IDs
```yaml
# Good:
id: code_review_workflow
id: parallel_research
id: multi_stage_analysis

# Bad:
id: Code Review          # Has spaces
id: code-review          # Uses hyphens
id: CodeReview           # Uses camelCase
id: cr                   # Not descriptive
```

### For Models
```yaml
# Good (tier-based):
model: gpt-4
model: gpt-3.5
model: claude-opus
model: claude-sonnet
model: llama-3

# Bad (version IDs):
model: gpt-4-20240314
model: claude-3-opus-20240229
model: claude-sonnet-4-5-20250929
```

---

## Timeouts Guide

```yaml
# Short tasks
timeout: "2m"
timeout: "5m"

# Medium tasks
timeout: "10m"
timeout: "15m"

# Long tasks
timeout: "20m"
timeout: "30m"

# Very long tasks
timeout: "1h"
timeout: "1h30m"
```

**Rule**: Set timeout higher than you think you need. Better to wait than timeout.

---

## Temperature Guide

```yaml
# Precise, analytical tasks
temperature: 0.0   # Completely deterministic
temperature: 0.2
temperature: 0.3

# Balanced tasks
temperature: 0.5

# Creative, exploratory tasks
temperature: 0.7
temperature: 0.8
temperature: 0.9   # Very creative/varied
```

---

## Max Tokens Guide

```yaml
# Quick responses
max_tokens: 1000
max_tokens: 1500

# Standard responses
max_tokens: 2000
max_tokens: 3000
max_tokens: 4000

# Long responses
max_tokens: 6000
max_tokens: 8000

# Very long responses
max_tokens: 12000
```

---

## Sign-Off Checklist

Before executing your workflow:

- [ ] YAML syntax is valid
- [ ] All required fields are present
- [ ] All IDs are unique
- [ ] System prompts are clear and specific
- [ ] Tools arrays are non-empty
- [ ] Timeouts are realistic
- [ ] Dependencies make logical sense
- [ ] Model references use tier names, not version IDs
- [ ] I understand the execution flow
- [ ] I'm ready to execute

---

## Troubleshooting Quick Guide

| Problem | Check |
|---------|-------|
| Validation fails | YAML syntax, required fields, unique IDs |
| Group not found | Check `depends_on` references |
| Agents timeout | Increase `timeout:`, reduce complexity |
| Agent produces poor output | Improve `system_prompt:`, adjust `temperature:` |
| Agent doesn't have needed access | Add missing tool to `tools:` array |
| No agents execute | Check `condition:` if present |
| Circular dependency detected | Verify `depends_on:` DAG has no cycles |
| Unknown field error | Check YAML indentation and field names |

---

## Final Validation Checklist

Before considering your workflow "done":

- [ ] File is saved in correct location
- [ ] File has `.yaml` extension
- [ ] YAML syntax validates
- [ ] All IDs are unique
- [ ] All dependencies resolve
- [ ] No circular dependencies
- [ ] All `context_sources` are valid
- [ ] All agent system prompts are clear
- [ ] All tools arrays are non-empty
- [ ] All timeouts are realistic
- [ ] Model names are tier-based, not version IDs
- [ ] Temperature/max_tokens are appropriate
- [ ] Metadata is filled in
- [ ] I can explain the execution flow from start to finish

---

Done! Your workflow is ready to validate and execute.
