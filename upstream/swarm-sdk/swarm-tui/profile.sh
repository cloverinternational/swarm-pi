#!/bin/bash

# SwarmOS TUI CPU Profiling Helper Script
# This script simplifies the profiling workflow

set -euo pipefail

PPROF_PORT="${PPROF_PORT:-6060}"
PROFILE_DURATION="${PROFILE_DURATION:-30}"
OUTPUT_DIR="${OUTPUT_DIR:-.}"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

print_header() {
    echo -e "${BLUE}=== $1 ===${NC}"
}

print_success() {
    echo -e "${GREEN}✓ $1${NC}"
}

print_error() {
    echo -e "${RED}✗ $1${NC}"
}

print_warning() {
    echo -e "${YELLOW}⚠ $1${NC}"
}

show_usage() {
    cat << 'EOF'
SwarmOS TUI Profiling Helper

Usage:
    ./profile.sh [command] [options]

Commands:
    start              Start app with pprof enabled
    idle               Capture idle profile (30s)
    stream             Capture streaming profile (30s)
    compare            Compare idle and stream profiles
    goroutines         Show active goroutines
    memory             Show memory profile
    help               Show this help message

Options:
    --port PORT        pprof port (default: 6060)
    --duration SECS    Profile duration (default: 30)
    --output DIR       Output directory (default: .)

Examples:
    # Start the app (in one terminal)
    ./profile.sh start

    # In another terminal, capture profiles
    ./profile.sh idle
    # Now trigger high CPU in the TUI (stream a response)
    ./profile.sh stream
    # Compare the results
    ./profile.sh compare

    # View goroutines
    ./profile.sh goroutines

    # Custom port
    ./profile.sh start --port 7070
    ./profile.sh idle --port 7070
EOF
}

check_pprof_available() {
    if ! command -v go &> /dev/null; then
        print_error "Go is not installed. pprof requires Go toolchain."
        exit 1
    fi
}

check_server_running() {
    if ! curl -fsS "http://localhost:${PPROF_PORT}/debug/pprof/" > /dev/null; then
        print_error "pprof server not responding on port ${PPROF_PORT}"
        print_warning "Did you start the app with SWARMOS_PPROF=${PPROF_PORT}?"
        return 1
    fi
    return 0
}

cmd_start() {
    print_header "Starting SwarmOS TUI with pprof"
    echo -e "${YELLOW}SWARMOS_PPROF=${PPROF_PORT} ./swarm${NC}"
    echo ""
    echo "pprof will be available at: http://localhost:${PPROF_PORT}/debug/pprof/"
    echo ""
    print_warning "Run this in another terminal to capture profiles:"
    echo "  ./profile.sh idle --port ${PPROF_PORT}"
    echo "  # Trigger high CPU in TUI..."
    echo "  ./profile.sh stream --port ${PPROF_PORT}"
    echo "  ./profile.sh compare --port ${PPROF_PORT}"
    echo ""

    export SWARMOS_PPROF=${PPROF_PORT}
    exec ./swarm
}

cmd_idle() {
    print_header "Capturing idle profile (${PROFILE_DURATION}s)"

    check_pprof_available
    check_server_running || return 1

    local output_file="${OUTPUT_DIR}/profile_idle_$(date +%s).pb.gz"
    mkdir -p -- "${OUTPUT_DIR}"

    echo "Recording CPU profile..."
    curl -fsS \
        "http://localhost:${PPROF_PORT}/debug/pprof/profile?seconds=${PROFILE_DURATION}" \
        -o "${output_file}"

    print_success "Profile saved to: ${output_file}"
    echo ""
    echo "To analyze:"
    echo "  go tool pprof ${output_file}"
    echo "  (pprof) top"
    echo "  (pprof) list main"
}

