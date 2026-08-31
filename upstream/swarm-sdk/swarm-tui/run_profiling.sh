#!/bin/bash

set -e
set -o pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
BINARY="$SCRIPT_DIR/swarm"
PPROF_PORT="6060"
OUTPUT_FILE="$SCRIPT_DIR/PROFILING_REPORT.txt"
TEMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/swarm_profile.XXXXXX")"
SWARM_OUTPUT_LOG="$TEMP_DIR/swarm_output.log"
SWARM_PID=""

cleanup() {
    if [ -n "$SWARM_PID" ]; then
        kill "$SWARM_PID" 2>/dev/null || true
        wait "$SWARM_PID" 2>/dev/null || true
        SWARM_PID=""
    fi
}

trap cleanup EXIT INT TERM

{
    echo "=== SWARM-TUI CPU PROFILING REPORT ==="
    echo "Timestamp: $(date)"
    echo "Working Directory: $(pwd)"
    echo ""

    # Step 1: Check binary
    echo "[STEP 1] Checking for swarm binary..."
    if [ ! -f "$BINARY" ]; then
        echo "✗ Binary not found: $BINARY"
        echo "Building from source..."
        make -C "$SCRIPT_DIR" build
    fi

    if [ ! -f "$BINARY" ]; then
        echo "✗ FATAL: Could not build binary"
        exit 1
    fi

    ls -lh "$BINARY"
    echo "✓ Binary ready"
    echo ""

    # Step 2: Start app with pprof
    echo "[STEP 2] Starting swarm with SWARMOS_PPROF=$PPROF_PORT..."
    export SWARMOS_PPROF=$PPROF_PORT
    timeout 65 "$BINARY" > "$SWARM_OUTPUT_LOG" 2>&1 &
    SWARM_PID=$!
    echo "✓ Process started (PID: $SWARM_PID)"
    echo ""

    # Step 3: Wait for startup and verify pprof
    echo "[STEP 3] Waiting 3 seconds for startup..."
    sleep 3

    echo "[STEP 3a] Verifying pprof endpoint..."
    if curl -fsS "http://localhost:$PPROF_PORT/debug/pprof/" > /dev/null; then
        echo "✓ pprof endpoint is responding"
    else
        echo "✗ pprof endpoint not responding (might be headless mode)"
    fi
    echo ""

    # Step 4: CPU Profile
    echo "[STEP 4] Collecting CPU profile (30 seconds)..."
    echo "    (This will take 30 seconds...)"
    CPU_PROFILE="$TEMP_DIR/cpu.prof"
    if timeout 35 curl -fsS -o "$CPU_PROFILE" "http://localhost:$PPROF_PORT/debug/pprof/profile?seconds=30"; then
        SIZE=$(wc -c < "$CPU_PROFILE")
        echo "✓ CPU profile collected ($SIZE bytes)"
    else
        rm -f "$CPU_PROFILE"
        echo "✗ Failed to collect CPU profile"
    fi
    echo ""

    # Step 5: Goroutine Profile
    echo "[STEP 5] Collecting goroutine profile..."
    GOROUTINE_PROFILE="$TEMP_DIR/goroutine.prof"
    if curl -fsS -o "$GOROUTINE_PROFILE" "http://localhost:$PPROF_PORT/debug/pprof/goroutine"; then
        SIZE=$(wc -c < "$GOROUTINE_PROFILE")
        echo "✓ Goroutine profile collected ($SIZE bytes)"
    else
        rm -f "$GOROUTINE_PROFILE"
        echo "✗ Failed to collect goroutine profile"
    fi
    echo ""

    # Step 6: Heap Profile
    echo "[STEP 6] Collecting heap (memory) profile..."
    HEAP_PROFILE="$TEMP_DIR/heap.prof"
    if curl -fsS -o "$HEAP_PROFILE" "http://localhost:$PPROF_PORT/debug/pprof/heap"; then
        SIZE=$(wc -c < "$HEAP_PROFILE")
        echo "✓ Heap profile collected ($SIZE bytes)"
    else
        rm -f "$HEAP_PROFILE"
        echo "✗ Failed to collect heap profile"
    fi
    echo ""

    # Kill the app
    echo "[STEP 7] Terminating swarm process (PID: $SWARM_PID)..."
    cleanup
    echo "✓ Process terminated"
    echo ""

    # Step 8: Analyze profiles
    echo "[STEP 8] ANALYZING PROFILES"
    echo "==========================================="
    echo ""

    if [ -s "$CPU_PROFILE" ]; then
        CPU_TOP_OUT="$TEMP_DIR/cpu_top.txt"
        CPU_TEXT_OUT="$TEMP_DIR/cpu_text.txt"
        echo "--- CPU PROFILE (TOP 20 FUNCTIONS) ---"
        go tool pprof -top "$BINARY" "$CPU_PROFILE" > "$CPU_TOP_OUT" 2>&1
        head -30 "$CPU_TOP_OUT"
        echo ""
        echo "--- CPU PROFILE (TEXT SUMMARY) ---"
        go tool pprof -text "$BINARY" "$CPU_PROFILE" > "$CPU_TEXT_OUT" 2>&1
        head -50 "$CPU_TEXT_OUT"
    else
        echo "✗ CPU profile is empty or failed to collect"
    fi
    echo ""

    if [ -s "$GOROUTINE_PROFILE" ]; then
        GOROUTINE_OUT="$TEMP_DIR/goroutine_top.txt"
        echo "--- GOROUTINE PROFILE ---"
        go tool pprof -top "$BINARY" "$GOROUTINE_PROFILE" > "$GOROUTINE_OUT" 2>&1
        head -20 "$GOROUTINE_OUT"
    else
        echo "✗ Goroutine profile is empty or failed to collect"
    fi
    echo ""

    if [ -s "$HEAP_PROFILE" ]; then
        HEAP_OUT="$TEMP_DIR/heap_top.txt"
        echo "--- HEAP PROFILE (MEMORY) ---"
        go tool pprof -top "$BINARY" "$HEAP_PROFILE" > "$HEAP_OUT" 2>&1
        head -20 "$HEAP_OUT"
    else
        echo "✗ Heap profile is empty or failed to collect"
    fi
    echo ""

    # Step 9: Additional analysis
    echo "--- DETAILED CPU ANALYSIS ---"
    if [ -s "$CPU_PROFILE" ]; then
        CPU_LIST_OUT="$TEMP_DIR/cpu_list.txt"
        go tool pprof -list=".*" "$BINARY" "$CPU_PROFILE" > "$CPU_LIST_OUT" 2>&1
        head -100 "$CPU_LIST_OUT"
    fi
    echo ""

    # Summary
    echo "=========================================="
    echo "[STEP 9] PROFILING COMPLETE"
    echo ""
    echo "Profile files saved to: $TEMP_DIR/"
    echo "  - cpu.prof (CPU profile)"
    echo "  - goroutine.prof (Goroutine profile)"
    echo "  - heap.prof (Memory profile)"
    echo ""
    echo "To further analyze:"
    echo "  go tool pprof -http=:8080 $BINARY $TEMP_DIR/cpu.prof"
    echo ""

} | tee "$OUTPUT_FILE"

echo ""
echo "✓ Full report saved to: $OUTPUT_FILE"
echo ""
echo "To view the report:"
echo "  cat $OUTPUT_FILE"
echo ""
