/**
 * Port of Swarm's builtin `bash` tool contract so `pi -p` sends and receives
 * the same bytes as `swarm -p`.
 *
 * Source of truth (swarm-sdk @ 3fafd3fa):
 *   internal/tools/builtin/bash.go   BashParams, Description, applyTimeout,
 *                                    resolveWorkdir, prepareCmd, buildResult,
 *                                    bashTruncateOutput, mergeOutput,
 *                                    bashCommandFailedError, runBatch
 *   internal/tools/xml.go            XMLBuilder (fmt %q attrs, CDATA fields)
 *   internal/tokens/tokens.go        Estimate = len/4 (min 1), Truncate
 *   internal/agent/agent_tools.go    "Error executing <tool>: <err>"
 *   internal/sdkerr/error.go         "<msg> (error_id=err_<hex>)"
 */
import { randomBytes } from "node:crypto";
import { spawn } from "node:child_process";
import { existsSync, lstatSync, mkdirSync, openSync, closeSync, readFileSync, realpathSync, statSync, writeFileSync } from "node:fs";
import { constants as osConstants, homedir, tmpdir } from "node:os";
import { basename, dirname, join, relative, resolve, isAbsolute } from "node:path";

export const SWARM_BASH_DESCRIPTION =
  "Execute a shell command and capture stdout, stderr, exit code, duration, and timeout status. Pipes and redirections are supported.\n\nSet timeout_seconds for every command; values below the enforced 60-second minimum are raised automatically. Use cwd instead of cd.";

/** Exact JSON Schema Swarm's SchemaFor[BashParams] emits (captured from the wire). */
export const SWARM_BASH_PARAMETERS = {
  properties: {
    command: { description: "Shell command to execute", type: "string" },
    cwd: { description: "Working directory (use this instead of 'cd')", type: "string" },
    description: { description: "Brief label for this command shown in the status header and TUI (5-10 words)", type: "string" },
    env: { additionalProperties: { type: "string" }, description: "Extra environment variables to set", properties: {}, type: "object" },
    timeout_seconds: { default: "60", description: "Max seconds to wait (minimum 60; values below 60 are raised automatically)", type: "integer" },
  },
  required: ["command"],
  type: "object",
} as const;

export interface BashParams { command: string; cwd?: string; env?: Record<string, string>; timeout_seconds?: number; description?: string; background?: boolean }

/** Pi's native Bash call preview: keep the command visible in the dim tool row. */
export function formatBashCall(args: { command?: string; timeout_seconds?: number; timeout?: number } | undefined, theme: any): string {
  const command = typeof args?.command === "string" ? args.command : "...";
  const timeout = args?.timeout_seconds ?? args?.timeout;
  const timeoutSuffix = timeout ? theme?.fg?.("muted", ` (timeout ${timeout}s)`) ?? ` (timeout ${timeout}s)` : "";
  const display = command || (theme?.fg?.("toolOutput", "...") ?? "...");
  const title = theme?.fg?.("toolTitle", theme?.bold?.(`$ ${display}`) ?? `$ ${display}`) ?? `$ ${display}`;
  return title + timeoutSuffix;
}

type WrapToWidth = (text: string, width: number) => string[];

/**
 * Minimal dependency-free fallback for headless consumers. The live extension
 * injects pi-tui's wrapTextWithAnsi so terminal-cell width is authoritative for
 * tabs, wide glyphs, and ANSI sequences.
 */
const wrapPlainText: WrapToWidth = (text, width) => {
  if (width <= 0) return [""];
  const normalized = text.replace(/\t/g, "   ");
  if (normalized.length <= width) return [normalized];
  return Array.from({ length: Math.ceil(normalized.length / width) }, (_, i) => normalized.slice(i * width, (i + 1) * width));
};

