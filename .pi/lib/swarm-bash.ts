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
import { existsSync, mkdirSync, mkdtempSync, statSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";

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

export interface BashParams { command: string; cwd?: string; env?: Record<string, string>; timeout_seconds?: number; description?: string }

const ANSI = /\x1b\[[0-9;:?]*[A-Za-z]/g;
export const stripANSI = (s: string) => (s.includes("\x1b") ? s.replace(ANSI, "") : s);

// tokens.go: charsPerToken = 4, Estimate = len/4 (1 when non-empty but < 4)
export const estimateTokens = (s: string) => (s === "" ? 0 : Math.max(1, Math.floor(Buffer.byteLength(s) / 4)));
const truncateTokens = (s: string, maxTokens: number) => Buffer.from(s).subarray(0, maxTokens * 4).toString("utf8");

/** Go %q for attribute values (ASCII-safe subset; Swarm attrs are ints/bools/short labels). */
function goQuote(value: string): string {
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

export interface Truncation { output: string; outputPath: string; truncated: boolean }
/** bash.go bashTruncateOutput: 2000-line and 12,500-token caps, full output spilled to disk. */
export function bashTruncateOutput(output: string, tempRoot = tmpdir()): Truncation {
  const maxLines = 2000, maxTokens = 12_500;
  const lineCount = (output.match(/\n/g) ?? []).length;
  if (lineCount <= maxLines && estimateTokens(output) <= maxTokens) return { output, outputPath: "", truncated: false };
  const dir = join(tempRoot, "swarm-tool-output");
  let outputPath = "";
  try { mkdirSync(dir, { recursive: true }); outputPath = join(mkdtempSync(join(dir, "bash-full-")), "bash-full.txt"); writeFileSync(outputPath, output); } catch { outputPath = ""; }
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

export interface BashOutcome { exitCode: number; durationMs: number; stdout: string; stderr: string; timedOut: boolean; requestedSecs: number; effectiveSecs: number; description?: string }

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
export function commandFailedMessage(exitCode: number, stdout: string, stderr: string, errorId = newErrorID()): string {
  const so = bashTruncateOutput(stripANSI(stdout)).output || "(no output)";
  const se = bashTruncateOutput(stripANSI(stderr)).output || "(no output)";
  return `Error executing bash: Command exited with code ${exitCode}: exit status ${exitCode}\n\nstderr:\n${se}\n\nstdout:\n${so} (error_id=${errorId})`;
}
export function timedOutMessage(effectiveSecs: number, exitCode: number, errorId = newErrorID()): string {
  return `Error executing bash: command timed out after ${effectiveSecs}s (exit_code=${exitCode}): context deadline exceeded (error_id=${errorId})`;
}
export function invalidCwdMessage(reason: string, errorId = newErrorID()): string {
  return `Error executing bash: ${reason} (error_id=${errorId})`;
}

export function resolveWorkdir(cwd: string | undefined, defaultCwd: string): { dir: string } | { error: string } {
  if (!cwd) return { dir: defaultCwd };
  const abs = resolve(cwd);
  if (!existsSync(abs)) return { error: `cwd does not exist: ${abs}` };
  try { if (!statSync(abs).isDirectory()) return { error: `cwd is not a directory: ${abs}` }; } catch (e) { return { error: `failed to access cwd ${abs}: ${String(e)}` }; }
  return { dir: abs };
}

export interface RunOptions { defaultCwd: string; shell?: string; signal?: AbortSignal }

/** bash.go runBatch: temp-file capture, /dev/null stdin, non-interactive env, 60s-min timeout. */
export function runSwarmBash(params: BashParams, options: RunOptions): Promise<BashOutcome | { error: string }> {
  const requestedSecs = Number.isFinite(params.timeout_seconds) ? Math.trunc(params.timeout_seconds as number) : 0;
  const effectiveSecs = requestedSecs > 0 ? Math.max(requestedSecs, 60) : 60;
  const wd = resolveWorkdir(params.cwd, options.defaultCwd);
  if ("error" in wd) return Promise.resolve({ error: invalidCwdMessage(wd.error) });
  const env: NodeJS.ProcessEnv = { ...process.env, TERM: "dumb", DEBIAN_FRONTEND: "noninteractive", CI: "true", PS1: "", PROMPT_COMMAND: "", ...(params.env ?? {}) };
  return new Promise((resolveP) => {
    const started = Date.now();
    const child = spawn(options.shell ?? "/bin/bash", ["-c", params.command], { cwd: wd.dir || undefined, env, stdio: ["ignore", "pipe", "pipe"] });
    const out: Buffer[] = [], err: Buffer[] = [];
    child.stdout.on("data", (d: Buffer) => out.push(d));
    child.stderr.on("data", (d: Buffer) => err.push(d));
    let timedOut = false;
    const timer = setTimeout(() => { timedOut = true; child.kill("SIGKILL"); }, effectiveSecs * 1000);
    const onAbort = () => child.kill("SIGKILL");
    options.signal?.addEventListener("abort", onAbort, { once: true });
    child.on("close", (code, signal) => {
      clearTimeout(timer);
      options.signal?.removeEventListener("abort", onAbort);
      // Go ProcessState.ExitCode() is -1 when killed by a signal.
      const exitCode = code ?? (signal ? -1 : 0);
      resolveP({ exitCode, durationMs: Date.now() - started, stdout: Buffer.concat(out).toString("utf8"), stderr: Buffer.concat(err).toString("utf8"), timedOut, requestedSecs, effectiveSecs, description: params.description });
    });
    child.on("error", (e) => { clearTimeout(timer); resolveP({ error: invalidCwdMessage(String(e)) }); });
  });
}
