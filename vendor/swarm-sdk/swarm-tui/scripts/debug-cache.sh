#!/bin/bash
# Enable cache debugging and run a simple test

# Set debug environment variables
export CACHE_DEBUG=1
export LOG_LEVEL=debug

# Run the chat with debugging
go test -v -run TestCacheDebugger ./internal/chat/...

# Show results
echo ""
echo "=== Cache Debug Files ==="
ls -lh /tmp/cache-debug/ 2>/dev/null || echo "No debug files found"

# Show comparison
echo ""
echo "=== Comparing Formats ==="
echo ""
echo "Raw Anthropic (1_raw_anthropic_*.json):"
cat /tmp/cache-debug/1_raw_anthropic_* 2>/dev/null | head -30

echo ""
echo "Canonical (2_canonical_*.json):"
cat /tmp/cache-debug/2_canonical_* 2>/dev/null | head -30

echo ""
echo "Transformed (3_transformed_*.json):"
cat /tmp/cache-debug/3_transformed_* 2>/dev/null | head -30
