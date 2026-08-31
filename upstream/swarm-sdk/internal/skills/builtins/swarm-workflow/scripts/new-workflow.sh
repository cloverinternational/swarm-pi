#!/usr/bin/env bash
# new-workflow.sh — Scaffold a new Swarm Workflow YAML
#
# Usage:
#   bash new-workflow.sh <workflow_id> [workflow_name] [output_dir]
#
# Examples:
#   bash new-workflow.sh my_analysis
#   bash new-workflow.sh code_review "Code Review Pipeline" ./workflows/
#   bash new-workflow.sh deploy "Deployment Workflow" ~/.swarm/workflows/
#
# Generates a 2-group parallel→sequential starter workflow with @current provider
# and commonly used tools pre-filled.

set -euo pipefail

YELLOW='\033[0;33m'
GREEN='\033[0;32m'
CYAN='\033[0;36m'
BOLD='\033[1m'
NC='\033[0m'

# ── Args ───────────────────────────────────────────────────────────────────────
if [ $# -lt 1 ]; then
  echo "Usage: bash new-workflow.sh <workflow_id> [\"Workflow Name\"] [output_dir]"
  echo ""
  echo "Examples:"
  echo "  bash new-workflow.sh my_analysis"
  echo "  bash new-workflow.sh code_review \"Code Review Pipeline\" ./workflows/"
  exit 1
fi

WORKFLOW_ID="$1"
WORKFLOW_NAME="${2:-$(echo "$WORKFLOW_ID" | sed 's/_/ /g' | awk '{for(i=1;i<=NF;i++) $i=toupper(substr($i,1,1)) tolower(substr($i,2)); print}')}"
OUTPUT_DIR="${3:-.}"
TIMESTAMP="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

# Validate ID format (snake_case, no spaces)
if ! echo "$WORKFLOW_ID" | grep -qE '^[a-z][a-z0-9_]*$'; then
  echo -e "${YELLOW}Warning:${NC} workflow ID should be snake_case (lowercase letters, digits, underscores)"
  echo -e "         Got: '$WORKFLOW_ID'"
fi

OUTPUT_FILE="${OUTPUT_DIR}/${WORKFLOW_ID}.yaml"

# Check output dir exists
if [ ! -d "$OUTPUT_DIR" ]; then
  echo -e "${CYAN}Creating directory: $OUTPUT_DIR${NC}"
  mkdir -p "$OUTPUT_DIR"
fi

# Check if file already exists
if [ -f "$OUTPUT_FILE" ]; then
  echo -e "${YELLOW}Warning: $OUTPUT_FILE already exists.${NC}"
  read -r -p "Overwrite? [y/N] " confirm
  if [[ ! "$confirm" =~ ^[Yy]$ ]]; then
    echo "Aborted."
    exit 1
  fi
fi

# ── Generate YAML ──────────────────────────────────────────────────────────────
cat > "$OUTPUT_FILE" << YAMLEOF
id: ${WORKFLOW_ID}
name: ${WORKFLOW_NAME}
version: 1.0.0
description: |
  TODO: Describe what this workflow does and when to use it.

config:
  max_duration: 30m
  allow_human_intervention: true
  fail_on_steering_block: false
  max_retries: 3
  timeout_behavior: partial

# Uncomment to collect user input before execution:
# parameters:
#   - id: my_param
#     question: "Enter a value:"
#     type: text
#     required: true

groups:
  # ── Group 1: Gather / Analyze (parallel) ─────────────────────────────────
  # Multiple agents running simultaneously, each investigating a different aspect.
  - id: gather
    name: Gather & Analyze
    description: Parallel analysis of the input
    execution: parallel
    timeout: 10m
    completion:
      type: all
      threshold: 1
    agents:
      - id: primary_agent
        name: Primary Agent
        provider: "@current"    # Uses your active provider/model
        model: "@current"
        system_prompt: |-
          TODO: Describe what this agent should do.
          Be specific — this prompt is injected directly into the agent.
        tools:
          - Bash
          - Read
          - Grep
        capabilities:
          max_tokens: 8000
          temperature: 0.3

      # Uncomment to add a second parallel agent:
      # - id: secondary_agent
      #   name: Secondary Agent
      #   provider: "@current"
      #   model: "@current"
      #   system_prompt: |-
      #     TODO: Secondary agent's role.
      #   tools:
      #     - Bash
      #     - Grep
      #   capabilities:
      #     max_tokens: 4000
      #     temperature: 0.3

  # ── Group 2: Synthesize / Act (sequential) ───────────────────────────────
  # Runs after gather completes. Receives gather's output as context.
  - id: synthesize
    name: Synthesize & Output
    description: Synthesizes results and produces final output
    execution: sequential
    depends_on:
      - gather               # Waits for Group 1 to complete
    timeout: 15m
    completion:
      type: all
      threshold: 1
    agents:
      - id: synthesis_agent
        name: Synthesis Agent
        provider: "@current"
        model: "@current"
        system_prompt: |-
          You will receive results from the previous analysis step.
          TODO: Describe what to do with those results.
          Produce a clear, structured final output.
        tools:
          - Read
          - Write
          - Bash
        capabilities:
          max_tokens: 16000
          temperature: 0.2

# Uncomment to add steering (meta-agent coordination):
# steering:
#   type: none    # options: none | rule | llm | hybrid

metadata:
  author: ""
  category: development
  created_at: "${TIMESTAMP}"
  tags: []
  use_cases: []
YAMLEOF

echo ""
echo -e "${GREEN}${BOLD}✓ Workflow created:${NC} ${CYAN}${OUTPUT_FILE}${NC}"
echo ""
echo "Next steps:"
echo "  1. Edit the system_prompt fields with your agent instructions"
echo "  2. Adjust tools lists to match what each agent needs"
echo "  3. Validate the workflow:"
echo "     bash scripts/validate-workflow.sh ${OUTPUT_FILE}"
echo "  4. Place in ./workflows/ or ~/.swarm/workflows/ to make it available in the TUI"
echo ""
echo "Reference docs:"
echo "  references/YAML-SCHEMA.md          — full field reference"
echo "  references/EXECUTION-STRATEGIES.md — when to use parallel/sequential/adversarial"
echo "  references/PROFILE-INTEGRATION.md  — @current and @profile provider resolution"
echo "  references/EXAMPLES.md             — real annotated examples"
