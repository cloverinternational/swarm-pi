# Workflow Copy & Paste Templates

Ready-to-use workflow YAML templates for common patterns. These work in any directory and use generic model references that don't go obsolete.

---

## Getting Started

1. **Create a new `.yaml` file** in your workflows directory
2. **Copy a template** from below
3. **Customize** the model names and system prompts
4. **Validate** using your workflow validation tool
5. **Execute** through your workflow runner

---

## Template 1: Simple Single-Agent Task

**Use When**: One agent handles the entire task

```yaml
id: simple_task
name: Simple Single-Agent Task
description: Basic workflow with one agent handling a straightforward task
version: 1.0.0

config:
  max_duration: "10m"
  allow_human_intervention: true
  max_retries: 2

groups:
  - id: task
    name: Task Execution
    execution: sequential
    timeout: "8m"
    completion:
      type: all
    
    agents:
      - id: executor
        name: Task Executor
        provider: openai           # Change to your provider
        model: gpt-4               # Change to your model
        system_prompt: |
          You are a helpful assistant.
          
          Your task is to: [SPECIFY YOUR TASK HERE]
          
          Provide clear, well-structured output.
        
        tools:
          - Bash
          - Read
          - Grep
        
        capabilities:
          max_tokens: 2000
          temperature: 0.5

metadata:
  author: Your Name
  category: general
  tags: [simple, sequential]
  use_cases: [Simple task execution]
```

---

## Template 2: Parallel Specialists

**Use When**: Multiple agents with different specialties analyze independently, then come to agreement

```yaml
id: parallel_specialists
name: Parallel Specialists Analysis
description: Multiple specialists analyze a topic from different angles in parallel
version: 1.0.0

config:
  max_duration: "20m"
  allow_human_intervention: true
  max_retries: 2

groups:
  - id: specialist_analysis
    name: Specialist Analysis
    description: Multiple specialists provide independent analysis
    execution: parallel
    timeout: "15m"
    
    completion:
      type: consensus
      threshold: 0.7
      min_agents: 2
    
    agents:
      - id: specialist_one
        name: Specialist One
        provider: anthropic
        model: claude-sonnet        # Change to your model
        system_prompt: |
          You are a [SPECIALIST DOMAIN 1] specialist.
          
          Analyze the topic from your unique perspective.
          Focus on: [KEY FOCUS AREA 1]
          
          Provide thorough analysis with specific insights.
        
        tools: [Read, Bash, Grep]
        capabilities:
          max_tokens: 4000
          temperature: 0.5
      
      - id: specialist_two
        name: Specialist Two
        provider: anthropic
        model: claude-sonnet
        system_prompt: |
          You are a [SPECIALIST DOMAIN 2] specialist.
          
          Analyze the topic from your unique perspective.
          Focus on: [KEY FOCUS AREA 2]
          
          Provide thorough analysis with specific insights.
        
        tools: [Read, Bash, Grep]
        capabilities:
          max_tokens: 4000
          temperature: 0.5
      
      - id: specialist_three
        name: Specialist Three
        provider: openai
        model: gpt-4
        system_prompt: |
          You are a [SPECIALIST DOMAIN 3] specialist.
          
          Analyze the topic from your unique perspective.
          Focus on: [KEY FOCUS AREA 3]
          
          Provide thorough analysis with specific insights.
        
        tools: [Read, Bash]
        capabilities:
          max_tokens: 4000
          temperature: 0.5

metadata:
  author: Your Name
  category: analysis
  tags: [parallel, specialists, consensus]
  use_cases: [Multi-perspective analysis, expert panels]
```

---

## Template 3: Sequential Multi-Stage Workflow

**Use When**: You need stages that depend on previous outputs (Plan → Execute → Review)

