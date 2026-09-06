#!/bin/bash
# profile-memory-tmux.sh
# Launch swarm-tui with pprof + live memory tracing across a 4-pane tmux session.
#
# Layout:
#   ┌──────────────────────┬──────────────────────┐
#   │  [A] swarm-tui       │  [B] memory overview  │
#   │  (pprof :6060)       │  top allocators/10s   │
#   ├──────────────────────┼──────────────────────┤
#   │  [C] snapshot loop   │  [D] forge analysis   │
#   │  (RSS delta/30s)     │  (inuse/alloc_space)  │
#   └──────────────────────┴──────────────────────┘
#
# Usage:
#   cd swarm-tui && ./scripts/profile-memory-tmux.sh
#   tmux attach -t swarm-profile
#
# Ad-hoc pprof while running:
#   go tool pprof -http=:8080 http://localhost:6060/debug/pprof/heap
#   go tool pprof -http=:8081 -diff_base=<old.pb.gz> <new.pb.gz>
#   curl -sf 'http://localhost:6060/debug/pprof/trace?seconds=10' -o /tmp/t.out && go tool trace /tmp/t.out
#   curl -sf 'http://localhost:6060/debug/pprof/goroutine?debug=2' | less

set -euo pipefail

# ── Config ──────────────────────────────────────────────────────────────────
PPROF_PORT="${PPROF_PORT:-6060}"
SESSION="${SESSION:-swarm-profile}"
SWARM_DIR="${SWARM_DIR:-$(cd "$(dirname "$0")/.." && pwd)}"
SWARM_BIN="${SWARM_DIR}/swarm"
SNAPSHOT_INTERVAL="${SNAPSHOT_INTERVAL:-30}"
PROFILE_DIR="/tmp/swarm-heap-$(date +%Y%m%d-%H%M%S)"

# ── Colours ──────────────────────────────────────────────────────────────────
RED='\033[0;31m'; GREEN='\033[0;32m'
CYAN='\033[0;36m'; BOLD='\033[1m'; NC='\033[0m'

# ── Preflight ────────────────────────────────────────────────────────────────
if ! command -v tmux &>/dev/null; then echo -e "${RED}tmux not found${NC}"; exit 1; fi
if ! command -v go &>/dev/null;   then echo -e "${RED}go not found — needed for pprof analysis${NC}"; exit 1; fi
if [ ! -x "$SWARM_BIN" ]; then
    echo -e "${RED}Binary not found: $SWARM_BIN${NC}"
    echo "Build first:  cd $SWARM_DIR && make build"
    exit 1
fi

mkdir -p "$PROFILE_DIR"

echo -e "${BOLD}${CYAN}swarm-tui memory profiler${NC}"
echo -e "  Binary  : ${SWARM_BIN}"
echo -e "  pprof   : http://localhost:${PPROF_PORT}/debug/pprof/"
echo -e "  Profiles: ${PROFILE_DIR}"
echo -e "  Session : ${SESSION}"
echo ""

tmux kill-session -t "$SESSION" 2>/dev/null || true

# ── [A] swarm-tui launcher — bake config values in ──────────────────────────
cat > "$PROFILE_DIR/launch.sh" << EOF
#!/bin/bash
export SWARMOS_PPROF=${PPROF_PORT}
# memprofilerate=512 → sample 1 in every 512 allocated bytes.
# Lower = more accurate / higher overhead. Use 1 for exhaustive tracing.
export GODEBUG='memprofilerate=512'
export GOMEMLIMIT='6GiB'
cd "${SWARM_DIR}"
echo -e '\033[1;36m[pprof] http://localhost:${PPROF_PORT}/debug/pprof/\033[0m'
echo -e '\033[1;33m[profiles] ${PROFILE_DIR}\033[0m'
echo ""
exec "${SWARM_BIN}" "\$@"
EOF
chmod +x "$PROFILE_DIR/launch.sh"

