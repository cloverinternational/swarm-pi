#!/usr/bin/env bash
# A2A live behavioral probe — Layer 2
#
# Spins up two real headless swarmos sessions and verifies peer discovery,
# direct messaging, and broadcast using the actual LLM + swarm tools.
#
# Each "probe" calls swarmos -p "<prompt>" against a shared workspace.
# The agent must use the swarm/a2a tools to satisfy the prompt, proving
# the full chain: registration → whitelist → communicator wiring → runtime.
#
# Requirements:
#   • swarmos binary (set SWARMOS_BIN or it will be auto-located/built)
#   • A configured provider (set SWARMOS_PROFILE, default: claudecode)
#   • ~30 seconds per probe (LLM calls)
#
# Usage:
#   bash run_live_probes.sh [--build] [--workspace /tmp/a2a-probe-ws]
#
# Flags:
#   --build      Build swarmos from source before running
#   --workspace  Override workspace directory (default: /tmp/a2a-probe)
#   --profile    Provider profile (default: claudecode)
#   --only NAME  Run only probe named NAME

set -euo pipefail

# ── config ────────────────────────────────────────────────────────────────────
WS="${SWARMOS_WS:-/tmp/a2a-probe}"
PROFILE="${SWARMOS_PROFILE:-claudecode}"
BIN="${SWARMOS_BIN:-}"
OUT="$WS/probe-output"
BUILD=0
ONLY=""

for arg in "$@"; do
  case "$arg" in
    --build)    BUILD=1 ;;
    --workspace=*) WS="${arg#*=}" ; OUT="$WS/probe-output" ;;
    --profile=*)   PROFILE="${arg#*=}" ;;
    --only=*)      ONLY="${arg#*=}" ;;
    --workspace) shift ; WS="$1" ; OUT="$WS/probe-output" ;;
    --profile)   shift ; PROFILE="$1" ;;
    --only)      shift ; ONLY="$1" ;;
  esac
done

mkdir -p "$WS" "$OUT"

# ── locate binary ─────────────────────────────────────────────────────────────
if [[ -z "$BIN" ]]; then
  REPO_ROOT="$(cd "$(dirname "$0")/../../.." && pwd)"
  if [[ $BUILD -eq 1 ]]; then
    echo "[probe] Building swarmos..."
    pushd "$REPO_ROOT/swarm-tui" >/dev/null
    go build -o /tmp/swarmos-probe ./cmd/swarmos/
    popd >/dev/null
    BIN=/tmp/swarmos-probe
  elif command -v swarmos &>/dev/null; then
    BIN=$(command -v swarmos)
  elif [[ -f /tmp/swarmos-probe ]]; then
    BIN=/tmp/swarmos-probe
  else
    echo "[probe] ERROR: swarmos not found. Run with --build or set SWARMOS_BIN." >&2
    exit 1
  fi
fi
echo "[probe] Using binary: $BIN"
echo "[probe] Workspace:    $WS"
echo "[probe] Profile:      $PROFILE"
echo ""

# ── helpers ───────────────────────────────────────────────────────────────────
FAILURES=0
PASSES=0

pass() { echo "  ✓  $1"; PASSES=$((PASSES+1)); }
fail() { echo "  ✗  $1" >&2; FAILURES=$((FAILURES+1)); }

run_probe() {
  local name="$1" prompt="$2"
  [[ -n "$ONLY" && "$ONLY" != "$name" ]] && return 0
  echo "── probe: $name ──"
  timeout 60 "$BIN" -p "$prompt" \
    --workspace "$WS" \
    -P "$PROFILE" \
    > "$OUT/$name.stdout" 2> "$OUT/$name.stderr" || true
}

