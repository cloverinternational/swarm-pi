#!/usr/bin/env bash
# validate-workflow.sh — Swarm Workflow YAML Linter
#
# Usage:
#   bash validate-workflow.sh <path-to-workflow.yaml>
#   bash validate-workflow.sh ./workflows/my_workflow.yaml
#
# Exit codes:
#   0  All checks passed (may have warnings)
#   1  One or more FATAL errors found

set -euo pipefail

YELLOW='\033[0;33m'
GREEN='\033[0;32m'
RED='\033[0;31m'
CYAN='\033[0;36m'
BOLD='\033[1m'
NC='\033[0m'

FAIL_COUNT=0
WARN_COUNT=0

ok()   { echo -e "  ${GREEN}[OK]${NC}   $*"; }
warn() { echo -e "  ${YELLOW}[WARN]${NC} $*"; ((WARN_COUNT++)) || true; }
fail() { echo -e "  ${RED}[FAIL]${NC} $*"; ((FAIL_COUNT++)) || true; }
info() { echo -e "  ${CYAN}[INFO]${NC} $*"; }

if [ $# -lt 1 ]; then
  echo "Usage: bash validate-workflow.sh <workflow.yaml>"
  exit 1
fi

YAML_FILE="$1"

echo -e "\n${BOLD}Swarm Workflow Validator${NC}"
echo -e "Checking: ${CYAN}${YAML_FILE}${NC}\n"

# ── 1. File exists ─────────────────────────────────────────────────────────────
if [ ! -f "$YAML_FILE" ]; then
  fail "File not found: $YAML_FILE"
  exit 1
fi
ok "File exists: $YAML_FILE"

# ── 2. Prefer Python for deep YAML validation ──────────────────────────────────
if command -v python3 &>/dev/null; then
  python3 - "$YAML_FILE" << 'PYEOF'
import sys
import re
import collections

try:
    import yaml
except ImportError:
    print("  \033[0;33m[WARN]\033[0m  PyYAML not installed — skipping deep YAML validation")
    print("         Install with: pip install pyyaml")
    sys.exit(0)

CANONICAL_TOOLS = {
    # Core file tools (swarmtools / forge)
    "Undo", "Shell",
    # Patch application (ii package — lowercase)
    "apply_patch",
    "Bash", "Read", "Write", "Edit", "Grep",
    "Task", "BackgroundTask", "ReadBackgroundCommand",
    "TodoRead", "TodoWrite",

}
TOOL_ALIASES = {
    "bash": "Bash",
    "file_read": "Read",
    "file_write": "Write",
    "file_edit": "Edit",
    "grep": "Grep",
    "task": "Task",
    "background_task": "BackgroundTask",
    "read_background_command": "ReadBackgroundCommand",
    "todo_read": "TodoRead",
    "todo_write": "TodoWrite",
}
VALID_EXECUTION  = {"parallel", "sequential", "adversarial"}
VALID_COMPLETION = {"all", "first", "consensus"}
VALID_OUTPUT     = {"raw", "synthesize", "first"}
VALID_STEERING   = {"none", "rule", "llm", "hybrid"}
VALID_PARAM_TYPES= {"text", "number", "choice", "multi_choice", "confirm"}
VALID_TIMEOUT_BEH= {"partial", "fail", "continue"}

YELLOW = "\033[0;33m"
GREEN  = "\033[0;32m"
RED    = "\033[0;31m"
NC     = "\033[0m"

fails = 0
warns = 0

def ok(msg):   print(f"  {GREEN}[OK]{NC}   {msg}")
def warn(msg): global warns;  warns  += 1; print(f"  {YELLOW}[WARN]{NC} {msg}")
def fail(msg): global fails;  fails  += 1; print(f"  {RED}[FAIL]{NC} {msg}")

# Load YAML
try:
    with open(sys.argv[1]) as f:
        doc = yaml.safe_load(f)
    ok("Valid YAML syntax")
except yaml.YAMLError as e:
    fail(f"YAML parse error: {e}")
    sys.exit(1)

if not isinstance(doc, dict):
    fail("Top-level must be a YAML mapping (dict)")
    sys.exit(1)

# ── Top-level required fields ──────────────────────────────────────────────────
for field in ("id", "name", "version", "groups"):
    if not doc.get(field):
        fail(f"Missing required top-level field: '{field}'")
    else:
        ok(f"Required field present: '{field}' = {repr(doc[field])}")

# ── Version format ─────────────────────────────────────────────────────────────
version = doc.get("version", "")
if version and not re.match(r"^\d+\.\d+\.\d+", str(version)):
    warn(f"'version' should be semver (e.g. 1.0.0), got: {version!r}")

# ── config section ─────────────────────────────────────────────────────────────
cfg = doc.get("config", {}) or {}
if cfg:
    tb = cfg.get("timeout_behavior", "partial")
    if tb not in VALID_TIMEOUT_BEH:
        warn(f"config.timeout_behavior should be one of {VALID_TIMEOUT_BEH}, got: {tb!r}")
    dur = cfg.get("max_duration", "")
    if dur:
        if not re.match(r"^\d+[smh]", str(dur)):
            warn(f"config.max_duration format unclear: {dur!r}. Expected format like '30m', '1h', '2h30m'")

# ── groups section ─────────────────────────────────────────────────────────────
groups = doc.get("groups") or []
if not isinstance(groups, list) or len(groups) == 0:
    fail("'groups' must be a non-empty list")
    sys.exit(1)

group_ids = []
id_counts = collections.Counter()

for i, grp in enumerate(groups):
    if not isinstance(grp, dict):
        fail(f"Group at index {i} is not a mapping")
        continue

    gid  = grp.get("id",   f"<unnamed-{i}>")
    gname= grp.get("name", f"<unnamed-{i}>")
    label = f"group[{i}] '{gid}'"

    # id
    if not grp.get("id"):
        warn(f"{label}: missing 'id' — will be auto-generated from name, but explicit IDs are preferred")
    else:
        id_counts[gid] += 1
        group_ids.append(gid)

    # name
    if not grp.get("name"):
        fail(f"{label}: missing required 'name'")

    # execution
    exec_strat = grp.get("execution", "")
    if exec_strat not in VALID_EXECUTION:
        fail(f"{label}: 'execution' must be one of {VALID_EXECUTION}, got: {exec_strat!r}")

    # timeout
    timeout = grp.get("timeout", "")
    if timeout and not re.match(r"^\d+[smh]", str(timeout)):
        warn(f"{label}: 'timeout' format unclear: {timeout!r}. Expected like '5m', '30s'")

    # completion
    comp = grp.get("completion", {}) or {}
    if comp:
        ctype = comp.get("type", "all")
        if ctype not in VALID_COMPLETION:
            fail(f"{label}: completion.type must be one of {VALID_COMPLETION}, got: {ctype!r}")

    # adversarial requires consensus completion
    if exec_strat == "adversarial":
        ctype = (grp.get("completion") or {}).get("type", "")
        if ctype != "consensus":
            fail(f"{label}: execution=adversarial requires completion.type=consensus, got: {ctype!r}")
        thresh = (grp.get("completion") or {}).get("threshold")
        if thresh is None:
            warn(f"{label}: execution=adversarial should have completion.threshold (e.g. 0.75)")
        elif float(thresh) >= 1.0:
            warn(f"{label}: completion.threshold=1.0 requires unanimous consensus which may never be reached")

    # output_strategy
    os_ = grp.get("output_strategy", "raw")
    if os_ not in VALID_OUTPUT:
        fail(f"{label}: output_strategy must be one of {VALID_OUTPUT}, got: {os_!r}")
    if os_ == "synthesize" and not grp.get("coordinator"):
        warn(f"{label}: output_strategy=synthesize requires a 'coordinator' agent definition")

    # agents
    agents = grp.get("agents") or []
    if not agents:
        fail(f"{label}: 'agents' must be a non-empty list")
        continue

    if exec_strat == "adversarial" and len(agents) < 2:
        fail(f"{label}: execution=adversarial requires at least 2 agents, got {len(agents)}")

    agent_ids_in_group = []
    for j, ag in enumerate(agents):
        if not isinstance(ag, dict):
            fail(f"{label} agent[{j}]: not a mapping")
            continue
        alabel = f"{label} agent[{j}] '{ag.get('name', '<unnamed>')}'"

        # name
        if not ag.get("name"):
            fail(f"{alabel}: missing required 'name'")

        # provider
        prov = ag.get("provider", "")
        if not prov:
            fail(f"{alabel}: missing required 'provider'")
        elif prov not in ("@current", "@profile") and prov.startswith("@") and prov not in ("@current", "@profile"):
            warn(f"{alabel}: unknown provider alias '{prov}' — valid aliases: @current, @profile")

        # model
        model = ag.get("model", "")
        if not model:
            fail(f"{alabel}: missing required 'model'")

        # tools
        tools = ag.get("tools") or []
        for t in tools:
            if t in TOOL_ALIASES:
                warn(f"{alabel}: tool '{t}' is an alias — use canonical name '{TOOL_ALIASES[t]}'")
            elif t not in CANONICAL_TOOLS:
                warn(f"{alabel}: unknown tool '{t}' — verify it exists in the SDK tool registry")

        # capabilities
        caps = ag.get("capabilities", {}) or {}
        if caps:
            temp = caps.get("temperature")
            if temp is not None and (float(temp) < 0.0 or float(temp) > 2.0):
                warn(f"{alabel}: temperature {temp} is outside normal range [0.0, 2.0]")
            mt = caps.get("max_tokens")
            if mt is not None and int(mt) < 100:
                warn(f"{alabel}: max_tokens={mt} is very low — agent may truncate output")

        # agent id uniqueness within group
        aid = ag.get("id")
        if aid:
            if aid in agent_ids_in_group:
                fail(f"{alabel}: duplicate agent id '{aid}' within group '{gid}'")
            agent_ids_in_group.append(aid)

# ── Group ID uniqueness ─────────────────────────────────────────────────────────
for gid, count in id_counts.items():
    if count > 1:
        fail(f"Duplicate group ID: '{gid}' appears {count} times")
if len(set(group_ids)) == len(group_ids):
    ok(f"Group IDs unique: {group_ids}")

# ── depends_on reference check ─────────────────────────────────────────────────
all_ids = set(group_ids)
for grp in groups:
    if not isinstance(grp, dict): continue
    gid = grp.get("id", "")
    for dep in (grp.get("depends_on") or []):
        if dep not in all_ids:
            # Suggest closest match
            candidates = [x for x in all_ids if x.startswith(dep[:3])]
            hint = f" — did you mean {candidates[0]!r}?" if candidates else ""
            fail(f"Group '{gid}' depends on non-existent group '{dep}'{hint}")
if not any(True for grp in groups if isinstance(grp, dict) and any(
    dep not in all_ids for dep in (grp.get("depends_on") or [])
)):
    ok("All depends_on references valid")

# ── Circular dependency detection (DFS) ────────────────────────────────────────
adj = {grp["id"]: list(grp.get("depends_on") or [])
       for grp in groups if isinstance(grp, dict) and grp.get("id")}

def has_cycle(node, visited, rec_stack, path):
    visited.add(node)
    rec_stack.add(node)
    path.append(node)
    for dep in adj.get(node, []):
        if dep not in visited:
            if has_cycle(dep, visited, rec_stack, path):
                return True
        elif dep in rec_stack:
            # Print cycle path
            cycle_start = path.index(dep)
            cycle = " → ".join(path[cycle_start:]) + f" → {dep}"
            fail(f"Circular dependency detected: {cycle}")
            return True
    path.pop()
    rec_stack.discard(node)
    return False

visited_all = set()
cycle_found = False
for gid in list(adj.keys()):
    if gid not in visited_all:
        if has_cycle(gid, visited_all, set(), []):
            cycle_found = True
if not cycle_found:
    ok("No circular dependencies detected")

# ── Steering section ───────────────────────────────────────────────────────────
steering = doc.get("steering", {}) or {}
if steering:
    stype = steering.get("type", "")
    if stype not in VALID_STEERING:
        fail(f"steering.type must be one of {VALID_STEERING}, got: {stype!r}")
    if stype in ("llm", "hybrid") and not steering.get("llm_meta_agent"):
        fail(f"steering.type={stype!r} requires steering.llm_meta_agent to be defined")
    if stype in ("rule", "hybrid") and not steering.get("rules"):
        warn(f"steering.type={stype!r} has no rules defined")

# ── Parameters section ─────────────────────────────────────────────────────────
params = doc.get("parameters") or []
param_ids = []
for k, p in enumerate(params):
    if not isinstance(p, dict): continue
    pid = p.get("id", f"<param-{k}>")
    if not p.get("id"):
        fail(f"Parameter[{k}]: missing required 'id'")
    if not p.get("question"):
        fail(f"Parameter '{pid}': missing required 'question'")
    ptype = p.get("type", "")
    if ptype not in VALID_PARAM_TYPES:
        fail(f"Parameter '{pid}': type must be one of {VALID_PARAM_TYPES}, got: {ptype!r}")
    if ptype in ("choice", "multi_choice") and not p.get("choices"):
        fail(f"Parameter '{pid}': type={ptype!r} requires a 'choices' list")
    param_ids.append(pid)

if len(param_ids) != len(set(param_ids)):
    fail("Duplicate parameter IDs found")
elif params:
    ok(f"Parameters valid: {param_ids}")

# ── Summary ────────────────────────────────────────────────────────────────────
print()
if fails == 0 and warns == 0:
    print(f"  \033[1;32m✓ All checks passed.\033[0m")
elif fails == 0:
    print(f"  \033[1;33m✓ Passed with {warns} warning(s). Review warnings before shipping.\033[0m")
else:
    print(f"  \033[1;31m✗ {fails} error(s), {warns} warning(s). Fix errors before running.\033[0m")

sys.exit(1 if fails > 0 else 0)
PYEOF
  PY_EXIT=$?
else
  # ── Fallback: basic grep-based checks ─────────────────────────────────────
  warn "python3 not found — running basic checks only"

  grep -q "^id:" "$YAML_FILE" && ok "Has 'id' field" || fail "Missing 'id' field"
  grep -q "^name:" "$YAML_FILE" && ok "Has 'name' field" || fail "Missing 'name' field"
  grep -q "^version:" "$YAML_FILE" && ok "Has 'version' field" || fail "Missing 'version' field"
  grep -q "^groups:" "$YAML_FILE" && ok "Has 'groups' field" || fail "Missing 'groups' field"

  PY_EXIT=0
fi

echo ""
if [ "$FAIL_COUNT" -gt 0 ] || [ "${PY_EXIT:-0}" -ne 0 ]; then
  echo -e "${RED}${BOLD}Validation failed.${NC} Fix errors before running this workflow."
  exit 1
else
  echo -e "${GREEN}${BOLD}Validation complete.${NC}"
  exit 0
fi