# ── [B] memory overview — bake PORT and PDIR in ─────────────────────────────
cat > "$PROFILE_DIR/heap-watch.sh" << EOF
#!/bin/bash
PORT="${PPROF_PORT}"
PDIR="${PROFILE_DIR}"

echo "Waiting for pprof on :\${PORT}..."
until curl -sf "http://localhost:\${PORT}/debug/pprof/" >/dev/null 2>&1; do sleep 1; done
echo -e "\033[1;32mpprof ready\033[0m"

while true; do
    clear
    ts=\$(date '+%H:%M:%S')
    echo -e "\033[1;36m=== MEMORY OVERVIEW \${ts} ===\033[0m"

    # First line: "heap profile: N: INUSE_BYTES [ALLOC_N: ALLOC_BYTES] @ heap/SAMPLE"
    first=\$(curl -sf "http://localhost:\${PORT}/debug/pprof/heap?debug=1" 2>/dev/null | head -1)
    if [ -n "\$first" ]; then
        inuse_bytes=\$(echo "\$first" | grep -oP '(?<=: )\\d+(?= \\[)' | head -1 || echo 0)
        alloc_bytes=\$(echo "\$first" | grep -oP '(?<=: )\\d+(?=\\] @)' | head -1 || echo 0)
        inuse_mb=\$(awk "BEGIN{printf \"%.1f\", \${inuse_bytes:-0}/1048576}")
        alloc_mb=\$(awk "BEGIN{printf \"%.2f\", \${alloc_bytes:-0}/1048576}")
        printf "  HeapInuse (live objs) : \033[1;33m%s MB\033[0m\n" "\$inuse_mb"
        printf "  TotalAlloc (cumul)    : \033[0;33m%s MB\033[0m\n" "\$alloc_mb"
    fi

    # PID via the pprof port — precise, no false pgrep matches
    pid=\$(lsof -ti:"\${PORT}" 2>/dev/null | head -1)
    if [ -n "\$pid" ]; then
        rss_kb=\$(awk '/VmRSS/{print \$2}' /proc/\$pid/status 2>/dev/null || echo 0)
        vsz_kb=\$(awk '/VmSize/{print \$2}' /proc/\$pid/status 2>/dev/null || echo 0)
        rss_mb=\$((rss_kb / 1024))
        vsz_mb=\$((vsz_kb / 1024))
        printf "  RSS  (OS physical)    : \033[1;32m%d MB\033[0m\n" "\$rss_mb"
        printf "  VSZ  (virtual)        : %d MB\n" "\$vsz_mb"
        printf "  PID                   : %s\n" "\$pid"
    fi

    echo ""
    echo -e "\033[1;36m=== TOP HEAP ALLOCATORS (inuse_space) ===\033[0m"

    latest=\$(ls -t "\${PDIR}"/heap_*.pb.gz 2>/dev/null | head -1)
    if [ -n "\$latest" ]; then
        printf "  \033[0;33m%s\033[0m\n" "\${latest##*/}"
        go tool pprof -top -inuse_space -unit=mb "\$latest" 2>/dev/null \\
            | grep -vE '^(File:|Build ID:|Type:|Time:|Showing|Dropped|\$)' \\
            | head -15
    else
        echo "  (waiting for first snapshot...)"
    fi

    echo ""
    echo -e "\033[1;36m=== GOROUTINES ===\033[0m"
    gdata=\$(curl -sf "http://localhost:\${PORT}/debug/pprof/goroutine?debug=1" 2>/dev/null)
    count=\$(echo "\$gdata" | grep -c '^goroutine ' 2>/dev/null || echo "?")
    printf "  Active: %s\n" "\$count"
    echo "\$gdata" \\
        | grep -oP '(?<=\\[)[^\\]]+(?=\\])' \\
        | sort | uniq -c | sort -rn | head -6 \\
        | awk '{printf "  %5d  %s\n", \$1, \$2}' \\
        || true

    echo ""
    printf "\033[0;33mRefreshing in 10s\033[0m"
    sleep 10
