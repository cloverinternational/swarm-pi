#!/bin/bash
# SwarmOS Profiling Script
# Usage: ./scripts/profile.sh [cpu|mem|goroutine|all]

set -e

PROFILE_TYPE="${1:-cpu}"
PPROF_PORT="${SWARMOS_PPROF_PORT:-6060}"
VIEWER_PORT="${SWARMOS_VIEWER_PORT:-8080}"
DURATION="${SWARMOS_PROFILE_DURATION:-30}"
OUTPUT_DIR="./profiles"

mkdir -p "$OUTPUT_DIR"
TIMESTAMP=$(date +%Y%m%d_%H%M%S)

echo "================================================"
echo "  SwarmOS Profiler"
echo "================================================"
echo ""
echo "This script helps you understand where your TUI"
echo "spends CPU time, memory, etc."
echo ""

# Check if swarmos is already running with pprof
check_pprof() {
    if curl -s "http://localhost:$PPROF_PORT/debug/pprof/" > /dev/null 2>&1; then
        return 0
    else
        return 1
    fi
}

# Function to capture CPU profile
capture_cpu() {
    echo "[CPU Profile] Capturing for ${DURATION} seconds..."
    echo "  -> Use your TUI normally during this time!"
    echo "  -> Do the actions you want to profile (scrolling, typing, etc.)"
    echo ""

    PROFILE_FILE="$OUTPUT_DIR/cpu_${TIMESTAMP}.prof"
    curl -s "http://localhost:$PPROF_PORT/debug/pprof/profile?seconds=$DURATION" -o "$PROFILE_FILE"

    echo ""
    echo "[Done] Profile saved to: $PROFILE_FILE"
    echo ""
    echo "Opening interactive viewer in browser..."
    go tool pprof -http=":$VIEWER_PORT" "$PROFILE_FILE" &
    VIEWER_PID=$!

    echo ""
    echo "================================================"
    echo "  Browser opened at: http://localhost:$VIEWER_PORT"
    echo "================================================"
    echo ""
    echo "HOW TO READ THE RESULTS:"
    echo ""
    echo "1. Click 'Flame Graph' in the top menu"
    echo "   - Wide bars = functions that take lots of time"
    echo "   - Tall stacks = deep call chains"
    echo "   - Click to zoom into a section"
    echo ""
    echo "2. Click 'Top' to see ranked list"
    echo "   - 'flat' = time in that function only"
    echo "   - 'cum' = time including functions it calls"
    echo ""
    echo "3. Click 'Source' to see line-by-line breakdown"
    echo ""
    echo "Press Ctrl+C to stop the viewer..."
    wait $VIEWER_PID 2>/dev/null || true
}

# Function to capture memory profile
capture_mem() {
    echo "[Memory Profile] Capturing heap snapshot..."

    PROFILE_FILE="$OUTPUT_DIR/heap_${TIMESTAMP}.prof"
    curl -s "http://localhost:$PPROF_PORT/debug/pprof/heap" -o "$PROFILE_FILE"

    echo "[Done] Profile saved to: $PROFILE_FILE"
    echo ""
    echo "Opening interactive viewer..."
    go tool pprof -http=":$VIEWER_PORT" "$PROFILE_FILE" &
    VIEWER_PID=$!

    echo ""
    echo "================================================"
    echo "  Browser opened at: http://localhost:$VIEWER_PORT"
    echo "================================================"
    echo ""
    echo "HOW TO READ MEMORY RESULTS:"
    echo ""
    echo "1. Use dropdown to switch between:"
    echo "   - 'inuse_space' = currently allocated memory"
    echo "   - 'alloc_space' = total allocated (including freed)"
    echo ""
    echo "2. Look for:"
    echo "   - Large allocations (memory hogs)"
    echo "   - Frequent small allocations (GC pressure)"
    echo ""
    echo "Press Ctrl+C to stop the viewer..."
    wait $VIEWER_PID 2>/dev/null || true
}

# Function to capture goroutine profile
capture_goroutine() {
    echo "[Goroutine Profile] Capturing..."

    PROFILE_FILE="$OUTPUT_DIR/goroutine_${TIMESTAMP}.prof"
    curl -s "http://localhost:$PPROF_PORT/debug/pprof/goroutine" -o "$PROFILE_FILE"

    echo "[Done] Profile saved to: $PROFILE_FILE"
    echo ""
    echo "Opening interactive viewer..."
    go tool pprof -http=":$VIEWER_PORT" "$PROFILE_FILE" &
    VIEWER_PID=$!

    echo ""
    echo "================================================"
    echo "  Browser opened at: http://localhost:$VIEWER_PORT"
    echo "================================================"
    echo ""
    echo "HOW TO READ GOROUTINE RESULTS:"
    echo ""
    echo "Look for:"
    echo "  - Too many goroutines (leak)"
    echo "  - Goroutines stuck waiting (deadlock)"
    echo "  - Goroutines in unexpected places"
    echo ""
    echo "Press Ctrl+C to stop the viewer..."
    wait $VIEWER_PID 2>/dev/null || true
}

