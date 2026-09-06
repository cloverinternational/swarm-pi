# SwarmOS Workflows

This directory contains pre-defined multi-agent workflow templates for common collaboration patterns. Each workflow defines how multiple AI agents coordinate to solve complex problems.

## Available Workflows

### 1. Panel of Experts (`panel_of_experts.yaml`)
**Pattern**: Multiple specialized experts → Synthesis → Execution

Multiple expert agents analyze a problem from different perspectives (security, performance, maintainability, UX), then a synthesizer combines their insights into a consensus recommendation, followed by implementation.

**Use Cases**:
- Architecture review
- Code review
- Feature planning
- Problem analysis
- Design decisions

**Execution Strategy**: Parallel experts → Sequential synthesis → Sequential execution

**Estimated Duration**: ~30 minutes

---

### 2. Plan and Execute (`plan_and_execute.yaml`)
**Pattern**: Strategic Planning → Execution → Verification

A strategic planner creates a detailed execution plan, an executor implements it step-by-step with full tool access, and a verifier validates the implementation.

**Use Cases**:
- Complex refactoring
- Feature implementation
- System migration
- Infrastructure changes
- Multi-step tasks

**Execution Strategy**: Sequential planning → Sequential execution → Sequential verification

**Estimated Duration**: ~45 minutes

---

### 3. Adversarial Review (`adversarial_review.yaml`)
**Pattern**: Creation → Multi-perspective Critique → Refinement → Final Review

A creator proposes a solution, multiple critics (adversarial, constructive, implementation) review it in parallel, a refiner incorporates feedback, and a final reviewer validates improvements.

**Use Cases**:
- Design review
- Architecture decisions
- Security review
- Code review
- Proposal evaluation
- Quality assurance

**Execution Strategy**: Sequential creation → Parallel critique → Sequential refinement → Sequential review

**Estimated Duration**: ~40 minutes

---

### 4. Research and Synthesis (`research_synthesis.yaml`)
**Pattern**: Parallel Research → Synthesis → Validation

Multiple specialized researchers (technical, domain, comparative, risk) investigate different aspects in parallel, a synthesizer combines findings into a comprehensive report, and a validator ensures quality.

**Use Cases**:
- Technology evaluation
- Requirements analysis
- Feasibility studies
- Competitive analysis
- Risk assessment
- Literature review

**Execution Strategy**: Parallel research → Sequential synthesis → Sequential validation

**Estimated Duration**: ~30 minutes

---

## Workflow Structure

Each workflow YAML file contains:

```yaml
id: workflow_identifier
name: Human-Readable Name
description: What this workflow does
version: 1.0.0

config:
  max_duration: 30m
  allow_human_intervention: true
  fail_on_steering_block: false
  max_retries: 2
  timeout_behavior: partial

groups:
  - id: group_id
    name: Group Name
    description: What this group does
    execution: parallel|sequential|adversarial
    depends_on: [dependencies]
    timeout: 10m
    
    agents:
      - id: agent_id
        name: Agent Name
        provider: anthropic
        model: claude-3-5-sonnet-20241022
        system_prompt: |
          Detailed instructions for this agent...
        capabilities:
          max_tokens: 4000
          temperature: 0.7
        tools: ["*"]
    
    completion:
      type: all|consensus|first|majority|quality
      threshold: 0.8

steering:
  type: llm-based|rule-based|hybrid|none
  # ... steering configuration

metadata:
  author: SwarmOS Team
  category: workflow_category
  tags: [tag1, tag2]
  use_cases: [use_case1, use_case2]
```

## Key Concepts

### Agent Groups
A **group** is a collection of agents executed together with a specific strategy:
- **Parallel**: All agents run concurrently
- **Sequential**: Agents run one after another  
- **Adversarial**: Agents debate until consensus

### Execution Strategies
- **parallel**: Run all agents at the same time (faster, independent)
- **sequential**: Run agents one by one (ordered, dependent)
- **adversarial**: Run agents in debate rounds (iterative improvement)

### Completion Criteria
- **all**: All agents must complete successfully
- **consensus**: Agents must reach agreement (with threshold)
- **first**: First successful agent completes the group
- **majority**: Majority of agents must succeed
- **quality**: Output must meet quality threshold

