#!/usr/bin/env node
// Interactive TUI parity harness. Unlike probe.mjs (one `-p` run per side),
// this keeps ONE recorder alive and launches the real `swarm` and `pi` TUIs
// in tmux sessions pointed at it, so a human (or a script) can drive both
// with identical keystrokes and compare every model-boundary request.
//
//   node tools/parity/tui-probe.mjs start --workspace <ws> [--seed-home <dir>]
//     → prints the tmux session names, scratch dir, and recorder port
//   node tools/parity/tui-probe.mjs compare --scratch <dir>
//     → canonical diff of the recorded swarm/pi requests (like probe.mjs)
//
// The scripted model answers by the LATEST user prompt: "PARITY_CAPTURE
// <step>" picks TUI_SCRIPTS[<step>] (a tool call), anything else gets a
// plain "PARITY_OK" text reply. Requests without the PARITY_CAPTURE marker
// (sub-agents, title generation…) get a fixed reply and are logged separately.
import { createServer } from "node:http";
import { appendFileSync, existsSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { cp, mkdir, writeFile } from "node:fs/promises";
import { join, resolve } from "node:path";
import { spawnSync } from "node:child_process";
import { tmpdir } from "node:os";
import { PI_SWARM_EXTENSIONS, PROBE_PROMPT, canonicalizeRequest, diffJson, discoverExtensionEntries, hasPrimaryPrompt, openAIChunk, respondNonStreaming, substituteRevisions, summarizeMismatches, wireComparison } from "./probe.mjs";

/** Steps addressed by name from the TUI prompt: "PARITY_CAPTURE <name>". */
export const TUI_SCRIPTS = {
  // Interactive-only tools (QuestionBroker / PlanBroker exist in the TUI).
  ask: { id: "call_tui_ask", tool: "ask_user_question", args: { question: "Which colour?", type: "choice", choices: ["red", "blue"] } },
  "ask-text": { id: "call_tui_ask_text", tool: "ask_user_question", args: { question: "Say something", type: "text" } },
  "ask-bad": { id: "call_tui_ask_bad", tool: "ask_user_question", args: { type: "choice" } },
  "plan-enter": { id: "call_tui_plan_enter", tool: "enter_plan_mode", args: {} },
  "plan-write": { id: "call_tui_plan_write", tool: "Bash", args: { command: "printf '# Plan\\n\\n1. step one\\n2. step two\\n' > plan.md && echo wrote" } },
  "plan-edit": { id: "call_tui_plan_edit", tool: "Bash", args: { command: "printf 'x' > mutate.txt" } },
  "plan-exit": { id: "call_tui_plan_exit", tool: "exit_plan_mode", args: { plan_file: "plan.md" } },
  "plan-exit-inline": { id: "call_tui_plan_exit_inline", tool: "exit_plan_mode", args: { plan: "# Inline plan\n\n- do it" } },
  "plan-exit-idle": { id: "call_tui_plan_exit_idle", tool: "exit_plan_mode", args: { plan: "never entered" } },
  "plan-exit-both": { id: "call_tui_plan_exit_both", tool: "exit_plan_mode", args: { plan: "a", plan_file: "plan.md" } },
  "plan-exit-none": { id: "call_tui_plan_exit_none", tool: "exit_plan_mode", args: {} },
  "skill-grill": { id: "call_tui_skill_grill", tool: "Skill", args: { skill: "grill-me" } },
  "skill-loop": { id: "call_tui_skill_loop", tool: "Skill", args: { skill: "loop", args: "5m /foo" } },
  // Interactive Swarm registers bgprocess `Bash`, not `bash`: this step is
  // the unknown-tool error path on both sides.
  echo: { id: "call_tui_echo", tool: "bash", args: { command: "printf tui-probe" } },
  // Swarm tails stdout and stderr with two independent 50ms pollers, so two
  // streams written in the same tick interleave non-deterministically on
  // Swarm's own side; separate them by more than one poll interval.
  bash: { id: "call_tui_bash", tool: "Bash", args: { command: "printf err >&2; for i in 1; do sleep 0.2; done; printf tui-probe; exit 3", description: "probe \"exit\"", timeout_seconds: 5 } },
  "bash-empty": { id: "call_tui_bash_empty", tool: "Bash", args: {} },
  "bash-notimeout": { id: "call_tui_bash_notimeout", tool: "Bash", args: { command: "echo hi" } },
  // Bare `sleep` is blocked by the builtin sleep-blocker on both runtimes
  // (an until/for loop body is allowed); these probe the idle-timeout
  // watcher: steady output keeps a command in the foreground, silence
  // auto-backgrounds it and a completion notification wakes the agent.
  "bash-sleep": { id: "call_tui_bash_sleep", tool: "Bash", args: { command: "sleep 3", timeout_seconds: 5 } },
  "bash-slow": { id: "call_tui_bash_slow", tool: "Bash", args: { command: "for i in 1 2 3; do echo tick$i; sleep 1; done", timeout_seconds: 2 } },
  "bash-bg": { id: "call_tui_bash_bg", tool: "Bash", args: { command: "for i in 1 2 3; do sleep 1; done; echo bg-done", timeout_seconds: 1, description: "backgrounder" } },
  "bash-bg-fail": { id: "call_tui_bash_bg_fail", tool: "Bash", args: { command: "for i in 1 2 3; do sleep 1; done; echo bg-oops >&2; exit 7", timeout_seconds: 1 } },
  "bash-big": { id: "call_tui_bash_big", tool: "Bash", args: { command: "seq 1 3000", timeout_seconds: 10 } },
  "bash-cwd": { id: "call_tui_bash_cwd", tool: "Bash", args: { command: "pwd", cwd: "/nonexistent-dir", timeout_seconds: 5 } },
  "read-bg-missing": { id: "call_tui_read_bg_missing", tool: "ReadBackgroundCommand", args: { task_id: "nope" } },
  "read-bg-list": { id: "call_tui_read_bg_list", tool: "ReadBackgroundCommand", args: { action: "list" } },
  "read-bg-none": { id: "call_tui_read_bg_none", tool: "ReadBackgroundCommand", args: {} },
  "read-bg-bad-action": { id: "call_tui_read_bg_bad_action", tool: "ReadBackgroundCommand", args: { task_id: "x", action: "restart" } },
  task: { id: "call_tui_task", tool: "TaskManage", args: { operations: [{ key: "a", op: "create", subject: "tui probe", status: "in_progress", active: true }] } },
};

const isPrimary = body => hasPrimaryPrompt(body, PROBE_PROMPT);
const lastUserText = body => {
  for (let i = (body.messages ?? []).length - 1; i >= 0; i--) {
    const m = body.messages[i];
    if (m?.role !== "user") continue;
    const text = typeof m.content === "string" ? m.content : (m.content ?? []).map(b => b?.text ?? "").join("");
    // Hook reminders (task-enforcement nudge, annoyance review) are appended
    // as their own user turns after the prompt on both runtimes.
    if (text.startsWith("<system-reminder")) continue;
    return text;
  }
  return "";
};

/** One recorder per side: each TUI is pointed at its own listener. */
export async function startTuiRecorder(scratch, side) {
  mkdirSync(scratch, { recursive: true });
  const server = createServer(async (req, res) => {
    if (req.method !== "POST") { res.writeHead(404).end(); return; }
    const chunks = []; for await (const c of req) chunks.push(c);
    const rawText = Buffer.concat(chunks).toString("utf8");
    const body = JSON.parse(rawText);
    appendFileSync(join(scratch, `${side}.raw.ndjson`), rawText + "\n");
    if (!body.stream) { respondNonStreaming(res, body); return; }
    res.writeHead(200, { "content-type": "text/event-stream", "cache-control": "no-cache", connection: "close" });
    const emit = v => res.write(`data: ${JSON.stringify(v)}\n\n`);
    if (!isPrimary(body)) {
      emit(openAIChunk(body.model, { role: "assistant", content: "SUBAGENT_OK" })); emit(openAIChunk(body.model, {}, "stop")); res.end("data: [DONE]\n\n"); return;
    }
    const last = lastUserText(body).trim();
    const stepName = last.startsWith(PROBE_PROMPT) ? last.slice(PROBE_PROMPT.length).trim().split(/\s+/)[0] : "";
    const step = TUI_SCRIPTS[stepName];
    // One tool call per prompt: issue it once (hook reminders arrive as extra
    // user turns, so "last message is a user" is not a reliable signal) and
    // reply with text once its result is in the transcript.
    const issued = step && (body.messages ?? []).some(m => Array.isArray(m?.tool_calls) && m.tool_calls.some(c => c?.id === step.id));
    const pending = step && !issued && (body.tools ?? []).length > 0;
    if (pending) {
      const args = substituteRevisions(step.args, body.messages ?? []);
      emit(openAIChunk(body.model, { role: "assistant", tool_calls: [{ index: 0, id: step.id, type: "function", function: { name: step.tool, arguments: JSON.stringify(args) } }] }));
      emit(openAIChunk(body.model, {}, "tool_calls"));
    } else {
      emit(openAIChunk(body.model, { role: "assistant", content: "PARITY_OK" }));
      emit(openAIChunk(body.model, {}, "stop"));
    }
    res.end("data: [DONE]\n\n");
  });
  await new Promise((ok, err) => { server.once("error", err); server.listen(0, "127.0.0.1", ok); });
  return { server, port: server.address().port, baseUrl: `http://127.0.0.1:${server.address().port}/v1` };
}

async function seedHome(home, seed) { await mkdir(home, { recursive: true }); if (seed) await cp(seed, home, { recursive: true }); }

export async function start({ workspace, seed }) {
  const scratch = resolve(process.env.PARITY_TUI_SCRATCH ?? join(tmpdir(), `pi-swarm-tui-${Date.now()}`));
  mkdirSync(scratch, { recursive: true });
  const swarmRec = await startTuiRecorder(scratch, "swarm");
  const piRec = await startTuiRecorder(scratch, "pi");
  // ONE home for both sides: the prompt renders <home_directory>, skills are
  // discovered under ~/.swarm and ~/.swarmos, and the two runtimes keep their
  // own config files (.swarm/config vs .pi/agent), so sharing is conflict-free
  // and makes the comparison strictly stronger.
  const home = join(scratch, "home"); await seedHome(home, seed);
  const swarmHome = home;
  const swarmEnv = { HOME: swarmHome, PARITY_API_KEY: "parity-dummy-key", XDG_CONFIG_HOME: join(swarmHome, ".config"), XDG_DATA_HOME: join(swarmHome, ".local", "share"), XDG_STATE_HOME: join(swarmHome, ".local", "state"), TERM: "xterm-256color" };
  // The TUI refuses the run-scoped --api-type/--base-url flags (-p only);
  // it reads providers from <root>/config/providers.json instead.
  await mkdir(join(swarmHome, ".swarm", "config"), { recursive: true });
  await writeFile(join(swarmHome, ".swarm", "config", "providers.json"), JSON.stringify([{
    name: "parity", display_name: "Parity Capture", color: "#888888", type: "api_key", api_type: "openai-compatible", base_url: swarmRec.baseUrl,
    api_key: "parity-dummy-key", source: "user", available: true,
    models: [{ id: "parity-model", display_name: "Parity Capture Model", context: "1000000", context_window: 1000000, max_tokens: 4096 }],
  }], null, 2));
  // app_init.go ignores -P/-m: the TUI picks config.json current_provider /
  // current_model, then keeps it only if hasProviderCredentials() — satisfied
  // here by the generic <PROVIDER>_API_KEY env (PARITY_API_KEY).
  await writeFile(join(swarmHome, ".swarm", "config", "config.json"), JSON.stringify({ current_provider: "parity", current_model: "parity-model" }, null, 2));
  // …but the ACTIVE PROFILE wins over config.json (settings.NewProfileManager
  // still reads the legacy ~/.swarmos/agent_profiles.json; the builtin
  // "balanced" default points at anthropic). Seed a profile whose "main"
  // alias is the parity provider. Note the TUI credential probe
  // (credentials.go getAPIKeyFromProviders) also reads ~/.swarmos/providers.json.
  await mkdir(join(swarmHome, ".swarmos"), { recursive: true });
  await writeFile(join(swarmHome, ".swarmos", "agent_profiles.json"), JSON.stringify({ default_profile: "parity", profiles: [{ id: "parity", name: "Parity", is_default: true, pointers: { main: { provider: "parity", model: "parity-model" } }, created_at: "2026-01-01T00:00:00Z", updated_at: "2026-01-01T00:00:00Z" }] }, null, 2));
  // --approval-mode is headless-only. The interactive TUI attaches an
  // InteractivePermissionChecker driven by ~/.swarmos/permissions.json
  // (permissions_config.go); level "yolo" makes every check PolicyAllow
  // (permission_engine.go), the counterpart of pi's --approve. Without it a
  // Bash call opens a modal that swallows the next scripted keystrokes.
  await writeFile(join(swarmHome, ".swarmos", "permissions.json"), JSON.stringify({ version: 1, level: "yolo", timeoutSeconds: 300, timeoutBehavior: "stop", defaults: { policies: {} }, overrides: {}, metadata: {} }, null, 2));
  const swarmArgs = ["--no-update", "--approval-mode", "auto", "--workspace", workspace];
  // pi
  const piHome = home;
  const agentDir = join(piHome, ".pi", "agent"); await mkdir(agentDir, { recursive: true });
  await writeFile(join(agentDir, "models.json"), JSON.stringify({ providers: { parity: { baseUrl: piRec.baseUrl, api: "openai-completions", apiKey: "parity-dummy-key", models: [{ id: "parity-model", name: "Parity Capture Model", reasoning: false, contextWindow: 1000000, maxTokens: 4096 }] } } }, null, 2));
  const piArgs = ["--provider", "parity", "--model", "parity-model", "--no-session", "--approve"];
  if (resolve(workspace) !== resolve(PI_SWARM_EXTENSIONS, "..", "..")) for (const e of await discoverExtensionEntries(PI_SWARM_EXTENSIONS)) piArgs.push("--extension", e);
  const piEnv = { HOME: piHome, PI_CODING_AGENT_DIR: agentDir, PI_OFFLINE: "1", XDG_CONFIG_HOME: join(piHome, ".config"), XDG_DATA_HOME: join(piHome, ".local", "share"), XDG_STATE_HOME: join(piHome, ".local", "state"), TERM: "xterm-256color" };
  const sh = (bin, args, env) => `cd ${JSON.stringify(workspace)} && env ${Object.entries(env).map(([k, v]) => `${k}=${JSON.stringify(v)}`).join(" ")} ${bin} ${args.map(a => JSON.stringify(a)).join(" ")}`;
  writeFileSync(join(scratch, "launch.json"), JSON.stringify({ workspace, scratch, swarm: { session: "parity-swarm", cmd: sh("swarm", swarmArgs, swarmEnv) }, pi: { session: "parity-pi", cmd: sh("pi", piArgs, piEnv) } }, null, 2));
  for (const [name, cmd] of [["parity-swarm", sh("swarm", swarmArgs, swarmEnv)], ["parity-pi", sh("pi", piArgs, piEnv)]]) {
    spawnSync("tmux", ["kill-session", "-t", name], { stdio: "ignore" });
    const r = spawnSync("tmux", ["new-session", "-d", "-s", name, "-x", "200", "-y", "50", "-c", workspace, cmd], { stdio: "inherit" });
    if (r.status !== 0) throw new Error(`tmux failed for ${name}`);
  }
  process.stdout.write(JSON.stringify({ scratch, sessions: ["parity-swarm", "parity-pi"], recorders: { swarm: swarmRec.port, pi: piRec.port } }) + "\n");
  // Keep the recorders alive until killed.
  await new Promise(() => {});
}

export function compare(scratch) {
  const read = side => { const p = join(scratch, `${side}.raw.ndjson`); return existsSync(p) ? readFileSync(p, "utf8").split("\n").filter(Boolean) : []; };
  const swarmRaw = read("swarm"), piRaw = read("pi");
  const split = raw => { const primary = [], auxiliary = []; for (const l of raw) { const b = JSON.parse(l); (isPrimary(b) ? primary : auxiliary).push(canonicalizeRequest(b)); } return { primary, auxiliary }; };
  const pi = split(piRaw), swarm = split(swarmRaw);
  const mismatches = [...diffJson(pi.primary, swarm.primary), ...diffJson(pi.auxiliary, swarm.auxiliary).map(e => ({ ...e, path: `/auxiliary${e.path}` }))];
  const wire = wireComparison(piRaw, swarmRaw, PROBE_PROMPT);
  return { counts: { pi: pi.primary.length, swarm: swarm.primary.length, piAuxiliary: pi.auxiliary.length, swarmAuxiliary: swarm.auxiliary.length }, mismatchCount: mismatches.length, categories: summarizeMismatches(mismatches), mismatches, wire: { identical: wire.identical, requests: wire.requests.filter(r => !r.identical).slice(0, 3), auxiliary: wire.auxiliary.filter(r => !r.identical).slice(0, 3) } };
}

if (import.meta.url === `file://${process.argv[1]}`) {
  const [cmd, ...rest] = process.argv.slice(2);
  const val = flag => { const i = rest.indexOf(flag); return i >= 0 ? rest[i + 1] : undefined; };
  if (cmd === "start") await start({ workspace: resolve(val("--workspace") ?? process.cwd()), seed: val("--seed-home") ? resolve(val("--seed-home")) : undefined });
  else if (cmd === "compare") { const r = compare(resolve(val("--scratch"))); process.stdout.write(JSON.stringify(r, null, 2) + "\n"); process.exit(r.mismatchCount === 0 && r.wire.identical ? 0 : 1); }
  else { process.stderr.write("usage: tui-probe.mjs start --workspace <ws> [--seed-home <dir>] | compare --scratch <dir>\n"); process.exit(2); }
}