```yaml
id: plan_execute_review
name: Plan, Execute, Review Workflow
description: Three-stage workflow with dependencies
version: 1.0.0

config:
  max_duration: "30m"
  allow_human_intervention: true
  max_retries: 2
  timeout_behavior: "partial"

groups:
  # Stage 1: Planning
  - id: planning
    name: Planning Stage
    description: Create detailed plan for execution
    execution: sequential
    timeout: "5m"
    
    completion:
      type: all
    
    agents:
      - id: planner
        name: Strategic Planner
        provider: anthropic
        model: claude-opus           # Use most capable model here
        system_prompt: |
          You are a strategic planner and analyst.
          
          Create a detailed, step-by-step plan for: [TASK DESCRIPTION]
          
          Your plan should:
          1. Define clear objectives
          2. Break down into specific, actionable steps
          3. Identify dependencies and sequencing
          4. Estimate resources and time for each step
          5. Flag potential risks or obstacles
          
          Format as a numbered list with clear explanations.
        
        tools: [Read, Bash, Grep]
        capabilities:
          max_tokens: 4000
          temperature: 0.3
  
  # Stage 2: Execution
  - id: execution
    name: Execution Stage
    description: Execute the plan from stage 1
    execution: sequential
    depends_on: ["planning"]
    timeout: "15m"
    
    completion:
      type: all
    
    agents:
      - id: executor
        name: Implementation Executor
        provider: anthropic
        model: claude-sonnet
        system_prompt: |
          You are an implementation specialist.
          
          Execute the plan provided from the planning stage.
          
          Steps:
          1. Review the plan carefully
          2. Execute each step methodically
          3. Document results and outputs
          4. Adapt if obstacles arise
          5. Provide detailed progress updates
          
          Focus on accuracy and quality over speed.
        
        context_sources:
          - "groups.planning.output"
        
        tools: [Read, Write, Bash, Grep]
        capabilities:
          max_tokens: 6000
          temperature: 0.3
  
  # Stage 3: Review
  - id: review
    name: Review Stage
    description: Verify execution quality and completeness
    execution: sequential
    depends_on: ["execution"]
    timeout: "5m"
    
    completion:
      type: all
    
    agents:
      - id: reviewer
        name: Quality Reviewer
        provider: anthropic
        model: claude-sonnet
        system_prompt: |
          You are a quality assurance specialist.
          
          Review the execution results against the plan.
          
          Evaluate:
          1. Was the plan followed accurately?
          2. Were all steps completed?
          3. Is the output of high quality?
          4. Were there any significant issues?
          5. What could be improved?
          
          Provide clear verdict: APPROVED or NEEDS_REVISION
        
        context_sources:
          - "groups.planning.output"
          - "groups.execution.output"
        
        tools: [Read, Bash, Grep]
        capabilities:
          max_tokens: 3000
          temperature: 0.3

metadata:
  author: Your Name
  category: workflow
  tags: [sequential, multi-stage, plan-execute-review]
  use_cases: [Feature development, refactoring, complex tasks]
```

---

## Template 4: Adversarial Debate & Refinement

**Use When**: You need rigorous critique and improvement through debate

