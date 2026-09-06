#!/usr/bin/env node
import { createHash } from "node:crypto";
import { createServer } from "node:http";
import { mkdtemp, mkdir, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { spawn } from "node:child_process";

const GENERATED_ID_KEYS = new Set(["tool_call_id"]);
const PROBE_PROMPT = "PARITY_CAPTURE";
/** Tool calls the scripted model issues, indexed by how many tool results it has seen. */
const TOOL_SCRIPT = [
  { id: "call_parity_probe", command: "printf parity-probe" },
  { id: "call_parity_probe_fail", command: "printf out; printf err >&2; exit 3" },
];
export const EXPECTED_PRIMARY_REQUESTS = TOOL_SCRIPT.length + 1;
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
          .replace(/(Fingerprint: ")[0-9a-f]{32}(")/g, "$1<fingerprint>$2");
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
    .replace(/(Fingerprint: \\")[0-9a-f]{32}(\\")/g, "$1<fingerprint>$2");
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

async function startRecorder() {
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
    const step = TOOL_SCRIPT[toolResults];
    if (step && (body.tools ?? []).some(tool => tool?.function?.name === "bash")) {
      emit(openAIChunk(body.model, {
        role: "assistant",
        tool_calls: [{
          index: 0,
          id: step.id,
          type: "function",
          function: { name: "bash", arguments: JSON.stringify({ command: step.command }) },
        }],
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

async function capturePi(workspace, scratch, profile) {
  const recorder = await startRecorder();
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
    const hookEnv = profile === "clean" ? { PI_SWARM_NO_HOOKS: "1" } : {};
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

async function captureSwarm(workspace, scratch, profile, projectSystemPrompt) {
  const recorder = await startRecorder();
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
      "--max-turns", "3",
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
  if (profile !== "clean" && profile !== "project") {
    throw new Error(`unknown parity profile: ${profile}`);
  }
  const scratch = await mkdtemp(join(tmpdir(), "pi-swarm-parity-"));
  try {
    const pi = await capturePi(workspace, scratch, profile);
    const piArtifacts = requestArtifacts(pi.requests, PROBE_PROMPT);
    const projectSystemPrompt = profile === "project"
      ? sharedPromptPrefix(primarySystemPrompt(pi.requests))
      : undefined;
    const swarm = await captureSwarm(workspace, scratch, profile, projectSystemPrompt);
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
    return { profile, output, mismatches, mismatchCategories, wire, pi: piArtifacts, swarm: swarmArtifacts };
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
  });
  process.stdout.write(`${JSON.stringify({
    profile: result.profile,
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
