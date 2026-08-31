#!/bin/bash

# cleanup-docs.sh
# Moves all markdown files and documentation to docs/ folder
# Keeps only: README.md, CHANGELOG.md, LICENSE, Makefile, install scripts, and shell scripts

# Colors for output
GREEN='\033[0;32m'
BLUE='\033[0;34m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

echo -e "${BLUE}=== Documentation Cleanup Script ===${NC}"
echo -e "${BLUE}Moving documentation files to docs/ folder...${NC}\n"

# Create docs folder if it doesn't exist
mkdir -p docs

# Define files that SHOULD stay in root
KEEP_FILES=(
    "README.md"
    "CHANGELOG.md"
    "LICENSE"
    "Makefile"
    "Dockerfile"
    ".gitignore"
    "install"
    "install.sh"
    "go.mod"
    "go.sum"
    "go.work.sum"
    "config.example.json"
)

# Define patterns for files to keep (shell scripts)
KEEP_PATTERNS=(
    "*.sh"
)

# Counter for moved files
moved_count=0

# Function to check if file should be kept in root
should_keep_file() {
    local file="$1"
    local basename=$(basename "$file")
    
    # Check against keep list
    for keep in "${KEEP_FILES[@]}"; do
        if [[ "$basename" == "$keep" ]]; then
            return 0
        fi
    done
    
    # Check against keep patterns (shell scripts stay in root)
    for pattern in "${KEEP_PATTERNS[@]}"; do
        if [[ "$basename" == $pattern ]]; then
            return 0
        fi
    done
    
    return 1
}

# Find all markdown files in root (not in subdirectories)
echo -e "${YELLOW}Processing markdown files...${NC}"
while IFS= read -r -d '' file; do
    basename=$(basename "$file")
    
    # Skip if it's in docs already
    if [[ "$file" == ./docs/* ]]; then
        continue
    fi
    
    # Check if file should be kept
    if should_keep_file "$file"; then
        echo -e "${GREEN}✓${NC} Keeping: $basename"
    else
        echo -e "${BLUE}→${NC} Moving: $basename"
        mv "$file" "docs/"
        ((moved_count++))
    fi
done < <(find . -maxdepth 1 -type f -name "*.md" -print0)

# Find all .txt files in root (not in subdirectories)
echo -e "\n${YELLOW}Processing text files...${NC}"
while IFS= read -r -d '' file; do
    basename=$(basename "$file")
    
    # Skip if it's in docs already
    if [[ "$file" == ./docs/* ]]; then
        continue
    fi
    
    echo -e "${BLUE}→${NC} Moving: $basename"
    mv "$file" "docs/"
    ((moved_count++))
done < <(find . -maxdepth 1 -type f -name "*.txt" -print0)

# Move orphaned todo directories if they exist
if [ -d "to-do" ]; then
    echo -e "\n${BLUE}→${NC} Moving: to-do/ directory"
    mv to-do docs/
    ((moved_count++))
fi

if [ -d "todo" ]; then
    echo -e "${BLUE}→${NC} Moving: todo/ directory"
    mv todo docs/
    ((moved_count++))
fi

# Move analysis directories
if [ -d "claude-code-analysis" ]; then
    echo -e "${BLUE}→${NC} Moving: claude-code-analysis/ directory"
    mv claude-code-analysis docs/
    ((moved_count++))
fi

if [ -d "token-counting-experiment" ]; then
    echo -e "${BLUE}→${NC} Moving: token-counting-experiment/ directory"
    mv token-counting-experiment docs/
    ((moved_count++))
fi

echo -e "\n${GREEN}=== Cleanup Complete ===${NC}"
echo -e "${GREEN}✓${NC} Moved ${moved_count} items to docs/"
echo -e "\n${YELLOW}Next steps:${NC}"
echo -e "  1. Review docs/ folder to ensure everything is correct"
echo -e "  2. Run: git add -A"
echo -e "  3. Run: git commit -m 'chore: move documentation to docs folder'"
