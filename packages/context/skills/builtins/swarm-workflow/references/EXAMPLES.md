# Workflow Examples — Annotated Real-World Patterns

## 1. Git Staging & Commit Workflow (Production)

The real workflow from `swarm-tui/workflows/git_staging_workflow.yaml`. Three sequential groups: stage → separate → commit.

```yaml
id: git_staging_workflow
name: Git Staging & Commit Workflow
version: 1.0.0
description: Stages changes, reviews them, separates into logical individual commits, then merges and commits

config:
  allow_human_intervention: true
  fail_on_steering_block: false
  max_duration: 20m
  max_retries: 3
  timeout_behavior: partial

steering:
  type: none

groups:
  - id: staging_review
    name: Staging & Review
    description: Stages changes and performs initial review
    execution: sequential          # sequential: review happens after staging
    timeout: 5m
    completion:
      type: all
      threshold: 1
    agents:
      - id: staging_agent
        name: Staging Agent
        provider: Cerebras          # literal: pinned to fast inference provider
        model: zai-glm-4.7
        system_prompt: |-
          You are a Git staging specialist. Your role is to:
          1. Analyze all changes using git status and git diff
          2. Stage appropriate changes using git add
          3. Review staged changes for quality, consistency, and potential issues
          4. Provide a detailed report of what was staged and any concerns
          5. Identify different types of changes (features, fixes, refactors, docs, etc.)
        tools: [Bash, Grep, Read, Write, Edit, TodoRead, TodoWrite, Task, BackgroundTask, ReadBackgroundCommand]
        capabilities:
          max_tokens: 25000
          temperature: 0.3

  - id: commit_separation
    name: Commit Separation
    description: Analyzes changes and separates them into logical individual commits
    execution: sequential
    depends_on: [staging_review]   # depends on staging_review completing first
    timeout: 8m
    completion:
      type: all
      threshold: 1
    agents:
      - id: separation_agent
        name: Commit Separator
        provider: Cerebras
        model: zai-glm-4.7
        system_prompt: |-
          You are a commit organization specialist. Your role is to:
          1. Analyze all staged changes from the previous stage
          2. Group changes into logical, atomic commits based on:
             - Type of change (feature, bugfix, refactor, docs, tests, style)
             - Related functionality or files
             - Dependencies between changes
          3. Create a detailed plan for individual commits with:
             - Which files/hunks belong to each commit
             - Descriptive commit messages following conventional commit format
             - Order of commits (respecting dependencies)
          4. Ensure each commit is self-contained and builds successfully
        tools: [Bash, Grep, Read, TodoRead, TodoWrite, ReadBackgroundCommand]
        capabilities:
          max_tokens: 8000
          temperature: 0.4

  - id: merge_commit
    name: Merge & Commit
    description: Executes the individual commits and performs final merge
    execution: sequential
    depends_on: [commit_separation] # depends on separation plan being ready
    timeout: 5m
    completion:
      type: all
      threshold: 1
    agents:
      - id: merge_agent
        name: Merge Agent
        provider: Cerebras
        model: zai-glm-4.7
        system_prompt: |-
          You are a Git commit execution specialist. Your role is to:
          1. Follow the commit plan from the separation stage
          2. For each planned commit: stage files, create commit, verify success
          3. After all commits: verify working directory is clean, execute any final merges
          4. Provide a summary of all commits created and final repository state
        tools: [Bash, Grep, Read, Write, TodoRead, TodoWrite, ReadBackgroundCommand]
        capabilities:
          max_tokens: 8000
          temperature: 0.2

metadata:
  author: Workflow Assistant
  category: development
  tags: [git, version-control, staging, commit]
  use_cases: [git workflow automation, code staging, commit organization]
```

---

## 2. Parallel Research + Synthesis

Fan-out pattern: multiple researchers gather data in parallel, then a coordinator synthesizes.

```yaml
id: parallel_research
name: Parallel Research & Synthesis
version: 1.0.0

groups:
  - id: research
    name: Research
    execution: parallel            # all 3 researchers run simultaneously
    timeout: 10m
    completion:
      type: all
    agents:
      - id: primary_researcher
        name: Primary Researcher
        provider: "@current"
        model: "@current"
        system_prompt: "Research the main topic thoroughly."
        tools: [Bash, Read, Grep]
      - id: context_researcher
        name: Context Researcher
        provider: "@profile"
        model: "@sub_agent"        # fast model for supporting research
        system_prompt: "Gather background context and related prior work."
        tools: [Bash, Read, Grep]
      - id: edge_case_researcher
        name: Edge Case Researcher
        provider: "@profile"
        model: "@sub_agent"
        system_prompt: "Find edge cases, exceptions, and potential failure modes."
        tools: [Bash, Grep]

  - id: synthesis
    name: Synthesis
    execution: sequential
    depends_on: [research]         # waits for all researchers
    output_strategy: synthesize    # coordinator merges the 3 outputs
    coordinator:
      id: synthesizer
      name: Synthesizer
      provider: "@profile"
      model: "@main"               # powerful model for synthesis
      system_prompt: |-
        You have received research from 3 agents. Synthesize into one document:
        1. Executive summary (3 sentences max)
        2. Key findings (bullet list)
        3. Edge cases and risks
        4. Recommended next steps
    agents:
      - id: formatter
        name: Formatter
        provider: "@current"
        model: "@current"
        system_prompt: "Format the synthesized research as a clean Markdown report."
        tools: [Write]
```

