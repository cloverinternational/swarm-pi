#!/usr/bin/env node
import { createHash } from "node:crypto";
import { createServer } from "node:http";
import { mkdtemp, mkdir, readFile, readdir, rm, writeFile } from "node:fs/promises";
import { existsSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { spawn } from "node:child_process";
import { fileURLToPath } from "node:url";

/** Pi-Swarm's extension directory (the port itself), for probing foreign workspaces. */
export const PI_SWARM_EXTENSIONS = resolve(fileURLToPath(new URL("../../.pi/extensions", import.meta.url)));

const GENERATED_ID_KEYS = new Set(["tool_call_id"]);
export const PROBE_PROMPT = "PARITY_CAPTURE";
/**
 * Tool calls the scripted model issues, indexed by how many tool results it
 * has seen. "default" covers the success + failure envelopes; "hooks" runs
 * long enough to cross Swarm's builtin hook thresholds (task nudges after
 * turn > 1 && toolCalls >= 2, repeated identical failures, a workspace write)
 * so hook-injected context is compared too.
 */
export const TOOL_SCRIPTS = {
  default: [
    { id: "call_parity_probe", command: "printf parity-probe" },
    { id: "call_parity_probe_fail", command: "printf out; printf err >&2; exit 3" },
  ],
  hooks: [
    { id: "call_h1", command: "printf one" },
    { id: "call_h2", command: "printf two" },
    { id: "call_h3", command: "printf out; printf err >&2; exit 3" },
    { id: "call_h4", command: "printf out; printf err >&2; exit 3" },
    { id: "call_h5", command: "printf x > .parity-probe-write && cat .parity-probe-write && rm .parity-probe-write" },
    { id: "call_h6", command: "sleep 0.2; printf six" },
    { id: "call_h7", command: "printf seven" },
    { id: "call_h8", command: "printf eight" },
    // pre-tool hook families: protected-branch (read-only git escape vs
    // mutating shell op), stdin-conflict (pipe + heredoc), and the task hooks
    // (state created through TaskManage, then more tool calls).
    { id: "call_h9", command: "git status --short | head -1" },
    { id: "call_h10", command: "git diff --stat | head -1 > .parity-git-probe; rm -f .parity-git-probe" },
    { id: "call_h11", command: "printf hi | cat <<'EOF'\nx\nEOF" },
    { id: "call_h12", tool: "TaskManage", args: { operations: [{ key: "t", op: "create", subject: "parity probe task", status: "in_progress", active: true }] } },
    { id: "call_h13", command: "printf thirteen" },
    { id: "call_h14", command: "printf fourteen" },
    { id: "call_h15", tool: "TaskManage", args: { operations: [{ key: "t", op: "list" }] } },
  ],
  // Task-enforcement advisory on the first mutating call (embedded into the
  // tool result), then a focused task, the autogenskills [SKILL REVIEW] at 6
  // tool calls, the onboarding-budget block at the 6th non-exempt call and
  // its incrementing seq on every later call, task-maintenance silence while
  // the meta-nudge window is consumed, and exempt calls passing through.
  budget: [
    { id: "call_b1", command: "printf x > .parity-b && rm .parity-b" },
    { id: "call_b2", command: "printf y > .parity-b && rm .parity-b" },
    { id: "call_b3", tool: "TaskManage", args: { operations: [{ key: "a", op: "create", subject: "budget probe", category: "acting", status: "in_progress", active: true }] } },
    { id: "call_b4", command: "printf 1 > .parity-b && rm .parity-b" },
    { id: "call_b5", command: "printf 2 > .parity-b && rm .parity-b" },
    { id: "call_b6", command: "printf 3 > .parity-b && rm .parity-b" },
    { id: "call_b7", command: "printf 4 > .parity-b && rm .parity-b" },
    { id: "call_b8", command: "printf 5 > .parity-b && rm .parity-b" },
    { id: "call_b9", command: "printf 6 > .parity-b && rm .parity-b" },
    { id: "call_b10", command: "printf 7 > .parity-b && rm .parity-b" },
    { id: "call_b11", command: "git status --short | head -1" },
    { id: "call_b12", tool: "TaskManage", args: { operations: [{ key: "a", op: "update", taskId: "1", status: "completed" }] } },
    { id: "call_b13", command: "printf 8 > .parity-b && rm .parity-b" },
  ],
  // Task focused BEFORE any mutating call: the meta-nudge window is still
  // free, so the autogenskills lifecycle [SKILL REVIEW] (post, priority 91)
  // claims it at the 6th tool call; a failing call in between exercises the
  // lifecycle's error path next to the annoyance nudge; then the block.
  review: [
    { id: "call_r1", tool: "TaskManage", args: { operations: [{ key: "a", op: "create", subject: "review probe", status: "in_progress", active: true }] } },
    { id: "call_r2", tool: "TaskManage", args: { operations: [{ key: "a", op: "list" }] } },
    { id: "call_r3", command: "printf 1 > .parity-r && rm .parity-r" },
    { id: "call_r4", command: "printf out; printf err >&2; exit 3" },
    { id: "call_r5", command: "printf 2 > .parity-r && rm .parity-r" },
    { id: "call_r6", command: "printf 3 > .parity-r && rm .parity-r" },
    { id: "call_r7", command: "printf 4 > .parity-r && rm .parity-r" },
    { id: "call_r8", command: "printf 5 > .parity-r && rm .parity-r" },
    { id: "call_r9", command: "printf 6 > .parity-r && rm .parity-r" },
    { id: "call_r10", tool: "SkillManage", args: { action: "list" } },
    { id: "call_r11", command: "printf 7 > .parity-r && rm .parity-r" },
  ],
  // Non-bash tool result envelopes and error paths: Read (ok/missing/range),
  // apply_patch (add/update/delete/bad patch), TaskManage validation
  // failures, SkillManage view/errors, listing tools with empty state, bash
  // cwd/description/truncation variants, and Undo without a snapshot.
  tools: [
    { id: "call_t1", tool: "TaskManage", args: { operations: [{ key: "a", op: "create", subject: "tools probe", status: "in_progress", active: true }] } },
    { id: "call_t2", tool: "Read", args: { file_path: "AGENTS.md" } },
    { id: "call_t3", tool: "Read", args: { file_path: "/nonexistent/parity.txt" } },
    { id: "call_t4", tool: "Read", args: { file_path: "AGENTS.md", offset: 2, limit: 3 } },
    { id: "call_t5", tool: "apply_patch", args: { input: "*** Begin Patch\n*** Add File: .parity-tools.txt\n+one\n+two\n*** End Patch" } },
    { id: "call_t6", tool: "apply_patch", args: { input: "*** Begin Patch\n*** Update File: .parity-tools.txt\n@@\n one\n-two\n+three\n*** End Patch" } },
    { id: "call_t7", tool: "apply_patch", args: { input: "*** Begin Patch\n*** Update File: .parity-tools.txt\n@@\n-missing\n+x\n*** End Patch" } },
    { id: "call_t8", tool: "apply_patch", args: { input: "*** Begin Patch\n*** Delete File: .parity-tools.txt\n*** End Patch" } },
    { id: "call_t9", tool: "apply_patch", args: { input: "not a patch" } },
    { id: "call_t10", tool: "TaskManage", args: { operations: [{ key: "b", op: "update", taskId: "999", status: "completed" }] } },
    { id: "call_t11", tool: "TaskManage", args: { operations: [{ key: "c", op: "create", subject: "" }] } },
    { id: "call_t12", tool: "TaskManage", args: { operations: [{ key: "d", op: "get", taskId: "1" }, { key: "e", op: "list" }] } },
    { id: "call_t13", tool: "SkillManage", args: { action: "view", name: "swarm-skill" } },
    { id: "call_t14", tool: "SkillManage", args: { action: "bogus" } },
    { id: "call_t15", tool: "SkillManage", args: { action: "view", name: "does-not-exist" } },
    { id: "call_t16", tool: "SkillManage", args: { action: "patch" } },
    { id: "call_t17", tool: "Skill", args: { skill: "swarm-skill" } },
    { id: "call_t18", tool: "Skill", args: { skill: "does-not-exist" } },
    { id: "call_t19", tool: "CronList", args: {} },
    { id: "call_t20", tool: "vault_list", args: {} },
    { id: "call_t21", tool: "HistorySearch", args: { query: "zzz-parity-none", limit: 1 } },
    { id: "call_t22", tool: "Undo", args: { path: "/nonexistent/parity.txt" } },
    { id: "call_t23", command: "printf hi", cwd: "/nonexistent-cwd" },
    { id: "call_t24", command: "printf hi", description: "say \"hi\"", timeout_seconds: 5 },
    { id: "call_t25", command: "seq 1 2500" },
    { id: "call_t26", command: "printf '\\033[31mred\\033[0m'" },
    { id: "call_t27", tool: "bash", args: {} },
    { id: "call_t28", tool: "Read", args: {} },
  ],
  // On-disk (non-builtin) skill invocation, only meaningful in a workspace
  // that ships project skills (tests/parity-probe.test.mjs makeStressWorkspace):
  // "Base directory for this skill:" prefix, named {{argName}} (frontmatter
  // `arguments`) and positional {{N}} substitution over strings.Fields(args),
  // ${SWARM_SKILL_DIR}/${SWARM_SESSION_ID}, disable-model-invocation refusal,
  // empty instructions, SkillManage view/history of a non-autogen skill, and
  // a list that overflows the catalogue.
  skills: [
    { id: "call_k1", tool: "TaskManage", args: { operations: [{ key: "a", op: "create", subject: "skills probe", status: "in_progress", active: true }] } },
    { id: "call_k2", tool: "Skill", args: { skill: "args-skill" } },
    { id: "call_k3", tool: "Skill", args: { skill: "args-skill", args: "alpha  beta\tgamma" } },
    { id: "call_k4", tool: "Skill", args: { skill: "args-skill", args: "only" } },
    { id: "call_k5", tool: "Skill", args: { skill: "hidden-skill" } },
    { id: "call_k6", tool: "Skill", args: { skill: "empty-skill" } },
    { id: "call_k7", tool: "Skill", args: { skill: "claude-side" } },
    { id: "call_k8", tool: "Skill", args: { skill: "" } },
    { id: "call_k9", tool: "SkillManage", args: { action: "view", name: "args-skill" } },
    { id: "call_k10", tool: "SkillManage", args: { action: "history", name: "args-skill" } },
    { id: "call_k11", tool: "SkillManage", args: { action: "list" } },
    { id: "call_k12", tool: "SkillManage", args: { action: "read_file", name: "args-skill", file_path: "references/notes.md" } },
  ],
  // Autogen package lifecycle inside the sandbox HOME: create (too short,
  // then valid), view → revision, patch (stale/valid revision), write_file,
  // read_file, absorb_files, archive (refused, then valid), history, undo,
  // and the list afterwards. Revision hashes are per-run and masked.
  mutations: [
    { id: "call_m1", tool: "TaskManage", args: { operations: [{ key: "a", op: "create", subject: "mutations probe", status: "in_progress", active: true }] } },
    { id: "call_m2", tool: "SkillManage", args: { action: "create", name: "probe-alpha", description: "Alpha probe", instructions: "too short" } },
    { id: "call_m3", tool: "SkillManage", args: { action: "create", name: "probe-alpha", description: "Alpha probe", instructions: "Step-by-step instructions that are long enough to satisfy the minimum instruction length. Step-by-step instructions that are long enough to satisfy the minimum instruction length. Step-by-step instructions that are long enough to satisfy the minimum instruction length. Step-by-step instructions that are long enough to satisfy the minimum instruction length.", tags: "probe, alpha", category: "testing" } },
    { id: "call_m4", tool: "SkillManage", args: { action: "create", name: "probe-alpha", description: "Alpha again", instructions: "Step-by-step instructions that are long enough to satisfy the minimum instruction length. Step-by-step instructions that are long enough to satisfy the minimum instruction length. Step-by-step instructions that are long enough to satisfy the minimum instruction length. Step-by-step instructions that are long enough to satisfy the minimum instruction length." } },
    { id: "call_m5", tool: "SkillManage", args: { action: "view", name: "probe-alpha" } },
    { id: "call_m6", tool: "SkillManage", args: { action: "patch", name: "probe-alpha", instructions: "Patched body Step-by-step instructions that are long enough to satisfy the minimum instruction length. Step-by-step instructions that are long enough to satisfy the minimum instruction length. Step-by-step instructions that are long enough to satisfy the minimum instruction length. Step-by-step instructions that are long enough to satisfy the minimum instruction length.", expected_revision: "0000000000000000000000000000000000000000000000000000000000000000" } },
    { id: "call_m7", tool: "SkillManage", args: { action: "patch", name: "probe-alpha", instructions: "Patched body Step-by-step instructions that are long enough to satisfy the minimum instruction length. Step-by-step instructions that are long enough to satisfy the minimum instruction length. Step-by-step instructions that are long enough to satisfy the minimum instruction length. Step-by-step instructions that are long enough to satisfy the minimum instruction length.", append: true, expected_revision: "$LAST_REVISION" } },
    { id: "call_m8", tool: "SkillManage", args: { action: "write_file", name: "probe-alpha", file_path: "references/notes.md", file_content: "notes body\n", expected_revision: "$LAST_REVISION" } },
    { id: "call_m9", tool: "SkillManage", args: { action: "write_file", name: "probe-alpha", file_path: "secrets/x.md", file_content: "nope", expected_revision: "$LAST_REVISION" } },
    { id: "call_m10", tool: "SkillManage", args: { action: "read_file", name: "probe-alpha", file_path: "references/notes.md" } },
    { id: "call_m11", tool: "SkillManage", args: { action: "read_file", name: "probe-alpha", file_path: "references/missing.md" } },
    { id: "call_m12", tool: "SkillManage", args: { action: "create", name: "probe-beta", description: "Beta probe", instructions: "Step-by-step instructions that are long enough to satisfy the minimum instruction length. Step-by-step instructions that are long enough to satisfy the minimum instruction length. Step-by-step instructions that are long enough to satisfy the minimum instruction length. Step-by-step instructions that are long enough to satisfy the minimum instruction length." } },
    { id: "call_m13", tool: "SkillManage", args: { action: "absorb_files", name: "probe-beta", from_skill: "probe-alpha", expected_revision: "$LAST_REVISION" } },
    { id: "call_m14", tool: "SkillManage", args: { action: "archive", name: "probe-alpha", expected_revision: "$REVISION:probe-alpha" } },
    { id: "call_m15", tool: "SkillManage", args: { action: "archive", name: "probe-alpha", absorbed_into: "probe-beta", expected_revision: "$REVISION:probe-alpha" } },
    { id: "call_m16", tool: "SkillManage", args: { action: "view", name: "probe-alpha" } },
    { id: "call_m17", tool: "SkillManage", args: { action: "history", name: "probe-beta" } },
    { id: "call_m18", tool: "SkillManage", args: { action: "undo", name: "probe-beta", expected_revision: "$REVISION:probe-beta" } },
    { id: "call_m19", tool: "SkillManage", args: { action: "list" } },
    { id: "call_m20", tool: "SkillManage", args: { action: "review", review_reason: "nothing reusable to save" } },
  ],
  // SkillManage edge cases the lifecycle scenario does not reach: invalid and
  // reserved names, paging, history limits, missing packages, patch merges,
  // empty write_file bodies, absorb_files with explicit/missing paths,
  // archive with dropped_files, read_file/undo against archived and absent
  // placements, and undo back to the absent baseline.
  mutations2: [
    { id: "call_n1", tool: "TaskManage", args: { operations: [{ key: "a", op: "create", subject: "edge probe", status: "in_progress", active: true }] } },
    { id: "call_n2", tool: "SkillManage", args: { action: "create", name: "Bad_Name", description: "x", instructions: "y" } },
    { id: "call_n3", tool: "SkillManage", args: { action: "create", name: "archive", description: "x", instructions: "y" } },
    { id: "call_n4", tool: "SkillManage", args: { action: "create", name: "edge-a", instructions: "y" } },
    { id: "call_n5", tool: "SkillManage", args: { action: "create", name: "edge-a", description: "Edge A — it's \"quoted\": yes", instructions: "Line one\n\nLine two with unicode café ✓\n", tags: " one, two ,, three ", category: "cat: egory" } },
    { id: "call_n6", tool: "SkillManage", args: { action: "view", name: "edge-a", offset: 5, limit: 7 } },
    { id: "call_n7", tool: "SkillManage", args: { action: "view", name: "edge-a", offset: 999 } },
    { id: "call_n8", tool: "SkillManage", args: { action: "view", name: "edge-a", limit: 0 } },
    { id: "call_n9", tool: "SkillManage", args: { action: "view", name: "missing-skill" } },
    { id: "call_n10", tool: "SkillManage", args: { action: "patch", name: "missing-skill", instructions: "z", expected_revision: "0000000000000000000000000000000000000000000000000000000000000000" } },
    { id: "call_n11", tool: "SkillManage", args: { action: "patch", name: "edge-a", description: "Edge A v2", tags: "two, four", expected_revision: "$REVISION:edge-a" } },
    { id: "call_n12", tool: "SkillManage", args: { action: "patch", name: "edge-a", instructions: "", append: true, expected_revision: "$REVISION:edge-a" } },
    { id: "call_n13", tool: "SkillManage", args: { action: "write_file", name: "edge-a", file_path: "references/empty.md", file_content: "", expected_revision: "$REVISION:edge-a" } },
    { id: "call_n14", tool: "SkillManage", args: { action: "write_file", name: "edge-a", file_path: "references/../../escape.md", file_content: "x", expected_revision: "$REVISION:edge-a" } },
    { id: "call_n15", tool: "SkillManage", args: { action: "write_file", name: "edge-a", file_path: "scripts/run.sh", file_content: "#!/bin/sh\necho hi\n", expected_revision: "$REVISION:edge-a" } },
    { id: "call_n16", tool: "SkillManage", args: { action: "write_file", name: "edge-a", file_path: "scripts/run.sh", file_content: "#!/bin/sh\necho hi\n" } },
    { id: "call_n17", tool: "SkillManage", args: { action: "history", name: "edge-a", limit: 2 } },
    { id: "call_n18", tool: "SkillManage", args: { action: "history", name: "edge-a", limit: 0 } },
    { id: "call_n19", tool: "SkillManage", args: { action: "history", name: "missing-skill" } },
    { id: "call_n20", tool: "SkillManage", args: { action: "create", name: "edge-b", description: "Edge B", instructions: "Step-by-step instructions that are long enough to satisfy the minimum instruction length. Step-by-step instructions that are long enough to satisfy the minimum instruction length. Step-by-step instructions that are long enough to satisfy the minimum instruction length. Step-by-step instructions that are long enough to satisfy the minimum instruction length." } },
    { id: "call_n21", tool: "SkillManage", args: { action: "absorb_files", name: "edge-b", from_skill: "edge-a", file_paths: "scripts/run.sh, references/nope.md", expected_revision: "$REVISION:edge-b" } },
    { id: "call_n22", tool: "SkillManage", args: { action: "absorb_files", name: "edge-b", from_skill: "edge-a", file_paths: "scripts/run.sh", expected_revision: "$REVISION:edge-b" } },
    { id: "call_n23", tool: "SkillManage", args: { action: "absorb_files", name: "edge-b", from_skill: "edge-a", expected_revision: "$REVISION:edge-b" } },
    { id: "call_n24", tool: "SkillManage", args: { action: "absorb_files", name: "edge-b", from_skill: "missing-skill", expected_revision: "$REVISION:edge-b" } },
    { id: "call_n25", tool: "SkillManage", args: { action: "absorb_files", name: "edge-b", from_skill: "edge-b", expected_revision: "$REVISION:edge-b" } },
    { id: "call_n26", tool: "SkillManage", args: { action: "archive", name: "edge-a", absorbed_into: "edge-b", dropped_files: "references/empty.md", expected_revision: "$REVISION:edge-a" } },
    { id: "call_n27", tool: "SkillManage", args: { action: "archive", name: "edge-a", absorbed_into: "edge-b", dropped_files: "references/empty.md", pruning_reason: "empty placeholder", expected_revision: "$REVISION:edge-a" } },
    { id: "call_n28", tool: "SkillManage", args: { action: "read_file", name: "edge-a", file_path: "scripts/run.sh" } },
    { id: "call_n29", tool: "SkillManage", args: { action: "patch", name: "edge-a", instructions: "after archive", expected_revision: "$REVISION:edge-a" } },
    { id: "call_n30", tool: "SkillManage", args: { action: "archive", name: "edge-a", pruning_reason: "again", expected_revision: "$REVISION:edge-a" } },
    { id: "call_n31", tool: "SkillManage", args: { action: "undo", name: "edge-a", expected_revision: "$REVISION:edge-a" } },
    { id: "call_n32", tool: "SkillManage", args: { action: "history", name: "edge-b" } },
    { id: "call_n33", tool: "SkillManage", args: { action: "undo", name: "edge-b", revision: "$REVISION:edge-b", expected_revision: "$REVISION:edge-b" } },
    { id: "call_n34", tool: "SkillManage", args: { action: "undo", name: "edge-b", revision: "$BASELINE:edge-b", expected_revision: "$REVISION:edge-b" } },
    { id: "call_n35", tool: "SkillManage", args: { action: "history", name: "edge-b" } },
    { id: "call_n36", tool: "SkillManage", args: { action: "view", name: "edge-b" } },
    { id: "call_n37", tool: "SkillManage", args: { action: "list" } },
  ],
  // Skill-tool edges: autogen packages created in-session (argument
  // substitution, support files, archived placement), builtins with args,
  // and SkillManage pointed at builtin/project skills instead of autogen.
  skills2: [
    { id: "call_s1", tool: "TaskManage", args: { operations: [{ key: "a", op: "create", subject: "skill edge probe", status: "in_progress", active: true }] } },
    { id: "call_s2", tool: "SkillManage", args: { action: "create", name: "dyn-skill", description: "Dynamic probe", instructions: "Run {{1}} then {{2}} with {{arg}} in ${SWARM_SKILL_DIR} session=[${SWARM_SESSION_ID}] all=[$ARGUMENTS] {{missing}}", tags: "dyn" } },
    { id: "call_s3", tool: "Skill", args: { skill: "dyn-skill", args: "alpha beta" } },
    { id: "call_s4", tool: "Skill", args: { skill: "dyn-skill" } },
    { id: "call_s5", tool: "SkillManage", args: { action: "write_file", name: "dyn-skill", file_path: "references/r.md", file_content: "ref body\n", expected_revision: "$REVISION:dyn-skill" } },
    { id: "call_s6", tool: "Skill", args: { skill: "dyn-skill", args: "gamma" } },
    { id: "call_s7", tool: "SkillManage", args: { action: "archive", name: "dyn-skill", pruning_reason: "probe", expected_revision: "$REVISION:dyn-skill" } },
    { id: "call_s8", tool: "Skill", args: { skill: "dyn-skill" } },
    { id: "call_s9", tool: "Skill", args: { skill: "loop", args: "5m /foo" } },
    { id: "call_s10", tool: "Skill", args: { skill: "swarm-skill", args: "install foo" } },
    { id: "call_s11", tool: "Skill", args: { skill: "Bad Name" } },
    { id: "call_s12", tool: "Skill", args: { skill: "claude-side", args: "a b" } },
    { id: "call_s13", tool: "SkillManage", args: { action: "view", name: "swarm-skill" } },
    { id: "call_s14", tool: "SkillManage", args: { action: "history", name: "swarm-skill" } },
    { id: "call_s15", tool: "SkillManage", args: { action: "read_file", name: "swarm-skill", file_path: "references/x.md" } },
    { id: "call_s16", tool: "SkillManage", args: { action: "view", name: "claude-side" } },
    { id: "call_s17", tool: "SkillManage", args: { action: "patch", name: "args-skill", instructions: "patched project skill", expected_revision: "$REVISION:args-skill" } },
    { id: "call_s18", tool: "SkillManage", args: { action: "view", name: "args-skill" } },
    { id: "call_s19", tool: "SkillManage", args: { action: "patch", name: "args-skill", instructions: "patched project skill", expected_revision: "$REVISION:args-skill" } },
    { id: "call_s20", tool: "Skill", args: { skill: "args-skill", args: "x" } },
    { id: "call_s21", tool: "SkillManage", args: { action: "list" } },
  ],
  // Agent-orchestration, vault two-person, and websearch tools: validation
  // and unknown-id error paths (no sub-agent is actually spawned, so the
  // scripted model sees only the primary conversation).
  agents: [
    { id: "call_g1", tool: "TaskManage", args: { operations: [{ key: "a", op: "create", subject: "agent tools probe", status: "in_progress", active: true }] } },
    { id: "call_g2", tool: "BackgroundTask", args: {} },
    { id: "call_g3", tool: "Subagent", args: { task: "x", agent_id: "a", preset: "b" } },
    { id: "call_gs1", tool: "Skill", args: { skill: "loop" } },
    { id: "call_g4", tool: "Subagent", args: { task: "x", run_in_background: true, auto_background_seconds: 1 } },
    { id: "call_g5", tool: "Subagent", args: { task: "x", agent_id: "no-such-agent" } },
    { id: "call_g6", tool: "Subagent", args: {} },
    { id: "call_gs2", tool: "Skill", args: { skill: "loop" } },
    { id: "call_g7", tool: "SubagentOutput", args: { agent_id: "nope", action: "status" } },
    { id: "call_g8", tool: "SubagentOutput", args: {} },
    { id: "call_g9", tool: "TaskOutput", args: { agent_id: "nope" } },
    { id: "call_gs3", tool: "Skill", args: { skill: "loop" } },
    { id: "call_g10", tool: "TaskOutput", args: {} },
    { id: "call_g11", tool: "TaskOutput", args: { agent_id: "nope", action: "result", offset: 5 } },
    { id: "call_g12", tool: "wait_for_agent", args: {} },
    { id: "call_gs4", tool: "Skill", args: { skill: "loop" } },
    { id: "call_g13", tool: "wait_for_agent", args: { agent_id: "nope", timeout_seconds: 1 } },
    { id: "call_g14", tool: "multi_agent_wait", args: { agent_ids: [] } },
    { id: "call_g15", tool: "multi_agent_wait", args: { agent_ids: ["nope", "nope2"], timeout_seconds: 1 } },
    { id: "call_gs5", tool: "Skill", args: { skill: "loop" } },
    { id: "call_g16", tool: "Delegate", args: {} },
    { id: "call_g17", tool: "DelegateOutput", args: { agent_id: "nope", action: "status" } },
    { id: "call_g18", tool: "DelegateOutput", args: { agent_id: "nope", action: "bogus" } },
    { id: "call_gs6", tool: "Skill", args: { skill: "loop" } },
    { id: "call_g19", tool: "vault_approve", args: { requestId: "nope" } },
    { id: "call_g20", tool: "vault_two_person_status", args: { requestId: "nope" } },
    { id: "call_g21", tool: "vault_exec", args: { credentialId: "nope", command: "true" } },
    { id: "call_gs7", tool: "Skill", args: { skill: "loop" } },
    { id: "call_g22", tool: "vault_add", args: { id: "x", kind: "bogus_kind", secret: "s" } },
    { id: "call_g23", tool: "websearch", args: { query: "" } },
    { id: "call_g24", tool: "websearch", args: { query: "x", allowed_domains: ["a.com"], blocked_domains: ["b.com"] } },
    { id: "call_g25", tool: "websearch", args: { query: "x", type: "bogus" } },
    { id: "call_g26", tool: "websearch", args: { query: "parity probe" } },
  ],
  // Real sub-agent spawns: every orchestration tool's happy path against a
  // scripted child that answers "SUBAGENT_OK" without tool calls.
  spawn: [
    { id: "call_p1", tool: "TaskManage", args: { operations: [{ key: "a", op: "create", subject: "spawn probe", status: "in_progress", active: true }] } },
    { id: "call_p2", tool: "BackgroundTask", args: { task: "say hi" } },
    { id: "call_p3", tool: "wait_for_agent", args: { agent_id: "$LAST_AGENT", timeout_seconds: 60 } },
    { id: "call_p4", tool: "TaskOutput", args: { agent_id: "$LAST_AGENT", action: "result" } },
    { id: "call_p5", tool: "TaskOutput", args: { agent_id: "$LAST_AGENT" } },
    { id: "call_p6", tool: "SubagentOutput", args: { agent_id: "$LAST_AGENT", action: "result", offset: 3 } },
    { id: "call_ps1", tool: "Skill", args: { skill: "loop" } },
    { id: "call_p7", tool: "Subagent", args: { task: "say hi", agent_id: "general-assistant" } },
    { id: "call_p8", tool: "Subagent", args: { task: "say hi", run_in_background: true } },
    { id: "call_p9", tool: "multi_agent_wait", args: { agent_ids: ["$LAST_AGENT"], timeout_seconds: 60 } },
    { id: "call_p10", tool: "TaskOutput", args: {} },
    { id: "call_ps2", tool: "Skill", args: { skill: "loop" } },
    { id: "call_p11", tool: "Delegate", args: { task: "say hi" } },
    { id: "call_p12", tool: "DelegateOutput", args: { agent_id: "$LAST_AGENT", action: "poll" } },
    { id: "call_p13", tool: "DelegateOutput", args: { agent_id: "$LAST_AGENT", action: "result" } },
    { id: "call_p14", tool: "DelegateOutput", args: { agent_id: "$LAST_AGENT", action: "cancel" } },
    { id: "call_p15", tool: "TaskOutput", args: { agent_id: "$LAST_AGENT", action: "cancel" } },
  ],
  // Message shapes the base scripts never exercise: assistant text next to a
  // User-scoped discovery through a seeded HOME (--seed-home): skills from
  // ~/.claude/skills, ~/.claude/commands, ~/.swarm/skills and the legacy
  // ~/.swarmos/skills tree that configmigrate merges into ~/.swarm/skills on
  // first launch. Invocations expose the resolved on-disk paths.
  home: [
    { id: "call_h1", tool: "TaskManage", args: { operations: [{ key: "a", op: "create", subject: "home probe", status: "in_progress", active: true }] } },
    { id: "call_h2", tool: "Skill", args: { skill: "home-legacy-skill" } },
    { id: "call_h3", tool: "Skill", args: { skill: "home-skill-dup" } },
    { id: "call_h4", tool: "Skill", args: { skill: "home-claude-skill" } },
    { id: "call_h5", tool: "Skill", args: { skill: "home-cmd" } },
    { id: "call_h6", tool: "Skill", args: { skill: "home-swarm-skill" } },
    { id: "call_h7", tool: "SkillManage", args: { action: "view", name: "home-swarm-skill" } },
    { id: "call_h8", tool: "SkillManage", args: { action: "list" } },
    { id: "call_h9", tool: "Skill", args: { skill: "home-autogen-dmi" } },
    { id: "call_h10", tool: "SkillManage", args: { action: "view", name: "home-autogen-dmi" } },
    { id: "call_h11", tool: "SkillManage", args: { action: "read_file", name: "home-autogen-dmi", file_path: "references/r.md" } },
    { id: "call_h12", tool: "SkillManage", args: { action: "history", name: "home-autogen-dmi" } },
    // Untracked → tracked (patch with the synthetic baseline id publishes a
    // HEAD), then out-of-band drift: view/read_file/history report a synthetic
    // external revision, a stale patch conflicts against it, and presenting
    // the synthetic id reconciles it as a persisted "external" revision.
    // The synthetic "untracked" id history reports differs from the "baseline"
    // id view reports (the action is hashed): a patch with the history id
    // conflicts, then a fresh view supplies the accepted one.
    { id: "call_h13", tool: "SkillManage", args: { action: "patch", name: "home-autogen-dmi", instructions: "Tracked body.", expected_revision: "$REVISION:home-autogen-dmi" } },
    { id: "call_h13b", tool: "SkillManage", args: { action: "view", name: "home-autogen-dmi" } },
    { id: "call_h13c", tool: "SkillManage", args: { action: "patch", name: "home-autogen-dmi", instructions: "Tracked body.", expected_revision: "$REVISION:home-autogen-dmi" } },
    { id: "call_h14", command: "printf 'drifted\\n' >> \"$HOME/.swarm/skills/autogen/home-autogen-dmi/references/r.md\" && echo appended" },
    { id: "call_h15", tool: "SkillManage", args: { action: "view", name: "home-autogen-dmi" } },
    { id: "call_h16", tool: "SkillManage", args: { action: "read_file", name: "home-autogen-dmi", file_path: "references/r.md" } },
    { id: "call_h17", tool: "SkillManage", args: { action: "history", name: "home-autogen-dmi" } },
    { id: "call_h18", tool: "SkillManage", args: { action: "patch", name: "home-autogen-dmi", instructions: "Stale patch.", expected_revision: "$BASELINE:home-autogen-dmi" } },
    { id: "call_h19", tool: "SkillManage", args: { action: "patch", name: "home-autogen-dmi", instructions: "Reconciled body.", expected_revision: "$REVISION:home-autogen-dmi" } },
    { id: "call_h20", tool: "SkillManage", args: { action: "history", name: "home-autogen-dmi" } },
    { id: "call_h21", tool: "SkillManage", args: { action: "view", name: "home-autogen-dmi" } },
  ],
  // xAI tools (advertised only with credentials, so run with --seed-home
  // carrying a legacy ~/.swarmos/xai_oauth.json): every pre-network
  // validation path of x_search / xai_web_search. Live calls are not probed
  // (api.x.ai is hard-coded and would need a real token).
  xai: [
    { id: "call_x1", tool: "TaskManage", args: { operations: [{ key: "a", op: "create", subject: "xai probe", status: "in_progress", active: true }] } },
    { id: "call_x2", tool: "x_search", args: { query: "   " } },
    { id: "call_x3", tool: "x_search", args: { query: "q", allowed_x_handles: ["@a", " ", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k"] } },
    { id: "call_x4", tool: "x_search", args: { query: "q", allowed_x_handles: ["@a"], excluded_x_handles: ["b"] } },
    { id: "call_x5", tool: "x_search", args: { query: "q", allowed_x_handles: [" ", "@"], excluded_x_handles: ["a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k"] } },
    { id: "call_x6", tool: "xai_web_search", args: {} },
    // Lift the 5-call onboarding budget so the web_search paths run too.
    { id: "call_x6b", tool: "Skill", args: { skill: "swarm-skill" } },
    { id: "call_x7", tool: "xai_web_search", args: { query: "q", allowed_domains: ["a.com"], excluded_domains: ["b.com"] } },
    { id: "call_x8", tool: "xai_web_search", args: { query: "q", allowed_domains: ["1", "2", "3", "4", "5", "6"] } },
    { id: "call_x9", tool: "xai_web_search", args: { query: "q", excluded_domains: [" ", "1", "2", "3", "4", "5", "6"] } },
    { id: "call_x10", tool: "xai_web_search", args: { query: "q", allowed_domains: [" "], excluded_domains: ["1", "2", "3", "4", "5", "6"] } },
  ],
  // tool call, reasoning_content, two tool calls in one assistant message,
  // an unknown tool name, an image Read (vision content in a tool result),
  // and the remaining tools' happy/error paths.
  shapes: [
    { id: "call_s1", text: "Let me look.", command: "printf one" },
    { id: "call_s2", reasoning: "thinking about it", command: "printf two" },
    { id: "call_s3", calls: [{ id: "call_s3a", tool: "bash", args: { command: "printf a" } }, { id: "call_s3b", tool: "bash", args: { command: "printf b" } }] },
    { id: "call_s4", tool: "nonexistent_tool", args: { x: 1 } },
    { id: "call_s5", command: "printf '\\x89PNG\\r\\n\\x1a\\n\\0\\0\\0\\rIHDR\\0\\0\\0\\x01\\0\\0\\0\\x01\\x08\\x06\\0\\0\\0\\x1f\\x15\\xc4\\x89\\0\\0\\0\\rIDATx\\x9cc\\xf8\\x0f\\0\\x01\\x01\\x01\\0\\x18\\xdd\\x8d\\xb4\\0\\0\\0\\0IEND\\xaeB\\x60\\x82' > .parity-probe.png" },
    { id: "call_s6", tool: "Read", args: { file_path: ".parity-probe.png" } },
    { id: "call_s7", command: "rm -f .parity-probe.png" },
    { id: "call_s8", tool: "CronCreate", args: { prompt: "parity tick", cron: "*/5 * * * *", recurring: true } },
    { id: "call_s9", tool: "CronList", args: {} },
    { id: "call_s10", tool: "CronDelete", args: { id: "nope" } },
    { id: "call_s11", tool: "ScheduleWakeup", args: { prompt: "later", delay: "5m" } },
    { id: "call_s12", tool: "ScheduleWakeup", args: { prompt: "later", delay: "bogus" } },
    { id: "call_s13", tool: "HistoryGet", args: { conversation_id: "does-not-exist" } },
    { id: "call_s14", tool: "vault_exec", args: { credentialId: "nope", command: "true" } },
    { id: "call_s15", tool: "vault_add", args: { id: "k", kind: "api_key", secret: "s" } },
    { id: "call_s16", tool: "apply_patch", args: { input: "*** Begin Patch\n*** Add File: .parity-mv.txt\n+x\n*** End Patch" } },
    { id: "call_s17", tool: "apply_patch", args: { input: "*** Begin Patch\n*** Update File: .parity-mv.txt\n*** Move to: .parity-mv2.txt\n@@\n-x\n+y\n*** End Patch" } },
    { id: "call_s18", tool: "Undo", args: { path: ".parity-mv2.txt" } },
    { id: "call_s19", command: "rm -f .parity-mv.txt .parity-mv2.txt" },
    { id: "call_s20", tool: "annoyed", args: { issue: "parity probe issue", category: "other", severity: "low", observed: "o", expected: "e" } },
    { id: "call_s21", tool: "SkillManage", args: { action: "history", name: "does-not-exist" } },
  ],
};
export const expectedPrimaryRequests = (scenario = "default") => TOOL_SCRIPTS[scenario].length + 1;
export const EXPECTED_PRIMARY_REQUESTS = expectedPrimaryRequests("default");
const CLEAN_SYSTEM_PROMPT = "You are a parity capture agent.";

function stableObject(value) {
  if (Array.isArray(value)) return value.map(stableObject);
  if (!value || typeof value !== "object") return value;
  return Object.fromEntries(
    Object.keys(value).sort().map(key => [key, stableObject(value[key])]),
  );
}

/**
 * ReadBackgroundCommand JSON (swarm-tui/internal/bgprocess): timestamps,
 * pids and durations are wall-clock, and `list` ranges a Go map, so its
 * entry order is random by construction. `escaped` selects the JSON-escaped
 * form used by the raw wire fingerprint (quotes as \" and newlines as \n).
 */
export function bgprocessMasks(escaped) {
  // Literal (text) and regex-source forms of the quote and newline tokens.
  const Q = escaped ? '\\"' : '"', NL = escaped ? "\\n" : "\n";
  const q = escaped ? '\\\\"' : '"', nl = escaped ? "\\\\n" : "\\n";
  const notQuote = escaped ? "\\\\" : '"';
  const fields = new RegExp(`${q}(timestamp|started_at|ended_at|last_output_at)${q}: ?${q}\\d{4}-\\d\\d-\\d\\d[T ][^${notQuote}]*${q}|${q}(duration_seconds|seconds_since_last_output|pid)${q}: ?-?\\d+(?:\\.\\d+)?(?:e[+-]?\\d+)?`, "g");
  const maskField = (_match, ts, num) => ts ? `${Q}${ts}${Q}: ${Q}<ts>${Q}` : `${Q}${num}${Q}: <n>`;
  const list = new RegExp(`(\\{${nl}  ${q}count${q}: \\d+,${nl}  ${q}processes${q}: \\[${nl}    \\{)([\\s\\S]*?)(${nl}    \\}${nl}  \\]${nl}\\})`, "g");
  const sortList = (_match, head, body, tail) => {
    const separator = `${NL}    },${NL}    {`;
    const entries = body.split(separator);
    const key = entry => (entry.match(new RegExp(`${q}(command|started_at)${q}: ?${q}[^${notQuote}]*${q}`, "g")) ?? []).join("|");
    entries.sort((a, b) => key(a) < key(b) ? -1 : key(a) > key(b) ? 1 : 0);
    return head + entries.join(separator) + tail;
  };
  return { fields, maskField, list, sortList };
}

export function canonicalizeRequest(request) {
  const bg = bgprocessMasks(false);
  const ids = new Map();
  let nextId = 1;
  const generatedId = value => {
    if (!ids.has(value)) ids.set(value, `<tool-call-${nextId++}>`);
    return ids.get(value);
  };
  // Autogen revision ids hash a manifest that embeds created_at (history.go
  // capture → time.Now()), so they are per-run. Number them in order of
  // first appearance across the request so reuse (view returning the create
  // revision, the model echoing it back as expected_revision) still has to
  // agree on both sides.
  const revisions = new Map();
  const revisionId = value => value.replace(/\b[0-9a-f]{64}\b/g, match => {
    if (!revisions.has(match)) revisions.set(match, `<rev-${revisions.size + 1}>`);
    return revisions.get(match);
  });
  const visit = (value, parentKey = "") => {
    if (Array.isArray(value)) return value.map(item => visit(item, parentKey));
    if (!value || typeof value !== "object") {
      if (GENERATED_ID_KEYS.has(parentKey) && typeof value === "string") return generatedId(value);
      // Echoed tool-call arguments carry revision ids and, for orchestration
      // follow-ups ($LAST_AGENT), the per-run UnixNano-suffixed agent ids.
      if (typeof value === "string" && parentKey === "arguments") return revisionId(value).replace(/\b([A-Za-z][A-Za-z0-9_-]*?)-\d{16,20}\b/g, "$1-<nanos>");
      // Swarm-format tool results carry wall-clock timing and random error
      // ids; both runtimes emit the same shape, so normalise the values.
      if (typeof value === "string" && parentKey === "content") {
        return revisionId(value)
          .replace(/ duration_ms="\d+"/g, ' duration_ms="<ms>"')
          .replace(/\(error_id=err_[0-9a-f]+\)/g, "(error_id=<id>)")
          // annoyance-nudge fingerprints hash the failure text, which
          // contains the random error_id above, so they are per-run too.
          .replace(/(Fingerprint: ")[0-9a-f]{32}(")/g, "$1<fingerprint>$2")
          // Task timestamps (RFC3339Nano) and bash spill files are per-run.
          .replace(/"(created_at|updated_at|completed_at)": ?"\d{4}-\d\d-\d\dT[^"]+"/g, '"$1":"<ts>"')
          .replace(/bash-full-\d+\.txt/g, "bash-full-<rand>.txt")
          .replace(/\b([A-Za-z][A-Za-z0-9_-]*?)-\d{16,20}\b/g, "$1-<nanos>")
          // bgprocess handles are bg-<UnixNano>-<pid>; the pid is per-side.
          .replace(/\bbg-<nanos>-\d+\b/g, "bg-<nanos>-<pid>")
          // bgprocess status/output/list JSON: wall-clock fields, pids, and
          // Go map-ordered `list` entries (manager.go List ranges a map).
          .replace(bg.list, bg.sortList)
          .replace(bg.fields, bg.maskField)
          // Orchestration results carry wall-clock durations/start times.
          .replace(/"(duration|start_time)": ?"[^"]*"/g, '"$1":"<t>"')
          .replace(/^(duration:|elapsed:)\s+\S+$/gm, "$1 <t>")
          .replace(/"fire_time": ?"[^"]+"/g, '"fire_time":"<ts>"').replace(/\(at \d\d:\d\d:\d\d\)/g, "(at <clock>)")
          .replace(/conversation: \d{8}-\d{6}-[a-z0-9]{6}/g, "conversation: <id>")
          .replace(/ \| at: \d{4}-\d\d-\d\dT[^ ]+Z/g, " | at: <ts>")
          .replace(/annoyed: GitHub API POST [^\n]*/g, "annoyed: GitHub API POST <gh>")
          // Each side runs under its own scratch HOME; paths under it that
          // leak into tool output (e.g. autogen dir errors) are per-side.
          .replace(/\/tmp\/pi-swarm-parity-[A-Za-z0-9]+\/(?:pi|swarm)-home/g, "<home>");
      }
      return value;
    }
    const output = {};
    for (const key of Object.keys(value).sort()) {
      const child = value[key];
      if (key === "id" && parentKey === "tool_calls" && typeof child === "string") {
        output[key] = generatedId(child);
      } else {
        output[key] = visit(child, key);
      }
    }
    return output;
  };
  return visit(request);
}

function escapePointer(value) {
  return String(value).replaceAll("~", "~0").replaceAll("/", "~1");
}

/**
 * Order-preserving wire fingerprint. `canonicalizeRequest` sorts keys so the
 * structural diff is stable; this keeps the bytes exactly as sent and masks
 * only values that are random per run (tool-call ids, error ids, timings).
 * Two runtimes that are "the same JSON" must agree here as well.
 */
export function wireFingerprint(rawText) {
  const ids = new Map();
  let next = 1;
  return rawText
    // Go encoding/json HTML-escapes <, >, & by default; JSON-equivalent to
    // the literal characters Node emits, so fold them before byte comparison.
    .replace(/\\u003c/g, "<").replace(/\\u003e/g, ">").replace(/\\u0026/g, "&")
    .replace(/"(?:call_|toolu_)[A-Za-z0-9_-]+"/g, match => {
      if (!ids.has(match)) ids.set(match, `"<tool-call-${next++}>"`);
      return ids.get(match);
    })
    .replace(/ duration_ms=\\"\d+\\"/g, ' duration_ms=\\"<ms>\\"')
    .replace(/\(error_id=err_[0-9a-f]+\)/g, "(error_id=<id>)")
    .replace(/(Fingerprint: \\")[0-9a-f]{32}(\\")/g, "$1<fingerprint>$2")
    .replace(/\/tmp\/pi-swarm-parity-[A-Za-z0-9]+\/(?:pi|swarm)-home/g, "<home>")
    .replace(/\\"(created_at|updated_at|completed_at)\\": ?\\"\d{4}-\d\d-\d\dT[^\\"]+\\"/g, '\\"$1\\":\\"<ts>\\"')
    .replace(/\b[0-9a-f]{64}\b/g, match => {
      if (!ids.has(match)) ids.set(match, `<rev-${next++}>`);
      return ids.get(match);
    })
    .replace(/bash-full-\d+\.txt/g, "bash-full-<rand>.txt")
    .replace(/\b([A-Za-z][A-Za-z0-9_-]*?)-\d{16,20}\b/g, "$1-<nanos>")
    .replace(/\bbg-<nanos>-\d+\b/g, "bg-<nanos>-<pid>")
    .replace(bgprocessMasks(true).list, bgprocessMasks(true).sortList)
    .replace(bgprocessMasks(true).fields, bgprocessMasks(true).maskField)
    .replace(/\\"(duration|start_time)\\": ?\\"[^\\"]*\\"/g, '\\"$1\\":\\"<t>\\"')
    .replace(/(\\n)(duration:|elapsed:)\s+[^\\]+?(?=\\n)/g, "$1$2 <t>")
    .replace(/\\"fire_time\\": ?\\"[^\\"]+\\"/g, '\\"fire_time\\":\\"<ts>\\"').replace(/\(at \d\d:\d\d:\d\d\)/g, "(at <clock>)")
    .replace(/conversation: \d{8}-\d{6}-[a-z0-9]{6}/g, "conversation: <id>")
    .replace(/ \| at: \d{4}-\d\d-\d\dT[^ \\]+Z/g, " | at: <ts>")
    // gh's stderr depends on the host's auth state; both sides run the same
    // gh binary, but the sanitized detail may differ in whitespace.
    .replace(/annoyed: GitHub API POST [^"]*?(?= \(error_id=)/g, "annoyed: GitHub API POST <gh>");
}

function compareFingerprints(left, right) {
  const requests = [];
  for (let index = 0; index < Math.max(left.length, right.length); index++) {
    const a = left[index], b = right[index];
    if (a === undefined || b === undefined) { requests.push({ index, identical: false, reason: "missing on one side" }); continue; }
    if (a === b) { requests.push({ index, identical: true, bytes: Buffer.byteLength(a) }); continue; }
    let offset = 0;
    while (offset < a.length && a[offset] === b[offset]) offset++;
    requests.push({ index, identical: false, offset, pi: a.slice(Math.max(0, offset - 60), offset + 120), swarm: b.slice(Math.max(0, offset - 60), offset + 120) });
  }
  return requests;
}

/**
 * Primary requests (the scripted conversation) are compared in order;
 * auxiliary requests (conversation-metadata calls, sub-agent conversations)
 * are compared in arrival order too — every byte that crosses the model
 * boundary counts, not just the main loop.
 */
export function wireComparison(piRaw, swarmRaw, probePrompt = PROBE_PROMPT) {
  const split = raw => { const primary = [], auxiliary = []; for (const text of raw) (hasPrimaryPrompt(JSON.parse(text), probePrompt) ? primary : auxiliary).push(wireFingerprint(text)); return { primary, auxiliary }; };
  const left = split(piRaw), right = split(swarmRaw);
  const requests = compareFingerprints(left.primary, right.primary);
  const auxiliary = compareFingerprints(left.auxiliary, right.auxiliary);
  return { identical: requests.every(entry => entry.identical) && auxiliary.every(entry => entry.identical), requests, auxiliary };
}

export function diffJson(left, right, path = "") {
  if (Object.is(left, right)) return [];
  if (Array.isArray(left) && Array.isArray(right)) {
    const differences = [];
    const length = Math.max(left.length, right.length);
    for (let index = 0; index < length; index++) {
      const pointer = `${path}/${index}`;
      if (index >= left.length) differences.push({ path: pointer, kind: "missing-left", right: right[index] });
      else if (index >= right.length) differences.push({ path: pointer, kind: "missing-right", left: left[index] });
      else differences.push(...diffJson(left[index], right[index], pointer));
    }
    return differences;
  }
  if (left && right && typeof left === "object" && typeof right === "object" && !Array.isArray(left) && !Array.isArray(right)) {
    const differences = [];
    const keys = [...new Set([...Object.keys(left), ...Object.keys(right)])].sort();
    for (const key of keys) {
      const pointer = `${path}/${escapePointer(key)}`;
      if (!(key in left)) differences.push({ path: pointer, kind: "missing-left", right: right[key] });
      else if (!(key in right)) differences.push({ path: pointer, kind: "missing-right", left: left[key] });
      else differences.push(...diffJson(left[key], right[key], pointer));
    }
    return differences;
  }
  return [{ path: path || "/", kind: "changed", left, right }];
}

export function summarizeMismatches(mismatches) {
  const categories = {};
  for (const mismatch of mismatches) {
    const segments = mismatch.path.split("/").filter(Boolean);
    if (/^\d+$/.test(segments[0] ?? "")) segments.shift();
    const category = segments[0] ?? "root";
    categories[category] = (categories[category] ?? 0) + 1;
  }
  return Object.fromEntries(Object.entries(categories).sort((left, right) =>
    right[1] - left[1] || left[0].localeCompare(right[0]),
  ));
}

function sha256(value) {
  return createHash("sha256").update(value).digest("hex");
}

export function hasPrimaryPrompt(request, prompt) {
  return (request.messages ?? []).some(message => {
    if (message?.role !== "user") return false;
    // "PARITY_CAPTURE", "PARITY_CAPTURE\n<context>" (headless) or "PARITY_CAPTURE <step>" (tui-probe).
    const primary = text => text === prompt || text.startsWith(`${prompt}\n`) || text.startsWith(`${prompt} `);
    if (typeof message.content === "string") return primary(message.content);
    return Array.isArray(message.content) && message.content.some(block => block?.type === "text" && typeof block.text === "string" && primary(block.text));
  });
}

export function primarySystemPrompt(requests, probePrompt = PROBE_PROMPT) {
  const initial = requests
    .map(canonicalizeRequest)
    .find(request => hasPrimaryPrompt(request, probePrompt));
  return (initial?.messages ?? [])
    .filter(message => message?.role === "system" || message?.role === "developer")
    .map(message => typeof message.content === "string" ? message.content : JSON.stringify(message.content))
    .join("\n");
}

export function sharedPromptPrefix(prompt) {
  let candidate = prompt.trimStart();
  while (candidate.startsWith("<available_skills>")) {
    const end = candidate.indexOf("</available_skills>");
    if (end < 0) break;
    candidate = candidate.slice(end + "</available_skills>".length).trimStart();
  }
  candidate = candidate
    .replace(/^When a user request matches an available skill,[^\n]*\n*/u, "")
    .trimStart();
  const offsets = ["\n\n<context_file ", "\n\n<available_skills>", "\n\n<swarmos_cached_context>", "\n\n<swarmos_context>"]
    .map(marker => candidate.indexOf(marker))
    .filter(offset => offset >= 0);
  return offsets.length ? candidate.slice(0, Math.min(...offsets)) : candidate;
}

export function promptSliceEvidence(prompt, sharedPrompt) {
  const offset = sharedPrompt ? prompt.indexOf(sharedPrompt) : -1;
  return {
    offset,
    present: offset >= 0,
    exact: offset >= 0 && prompt.slice(offset, offset + sharedPrompt.length) === sharedPrompt,
  };
}

export function requestArtifacts(requests, probePrompt) {
  const all = requests.map(canonicalizeRequest);
  const canonical = all.filter(request => hasPrimaryPrompt(request, probePrompt));
  const auxiliaryRequests = all.filter(request => !hasPrimaryPrompt(request, probePrompt));
  const initial = canonical[0] ?? {};
  const messages = canonical.flatMap(request => Array.isArray(request.messages) ? [request.messages] : []);
  const promptMessages = (initial.messages ?? []).filter(message => message?.role === "system" || message?.role === "developer");
  const prompt = promptMessages.map(message => typeof message.content === "string" ? message.content : JSON.stringify(message.content)).join("\n");
  const skillBlocks = [...prompt.matchAll(/<available_skills>[\s\S]*?<\/available_skills>/g)].map(match => match[0]);
  const actions = canonical.flatMap(request => (request.messages ?? []).filter(message =>
    message?.role === "tool" || Array.isArray(message?.tool_calls),
  ));
  return {
    requests: canonical,
    auxiliaryRequests,
    prompt: { bytes: Buffer.byteLength(prompt), sha256: sha256(prompt), text: prompt },
    tools: initial.tools ?? [],
    skills: skillBlocks.map(text => ({ bytes: Buffer.byteLength(text), sha256: sha256(text), text })),
    messages,
    actions,
  };
}

/**
 * Swarm's conversation title/summary call (client/conversation_metadata.go)
 * is a NON-streaming provider.Chat. Answer it with a chat.completion JSON
 * body carrying valid metadata so the generation path is exercised once per
 * turn (a non-JSON reply would make both runtimes retry once).
 */
export const METADATA_REPLY = JSON.stringify({ title: "Parity Capture Probe", summary: "The user sent a parity capture prompt. The agent ran the scripted tool calls and replied." });
export function respondNonStreaming(res, body, content = METADATA_REPLY) {
  res.writeHead(200, { "content-type": "application/json", connection: "close" });
  res.end(JSON.stringify({ id: "chatcmpl-parity", object: "chat.completion", created: 1, model: body.model, choices: [{ index: 0, message: { role: "assistant", content }, finish_reason: "stop" }], usage: { prompt_tokens: 1, completion_tokens: 1, total_tokens: 2 } }));
}

export function openAIChunk(model, delta, finishReason = null) {
  return {
    id: "chatcmpl-parity",
    object: "chat.completion.chunk",
    created: 1,
    model,
    choices: [{ index: 0, delta, finish_reason: finishReason }],
  };
}

export function substituteRevisions(args, messages) {
  let text = JSON.stringify(args);
  if (!text.includes("$LAST_REVISION") && !text.includes("$REVISION:") && !text.includes("$BASELINE:") && !text.includes("$LAST_AGENT")) return args;
  const byCall = new Map();
  const byName = new Map();
  const baseline = new Map();
  let last = "";
  for (const message of messages) {
    if (Array.isArray(message?.tool_calls)) for (const call of message.tool_calls) {
      try { const parsed = JSON.parse(call.function?.arguments ?? "{}"); byCall.set(call.id, { name: parsed.name ?? "", action: parsed.action ?? "" }); } catch { /* not JSON */ }
    }
    if (message?.role !== "tool" || typeof message.content !== "string") continue;
    const hexes = message.content.match(/\b[0-9a-f]{64}\b/g);
    if (!hexes) continue;
    const meta = byCall.get(message.tool_call_id) ?? { name: "", action: "" };
    const revision = meta.action === "history" ? hexes[0] : hexes[hexes.length - 1];
    last = revision;
    if (meta.name) byName.set(meta.name, revision);
    // "$BASELINE:<skill>": the oldest id in that skill's latest history listing.
    if (meta.name && meta.action === "history") baseline.set(meta.name, hexes[hexes.length - 1]);
  }
  // "$LAST_AGENT": the agent_id reported by the most recent orchestration result.
  let lastAgent = "";
  for (const message of messages) {
    if (message?.role !== "tool" || typeof message.content !== "string") continue;
    const found = message.content.match(/"agent_id":\s*"([^"]+)"|agent_id[=:]\s*"?([A-Za-z0-9_.-]+)/g);
    if (found) { const m = found[found.length - 1].match(/"agent_id":\s*"([^"]+)"|agent_id[=:]\s*"?([A-Za-z0-9_.-]+)/); lastAgent = m[1] ?? m[2]; }
  }
  text = text.replace(/\$LAST_AGENT/g, lastAgent || "$LAST_AGENT");
  return JSON.parse(text.replace(/\$REVISION:([a-z0-9-]+)/g, (match, name) => byName.get(name) ?? match).replace(/\$BASELINE:([a-z0-9-]+)/g, (match, name) => baseline.get(name) ?? match).replace(/\$LAST_REVISION/g, last || "$LAST_REVISION"));
}

async function startRecorder(script = TOOL_SCRIPTS.default) {
  const requests = [];
  const rawRequests = [];
  const server = createServer(async (req, res) => {
    if (req.method !== "POST") {
      res.writeHead(404).end();
      return;
    }
    const chunks = [];
    for await (const chunk of req) chunks.push(chunk);
    const rawText = Buffer.concat(chunks).toString("utf8");
    const body = JSON.parse(rawText);
    requests.push(body);
    rawRequests.push(rawText);
    if (!body.stream) { respondNonStreaming(res, body); return; }
    const hasToolResult = Array.isArray(body.messages) && body.messages.some(message => message?.role === "tool");
    // Sub-agents spawned by the orchestration tools share this model: answer
    // them with a fixed sentence and no tool calls so the primary script's
    // step indexing is unaffected. Their requests are kept in the raw log
    // but excluded from the comparison (hasPrimaryPrompt).
    if (!hasPrimaryPrompt(body, PROBE_PROMPT)) {
      res.writeHead(200, { "content-type": "text/event-stream", "cache-control": "no-cache", connection: "close" });
      res.write(`data: ${JSON.stringify(openAIChunk(body.model, { role: "assistant", content: "SUBAGENT_OK" }))}\n\n`);
      res.write(`data: ${JSON.stringify(openAIChunk(body.model, {}, "stop"))}\n\n`);
      res.end("data: [DONE]\n\n");
      return;
    }
    res.writeHead(200, {
      "content-type": "text/event-stream",
      "cache-control": "no-cache",
      connection: "close",
    });
    const emit = value => res.write(`data: ${JSON.stringify(value)}\n\n`);
    // Scripted model: turn 1 runs a succeeding command, turn 2 a failing one
    // (non-zero exit with stderr), turn 3 answers. Each follow-up request then
    // carries the success envelope and the "Error executing bash" envelope
    // respectively, so both result shapes are compared on the wire.
    const toolResults = Array.isArray(body.messages) ? body.messages.filter(message => message?.role === "tool").length : 0;
    // Steps are indexed by completed tool CALLS (a parallel step consumes
    // one index per call) so multi-call steps line up on both runtimes.
    let consumed = 0, step;
    for (const candidate of script) { if (consumed >= toolResults) { step = candidate; break; } consumed += candidate.calls?.length ?? 1; }
    let calls = step ? (step.calls ?? [{ id: step.id, tool: step.tool ?? "bash", args: step.args ?? { command: step.command, ...(step.cwd ? { cwd: step.cwd } : {}), ...(step.description ? { description: step.description } : {}), ...(step.timeout_seconds ? { timeout_seconds: step.timeout_seconds } : {}) } }]) : [];
    // Scripted SkillManage mutations need the revision hash the runtime
    // reported earlier in THIS conversation (per-run, per-side). Resolve
    // "$LAST_REVISION" and "$REVISION:<skill>" from prior tool results: the
    // newest 64-hex token in a result (history lists newest first, so its
    // first token) keyed by the call's `name` argument.
    calls = calls.map(call => ({ ...call, args: substituteRevisions(call.args, body.messages ?? []) }));
    if (step && consumed === toolResults && (body.tools ?? []).length > 0) {
      if (step.reasoning) emit(openAIChunk(body.model, { role: "assistant", reasoning_content: step.reasoning }));
      if (step.text) emit(openAIChunk(body.model, { role: "assistant", content: step.text }));
      emit(openAIChunk(body.model, {
        role: "assistant",
        tool_calls: calls.map((call, index) => ({ index, id: call.id, type: "function", function: { name: call.tool, arguments: JSON.stringify(call.args) } })),
      }));
      emit(openAIChunk(body.model, {}, "tool_calls"));
    } else {
      emit(openAIChunk(body.model, { role: "assistant", content: "PARITY_OK" }));
      emit(openAIChunk(body.model, {}, "stop"));
    }
    res.end("data: [DONE]\n\n");
  });
  await new Promise((resolvePromise, reject) => {
    server.once("error", reject);
    server.listen(0, "127.0.0.1", resolvePromise);
  });
  const address = server.address();
  if (!address || typeof address === "string") throw new Error("recorder did not bind a TCP port");
  return {
    baseUrl: `http://127.0.0.1:${address.port}/v1`,
    requests,
    rawRequests,
    close: () => new Promise((resolvePromise, reject) => server.close(error => error ? reject(error) : resolvePromise())),
  };
}


/** Pi's discoverExtensionsInDir: top-level *.ts/*.js files plus subdirectories with an index.ts/js. */
export async function discoverExtensionEntries(dir) {
  const entries = [];
  for (const item of (await readdir(dir, { withFileTypes: true })).sort((a, b) => a.name.localeCompare(b.name))) {
    if (item.name.startsWith(".") || item.name.startsWith("_")) continue;
    const path = join(dir, item.name);
    if (item.isFile() && /\.(ts|js)$/.test(item.name) && !/\.(test|spec)\.(ts|js)$/.test(item.name)) entries.push(path);
    else if (item.isDirectory()) for (const index of ["index.ts", "index.js"]) if (existsSync(join(path, index))) { entries.push(join(path, index)); break; }
  }
  return entries;
}

async function run(command, args, options) {
  return new Promise((resolvePromise, reject) => {
    const child = spawn(command, args, {
      cwd: options.cwd,
      env: { ...process.env, ...options.env },
      stdio: ["ignore", "pipe", "pipe"],
    });
    const stdout = [];
    const stderr = [];
    child.stdout.on("data", chunk => stdout.push(chunk));
    child.stderr.on("data", chunk => stderr.push(chunk));
    child.once("error", reject);
    child.once("exit", code => {
      const result = {
        code,
        stdout: Buffer.concat(stdout).toString("utf8"),
        stderr: Buffer.concat(stderr).toString("utf8"),
      };
      if (code === 0) resolvePromise(result);
      else reject(new Error(`${command} exited ${code}\n${result.stderr.slice(-4000)}`));
    });
  });
}

// Optional HOME seed: a directory tree copied into BOTH scratch HOMEs before
// launch (user-level CLAUDE.md/SWARM.md, ~/.claude/skills, ~/.swarmos/skills,
// OAuth stubs…), so user-scoped discovery can be compared too.
async function seedHome(home, seed) {
  if (!seed) return;
  const { cp } = await import("node:fs/promises");
  await mkdir(home, { recursive: true });
  await cp(seed, home, { recursive: true });
}

async function capturePi(workspace, scratch, profile, script, maxTurns = 0, seed) {
  const recorder = await startRecorder(script);
  try {
    const home = join(scratch, "pi-home");
    await seedHome(home, seed);
    const agentDir = join(home, ".pi", "agent");
    await mkdir(agentDir, { recursive: true });
    await writeFile(join(agentDir, "models.json"), JSON.stringify({
      providers: {
        parity: {
          baseUrl: recorder.baseUrl,
          api: "openai-completions",
          apiKey: "parity-dummy-key",
          models: [{
            id: "parity-model",
            name: "Parity Capture Model",
            reasoning: false,
            contextWindow: 1000000,
            maxTokens: 4096,
          }],
        },
      },
    }, null, 2));
    const args = [
      "--provider", "parity",
      "--model", "parity-model",
      "--no-session",
      "--mode", "json",
      "--print",
    ];
    if (profile === "clean") {
      // Swarm --clean-agent = no project memory, no skills, no hooks, explicit
      // prompt. Pi-Swarm's extensions must stay loaded (they ARE the port);
      // the equivalent isolation is expressed through Pi's own flags.
      args.push(
        "--no-context-files",
        "--no-skills",
        "--system-prompt", CLEAN_SYSTEM_PROMPT,
        "--tools", "bash",
      );
    }
    // Trust the workspace so its .pi/extensions load (both profiles).
    args.push("--approve");
    // Pi discovers extensions from <workspace>/.pi/extensions and <agentDir>/
    // extensions only. When probing a workspace that is not this repository,
    // load the port explicitly (Pi does not realpath symlinked directories, so
    // a linked .pi/extensions would break the extensions' relative imports).
    if (resolve(workspace) !== resolve(PI_SWARM_EXTENSIONS, "..", "..")) {
      for (const entry of await discoverExtensionEntries(PI_SWARM_EXTENSIONS)) args.push("--extension", entry);
    }
    // --clean-agent also means --no-hooks on the Swarm side; Pi-Swarm's hook
    // groups read PI_SWARM_NO_HOOKS (see .pi/hook-state.ts).
    const hookEnv = { ...(profile === "clean" ? { PI_SWARM_NO_HOOKS: "1" } : {}), ...(maxTurns > 0 ? { PI_SWARM_MAX_TURNS: String(maxTurns) } : {}) };
    args.push(PROBE_PROMPT);
    const processResult = await run("pi", args, {
      cwd: workspace,
      env: {
        HOME: home,
        ...hookEnv,
        PI_CODING_AGENT_DIR: agentDir,
        PI_OFFLINE: "1",
        XDG_CONFIG_HOME: join(home, ".config"),
        XDG_DATA_HOME: join(home, ".local", "share"),
        XDG_STATE_HOME: join(home, ".local", "state"),
      },
    });
    return { requests: recorder.requests, rawRequests: recorder.rawRequests, process: processResult };
  } finally {
    await recorder.close();
  }
}

async function captureSwarm(workspace, scratch, profile, script, maxTurns = 0, seed) {
  const recorder = await startRecorder(script);
  try {
    const home = join(scratch, "swarm-home");
    await mkdir(home, { recursive: true });
    await seedHome(home, seed);
    const args = [
      "--no-update",
      "--approval-mode", "auto",
      "--api-type", "openai",
      "--base-url", recorder.baseUrl,
      "--api-key-env", "PARITY_API_KEY",
      "-m", "parity-model",
      "--max-tokens", "4096",
      // Swarm -p default is 0 (unlimited); the probe passes the same limit to
      // both sides (Pi via PI_SWARM_MAX_TURNS) so the turn-limit path can be
      // compared without introducing an asymmetry.
      "--max-turns", String(maxTurns),
      "--workspace", workspace,
      "--output-format", "stream-json",
    ];
    if (profile === "clean") {
      args.push(
        "--clean-agent",
        "--system-prompt", CLEAN_SYSTEM_PROMPT,
        "--tools", "bash",
      );
    }
    // The project profile runs Swarm's NATIVE base prompt (settings/system_prompt.go
    // SwarmForge + RenderWorkspaceContext): no --system-prompt-file, so the
    // prompt bytes are compared, not copied from Pi.
    args.push("-p", PROBE_PROMPT);
    const processResult = await run(process.env.PARITY_SWARM_BIN || "swarm", args, {
      cwd: workspace,
      env: {
        HOME: home,
        PARITY_API_KEY: "parity-dummy-key",
        XDG_CONFIG_HOME: join(home, ".config"),
        XDG_DATA_HOME: join(home, ".local", "share"),
        XDG_STATE_HOME: join(home, ".local", "state"),
      },
    });
    return { requests: recorder.requests, rawRequests: recorder.rawRequests, process: processResult };
  } finally {
    await recorder.close();
  }
}

export async function captureParity(options = {}) {
  const workspace = resolve(options.workspace ?? process.cwd());
  const output = resolve(options.output ?? join(workspace, "artifacts", "parity", "default"));
  const profile = options.profile ?? "clean";
  const scenario = options.scenario ?? "default";
  if (!TOOL_SCRIPTS[scenario]) throw new Error(`unknown parity scenario: ${scenario}`);
  const script = TOOL_SCRIPTS[scenario];
  const maxTurns = Number(options.maxTurns ?? 0) || 0;
  const seed = options.seedHome ? resolve(options.seedHome) : undefined;
  if (profile !== "clean" && profile !== "project") {
    throw new Error(`unknown parity profile: ${profile}`);
  }
  const scratch = await mkdtemp(join(tmpdir(), "pi-swarm-parity-"));
  try {
    const pi = await capturePi(workspace, scratch, profile, script, maxTurns, seed);
    const piArtifacts = requestArtifacts(pi.requests, PROBE_PROMPT);
    const swarm = await captureSwarm(workspace, scratch, profile, script, maxTurns, seed);
    const swarmArtifacts = requestArtifacts(swarm.requests, PROBE_PROMPT);
    // Evidence that the base prompt (skills catalogue and context blocks
    // aside) is the same on both sides, independent of the request diff.
    const projectSystemPrompt = profile === "project" ? sharedPromptPrefix(primarySystemPrompt(pi.requests)) : undefined;
    const sharedPrompt = projectSystemPrompt ? {
      bytes: Buffer.byteLength(projectSystemPrompt),
      sha256: sha256(projectSystemPrompt),
      pi: promptSliceEvidence(piArtifacts.prompt.text, projectSystemPrompt),
      swarm: promptSliceEvidence(swarmArtifacts.prompt.text, projectSystemPrompt),
    } : undefined;
    const mismatches = [
      ...diffJson(piArtifacts.requests, swarmArtifacts.requests),
      ...diffJson(piArtifacts.auxiliaryRequests, swarmArtifacts.auxiliaryRequests).map(entry => ({ ...entry, path: `/auxiliary${entry.path}` })),
    ];
    const mismatchCategories = summarizeMismatches(mismatches);
    const wire = wireComparison(pi.rawRequests ?? [], swarm.rawRequests ?? [], PROBE_PROMPT);
    await mkdir(output, { recursive: true });
    const writeJson = (name, value) => writeFile(join(output, name), `${JSON.stringify(stableObject(value), null, 2)}\n`);
    await Promise.all([
      writeJson("pi.json", piArtifacts),
      writeJson("swarm.json", swarmArtifacts),
      writeJson("wire.json", wire),
      writeFile(join(output, "pi.raw.ndjson"), `${(pi.rawRequests ?? []).join("\n")}\n`),
      writeFile(join(output, "swarm.raw.ndjson"), `${(swarm.rawRequests ?? []).join("\n")}\n`),
      writeJson("prompt.json", { pi: piArtifacts.prompt, swarm: swarmArtifacts.prompt, shared: sharedPrompt }),
      writeJson("tools.json", { pi: piArtifacts.tools, swarm: swarmArtifacts.tools }),
      writeJson("skills.json", { pi: piArtifacts.skills, swarm: swarmArtifacts.skills }),
      writeJson("messages.json", { pi: piArtifacts.messages, swarm: swarmArtifacts.messages }),
      writeJson("actions.json", { pi: piArtifacts.actions, swarm: swarmArtifacts.actions }),
      writeJson("mismatches.json", {
        profile,
        sharedPrompt,
        count: mismatches.length,
        categories: mismatchCategories,
        mismatches,
        pi: {
          requestCount: piArtifacts.requests.length,
          auxiliaryRequestCount: piArtifacts.auxiliaryRequests.length,
          prompt: { bytes: piArtifacts.prompt.bytes, sha256: piArtifacts.prompt.sha256 },
          tools: piArtifacts.tools.length,
          skills: piArtifacts.skills.length,
        },
        swarm: {
          requestCount: swarmArtifacts.requests.length,
          auxiliaryRequestCount: swarmArtifacts.auxiliaryRequests.length,
          prompt: { bytes: swarmArtifacts.prompt.bytes, sha256: swarmArtifacts.prompt.sha256 },
          tools: swarmArtifacts.tools.length,
          skills: swarmArtifacts.skills.length,
        },
      }),
      writeFile(join(output, "pi.ndjson"), pi.process.stdout),
      writeFile(join(output, "swarm.ndjson"), swarm.process.stdout),
    ]);
    return { profile, scenario, maxTurns, output, mismatches, mismatchCategories, wire, pi: piArtifacts, swarm: swarmArtifacts };
  } finally {
    await rm(scratch, { recursive: true, force: true });
  }
}

async function main() {
  const args = process.argv.slice(2);
  const valueAfter = flag => {
    const index = args.indexOf(flag);
    return index >= 0 ? args[index + 1] : undefined;
  };
  const result = await captureParity({
    workspace: valueAfter("--workspace"),
    output: valueAfter("--output"),
    profile: valueAfter("--profile"),
    scenario: valueAfter("--scenario"),
    maxTurns: valueAfter("--max-turns"),
    seedHome: valueAfter("--seed-home"),
  });
  process.stdout.write(`${JSON.stringify({
    profile: result.profile,
    scenario: result.scenario,
    maxTurns: result.maxTurns,
    output: result.output,
    mismatchCount: result.mismatches.length,
    mismatchCategories: result.mismatchCategories,
    wireIdentical: result.wire.identical,
    wireRequests: result.wire.requests,
    pi: {
      requests: result.pi.requests.length,
      auxiliaryRequests: result.pi.auxiliaryRequests.length,
      promptBytes: result.pi.prompt.bytes,
      promptSha256: result.pi.prompt.sha256,
      tools: result.pi.tools.length,
      skills: result.pi.skills.length,
    },
    swarm: {
      requests: result.swarm.requests.length,
      auxiliaryRequests: result.swarm.auxiliaryRequests.length,
      promptBytes: result.swarm.prompt.bytes,
      promptSha256: result.swarm.prompt.sha256,
      tools: result.swarm.tools.length,
      skills: result.swarm.skills.length,
    },
  }, null, 2)}\n`);
}

if (process.argv[1] && resolve(process.argv[1]) === resolve(new URL(import.meta.url).pathname)) {
  main().catch(error => {
    console.error(error instanceof Error ? error.stack : String(error));
    process.exitCode = 1;
  });
}
