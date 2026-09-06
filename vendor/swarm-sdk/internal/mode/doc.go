// Package mode provides multi-agent orchestration capabilities (Ring 3).
//
// The mode package implements the orchestration layer of the SDK, enabling
// complex multi-agent workflows with groups, dependencies, and steering.
//
// # Core Concepts
//
// Mode: A complete workflow definition with agent groups, execution strategies,
// and steering logic. Modes are typically loaded from YAML files.
//
// Agent Group: A collection of agents executed together with a specific strategy
// (parallel, sequential, or adversarial).
//
// Workflow: The execution engine that coordinates groups based on dependencies
// and transitions.
//
// Steering: Decision-making system that can be rule-based, LLM-based, or hybrid.
//
// # Example Usage
//
//	// Create a mode programmatically
//	mode := mode.NewMode("code_review", "Code Review Mode")
//
//	// Add a planning group (parallel execution)
//	planningGroup := mode.NewAgentGroup("planning", "Planning", mode.ExecutionParallel)
//	planningGroup.AddAgent(architectAgent)
//	planningGroup.AddAgent(reviewerAgent)
//	mode.AddGroup(planningGroup)
//
//	// Add an implementation group (sequential execution)
//	implGroup := mode.NewAgentGroup("implementation", "Implementation", mode.ExecutionSequential)
//	implGroup.AddDependency("planning")
//	implGroup.AddAgent(implementerAgent)
//	mode.AddGroup(implGroup)
//
//	// Add transition rule
//	transition := mode.NewTransition("planning", "implementation", "consensus_reached")
//	mode.AddTransition(transition)
//
//	// Execute the mode
//	executor := mode.NewModeExecutor(registry, factory, logger, tracer)
//	result, err := executor.Execute(ctx, "code_review", "Fix authentication bug")
//
// # YAML Configuration
//
// Modes are typically defined in YAML:
//
//	name: "code_review"
//	description: "Multi-agent code review workflow"
//	version: "1.0"
//
//	groups:
//	  - name: "planning"
//	    execution: "parallel"
//	    agents:
//	      - name: "architect"
//	        provider: "openai"
//	        model: "gpt-4"
//	      - name: "reviewer"
//	        provider: "anthropic"
//	        model: "claude-sonnet-4"
//
//	  - name: "implementation"
//	    execution: "sequential"
//	    depends_on: ["planning"]
//	    agents:
//	      - name: "implementer"
//	        provider: "anthropic"
//	        model: "claude-sonnet-4"
//
//	transitions:
//	  - from: "planning"
//	    to: "implementation"
//	    condition: "consensus_reached"
//
// # Architecture
//
// Ring 3 (Orchestration Layer) consists of:
//
//	mode/
//	├── mode.go           - Mode definition
//	├── group.go          - Agent group definition
//	├── transition.go     - Transition rules
//	├── loader.go         - YAML parser
//	├── registry.go       - Mode registry
//	├── workflow.go       - Workflow engine
//	├── dag.go            - DAG execution
//	├── consensus.go      - Consensus evaluation
//	├── synthesis.go      - Output synthesis
//	└── steering/         - Steering system
//	    ├── rule_based.go     - Rule-based steering
//	    ├── llm_based.go      - LLM-based steering
//	    └── hybrid.go         - Hybrid steering
package mode