export function bashResultComponent(result: any, options: any = {}, theme: any = {}, wrapToWidth: WrapToWidth = wrapPlainText): { render: (width: number) => string[]; invalidate: () => void } {
  const command = typeof result?.details?.command === "string" ? result.details.command : "";
  const text = Array.isArray(result?.content) ? result.content.filter((part: any) => part?.type === "text").map((part: any) => part.text).join("\n") : "";
  const prefix = command ? `${options?.isPartial ? "⋯" : result?.isError || options?.isError ? "✗" : "✓"} $ ${command}` : "";
  const value = [prefix, text].filter(Boolean).join("\n");
  return { render: (width: number) => width <= 0 ? [""] : value.split("\n").flatMap((line: string) => wrapToWidth(line, width)), invalidate: () => {} };
}

export function bashCallComponent(value: string, truncate?: (text: string, width: number) => string): { render: (width: number) => string[]; invalidate: () => void } {
  // Pi validates every rendered line against the terminal width. Commands can
  // be arbitrarily long (especially repository-discovery commands), so the
  // preview must be width-bounded independently of the model-facing command.
  const visible = (text: string) => stripANSI(text).length;
  const fit = (text: string, width: number) => {
    if (width <= 0) return "";
    if (visible(text) <= width) return text;
    if (width <= 1) return text.slice(0, width);
    return `${text.slice(0, width - 1)}…`;
  };
  return { render: (width: number) => [truncate ? truncate(value, width) : fit(value, width)], invalidate: () => {} };
}

