#!/bin/bash

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Configuration
BATCH_SIZE=10
REMOTE="origin"
BRANCH="main"

echo -e "${YELLOW}=== Git Push in Batches Script ===${NC}"
echo "Pushing commits in batches of $BATCH_SIZE..."
echo ""

# Get total commits to push
TOTAL_COMMITS=$(git log ${REMOTE}/${BRANCH}..${BRANCH} --oneline | wc -l)
echo -e "${YELLOW}Total commits to push: $TOTAL_COMMITS${NC}"
echo ""

if [ $TOTAL_COMMITS -eq 0 ]; then
    echo -e "${GREEN}No commits to push!${NC}"
    exit 0
fi

# Get list of commits (oldest first)
COMMITS=($(git log ${REMOTE}/${BRANCH}..${BRANCH} --oneline | tac | awk '{print $1}'))

# Calculate number of batches
BATCHES=$(( ($TOTAL_COMMITS + $BATCH_SIZE - 1) / $BATCH_SIZE ))
echo -e "${YELLOW}Will push in $BATCHES batches of up to $BATCH_SIZE commits${NC}"
echo ""

# Push in batches
PUSHED=0
for ((i = 0; i < BATCHES; i++)); do
    START=$((i * BATCH_SIZE))
    END=$(( (i + 1) * BATCH_SIZE ))
    
    if [ $END -gt $TOTAL_COMMITS ]; then
        END=$TOTAL_COMMITS
    fi
    
    COMMIT_HASH=${COMMITS[$START]}
    BATCH_NUM=$((i + 1))
    BATCH_COUNT=$((END - START))
    
    echo -e "${YELLOW}Batch $BATCH_NUM/$BATCHES: Pushing commits $((START + 1))-$END (up to $COMMIT_HASH)${NC}"
    
    if git push --force origin $COMMIT_HASH:$BRANCH 2>&1; then
        PUSHED=$((PUSHED + BATCH_COUNT))
        echo -e "${GREEN}✓ Batch $BATCH_NUM complete ($PUSHED/$TOTAL_COMMITS commits pushed)${NC}"
        echo ""
        
        # Brief pause between batches
        if [ $i -lt $((BATCHES - 1)) ]; then
            echo -e "${YELLOW}Waiting 2 seconds before next batch...${NC}"
            sleep 2
        fi
    else
        echo -e "${RED}✗ Batch $BATCH_NUM failed!${NC}"
        echo "Push stopped. Try running the script again."
        exit 1
    fi
done

# Final force push to ensure everything is synced
echo ""
echo -e "${YELLOW}Running final force push to sync everything...${NC}"
if git push --force origin $BRANCH 2>&1; then
    echo -e "${GREEN}✓ Final push complete!${NC}"
    echo -e "${GREEN}All commits successfully pushed to $REMOTE/$BRANCH${NC}"
else
    echo -e "${RED}✗ Final push failed${NC}"
    exit 1
fi
