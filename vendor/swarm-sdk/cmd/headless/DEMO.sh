#!/bin/bash
# Headless Client Demo Script

set -e

echo "==================================="
echo "SwarmOS SDK Headless Client Demo"
echo "==================================="
echo ""

# 1. Create a new conversation
echo "1. Creating a new conversation..."
CONV_ID=$(./bin/headless new -provider anthropic 2>&1 | grep "Created conversation:" | awk '{print $3}')
echo "   Created: $CONV_ID"
echo ""

# 2. Send a message
echo "2. Sending a message..."
./bin/headless send -id $CONV_ID -message "Explain what SwarmOS is in one sentence" 2>&1 | grep -E "(User:|Assistant:)"
echo ""

# 3. Send follow-up
echo "3. Sending a follow-up question..."
./bin/headless send -id $CONV_ID -message "What programming language is it written in?" 2>&1 | grep -E "(User:|Assistant:)"
echo ""

# 4. Resume and view conversation
echo "4. Resuming conversation..."
./bin/headless resume -id $CONV_ID 2>&1 | head -10
echo ""

# 5. Branch the conversation
echo "5. Branching conversation at message 1..."
BRANCH_ID=$(./bin/headless branch -id $CONV_ID -at 1 2>&1 | grep "Branch:" | awk '{print $2}')
echo "   Branched to: $BRANCH_ID"
echo ""

# 6. Continue branch differently
echo "6. Continuing branch with different question..."
./bin/headless send -id $BRANCH_ID -message "What are the key benefits?" 2>&1 | grep -E "(User:|Assistant:)"
echo ""

# 7. Export original conversation
echo "7. Exporting original conversation to JSON..."
./bin/headless export -id $CONV_ID -format json -output /home/alejandro/Swarm/SwarmCode/swarm-go/swarmos/sdk/.tmp/conv_original.json 2>&1 | tail -1
echo ""

# 8. Export branched conversation
echo "8. Exporting branched conversation to JSON..."
./bin/headless export -id $BRANCH_ID -format json -output /home/alejandro/Swarm/SwarmCode/swarm-go/swarmos/sdk/.tmp/conv_branch.json 2>&1 | tail -1
echo ""

# 9. List all conversations
echo "9. Listing all conversations..."
./bin/headless list 2>&1 | head -20
echo ""

echo "==================================="
echo "Demo completed successfully!"
echo ""
echo "Conversation files stored in: ~/.swarmos/conversations/"
echo "Exported files: /home/alejandro/Swarm/SwarmCode/swarm-go/swarmos/sdk/.tmp/conv_original.json and /home/alejandro/Swarm/SwarmCode/swarm-go/swarmos/sdk/.tmp/conv_branch.json"
echo "==================================="