cmd_stream() {
    print_header "Capturing streaming profile (${PROFILE_DURATION}s)"
    print_warning "Make sure you're actively streaming a response in the TUI!"
    echo ""

    check_pprof_available
    check_server_running || return 1

    local output_file="${OUTPUT_DIR}/profile_stream_$(date +%s).pb.gz"
    mkdir -p -- "${OUTPUT_DIR}"

    echo "Recording CPU profile..."
    curl -fsS \
        "http://localhost:${PPROF_PORT}/debug/pprof/profile?seconds=${PROFILE_DURATION}" \
        -o "${output_file}"

    print_success "Profile saved to: ${output_file}"
    echo ""
    echo "To analyze:"
    echo "  go tool pprof ${output_file}"
    echo "  (pprof) top"
}

cmd_compare() {
    print_header "Comparing profiles"

    check_pprof_available

    local idle_file=""
    local stream_file=""
    local file

    # Find most recent idle and stream profiles.
    for file in "${OUTPUT_DIR}"/profile_idle_*.pb.gz; do
        [[ -e "${file}" ]] || break
        [[ -z "${idle_file}" || "${file}" -nt "${idle_file}" ]] && idle_file="${file}"
    done
    for file in "${OUTPUT_DIR}"/profile_stream_*.pb.gz; do
        [[ -e "${file}" ]] || break
        [[ -z "${stream_file}" || "${file}" -nt "${stream_file}" ]] && stream_file="${file}"
    done

    if [ -z "$idle_file" ] || [ -z "$stream_file" ]; then
        print_error "Could not find profile files"
        echo "Make sure you've run:"
        echo "  ./profile.sh idle --port ${PPROF_PORT}"
        echo "  ./profile.sh stream --port ${PPROF_PORT}"
        return 1
    fi

    print_success "Idle profile:    ${idle_file}"
    print_success "Stream profile:  ${stream_file}"
    echo ""

    go tool pprof -base="${idle_file}" "${stream_file}"
}

cmd_goroutines() {
    print_header "Active goroutines"

    check_pprof_available
    check_server_running || return 1

    echo "Fetching goroutine profile..."
    curl -fsS "http://localhost:${PPROF_PORT}/debug/pprof/goroutine?debug=2" | awk 'NR <= 50 { print }'
    echo ""
    print_warning "To see full output with stack traces:"
    echo "  curl -fsS http://localhost:${PPROF_PORT}/debug/pprof/goroutine?debug=2 | less"
}

cmd_memory() {
    print_header "Memory profile"

    check_pprof_available
    check_server_running || return 1

    local output_file="${OUTPUT_DIR}/profile_memory_$(date +%s).pb.gz"
    mkdir -p -- "${OUTPUT_DIR}"

    echo "Recording memory profile..."
    curl -fsS \
        "http://localhost:${PPROF_PORT}/debug/pprof/heap?debug=0" \
        -o "${output_file}"

    print_success "Profile saved to: ${output_file}"
    echo ""
    echo "To analyze:"
    echo "  go tool pprof ${output_file}"
    echo "  (pprof) top"
}

# Parse arguments
CMD="help"
while [[ $# -gt 0 ]]; do
    case $1 in
        --port|--duration|--output)
            if [[ $# -lt 2 || "${2:-}" == --* ]]; then
                print_error "Missing value for $1"
                show_usage
                exit 1
            fi
            case $1 in
                --port)
                    PPROF_PORT="$2"
                    ;;
                --duration)
                    PROFILE_DURATION="$2"
                    ;;
                --output)
                    OUTPUT_DIR="$2"
                    ;;
            esac
            shift 2
            ;;
        start|idle|stream|compare|goroutines|memory|help)
            CMD="$1"
            shift
            ;;
        *)
            print_error "Unknown option: $1"
            show_usage
            exit 1
            ;;
    esac
done

# Execute command
case $CMD in
    start)
        cmd_start
        ;;
    idle)
        cmd_idle
        ;;
    stream)
        cmd_stream
        ;;
    compare)
        cmd_compare
        ;;
    goroutines)
        cmd_goroutines
        ;;
    memory)
        cmd_memory
        ;;
    help)
        show_usage
        ;;
    *)
        print_error "Unknown command: $CMD"
        show_usage
        exit 1
        ;;
esac
