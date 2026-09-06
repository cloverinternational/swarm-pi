# CORRECTED WORKFLOW TEMPLATES - With Proper Completion Criteria

These templates have been corrected based on how completion criteria ACTUALLY works in the code.

---

## Template 1: Simple Single Agent (WORKS)

```yaml
id: simple_single_agent
name: Simple Single Agent
version: 1.0.0

config:
  max_duration: "10m"

groups:
  - id: main
    execution: sequential
    timeout: "8m"
    completion:
      type: all              # All agents (just 1) must succeed
      threshold: 1.0
    agents:
      - id: worker
        name: Worker
        provider: openai
        model: gpt-4
        system_prompt: "Complete the task."
        tools: [Bash, Read, Grep]
        capabilities:
          max_tokens: 4000
          temperature: 0.5
```

✅ **Works because**: 1 agent, `type: all`, 1/1 = 100%

---

## Template 2: Parallel Specialists (CORRECTED)

**IMPORTANT**: Use `consensus` for parallel, not `all`!

```yaml
id: parallel_specialists
name: Parallel Specialists
version: 1.0.0

config:
  max_duration: "20m"

groups:
  - id: analysis
    execution: parallel
    timeout: "15m"
    completion:
      type: consensus        # ← Changed from "all" to "consensus"
      threshold: 0.7         # ← 70% need to agree
      max_failures: 1        # ← Allow 1 to fail
    agents:
      - id: tech_expert
        name: Technical Expert
        provider: openai
        model: gpt-4
        system_prompt: |
          You are a technical expert.
          Analyze: [TASK]
          Focus on technical aspects.
        tools: [Bash, Read, Grep]
        capabilities:
          max_tokens: 4000
          temperature: 0.5
      
      - id: business_expert
        name: Business Expert
        provider: openai
        model: gpt-4
        system_prompt: |
          You are a business expert.
          Analyze: [TASK]
          Focus on business aspects.
        tools: [Bash, Read, Grep]
        capabilities:
          max_tokens: 4000
          temperature: 0.5
      
      - id: risk_expert
        name: Risk Expert
        provider: openai
        model: gpt-4
        system_prompt: |
          You are a risk expert.
          Analyze: [TASK]
          Focus on risks.
        tools: [Bash, Read, Grep]
        capabilities:
          max_tokens: 4000
          temperature: 0.5
```

✅ **Works because**: 
- 3 agents run in parallel
- Need 70% success (2/3 = 66.7% is close but might fail; 3/3 = 100% passes)
- If 1 fails and 2 succeed: 66.7% < 70% → Still might fail
- Better: Change threshold to 0.6 or use `type: majority`

---

## Template 2B: Parallel with Majority (SAFER)

```yaml
id: parallel_specialists
name: Parallel Specialists
version: 1.0.0

config:
  max_duration: "20m"

groups:
  - id: analysis
    execution: parallel
    timeout: "15m"
    completion:
      type: majority         # ← More than 50% must succeed
    agents:
      - id: tech_expert
        # ... (same as above)
      - id: business_expert
        # ... (same as above)
      - id: risk_expert
        # ... (same as above)
```

✅ **Works because**: 
- Need > 50% success
- 2/3 = 66.7% > 50% ✅
- 1/3 = 33.3% < 50% ❌
- Even if 1 fails, 2 succeed → Works

---

## Template 3: Sequential Multi-Stage (WORKS)

```yaml
id: plan_execute_review
name: Plan Execute Review
version: 1.0.0

config:
  max_duration: "30m"

groups:
  # Stage 1: Planning
  - id: planning
    execution: sequential
    timeout: "5m"
    completion:
      type: all              # ← Single agent, must succeed
      max_failures: 0
    agents:
      - id: planner
        name: Planner
        provider: openai
        model: gpt-4
        system_prompt: "Create a detailed plan for: [TASK]"
        tools: [Bash, Read]
        capabilities:
          max_tokens: 4000
          temperature: 0.3

  # Stage 2: Execution
  - id: execution
    execution: sequential
    depends_on: [planning]
    timeout: "15m"
    completion:
      type: all              # ← Single agent, must succeed
      max_failures: 0
    agents:
      - id: executor
        name: Executor
        provider: openai
        model: gpt-4
        system_prompt: "Execute the plan from the planning stage."
        context_sources:
          - "groups.planning.output"
        tools: [Bash, Read, Write]
        capabilities:
          max_tokens: 8000
          temperature: 0.2

  # Stage 3: Review
  - id: review
    execution: sequential
    depends_on: [execution]
    timeout: "5m"
    completion:
      type: all              # ← Single agent, must succeed
      max_failures: 0
    agents:
      - id: reviewer
        name: Reviewer
        provider: openai
        model: gpt-4
        system_prompt: "Review the execution results."
        context_sources:
          - "groups.planning.output"
          - "groups.execution.output"
        tools: [Bash, Read]
        capabilities:
          max_tokens: 4000
          temperature: 0.3
```

✅ **Works because**: 
- Each stage has 1 agent
- `type: all` with 1 agent = that agent must succeed
- If any agent errors → stage fails

---

## Template 4: Parallel Code Review with Consensus