```yaml
id: adversarial_debate
name: Adversarial Debate & Refinement
description: Proposal → Critique → Refinement workflow
version: 1.0.0

config:
  max_duration: "20m"
  allow_human_intervention: true
  max_retries: 2

groups:
  # Stage 1: Initial Proposal
  - id: proposal
    name: Proposal Creation
    description: Create initial proposal or solution
    execution: sequential
    timeout: "5m"
    
    completion:
      type: all
    
    agents:
      - id: proposer
        name: Proposer
        provider: anthropic
        model: claude-sonnet
        system_prompt: |
          You are a creative proposer.
          
          Create a comprehensive proposal for: [PROPOSAL TOPIC]
          
          Your proposal should:
          1. Clearly state the objective
          2. Provide detailed approach/methodology
          3. Explain expected benefits
          4. Acknowledge potential concerns
          5. Be thorough and well-reasoned
          
          Make the strongest possible case.
        
        tools: [Read, Bash]
        capabilities:
          max_tokens: 4000
          temperature: 0.6
  
  # Stage 2: Parallel Critique
  - id: critique
    name: Adversarial Critique
    description: Multiple critics provide different perspectives
    execution: parallel
    depends_on: ["proposal"]
    timeout: "8m"
    
    completion:
      type: consensus
      threshold: 0.6
      min_agents: 2
    
    agents:
      - id: skeptic
        name: Critical Skeptic
        provider: openai
        model: gpt-4
        system_prompt: |
          You are a critical skeptic.
          
          Your role is to find weaknesses in the proposal.
          
          Identify:
          1. Logical flaws or assumptions
          2. Unrealistic expectations
          3. Hidden costs or risks
          4. Better alternatives
          5. Implementation challenges
          
          Be constructive but thorough in your critique.
        
        context_sources:
          - "groups.proposal.output"
        
        tools: [Read, Bash]
        capabilities:
          max_tokens: 3000
          temperature: 0.6
      
      - id: supporter
        name: Proposal Supporter
        provider: anthropic
        model: claude-sonnet
        system_prompt: |
          You are a proposal advocate.
          
          Your role is to defend and strengthen the proposal.
          
          Your tasks:
          1. Highlight overlooked benefits
          2. Address potential criticisms
          3. Provide supporting evidence
          4. Build consensus around the proposal
          5. Strengthen weak points
          
          Make the strongest case for the proposal.
        
        context_sources:
          - "groups.proposal.output"
        
        tools: [Read, Bash]
        capabilities:
          max_tokens: 3000
          temperature: 0.6
  
  # Stage 3: Refinement
  - id: refinement
    name: Proposal Refinement
    description: Incorporate critiques and improve proposal
    execution: sequential
    depends_on: ["critique"]
    timeout: "5m"
    
    completion:
      type: all
    
    agents:
      - id: refiner
        name: Refiner
        provider: anthropic
        model: claude-opus
        system_prompt: |
          You are a proposal refinement specialist.
          
          Synthesize the critique and create an improved proposal.
          
          Create a refined proposal that:
          1. Addresses all major critiques
          2. Strengthens identified weak points
          3. Incorporates valid suggestions
          4. Maintains core value proposition
          5. Is more realistic and robust
          
          For each criticism addressed, explain how.
        
        context_sources:
          - "groups.proposal.output"
          - "groups.critique.output"
        
        tools: [Read, Write, Bash]
        capabilities:
          max_tokens: 6000
          temperature: 0.4

metadata:
  author: Your Name
  category: design-review
  tags: [adversarial, debate, critique, refinement]
  use_cases: [Design review, proposal evaluation, architecture decisions]
```

---

## Template 5: Hybrid with Quality Gates & Steering

**Use When**: You need intelligent oversight and conditional routing

```yaml
id: hybrid_quality_gates
name: Hybrid Workflow with Quality Gates
description: Multi-stage workflow with steering and quality gates
version: 1.0.0

config:
  max_duration: "25m"
  allow_human_intervention: true
  fail_on_steering_block: false
  max_retries: 2

steering:
  type: hybrid
  
  llm_meta_agent:
    id: overseer
    name: Workflow Overseer
    provider: anthropic
    model: claude-opus
    system_prompt: |
      You oversee the entire workflow execution.
      
      Your responsibilities:
      1. Monitor execution quality
      2. Evaluate consensus and agreement between agents
      3. Determine if additional deep-dive analysis is needed
      4. Make routing decisions
      5. Flag quality issues
      
      Be objective and rigorous in your oversight.
  
  rules:
    - id: quality_threshold
      condition: "output.quality < 0.7"
      action: "escalate_to_human"
      priority: 20
    
    - id: good_consensus
      condition: "output.confidence >= 0.8"
      action: "approve"
      priority: 10
    
    - id: critical_issues
      condition: "output contains 'CRITICAL'"
      action: "route_to_group"
      target_group: "deep_dive"
      priority: 30

groups:
  # Stage 1: Initial Analysis
  - id: initial_analysis
    name: Initial Analysis
    description: First-pass analysis by multiple agents
    execution: parallel
    timeout: "8m"
    
    completion:
      type: consensus
      threshold: 0.7
      min_agents: 2
    
    agents:
      - id: analyst_a
        name: Analyst A
        provider: anthropic
        model: claude-sonnet
        system_prompt: |
          You are an analyst. Analyze the topic thoroughly.
          
          Provide:
          1. Key findings
          2. Supporting evidence
          3. Confidence level (0-1)
          4. Potential issues
          
          Be specific and evidence-based.
        
        tools: [Read, Bash, Grep]
        capabilities:
          max_tokens: 4000
          temperature: 0.5
      
      - id: analyst_b
        name: Analyst B
        provider: openai
        model: gpt-4
        system_prompt: |
          You are an analyst with a different perspective.
          
          Analyze independently and provide:
          1. Alternative viewpoints
          2. Potential blind spots in other analyses
          3. Risk assessment
          4. Confidence level (0-1)
          
          Don't just agree—provide genuine alternative perspective.
        
        tools: [Read, Bash]
        capabilities:
          max_tokens: 4000
          temperature: 0.6
  
  # Stage 2: Deep Dive (Optional, routing-driven)
  - id: deep_dive
    name: Deep Dive Analysis
    description: Detailed investigation of critical issues
    execution: sequential
    depends_on: ["initial_analysis"]
    timeout: "10m"
    optional: true
    
    completion:
      type: all
    
    agents:
      - id: expert
        name: Subject Matter Expert
        provider: anthropic
        model: claude-opus
        system_prompt: |
          You are a subject matter expert.
          
          Perform deep analysis of critical issues.
          
          Investigate:
          1. Root causes of identified issues
          2. Expert perspective and solutions
          3. Impact assessment
          4. Detailed recommendations
          
          Be thorough and definitive.
        
        context_sources:
          - "groups.initial_analysis.output"
        
        tools: [Read, Bash, Grep, Write]
        capabilities:
          max_tokens: 6000
          temperature: 0.3
  
  # Stage 3: Synthesis
  - id: synthesis
    name: Final Synthesis
    description: Synthesize all findings into final output
    execution: sequential
    depends_on: ["initial_analysis"]
    timeout: "5m"
    
    completion:
      type: all
    
    agents:
      - id: synthesizer
        name: Synthesis Agent
        provider: anthropic
        model: claude-opus
        system_prompt: |
          You are a synthesis specialist.
          
          Create the final output incorporating:
          1. Initial analysis findings
          2. Deep-dive findings (if available)
          3. Overseer recommendations
          4. Areas of agreement
          5. Remaining uncertainties
          
          Produce clear, actionable output.
        
        context_sources:
          - "groups.initial_analysis.output"
          - "groups.deep_dive.output"
        
        tools: [Read, Write, Bash]
        capabilities:
          max_tokens: 6000
          temperature: 0.4

metadata:
  author: Your Name
  category: hybrid
  tags: [steering, quality-gates, intelligent-routing]
  use_cases: [Complex decisions, risk-sensitive workflows, quality-critical processes]
```