# Function to show quick text summary
quick_summary() {
    echo "[Quick Summary] Top CPU consumers:"
    echo ""
    go tool pprof -top -cum "http://localhost:$PPROF_PORT/debug/pprof/profile?seconds=10" 2>/dev/null | head -20
}

# Main logic
case "$PROFILE_TYPE" in
    cpu)
        if ! check_pprof; then
            echo "ERROR: SwarmOS is not running with profiling enabled!"
            echo ""
            echo "Start it with:"
            echo "  SWARMOS_PPROF=6060 ./swarmos"
            echo ""
            echo "Then run this script in another terminal."
            exit 1
        fi
        capture_cpu
        ;;
    mem|memory|heap)
        if ! check_pprof; then
            echo "ERROR: SwarmOS is not running with profiling enabled!"
            echo ""
            echo "Start it with:"
            echo "  SWARMOS_PPROF=6060 ./swarmos"
            exit 1
        fi
        capture_mem
        ;;
    goroutine|goroutines)
        if ! check_pprof; then
            echo "ERROR: SwarmOS is not running with profiling enabled!"
            echo ""
            echo "Start it with:"
            echo "  SWARMOS_PPROF=6060 ./swarmos"
            exit 1
        fi
        capture_goroutine
        ;;
    quick)
        if ! check_pprof; then
            echo "ERROR: SwarmOS is not running with profiling enabled!"
            exit 1
        fi
        quick_summary
        ;;
    all)
        if ! check_pprof; then
            echo "ERROR: SwarmOS is not running with profiling enabled!"
            echo ""
            echo "Start it with:"
            echo "  SWARMOS_PPROF=6060 ./swarmos"
            exit 1
        fi
        echo "Capturing all profiles..."

        # CPU
        echo "[1/3] CPU profile (${DURATION}s)..."
        curl -s "http://localhost:$PPROF_PORT/debug/pprof/profile?seconds=$DURATION" -o "$OUTPUT_DIR/cpu_${TIMESTAMP}.prof"

        # Memory
        echo "[2/3] Memory profile..."
        curl -s "http://localhost:$PPROF_PORT/debug/pprof/heap" -o "$OUTPUT_DIR/heap_${TIMESTAMP}.prof"

        # Goroutines
        echo "[3/3] Goroutine profile..."
        curl -s "http://localhost:$PPROF_PORT/debug/pprof/goroutine" -o "$OUTPUT_DIR/goroutine_${TIMESTAMP}.prof"

        echo ""
        echo "All profiles saved to: $OUTPUT_DIR/"
        ls -la "$OUTPUT_DIR"/*_${TIMESTAMP}.prof
        echo ""
        echo "To view any profile:"
        echo "  go tool pprof -http=:8080 $OUTPUT_DIR/cpu_${TIMESTAMP}.prof"
        ;;
    help|--help|-h)
        echo "Usage: $0 [cpu|mem|goroutine|quick|all]"
        echo ""
        echo "Options:"
        echo "  cpu        - Capture CPU profile (default, ${DURATION}s)"
        echo "  mem        - Capture memory/heap profile"
        echo "  goroutine  - Capture goroutine profile"
        echo "  quick      - Quick 10s summary (text only)"
        echo "  all        - Capture all profiles"
        echo ""
        echo "Environment variables:"
        echo "  SWARMOS_PPROF_PORT      - pprof server port (default: 6060)"
        echo "  SWARMOS_VIEWER_PORT     - browser viewer port (default: 8080)"
        echo "  SWARMOS_PROFILE_DURATION - CPU profile duration (default: 30s)"
        echo ""
        echo "STEP BY STEP:"
        echo ""
        echo "  1. Terminal 1: Start TUI with profiling"
        echo "     $ SWARMOS_PPROF=6060 ./swarmos"
        echo ""
        echo "  2. Terminal 2: Run profiler"
        echo "     $ ./scripts/profile.sh cpu"
        echo ""
        echo "  3. Use your TUI normally for 30 seconds"
        echo ""
        echo "  4. Browser opens with flame graph results"
        ;;
    *)
        echo "Unknown profile type: $PROFILE_TYPE"
        echo "Run '$0 help' for usage"
        exit 1
        ;;
esac
