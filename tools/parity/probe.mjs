#!/usr/bin/env node
import { createHash } from "node:crypto";
import { createServer } from "node:http";
import { mkdtemp, mkdir, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { spawn } from "node:child_process";

const GENERATED_ID_KEYS = new Set(["tool_call_id"]);
const PROBE_PROMPT = "PARITY_CAPTURE";
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
  // Message shapes the base scripts never exercise: assistant text next to a
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

export function canonicalizeRequest(request) {
  const ids = new Map();
  let nextId = 1;
  const generatedId = value => {
    if (!ids.has(value)) ids.set(value, `<tool-call-${nextId++}>`);
    return ids.get(value);
  };
  const visit = (value, parentKey = "") => {
    if (Array.isArray(value)) return value.map(item => visit(item, parentKey));
    if (!value || typeof value !== "object") {
      if (GENERATED_ID_KEYS.has(parentKey) && typeof value === "string") return generatedId(value);
      // Swarm-format tool results carry wall-clock timing and random error
      // ids; both runtimes emit the same shape, so normalise the values.
      if (typeof value === "string" && parentKey === "content") {
        return value
          .replace(/ duration_ms="\d+"/g, ' duration_ms="<ms>"')
          .replace(/\(error_id=err_[0-9a-f]+\)/g, "(error_id=<id>)")
          // annoyance-nudge fingerprints hash the failure text, which
          // contains the random error_id above, so they are per-run too.
          .replace(/(Fingerprint: ")[0-9a-f]{32}(")/g, "$1<fingerprint>$2")
          // Task timestamps (RFC3339Nano) and bash spill files are per-run.
          .replace(/"(created_at|updated_at|completed_at)":"\d{4}-\d\d-\d\dT[^"]+"/g, '"$1":"<ts>"')
          .replace(/bash-full-\d+\.txt/g, "bash-full-<rand>.txt")
          .replace(/\b(task|wakeup)-\d{16,20}\b/g, "$1-<nanos>")
          .replace(/"fire_time": ?"[^"]+"/g, '"fire_time":"<ts>"').replace(/\(at \d\d:\d\d:\d\d\)/g, "(at <clock>)")
          .replace(/conversation: \d{8}-\d{6}-[a-z0-9]{6}/g, "conversation: <id>")
          .replace(/ \| at: \d{4}-\d\d-\d\dT[^ ]+Z/g, " | at: <ts>")
          .replace(/annoyed: GitHub API POST [^\n]*/g, "annoyed: GitHub API POST <gh>");
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
    .replace(/\\"(created_at|updated_at|completed_at)\\":\\"\d{4}-\d\d-\d\dT[^\\"]+\\"/g, '\\"$1\\":\\"<ts>\\"')
    .replace(/bash-full-\d+\.txt/g, "bash-full-<rand>.txt")
    .replace(/\b(task|wakeup)-\d{16,20}\b/g, "$1-<nanos>")
    .replace(/\\"fire_time\\": ?\\"[^\\"]+\\"/g, '\\"fire_time\\":\\"<ts>\\"').replace(/\(at \d\d:\d\d:\d\d\)/g, "(at <clock>)")
    .replace(/conversation: \d{8}-\d{6}-[a-z0-9]{6}/g, "conversation: <id>")
    .replace(/ \| at: \d{4}-\d\d-\d\dT[^ \\]+Z/g, " | at: <ts>")
    // gh's stderr depends on the host's auth state; both sides run the same
    // gh binary, but the sanitized detail may differ in whitespace.
    .replace(/annoyed: GitHub API POST [^"]*?(?= \(error_id=)/g, "annoyed: GitHub API POST <gh>");
}

export function wireComparison(piRaw, swarmRaw, probePrompt = PROBE_PROMPT) {
  const primary = raw => raw.filter(text => hasPrimaryPrompt(JSON.parse(text), probePrompt));
  const left = primary(piRaw).map(wireFingerprint);
  const right = primary(swarmRaw).map(wireFingerprint);
  const requests = [];
  for (let index = 0; index < Math.max(left.length, right.length); index++) {
    const a = left[index], b = right[index];
    if (a === undefined || b === undefined) { requests.push({ index, identical: false, reason: "missing on one side" }); continue; }
    if (a === b) { requests.push({ index, identical: true, bytes: Buffer.byteLength(a) }); continue; }
    let offset = 0;
    while (offset < a.length && a[offset] === b[offset]) offset++;
    requests.push({ index, identical: false, offset, pi: a.slice(Math.max(0, offset - 60), offset + 120), swarm: b.slice(Math.max(0, offset - 60), offset + 120) });
  }
  return { identical: requests.every(entry => entry.identical), requests };
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

function hasPrimaryPrompt(request, prompt) {
  return (request.messages ?? []).some(message => {
    if (message?.role !== "user") return false;
    if (typeof message.content === "string") return message.content === prompt || message.content.startsWith(`${prompt}\n`);
    return Array.isArray(message.content) && message.content.some(block => block?.type === "text" && (block.text === prompt || block.text?.startsWith(`${prompt}\n`)));
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

function requestArtifacts(requests, probePrompt) {
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

function openAIChunk(model, delta, finishReason = null) {
  return {
    id: "chatcmpl-parity",
    object: "chat.completion.chunk",
    created: 1,
    model,
    choices: [{ index: 0, delta, finish_reason: finishReason }],
  };
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
    const hasToolResult = Array.isArray(body.messages) && body.messages.some(message => message?.role === "tool");
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
    const calls = step ? (step.calls ?? [{ id: step.id, tool: step.tool ?? "bash", args: step.args ?? { command: step.command, ...(step.cwd ? { cwd: step.cwd } : {}), ...(step.description ? { description: step.description } : {}), ...(step.timeout_seconds ? { timeout_seconds: step.timeout_seconds } : {}) } }]) : [];
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

async function capturePi(workspace, scratch, profile, script, maxTurns = 0) {
  const recorder = await startRecorder(script);
  try {
    const home = join(scratch, "pi-home");
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

async function captureSwarm(workspace, scratch, profile, projectSystemPrompt, script, maxTurns = 0) {
  const recorder = await startRecorder(script);
  try {
    const home = join(scratch, "swarm-home");
    await mkdir(home, { recursive: true });
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
    } else if (projectSystemPrompt) {
      const systemPromptFile = join(scratch, "project-system-prompt.txt");
      await writeFile(systemPromptFile, projectSystemPrompt);
      args.push("--system-prompt-file", systemPromptFile);
    }
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
  const output = resolve(options.output ?? join(workspace, ".parity"));
  const profile = options.profile ?? "clean";
  const scenario = options.scenario ?? "default";
  if (!TOOL_SCRIPTS[scenario]) throw new Error(`unknown parity scenario: ${scenario}`);
  const script = TOOL_SCRIPTS[scenario];
  const maxTurns = Number(options.maxTurns ?? 0) || 0;
  if (profile !== "clean" && profile !== "project") {
    throw new Error(`unknown parity profile: ${profile}`);
  }
  const scratch = await mkdtemp(join(tmpdir(), "pi-swarm-parity-"));
  try {
    const pi = await capturePi(workspace, scratch, profile, script, maxTurns);
    const piArtifacts = requestArtifacts(pi.requests, PROBE_PROMPT);
    const projectSystemPrompt = profile === "project"
      ? sharedPromptPrefix(primarySystemPrompt(pi.requests))
      : undefined;
    const swarm = await captureSwarm(workspace, scratch, profile, projectSystemPrompt, script, maxTurns);
    const swarmArtifacts = requestArtifacts(swarm.requests, PROBE_PROMPT);
    const sharedPrompt = projectSystemPrompt ? {
      bytes: Buffer.byteLength(projectSystemPrompt),
      sha256: sha256(projectSystemPrompt),
      pi: promptSliceEvidence(piArtifacts.prompt.text, projectSystemPrompt),
      swarm: promptSliceEvidence(swarmArtifacts.prompt.text, projectSystemPrompt),
    } : undefined;
    const mismatches = diffJson(piArtifacts.requests, swarmArtifacts.requests);
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