done
EOF
chmod +x "$PROFILE_DIR/heap-watch.sh"

# ── [C] snapshot loop — all config baked in ─────────────────────────────────
cat > "$PROFILE_DIR/snapshot-loop.sh" << EOF
#!/bin/bash
PORT="${PPROF_PORT}"
PDIR="${PROFILE_DIR}"
INTERVAL="${SNAPSHOT_INTERVAL}"

echo "Waiting for pprof..."
until curl -sf "http://localhost:\${PORT}/debug/pprof/" >/dev/null 2>&1; do sleep 1; done
echo -e "\033[1;32mSnapshot loop — every \${INTERVAL}s\033[0m"
echo "Saving to: \${PDIR}"
echo ""

n=0
prev_rss=0

while true; do
    ts=\$(date '+%Y%m%d-%H%M%S')
    f="\${PDIR}/heap_\${ts}_\$(printf '%03d' \$n).pb.gz"
    curl -sf "http://localhost:\${PORT}/debug/pprof/heap" -o "\$f" 2>/dev/null
    size_kb=\$(du -k "\$f" 2>/dev/null | cut -f1)

    pid=\$(lsof -ti:"\${PORT}" 2>/dev/null | head -1)
    rss_mb=0; vsz_mb=0
    if [ -n "\$pid" ]; then
        rss_kb=\$(awk '/VmRSS/{print \$2}' /proc/\$pid/status 2>/dev/null || echo 0)
        vsz_kb=\$(awk '/VmSize/{print \$2}' /proc/\$pid/status 2>/dev/null || echo 0)
        rss_mb=\$((rss_kb / 1024))
        vsz_mb=\$((vsz_kb / 1024))
    fi

    delta=\$((rss_mb - prev_rss))
    if [ \$delta -gt 5 ]; then
        delta_c="\033[0;31m(+\${delta}MB)\033[0m"
    elif [ \$delta -lt -5 ]; then
        delta_c="\033[0;32m(\${delta}MB)\033[0m"
    else
        delta_c="(~stable)"
    fi

    printf "\033[0;33m[\${ts}]\033[0m #%03d  RSS:\033[1m%5dMB\033[0m %b  VSZ:%5dMB  pb.gz:%dKB\n" \\
        "\$n" "\$rss_mb" "\$delta_c" "\$vsz_mb" "\$size_kb"

    prev_rss=\$rss_mb
    n=\$((n + 1))
    sleep \$INTERVAL
done
EOF
chmod +x "$PROFILE_DIR/snapshot-loop.sh"

# ── [D] forge analysis — all config baked in ─────────────────────────────────
cat > "$PROFILE_DIR/forge-analysis.sh" << EOF
#!/bin/bash
PDIR="${PROFILE_DIR}"
INTERVAL="${SNAPSHOT_INTERVAL}"

echo "Waiting for first snapshot..."
until ls "\${PDIR}"/heap_*.pb.gz >/dev/null 2>&1; do sleep 3; done
echo -e "\033[1;32mForge allocator analysis — every \${INTERVAL}s\033[0m"
echo ""

prev_f=""