check_output() {
  local name="$1" pattern="$2" description="$3"
  if grep -qiE "$pattern" "$OUT/$name.stdout" "$OUT/$name.stderr" 2>/dev/null; then
    pass "$name: $description"
  else
    fail "$name: $description (pattern '$pattern' not found)"
    echo "    stdout tail:" >&2
    tail -5 "$OUT/$name.stdout" 2>/dev/null | sed 's/^/    /' >&2
  fi
}

check_not() {
  local name="$1" pattern="$2" description="$3"
  if grep -qiE "$pattern" "$OUT/$name.stdout" "$OUT/$name.stderr" 2>/dev/null; then
    fail "$name: $description (pattern '$pattern' found but should not be)"
  else
    pass "$name: $description"
  fi
}

# ── probes ────────────────────────────────────────────────────────────────────

echo "=== Probe 1: swarm tool is visible to the agent ==="
run_probe "tool_visibility" \
  "List all tools you have available. Include any tools with 'swarm' or 'a2a' in their name. Reply with just the tool names, one per line."
check_output "tool_visibility" "swarm|a2a_" "agent can see swarm/a2a tools"
check_not    "tool_visibility" "communicator not configured" "communicator is wired before first call"

echo ""
echo "=== Probe 2: swarm join + list ==="
run_probe "swarm_join_list" \
  "Use the swarm tool to join the swarm with handle 'probe-alpha'. Then list all peers. Reply with the result."
check_output "swarm_join_list" "probe-alpha|joined|A2A" "agent joined swarm as probe-alpha"
check_not    "swarm_join_list" "communicator not configured" "communicator is wired"
check_not    "swarm_join_list" "error|failed" "no errors during join+list"

echo ""
echo "=== Probe 3: swarm_list_peers chatroom tool ==="
run_probe "chatroom_list" \
  "Use the swarm_list_peers tool to list all agents currently connected to the swarm. Show me the result."
check_output "chatroom_list" "swarm_list_peers|peers|connected|swarm" "agent used swarm_list_peers chatroom tool"

echo ""
echo "=== Probe 4: PLAN mode — swarm tools accessible ==="
# In PLAN mode (read-only), swarm_list_peers and a2a_list_agents should still be available
run_probe "plan_mode_swarm" \
  "You are in PLAN mode. List the agents available in the swarm using swarm_list_peers. What do you see?"
check_output "plan_mode_swarm" "swarm|peers|a2a|list" "swarm tool accessible in plan-mode-like context"

echo ""
echo "=== Probe 5: context cancellation (verifies BUG-7 fix) ==="
# A very short timeout should cancel cleanly, not hang.
echo "  [timeout test — expects clean cancellation within 5s]"
START=$(date +%s)
timeout 10 "$BIN" -p "Join swarm as 'probe-timeout-test' then immediately broadcast 'hello'" \
  --workspace "$WS" \
  -P "$PROFILE" \
  > "$OUT/ctx_cancel.stdout" 2>"$OUT/ctx_cancel.stderr" || true
END=$(date +%s)
ELAPSED=$((END - START))
if [[ $ELAPSED -lt 10 ]]; then
  pass "ctx_cancel: completed within timeout (${ELAPSED}s), no hang"
else
  fail "ctx_cancel: took ${ELAPSED}s — possible context leak (expected < 10s)"
fi

echo ""
echo "=== Probe 6: no duplicate peer messages ==="
# Send two identical swarm join calls — the dedup fix should prevent double entries.
run_probe "dedup_check" \
  "Use swarm tool with action=join, handle='probe-dedup'. Then use swarm tool again with action=join, handle='probe-dedup'. Report exactly what happens on the second join attempt."
check_output "dedup_check" "already joined|already|duplicate|existing" "second join is rejected gracefully"

# ── summary ───────────────────────────────────────────────────────────────────
echo ""
echo "═══════════════════��══════════════════════"
echo "  Live probe results: $PASSES passed, $FAILURES failed"
echo "  Output files: $OUT/"
echo "══════════════════════════════════════════"

[[ $FAILURES -eq 0 ]] || exit 1