const ANSI = /\x1b\[[0-9;:?]*[A-Za-z]/g;
export const stripANSI = (s: string) => (s.includes("\x1b") ? s.replace(ANSI, "") : s);

// tokens.go: charsPerToken = 4, Estimate = len/4 (1 when non-empty but < 4)
export const estimateTokens = (s: string) => (s === "" ? 0 : Math.max(1, Math.floor(Buffer.byteLength(s) / 4)));
const truncateTokens = (s: string, maxTokens: number) => Buffer.from(s).subarray(0, maxTokens * 4).toString("utf8");

/** Go %q for attribute values (ASCII-safe subset; Swarm attrs are ints/bools/short labels). */
export function goQuote(value: string): string {
  let out = "\"";
  for (const ch of value) {
    const code = ch.codePointAt(0)!;
    if (ch === "\"") out += "\\\"";
    else if (ch === "\\") out += "\\\\";
    else if (ch === "\n") out += "\\n";
    else if (ch === "\r") out += "\\r";
    else if (ch === "\t") out += "\\t";
    else if (code < 0x20 || code === 0x7f) out += `\\x${code.toString(16).padStart(2, "0")}`;
    else out += ch;
  }
  return out + "\"";
}

export const mergeOutput = (stdout: string, stderr: string) => (stderr.length === 0 ? stdout : stdout.length === 0 ? stderr : stdout + "\n" + stderr);

/**
 * Go ProcessState.ExitCode() is -1 for a signalled process, which on its own
 * tells the model nothing. A `pkill -f <pattern>` whose pattern also matches
 * this tool's own `bash -c <command>` argv kills the shell running it, so the
 * bare -1 reads like a harness crash. Name the signal instead.
 */
export function signalNote(exitCode: number, signal: NodeJS.Signals | null | undefined): string {
  if (exitCode !== -1 || !signal) return "";
  return `Command terminated by ${signal}. A shell killed by its own command (for example \`pkill -f <pattern>\` whose pattern also matches this command line) reports no exit status; statements after the kill did not run.`;
}

/**
 * A wrapper that owns the PTY (`script`) reaps the signalled shell itself and
 * reports the shell's death as a normal exit status of 128+signal, so Node sees
 * no signal at all. codex encodes the same convention as
 * `EXIT_CODE_SIGNAL_BASE + <signal>`. Recover the signal so a self-`pkill`
 * still gets explained instead of surfacing as a bare "exit status 143".
 */
const SIGNAL_EXIT_BASE = 128;
const SIGNALLED_EXITS = new Map<number, NodeJS.Signals>(
  (["SIGINT", "SIGQUIT", "SIGABRT", "SIGKILL", "SIGSEGV", "SIGPIPE", "SIGTERM"] as const)
    .map((name) => [SIGNAL_EXIT_BASE + osConstants.signals[name], name]),
);
export function decodeSignalExit(code: number | null, signal: NodeJS.Signals | null): { exitCode: number; signal: NodeJS.Signals | null } {
  if (signal) return { exitCode: code ?? -1, signal };
  const decoded = code === null ? undefined : SIGNALLED_EXITS.get(code);
  if (decoded) return { exitCode: -1, signal: decoded };
  return { exitCode: code ?? 0, signal: null };
}

export interface Truncation { output: string; outputPath: string; truncated: boolean }
/** os.CreateTemp(dir, "bash-full-*.txt"): the "*" becomes a random uint32 in decimal. */
function createTempLikeGo(dir: string, prefix: string, suffix: string): string {
  for (let attempt = 0; attempt < 10_000; attempt++) {
    const name = join(dir, `${prefix}${randomBytes(4).readUInt32LE(0)}${suffix}`);
    try { closeSync(openSync(name, "wx", 0o600)); return name; } catch (e: any) { if (e?.code !== "EEXIST") throw e; }
  }
  throw new Error("createTemp: too many attempts");
}
/** bash.go bashTruncateOutput: 2000-line and 12,500-token caps, full output spilled to disk. */
export function bashTruncateOutput(output: string, tempRoot = tmpdir()): Truncation {
  const maxLines = 2000, maxTokens = 12_500;
  const lineCount = (output.match(/\n/g) ?? []).length;
  if (lineCount <= maxLines && estimateTokens(output) <= maxTokens) return { output, outputPath: "", truncated: false };
  const dir = join(tempRoot, "swarm-tool-output");
  let outputPath = "";
  try { mkdirSync(dir, { recursive: true, mode: 0o755 }); outputPath = createTempLikeGo(dir, "bash-full-", ".txt"); writeFileSync(outputPath, output); } catch { outputPath = ""; }
  const hint = `Full output saved to: ${outputPath}\nUse \`sed -n 'START,ENDp' FILE\` to view sections, or \`rg PATTERN FILE\` to search within it.`;
  if (lineCount > maxLines) {
    const lines = output.split("\n");
    output = lines.slice(0, maxLines).join("\n") + `\n\n...${lineCount - maxLines} lines truncated...\n\n${hint}`;
  }
  if (estimateTokens(output) > maxTokens) {
    const dropped = estimateTokens(output) - maxTokens;
    output = truncateTokens(output, maxTokens) + `\n\n...~${dropped} tokens truncated...\n\n${hint}`;
  }
  return { output, outputPath, truncated: true };
}

export interface BashOutcome { exitCode: number; durationMs: number; stdout: string; stderr: string; timedOut: boolean; requestedSecs: number; effectiveSecs: number; description?: string; signal?: NodeJS.Signals | null }

/** bash.go buildResult → tools.NewXML("result")…Build(). */
export function buildResultXML(o: BashOutcome): string {
  const stdout = stripANSI(o.stdout), stderr = stripANSI(o.stderr);
  let merged = mergeOutput(stdout, stderr);
  if (merged === "") merged = "(no output)";
  const t = bashTruncateOutput(merged);
  const attrs = [`exit_code="${o.exitCode}"`, `duration_ms="${o.durationMs}"`, `timed_out="${o.timedOut ? "true" : "false"}"`];
  if (o.description) attrs.push(`description=${goQuote(o.description)}`);
  let body = "";
  if (o.requestedSecs > 0 && o.requestedSecs < 60) body += `  <timeout_clamped requested_seconds="${o.requestedSecs}" effective_seconds="${o.effectiveSecs}"/>\n`;
  const field = (tag: string, value: string) => `  <${tag}><![CDATA[${value}]]></${tag}>\n`;
  if (t.truncated) {
    const msg = `(output truncated — full content saved to output_path)\nFull output saved to: ${t.outputPath}\nUse \`sed -n 'START,ENDp' FILE\` to view sections, or \`rg PATTERN FILE\` to search within it.`;
    body += field("stdout", msg) + field("stderr", msg) + `  <output_file path=${goQuote(t.outputPath)}/>\n`;
  } else {
    body += field("stdout", stdout) + field("stderr", stderr);
  }
  return `<result ${attrs.join(" ")}>\n${body}</result>`;
}

export const newErrorID = () => "err_" + randomBytes(10).toString("hex");

/** bash.go bashCommandFailedError as surfaced by agent_tools.go + sdkerr.Error(). */
export function commandFailedMessage(exitCode: number, stdout: string, stderr: string, errorId = newErrorID(), signal?: NodeJS.Signals | null): string {
  const so = bashTruncateOutput(stripANSI(stdout)).output || "(no output)";
  const se = bashTruncateOutput(stripANSI(stderr)).output || "(no output)";
  const note = signalNote(exitCode, signal);
  const status = note ? `signal: ${signal!.toLowerCase().replace(/^sig/, "")}` : `exit status ${exitCode}`;
  return `Error executing bash: Command exited with code ${exitCode}: ${status}\n\n${note ? `${note}\n\n` : ""}stderr:\n${se}\n\nstdout:\n${so} (error_id=${errorId})`;
}
export function timedOutMessage(effectiveSecs: number, exitCode: number, errorId = newErrorID()): string {
  return `Error executing bash: command timed out after ${effectiveSecs}s (exit_code=${exitCode}): context deadline exceeded (error_id=${errorId})`;
}
export function invalidCwdMessage(reason: string, errorId = newErrorID()): string {
  return `Error executing bash: ${reason} (error_id=${errorId})`;
}

/**
 * swarm-tui sdk_integration.go builtinAllowedPaths: [workspaceRoot, /tmp,
 * ~/.swarmos] (the TUI overrides path_guard.go defaultAllowedPaths; note the
 * legacy `.swarmos` spelling). `--allow-all-paths` makes the list empty.
 */
export function defaultAllowedPaths(workspaceRoot = process.cwd(), home = homedir()): string[] {
  return [resolve(workspaceRoot), "/tmp", join(home, ".swarmos")];
}

/** path_guard.go resolvePathForCheck: EvalSymlinks via the nearest existing ancestor. */
export function resolvePathForCheck(absPath: string): string {
  const cleaned = resolve(absPath);
  if (!isAbsolute(cleaned)) throw new Error("path must be absolute");
  try { return realpathSync(cleaned); } catch (e: any) { if (e?.code !== "ENOENT") throw e; }
  let current = cleaned;
  for (;;) {
    let exists = false;
    try { statSync(current); exists = true; } catch (e: any) { if (e?.code !== "ENOENT") throw e; }
    if (exists) {
      const resolvedCurrent = realpathSync(current);
      if (current === cleaned) return resolvedCurrent;
      return join(resolvedCurrent, relative(current, cleaned));
    }
    const parent = dirname(current);
    if (parent === current) return cleaned;
    current = parent;
  }
}

const pathWithinRoot = (root: string, target: string) => {
  const rel = relative(root, target);
  if (rel === "") return true;
  if (rel === "..") return false;
  return !rel.startsWith(`..${"/"}`) && !isAbsolute(rel);
};

/** path_guard.go checkAllowedPath: undefined when allowed, else Swarm's error text. */
export function checkAllowedPath(absPath: string, allowedPaths: readonly string[]): string | undefined {
  if (allowedPaths.length === 0) return undefined;
  let resolvedTarget: string;
  try { resolvedTarget = resolvePathForCheck(absPath); } catch (e) { return `failed to resolve path: ${String((e as Error).message ?? e)}`; }
  for (const allowed of allowedPaths) {
    if (!allowed) continue;
    let resolvedAllowed: string;
    try { resolvedAllowed = resolvePathForCheck(resolve(allowed)); } catch { continue; }
    if (pathWithinRoot(resolvedAllowed, resolvedTarget)) return undefined;
  }
  return `Path not allowed (not_allowed): ${absPath}`;
}

/**
 * `git rev-parse --git-common-dir` without spawning git: the shared directory
 * that every linked worktree of one repository points at. Empty when `dir` is
 * not inside a repository.
 */
export function gitCommonDir(dir: string): string {
  let current = resolve(dir);
  for (;;) {
    const dot = join(current, ".git");
    if (existsSync(dot)) {
      if (lstatSync(dot).isDirectory()) return realpathSync(dot);
      const line = readFileSync(dot, "utf8").trim();
      if (line.startsWith("gitdir:")) {
        let git = line.slice(7).trim();
        if (!isAbsolute(git)) git = resolve(current, git);
        // <common>/worktrees/<name> for a linked worktree; <common> otherwise.
        const parent = dirname(git);
        const target = basename(parent) === "worktrees" ? dirname(parent) : git;
        try { return realpathSync(target); } catch { return ""; }
      }
    }
    const parent = dirname(current);
    if (parent === current) return "";
    current = parent;
  }
}

/** True when both paths are checkouts of the same repository. */
export function sharesGitCommonDir(workspace: string, target: string): boolean {
  let common: string;
  try { common = gitCommonDir(workspace); } catch { return false; }
  if (!common) return false;
  let resolved: string;
  try { resolved = resolvePathForCheck(resolve(target)); } catch { return false; }
  if (!existsSync(resolved)) return false;
  try { return gitCommonDir(resolved) === common; } catch { return false; }
}

export function resolveWorkdir(cwd: string | undefined, defaultCwd: string, allowedPaths: readonly string[] = defaultAllowedPaths(defaultCwd)): { dir: string } | { error: string } {
  if (!cwd) return { dir: defaultCwd };
  const abs = resolve(cwd);
  let denied = checkAllowedPath(abs, allowedPaths);
  // A linked git worktree of the workspace's own repository is the same
  // authorized checkout reached by another path, so read-only inspection of it
  // must not be refused just because it lives outside the workspace root.
  // apply_patch already allows this via the shared git common directory.
  if (denied && sharesGitCommonDir(defaultCwd, abs)) denied = undefined;
  if (denied) return { error: denied };
  if (!existsSync(abs)) return { error: `cwd does not exist: ${abs}` };
  try { if (!statSync(abs).isDirectory()) return { error: `cwd is not a directory: ${abs}` }; } catch (e) { return { error: `failed to access cwd ${abs}: ${String(e)}` }; }
  return { dir: abs };
}

export interface RunOptions { defaultCwd: string; shell?: string; signal?: AbortSignal; onData?: (chunk: { stream: "stdout" | "stderr"; text: string }) => void }

/**
 * bash.go prepareCmd: `cmd.WaitDelay = 2 * time.Second`. Go issue #21922 — a
 * background child that inherits the shell's stdout/stderr keeps those pipes
 * open after the shell itself exits, so waiting for stream EOF hangs until the
 * tool's own timeout. Go stops waiting 2s after process exit and closes the
 * pipes itself; without this a bounded command such as `svc start & echo ok`
 * burns the whole timeout and reports nothing.
 */
export const WAIT_DELAY_MS = 2000;

/**
 * Signal the whole process group, falling back to the direct child.
 *
 * A timeout or abort that kills only the direct shell leaves its children
 * running: `svc & long-task` orphans `svc`, which keeps holding the output
 * pipe and the port it bound. codex's exec.rs does the same thing via
 * `kill_child_process_group` before `start_kill`, and opencode's `killTree`
 * signals `-pid` before falling back to the child. Requires the child to have
 * been spawned detached so it leads its own group; otherwise `-pid` would
 * signal this agent's own group too.
 */
export function killProcessTree(child: { pid?: number; kill: (s: NodeJS.Signals) => boolean }, signal: NodeJS.Signals = "SIGKILL"): void {
  const pid = child.pid;
  if (pid && process.platform !== "win32") {
    try { process.kill(-pid, signal); return; } catch { /* group already gone, or not a group leader */ }
  }
  try { child.kill(signal); } catch { /* already exited */ }
}

/** bash.go runBatch: temp-file capture, /dev/null stdin, non-interactive env, 60s-min timeout. */
export function runSwarmBash(params: BashParams, options: RunOptions): Promise<BashOutcome | { error: string }> {
  const requestedSecs = Number.isFinite(params.timeout_seconds) ? Math.trunc(params.timeout_seconds as number) : 0;
  const effectiveSecs = requestedSecs > 0 ? Math.max(requestedSecs, 60) : 60;
  const wd = resolveWorkdir(params.cwd, options.defaultCwd);
  if ("error" in wd) return Promise.resolve({ error: invalidCwdMessage(wd.error) });
  const env: NodeJS.ProcessEnv = { ...process.env, TERM: "dumb", DEBIAN_FRONTEND: "noninteractive", CI: "true", PS1: "", PROMPT_COMMAND: "", ...(params.env ?? {}) };
  return new Promise((resolveP) => {
    const started = Date.now();
    // detached: the shell leads its own process group, so a timeout or abort can
    // signal the whole tree instead of orphaning children that keep the output
    // pipe (and any bound port) alive. bgprocess spawns the same way.
    const child = spawn(options.shell ?? "/bin/bash", ["-c", params.command], { cwd: wd.dir || undefined, env, stdio: ["ignore", "pipe", "pipe"], detached: process.platform !== "win32" });
    const out: Buffer[] = [], err: Buffer[] = [];
    child.stdout.on("data", (d: Buffer) => { out.push(d); options.onData?.({ stream: "stdout", text: d.toString("utf8") }); });
    child.stderr.on("data", (d: Buffer) => { err.push(d); options.onData?.({ stream: "stderr", text: d.toString("utf8") }); });
    let timedOut = false;
    const timer = setTimeout(() => { timedOut = true; killProcessTree(child); }, effectiveSecs * 1000);
    const onAbort = () => killProcessTree(child);
    options.signal?.addEventListener("abort", onAbort, { once: true });
    // "close" fires only once every pipe holder is gone, which an inherited
    // background child defers indefinitely; "exit" fires on process exit. Wait
    // for whichever comes first, then allow WAIT_DELAY_MS for in-flight output.
    let settled = false;
    const settle = (code: number | null, signal: NodeJS.Signals | null) => {
      if (settled) return;
      settled = true;
      clearTimeout(timer);
      clearTimeout(waitDelay);
      options.signal?.removeEventListener("abort", onAbort);
      // Go ProcessState.ExitCode() is -1 when killed by a signal.
      const exitCode = code ?? (signal ? -1 : 0);
      resolveP({ exitCode, durationMs: Date.now() - started, stdout: Buffer.concat(out).toString("utf8"), stderr: Buffer.concat(err).toString("utf8"), timedOut, requestedSecs, effectiveSecs, description: params.description, signal });
    };
    let waitDelay: ReturnType<typeof setTimeout>;
    child.on("close", (code, signal) => settle(code, signal));
    child.on("exit", (code, signal) => {
      waitDelay = setTimeout(() => settle(code, signal), WAIT_DELAY_MS);
      waitDelay.unref?.();
    });
    child.on("error", (e) => { clearTimeout(timer); settled = true; resolveP({ error: invalidCwdMessage(String(e)) }); });
  });
}