### Dependencies
Groups can depend on other groups:
```yaml
depends_on:
  - previous_group_id
```

### Context Sources
Agents can access outputs from previous groups:
```yaml
context_sources:
  - "groups.expert_panel.output"
  - "groups.planning.output"
```

### Steering
Quality control and oversight:
- **LLM-based**: Meta-agent makes decisions
- **Rule-based**: Predefined rules trigger actions
- **Hybrid**: Combination of LLM and rules
- **None**: No oversight (auto-approve)

## Using Workflows

### CLI (Coming Soon)
```bash
# List available workflows
swarmos workflow list

# Run a workflow
swarmos workflow run panel_of_experts --input "Review the authentication system"

# Dry-run (validate DAG without executing)
swarmos workflow validate panel_of_experts
```

### TUI (Coming Soon)
1. Launch SwarmOS TUI
2. Navigate to Workflows
3. Select a workflow
4. Provide input
5. Monitor execution in real-time
6. Review results

### Programmatically
```go
import (
    "github.com/Swarm-Code/mono/swarm-sdk/mode"
    "github.com/Swarm-Code/mono/swarm-sdk/agent"
)

// Load workflow
loader := mode.NewModeLoader(logger, tracer)
workflow, err := loader.LoadFromFile("workflows/panel_of_experts.yaml")

// Execute workflow
engine := mode.NewWorkflowEngine(workflow, logger, tracer)
result, err := engine.Execute(ctx, "Your input here", agentFactory)
```

## Creating Custom Workflows

### 1. Start with a Template
Copy an existing workflow that's closest to your needs.

### 2. Customize Agents
Modify agent system prompts, models, and capabilities.

### 3. Adjust Groups
Change execution strategies, dependencies, and completion criteria.

### 4. Add Steering (Optional)
Configure quality gates and oversight if needed.

### 5. Test
Validate with dry-run before executing.

### Example: Simple Two-Agent Workflow
```yaml
id: simple_workflow
name: Simple Workflow
version: 1.0.0

groups:
  - id: analysis
    execution: sequential
    agents:
      - id: analyzer
        name: Analyzer
        provider: anthropic
        model: claude-3-5-sonnet-20241022
        system_prompt: "Analyze the input and provide insights."
        capabilities:
          max_tokens: 4000
```

## Best Practices

1. **Clear System Prompts**: Be specific about what each agent should do
2. **Appropriate Models**: Use powerful models (Opus) for synthesis, cheaper models (Sonnet) for specialized tasks
3. **Timeouts**: Set realistic timeouts for each group
4. **Error Handling**: Use `max_failures` in completion criteria
5. **Context Sources**: Pass relevant context between groups
6. **Temperature**: Lower for implementation (0.3), higher for creativity (0.8)
7. **Tool Access**: Only give tools to agents that need them
8. **Testing**: Always dry-run before production use

## Advanced Features

### Human Intervention
Set `allow_human_intervention: true` to pause workflows for human input.

### Retry Policies
```yaml
config:
  max_retries: 3
```

### Partial Results
```yaml
config:
  timeout_behavior: partial  # Return results even if some groups fail
```

### Quality Gates
```yaml
steering:
  rules:
    - id: quality_check
      condition: "group.output.length < 1000"
      action: retry_with_more_context
```

## Troubleshooting

### Workflow Fails Immediately
- Check YAML syntax
- Validate all required fields are present
- Ensure dependencies reference existing groups

### Agents Timeout
- Increase group timeout
- Reduce agent complexity
- Use faster models

### Poor Quality Results
- Improve system prompts
- Add steering/oversight
- Use more powerful models
- Increase max_tokens

### High Costs
- Use cheaper models where appropriate
- Reduce max_tokens
- Optimize system prompts
- Use sequential instead of parallel where possible

## Contributing

To contribute a new workflow template:
1. Follow the structure of existing workflows
2. Add comprehensive system prompts
3. Include metadata (use cases, tags)
4. Test thoroughly
5. Document in this README

## License

See repository LICENSE file.