while true; do
    latest=\$(ls -t "\${PDIR}"/heap_*.pb.gz 2>/dev/null | head -1)
    if [ -z "\$latest" ] || [ "\$latest" = "\$prev_f" ]; then
        sleep 5; continue
    fi
    prev_f=\$latest

    ts=\$(date '+%H:%M:%S')
    echo -e "\033[1;36m=== TOP ALLOCATORS \${ts} ===\033[0m"
    echo "  \${latest##*/}"
    echo ""

    echo -e "\033[1;33m-- inuse_space top 15 --\033[0m"
    go tool pprof -top -inuse_space -unit=mb "\$latest" 2>/dev/null \\
        | grep -vE '^(File:|Build ID:|Type:|Time:|Showing|Dropped|\$)' | head -18 \\
        || echo "  (no data)"

    echo ""
    echo -e "\033[1;33m-- forge focus (inuse_space) --\033[0m"
    out=\$(go tool pprof -top -inuse_space -unit=mb -focus='tools/forge' "\$latest" 2>/dev/null \\
        | grep -vE '^(File:|Build ID:|Type:|Time:|Showing|Dropped|\$)' | head -15)
    if [ -z "\$out" ] || echo "\$out" | grep -qE 'no nodes|no samples'; then
        echo "  (no forge allocations yet — trigger some SemanticGrep calls)"
    else
        echo "\$out"
    fi

    echo ""
    echo -e "\033[1;33m-- alloc_space top 10 (cumulative) --\033[0m"
    go tool pprof -top -alloc_space -unit=mb "\$latest" 2>/dev/null \\
        | grep -vE '^(File:|Build ID:|Type:|Time:|Showing|Dropped|\$)' | head -13 \\
        || echo "  (no data)"

    echo ""
    printf "\033[0;33mNext analysis after \${INTERVAL}s\033[0m\n"
    sleep \$INTERVAL
done
EOF
chmod +x "$PROFILE_DIR/forge-analysis.sh"

# ── Build tmux layout using pane IDs (immune to base-index settings) ─────────

tmux new-session -d -s "$SESSION" -x 240 -y 60

PANE_A=$(tmux list-panes -t "$SESSION" -F '#{pane_id}' | head -1)

# [A] swarm-tui
tmux send-keys -t "$PANE_A" "\"$PROFILE_DIR/launch.sh\"" Enter

# [B] split right (42% width) → memory overview
# tmux 3.4 removed split-window's legacy -p percentage flag. The -l option
# accepts percentages across both old and new tmux releases.
PANE_B=$(tmux split-window -t "$PANE_A" -h -l 42% -P -F '#{pane_id}')
tmux send-keys -t "$PANE_B" "\"$PROFILE_DIR/heap-watch.sh\"" Enter

# [C] split below A (35% height) → snapshot loop
PANE_C=$(tmux split-window -t "$PANE_A" -v -l 35% -P -F '#{pane_id}')
tmux send-keys -t "$PANE_C" "\"$PROFILE_DIR/snapshot-loop.sh\"" Enter

# [D] split below B (35% height) → forge analysis
PANE_D=$(tmux split-window -t "$PANE_B" -v -l 35% -P -F '#{pane_id}')
tmux send-keys -t "$PANE_D" "\"$PROFILE_DIR/forge-analysis.sh\"" Enter

# Pane titles
tmux select-pane -t "$PANE_A" -T "swarm-tui [pprof::${PPROF_PORT}]"
tmux select-pane -t "$PANE_B" -T "memory-overview"
tmux select-pane -t "$PANE_C" -T "snapshots"
tmux select-pane -t "$PANE_D" -T "forge-allocs"

tmux select-pane -t "$PANE_A"

# ── Usage summary ─────────────────────────────────────────────────────────────
echo ""
echo -e "${GREEN}Session '${SESSION}' ready.${NC}"
echo -e "  ${BOLD}Attach:${NC}  tmux attach -t ${SESSION}"
echo -e "  Panes :  A=${PANE_A} (tui)  B=${PANE_B} (heap)  C=${PANE_C} (snap)  D=${PANE_D} (forge)"
echo -e "  Profiles: ${PROFILE_DIR}"
echo ""
echo -e "${CYAN}Ad-hoc pprof:${NC}"
echo "  go tool pprof -http=:8080 http://localhost:${PPROF_PORT}/debug/pprof/heap"
echo "  go tool pprof -http=:8081 -diff_base=\${OLD}.pb.gz \${NEW}.pb.gz"
echo "  curl -sf 'http://localhost:${PPROF_PORT}/debug/pprof/trace?seconds=10' -o /tmp/t.out && go tool trace /tmp/t.out"
echo "  curl -sf 'http://localhost:${PPROF_PORT}/debug/pprof/goroutine?debug=2' | less"
echo ""

tmux attach -t "$SESSION"