```yaml
id: code_review
name: Parallel Code Review
version: 1.0.0

config:
  max_duration: "20m"

groups:
  - id: review
    execution: parallel
    timeout: "15m"
    completion:
      type: consensus        # ← Need agreement
      threshold: 0.67        # ← 67% (2/3)
    agents:
      - id: security_reviewer
        name: Security Reviewer
        provider: openai
        model: gpt-4
        system_prompt: "Review code for security issues."
        tools: [Bash, Read, Grep]
        capabilities:
          max_tokens: 3000
          temperature: 0.3
      
      - id: performance_reviewer
        name: Performance Reviewer
        provider: openai
        model: gpt-4
        system_prompt: "Review code for performance issues."
        tools: [Bash, Read, Grep]
        capabilities:
          max_tokens: 3000
          temperature: 0.3
      
      - id: style_reviewer
        name: Style Reviewer
        provider: openai
        model: gpt-4
        system_prompt: "Review code style and readability."
        tools: [Bash, Read, Grep]
        capabilities:
          max_tokens: 3000
          temperature: 0.3
```

✅ **Works because**: 
- 3 agents in parallel
- Need 67% success (2/3 agents succeeding = 66.7% ≈ 67%)
- Actually need all 3 to succeed with this threshold

⚠️ **Better**: Use `threshold: 0.6` (60%) to allow 1 failure, or use `type: majority`

---

## Template 5: First-One-Wins (SAFE)

```yaml
id: first_one_wins
name: First Success Wins
version: 1.0.0

config:
  max_duration: "10m"

groups:
  - id: analysis
    execution: parallel
    timeout: "8m"
    completion:
      type: first            # ← Only need ONE to succeed
    agents:
      - id: fast_analyzer
        name: Fast Analyzer
        provider: openai
        model: gpt-3.5-turbo  # Fast and cheap
        system_prompt: "Analyze quickly: [TASK]"
        tools: [Bash, Read]
        capabilities:
          max_tokens: 2000
          temperature: 0.5
      
      - id: thorough_analyzer
        name: Thorough Analyzer
        provider: openai
        model: gpt-4          # More thorough
        system_prompt: "Analyze thoroughly: [TASK]"
        tools: [Bash, Read, Grep]
        capabilities:
          max_tokens: 4000
          temperature: 0.5
```

✅ **Works because**: 
- Only need ONE agent to succeed
- Fast one might succeed first
- If it fails, thorough one might succeed
- Only fails if ALL fail

---

## Template 6: Strict Sequential with Tolerance

```yaml
id: sequential_with_tolerance
name: Sequential with Tolerance
version: 1.0.0

config:
  max_duration: "30m"

groups:
  - id: pipeline
    execution: sequential
    timeout: "25m"
    completion:
      type: all
      max_failures: 0        # ← Each step must work
    agents:
      - id: step1
        name: Step 1
        provider: openai
        model: gpt-4
        system_prompt: "Step 1: Initialize"
        tools: [Bash, Read]
        capabilities:
          max_tokens: 2000
          temperature: 0.3
      
      - id: step2
        name: Step 2
        provider: openai
        model: gpt-4
        system_prompt: "Step 2: Process using output from step 1"
        tools: [Bash, Read, Write]
        capabilities:
          max_tokens: 4000
          temperature: 0.3
      
      - id: step3
        name: Step 3
        provider: openai
        model: gpt-4
        system_prompt: "Step 3: Finalize using outputs from step 2"
        tools: [Bash, Read, Write]
        capabilities:
          max_tokens: 2000
          temperature: 0.3
```

✅ **Works because**: 
- Sequential: step1 → step2 → step3
- Each must succeed for next to run
- Input passes from one to next (currentInput = output)
- If any step errors, pipeline stops

---

## Quick Reference: Which Completion Type to Use

| Scenario | Type | Why |
|---|---|---|
| **1 agent** | `all` | Simple, must succeed |
| **Multiple parallel agents** | `consensus` or `majority` | Allows flexibility |
| **Need at least one answer** | `first` | Fastest solution wins |
| **Need > 50% agreement** | `majority` | Quorum based |
| **Need X% agreement** | `consensus` + threshold | Flexible threshold |
| **Sequential pipeline** | `all` | Each step must work |

---

## Recommended Safe Defaults

### For Parallel Agents:
```yaml
completion:
  type: majority         # > 50% must succeed
```
- Most forgiving
- Allows some failures
- Works with any agent count

### For Consensus Requirements:
```yaml
completion:
  type: consensus
  threshold: 0.6         # 60% minimum
```
- Requires majority but flexible
- Threshold can be tuned

### For Sequential:
```yaml
completion:
  type: all
  max_failures: 0
```
- Each step critical
- No tolerance for failure

---

## Common Mistakes Fixed

❌ **OLD (BROKEN)**:
```yaml
completion:
  type: all              # ALL agents must succeed
agents:
  - agent1
  - agent2
  - agent3
```
→ If any agent fails → entire group fails

✅ **NEW (WORKS)**:
```yaml
completion:
  type: majority         # > 50% must succeed
agents:
  - agent1
  - agent2
  - agent3
```
→ 2/3 succeed = 66.7% > 50% ✅

---

Use these corrected templates. They work with the actual code logic.
