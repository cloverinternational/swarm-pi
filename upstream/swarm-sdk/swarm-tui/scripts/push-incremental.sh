#!/bin/bash

# Get all commits that need to be pushed
COMMITS=$(git log --oneline origin/main..main)
TOTAL=$(echo "$COMMITS" | wc -l)

echo "Total commits to push: $TOTAL"

# Push in batches of 50
BATCH_SIZE=50
CURRENT=0

while [ $CURRENT -lt $TOTAL ]; do
    END=$((CURRENT + BATCH_SIZE))
    if [ $END -gt $TOTAL ]; then
        END=$TOTAL
    fi
    
    # Get the commit hash at position END (from the end, so we push oldest first)
    COMMIT_HASH=$(git log --oneline origin/main..main | tail -n $((END + 1)) | head -n 1 | cut -d' ' -f1)
    
    echo "Pushing batch: commits $CURRENT to $END (up to $COMMIT_HASH)"
    git push origin $COMMIT_HASH:main 2>&1 | tail -n 5
    
    CURRENT=$END
done

echo "Final force push to ensure all commits are there..."
git push --force origin main