---

## 3. Adversarial Code Review

Two-agent debate with a judge. Classic red-team / blue-team pattern.

```yaml
id: adversarial_code_review
name: Adversarial Code Review
version: 1.0.0

groups:
  - id: debate
    name: Security Debate
    execution: adversarial
    timeout: 20m
    completion:
      type: consensus
      threshold: 0.75
    agents:
      - id: red_team
        name: Red Team
        provider: "@current"
        model: "@current"
        system_prompt: |-
          You are a security researcher. Attack this code. Find:
          - Injection vulnerabilities (SQL, command, path traversal)
          - Authentication/authorization bypasses
          - Data exposure risks
          - Logic flaws and race conditions
          Be aggressive and thorough.
        tools: [Read, Grep]
        capabilities:
          max_tokens: 4000
          temperature: 0.7
      - id: blue_team
        name: Blue Team
        provider: "@current"
        model: "@current"
        system_prompt: |-
          You are a security engineer defending the implementation.
          For each vulnerability raised, either: prove it's a false positive,
          or provide a specific code-level mitigation.
        tools: [Read, Grep]
        capabilities:
          max_tokens: 4000
          temperature: 0.5
      - id: judge
        name: Security Judge
        provider: "@profile"
        model: "@steering"         # steering model for coordination
        system_prompt: |-
          Judge the debate. Output one of:
          APPROVED: safe to merge
          CONDITIONAL: safe with listed changes (enumerate them)
          REJECTED: must not merge (explain why)
        tools: [Read]
        capabilities:
          max_tokens: 2000
          temperature: 0.2
```

---

## 4. Interactive Parameterized Workflow

Uses `parameters` to prompt the user before execution.

```yaml
id: env_deploy_workflow
name: Environment Deployment
version: 1.0.0

parameters:
  - id: target_env
    question: "Which environment are you deploying to?"
    type: choice
    required: true
    choices: [dev, staging, production]
  - id: dry_run
    question: "Run as dry-run (no actual changes)?"
    type: confirm
    default: true
  - id: version_tag
    question: "Which version tag to deploy? (e.g. v1.2.3)"
    type: text
    required: true
    validation:
      pattern: "^v[0-9]+\\.[0-9]+\\.[0-9]+$"

groups:
  - id: preflight
    name: Preflight Checks
    execution: parallel
    agents:
      - id: env_check
        name: Environment Check
        provider: "@current"
        model: "@current"
        system_prompt: |-
          Check deployment environment readiness.
          Verify: connectivity, credentials, current deployment state.
        tools: [Bash]

  - id: deploy
    name: Deploy
    execution: sequential
    depends_on: [preflight]
    agents:
      - id: deployer
        name: Deployer
        provider: "@current"
        model: "@current"
        system_prompt: |-
          Execute the deployment following the preflight check results.
          The target environment and version are available in your context.
        tools: [Bash, Read]
```

---

## 5. Diamond DAG — Parallel Analysis + Merge

```yaml
id: diamond_workflow
name: Diamond DAG Example
version: 1.0.0

groups:
  - id: load
    name: Load & Parse
    execution: sequential
    agents:
      - id: loader
        name: Loader
        provider: "@current"
        model: "@current"
        system_prompt: "Load and parse the input data."
        tools: [Read, Bash]

  - id: static_analysis
    name: Static Analysis
    execution: parallel
    depends_on: [load]             # both analysis groups start after load
    agents:
      - id: linter
        name: Linter
        provider: "@profile"
        model: "@sub_agent"
        system_prompt: "Run static analysis and linting."
        tools: [Bash]

  - id: dynamic_analysis
    name: Dynamic Analysis
    execution: parallel
    depends_on: [load]             # also depends on load (diamond shape)
    agents:
      - id: tester
        name: Tester
        provider: "@profile"
        model: "@sub_agent"
        system_prompt: "Run dynamic tests and check runtime behavior."
        tools: [Bash]

  - id: merge_results
    name: Merge Results
    execution: sequential
    depends_on: [static_analysis, dynamic_analysis]  # waits for BOTH branches
    agents:
      - id: merger
        name: Merger
        provider: "@current"
        model: "@current"
        system_prompt: "Merge static and dynamic analysis results into a final report."
        tools: [Write, Read]
```