---

## Template 6: Interactive Forms & Dynamic Behavior

**Use When**: Workflow behavior changes based on user input

```yaml
id: interactive_analysis
name: Interactive User-Driven Analysis
description: Workflow that adapts based on user form inputs
version: 1.0.0

config:
  max_duration: "20m"
  allow_human_intervention: true

# Input forms presented to user
inputs:
  - id: analysis_topic
    name: Analysis Topic
    type: text
    required: true
    description: "What do you want to analyze?"
    placeholder: "e.g., Security vulnerabilities in API"
  
  - id: analysis_depth
    name: Analysis Depth
    type: choice
    required: true
    description: "How deep should the analysis be?"
    options:
      - label: "Quick (5-10 min)"
        value: "shallow"
      - label: "Standard (15-20 min)"
        value: "medium"
      - label: "Deep (25-30 min)"
        value: "deep"
    default: "medium"
  
  - id: analysis_angles
    name: Analysis Perspectives
    type: multi_choice
    required: true
    description: "Which perspectives should we analyze?"
    options:
      - label: "Technical"
        value: "technical"
      - label: "Business"
        value: "business"
      - label: "Risk/Security"
        value: "risk"
    default: ["technical", "business"]
  
  - id: output_style
    name: Output Style
    type: choice
    required: true
    description: "How should results be formatted?"
    options:
      - label: "Executive Summary"
        value: "summary"
      - label: "Detailed Report"
        value: "detailed"
      - label: "Bullet Points"
        value: "bullets"
    default: "summary"

groups:
  # Stage 1: Input preparation
  - id: input_prep
    name: Input Preparation
    execution: sequential
    timeout: "2m"
    
    completion:
      type: all
    
    agents:
      - id: input_processor
        name: Input Processor
        provider: anthropic
        model: claude-sonnet
        system_prompt: |
          You are an input processor.
          
          Process the user inputs:
          - Topic: ${inputs.analysis_topic}
          - Depth: ${inputs.analysis_depth}
          - Angles: ${inputs.analysis_angles}
          - Output: ${inputs.output_style}
          
          Prepare a research plan that:
          1. Clarifies the analysis scope
          2. Sets depth parameters
          3. Prepares analysis instructions
          4. Defines success criteria
        
        tools: [Read, Bash]
        capabilities:
          max_tokens: 2000
          temperature: 0.3
  
  # Stage 2: Parallel Analysis (agents conditional on user selection)
  - id: analysis
    name: Analysis
    execution: parallel
    depends_on: ["input_prep"]
    timeout: "15m"
    
    completion:
      type: all
    
    agents:
      # Technical analyst (always runs)
      - id: technical_analyst
        name: Technical Analyst
        provider: anthropic
        model: claude-sonnet
        system_prompt: |
          You are a technical analyst.
          
          Analyze: ${inputs.analysis_topic}
          Depth: ${inputs.analysis_depth}
          
          ${inputs.analysis_depth === 'deep' ? 'Provide comprehensive technical analysis with examples.' : 'Focus on key technical insights.'}
        
        context_sources: ["groups.input_prep.output"]
        tools: [Read, Bash, Grep]
        capabilities:
          max_tokens: |
            ${inputs.analysis_depth === 'shallow' ? 2000 : inputs.analysis_depth === 'medium' ? 4000 : 6000}
          temperature: 0.5
      
      # Business analyst (conditional on user selection)
      - id: business_analyst
        name: Business Analyst
        provider: anthropic
        model: claude-sonnet
        system_prompt: |
          You are a business analyst.
          
          Analyze: ${inputs.analysis_topic}
          Focus on business implications and value.
        
        context_sources: ["groups.input_prep.output"]
        tools: [Read, Bash]
        capabilities:
          max_tokens: 4000
          temperature: 0.5
        condition: |
          ${inputs.analysis_angles.includes('business')}
      
      # Risk analyst (conditional on user selection)
      - id: risk_analyst
        name: Risk Analyst
        provider: anthropic
        model: claude-sonnet
        system_prompt: |
          You are a risk and security specialist.
          
          Analyze: ${inputs.analysis_topic}
          Focus on risks, threats, and security implications.
        
        context_sources: ["groups.input_prep.output"]
        tools: [Read, Bash, Grep]
        capabilities:
          max_tokens: 4000
          temperature: 0.5
        condition: |
          ${inputs.analysis_angles.includes('risk')}
  
  # Stage 3: Output formatting
  - id: formatting
    name: Output Formatting
    execution: sequential
    depends_on: ["analysis"]
    timeout: "3m"
    
    completion:
      type: all
    
    agents:
      - id: formatter
        name: Output Formatter
        provider: anthropic
        model: claude-sonnet
        system_prompt: |
          You are an output formatter.
          
          Format the analysis results as: ${inputs.output_style}
          
          If summary: 2-3 paragraphs, highlight key points
          If detailed: comprehensive structured report
          If bullets: concise, scannable bullet points
          
          Ensure professional presentation.
        
        context_sources: ["groups.analysis.output"]
        tools: [Read, Write]
        capabilities:
          max_tokens: 4000
          temperature: 0.4

metadata:
  author: Your Name
  category: interactive
  tags: [interactive, forms, dynamic, user-driven]
  use_cases: [Customizable analysis, flexible workflows]
```

