#!/bin/bash
# Minimal test to reproduce parallel sub-agent merging bug

cd /home/swarm/mono/swarm-tui

# Enable debug logging
export SWARM_DEBUG=1

# Create a test conversation with 2 parallel Task calls
echo "Testing parallel Task calls..."
echo "This should create 2 separate sub-agent boxes, not merge them into one."
echo ""

# Run the TUI in non-interactive mode with a prompt that triggers parallel Task calls
./swarm --once "Run 2 parallel sub-agents: one to count 1-5, another to count 6-10" 2>&1 | tee /tmp/parallel_test.log

echo ""
echo "Check /tmp/parallel_test.log for diagnostic output"
