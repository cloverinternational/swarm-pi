# Execution Strategies — Decision Guide

## Overview

Every `AgentGroup` has an `execution` field that controls how its agents run relative to each other. This is separate from the DAG-level scheduling (which is controlled by `depends_on`).

```
Intra-group:  execution: parallel | sequential | adversarial
Inter-group:  depends_on: [group_ids...]
```

---

## `parallel` — Concurrent Independent Execution

All agents in the group launch simultaneously in separate goroutines. Each agent receives the same input (the group's accumulated context). They do **not** see each other's outputs during execution — only the final merged result is passed downstream.

**Use when:**
- Agents are doing independent research / analysis on the same input
- You want to speed up a step by splitting work across agents
- Agents use different models and you want to compare their perspectives
- Gathering data from multiple sources simultaneously

**Completion types for parallel groups:**
- `type: all` — every agent must finish (most common)
- `type: first` — the first agent to succeed wins; others are cancelled
- `type: consensus` — agents must agree above a `threshold`

```yaml
- id: multi_model_analysis
  name: Multi-Model Analysis
  execution: parallel
  completion:
    type: all
  agents:
    - id: fast_agent
      name: Fast Check
      provider: "@current"
      model: "@current"
      system_prompt: "Quickly scan for obvious issues."
      tools: [Bash, Grep]
      capabilities:
        max_tokens: 2000
        temperature: 0.1
    - id: deep_agent
      name: Deep Analysis
      provider: anthropic
      model: claude-sonnet-4
      system_prompt: "Perform a thorough analysis."
      tools: [Read, Grep, Bash]
      capabilities:
        max_tokens: 16000
        temperature: 0.2
```

---

## `sequential` — Pipeline Execution

Agents run one after another. Each agent receives the output of the previous agent as additional context. This creates a natural pipeline where each step builds on the last.

**Use when:**
- Step 1 must complete before step 2 can begin (e.g., plan → implement → verify)
- Each agent refines or extends the previous agent's work
- You're building a multi-pass refinement loop
- Actions must happen in a specific order (e.g., read then write)

**Context flow:** Agent N's output is appended to the context for Agent N+1.

```yaml
- id: plan_implement_verify
  name: Plan → Implement → Verify
  execution: sequential
  agents:
    - id: planner
      name: Planner
      provider: "@current"
      model: "@current"
      system_prompt: "Create a detailed implementation plan."
      tools: [Read, Grep]
      capabilities:
        max_tokens: 4000
        temperature: 0.4
    - id: implementer
      name: Implementer
      provider: "@current"
      model: "@current"
      system_prompt: "Execute the plan from the previous step exactly."
      tools: [Read, Write, Edit, Bash]
      capabilities:
        max_tokens: 16000
        temperature: 0.1
    - id: verifier
      name: Verifier
      provider: "@current"
      model: "@current"
      system_prompt: "Verify the implementation from the previous step is correct."
      tools: [Bash, Read]
      capabilities:
        max_tokens: 4000
        temperature: 0.0
```

---

## `adversarial` — Debate Until Consensus

Agents run in multiple debate rounds. Each agent can see the others' arguments and must respond. The group completes when agents reach consensus above the `threshold`, or when `max_turns` is exhausted.

**Use when:**
- You need a quality gate via critique (one agent proposes, another challenges)
- Decision-making where you want multiple perspectives stress-tested
- Security reviews (attacker vs. defender)
- Architecture decisions (proponent vs. critic)

**Requirements for adversarial:**
- At least 2 agents
- `completion.type: consensus` with a `threshold`
- Higher temperatures recommended (0.6–0.8) to encourage diverse perspectives

```yaml
- id: security_debate
  name: Security Review Debate
  execution: adversarial
  timeout: 15m
  completion:
    type: consensus
    threshold: 0.75
    min_agents: 2
  agents:
    - id: attacker
      name: Red Team
      provider: "@current"
      model: "@current"
      system_prompt: |-
        You are a security researcher. Find every vulnerability in the proposed code.
        Be aggressive. Look for injection, auth bypasses, data exposure, and logic flaws.
      tools: [Read, Grep]
      capabilities:
        max_tokens: 4000
        temperature: 0.7
    - id: defender
      name: Blue Team
      provider: "@current"
      model: "@current"
      system_prompt: |-
        You are a security engineer defending the implementation.
        Address all concerns raised by the red team with specific mitigations.
      tools: [Read, Grep]
      capabilities:
        max_tokens: 4000
        temperature: 0.6
    - id: judge
      name: Judge
      provider: "@current"
      model: "@current"
      system_prompt: |-
        You are a neutral security architect. Evaluate both sides and render a verdict:
        APPROVED (safe to ship), CONDITIONAL (safe with listed changes), or REJECTED.
      tools: [Read]
      capabilities:
        max_tokens: 2000
        temperature: 0.3
```

---

## DAG Patterns

### Linear Pipeline
```
[gather] → [analyze] → [report]
```
```yaml
- id: gather
- id: analyze
  depends_on: [gather]
- id: report
  depends_on: [analyze]
```

### Fan-Out / Fan-In
```
         ┌─ [scan_a] ─┐
[setup] ─┤             ├─ [merge]
         └─ [scan_b] ─┘
```
```yaml
- id: setup
- id: scan_a
  depends_on: [setup]
- id: scan_b
  depends_on: [setup]
- id: merge
  depends_on: [scan_a, scan_b]
```

### Diamond
```
       ┌─ [analyze] ─┐
[plan] ─┤              ├─ [commit]
       └─ [test]    ─┘
```
```yaml
- id: plan
- id: analyze
  depends_on: [plan]
- id: test
  depends_on: [plan]
- id: commit
  depends_on: [analyze, test]
```

### Independent Parallel Chains
```
[chain_a_1] → [chain_a_2]
[chain_b_1] → [chain_b_2]
```
```yaml
- id: chain_a_1
- id: chain_a_2
  depends_on: [chain_a_1]
- id: chain_b_1
- id: chain_b_2
  depends_on: [chain_b_1]
```
Both chains start simultaneously at layer 0, independently.

---

## Completion Criteria Reference

| `type` | Meaning | Best for |
|---|---|---|
| `all` | Every agent must complete successfully | When all results are needed |
| `first` | First successful agent wins; others cancelled | Race condition / fastest wins |
| `consensus` | Agents must agree above `threshold` | Quality gates, decision-making |

### Consensus Threshold Guide

| Threshold | Meaning |
|---|---|
| `1.0` | Unanimous (all agents must agree) |
| `0.8` | Strong majority (e.g., 4/5 agents) |
| `0.67` | Two-thirds majority |
| `0.5` | Simple majority |

---

## Output Strategy Reference

| Strategy | Behavior | Use when |
|---|---|---|
| `raw` (default) | Concatenates all agent outputs | Downstream group reads all outputs |
| `first` | Uses only the first successful agent's output | Race pattern |
| `synthesize` | Uses a `coordinator` agent to merge all outputs | You need a single cohesive output |

For `synthesize`, define a `coordinator` agent on the group. The coordinator receives all agent outputs and produces the group's final output.

```yaml
- id: synthesized_group
  execution: parallel
  output_strategy: synthesize
  coordinator:
    id: synthesizer
    name: Synthesizer
    provider: "@current"
    model: "@current"
    system_prompt: |-
      You receive multiple agent outputs. Synthesize them into one coherent result.
      Resolve conflicts by choosing the most well-reasoned position.
  agents:
    - ...
```