---

## Customization Quick Reference

### Change the Model

```yaml
# Just change these two fields:
provider: anthropic        # or: openai, local_llm, etc.
model: claude-opus         # Use descriptive names, not version IDs
```

### Change the Task

```yaml
# Modify the system_prompt:
system_prompt: |
  You are a [ROLE].
  
  Your task is to: [TASK DESCRIPTION]
  
  Focus on: [KEY AREAS]
```

### Change the Depth

```yaml
# Adjust max_tokens:
capabilities:
  max_tokens: 2000    # Shallow/quick
  max_tokens: 4000    # Standard
  max_tokens: 8000    # Deep/detailed
```

### Change the Precision

```yaml
# Adjust temperature:
capabilities:
  temperature: 0.2    # Precise, factual
  temperature: 0.5    # Balanced
  temperature: 0.8    # Creative
```

---

## Quick Start Workflow

1. **Pick a template** above
2. **Copy the code**
3. **Replace these sections:**
   - `id:` - unique identifier
   - `name:` - display name
   - `[TASK DESCRIPTION]` - your specific task
   - `provider:` and `model:` - your AI providers
   - `system_prompt:` sections - detailed instructions
4. **Save as** `my_workflow.yaml`
5. **Validate** using your workflow tool
6. **Execute** through your workflow runner

---

Done! All templates are generic, provider-agnostic, and work in any directory.
