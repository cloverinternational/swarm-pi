/**
 * TypeScript port of swarm-tui/internal/bgprocess.  This module deliberately
 * keeps presentation (including JSON field order) next to the process state:
 * ReadBackgroundCommand's output is a wire contract, not merely diagnostics.
 */
import { spawn, type ChildProcess } from "node:child_process";
import { existsSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { stripANSI, type BashParams } from "./swarm-bash.ts";
import { setRunningWork } from "../ui/running-work.ts";

export type ProcessState = "running" | "completed" | "failed" | "cancelled";
export interface BackgroundLine {
  line_number: number;
  timestamp: string;
  stream: "stdout" | "stderr";
  content: string;
}
interface ProcessRecord {
  id: string;
  command: string;
  cwd: string;
  env: Record<string, string>;
  state: ProcessState;
  child: ChildProcess;
  pid: number;
  startedMs: number;
  endedMs?: number;
  exitCode?: number;
  signal?: NodeJS.Signals | null;
  lines: BackgroundLine[];
  bytesWritten: number;
  stdout: string;
  stderr: string;
  /** Bytes read from the child but not yet turned into lines (executor.go polls every 50ms). */
  pendingStdout: Buffer[];
  pendingStderr: Buffer[];
  completion: Promise<void>;
  /** Resolves once the child has spawned or failed to (Node reports both asynchronously). */
  spawned: Promise<Error | undefined>;
  spawnError?: Error;
  backgrounded: boolean;
}

export interface BackgroundBashParams extends BashParams { background?: boolean }
export const BACKGROUND_BASH_DETACH = Symbol.for("pi-swarm-background-bash-detach");
export interface ReadBackgroundParams {
  task_id?: string;
  action?: string;
  from_line?: number;
  max_lines?: number;
  pattern?: string;
  stream?: string;
}
export interface ToolText { text: string; details?: Record<string, unknown> }
/** app_init.go completion/error callbacks → app_update.go bgProcessDoneMsg. */
export interface BackgroundDone { id: string; command: string; state: ProcessState; exitCode: number; outputPreview: string; outputTruncated: boolean }

/** sdk_integration.go: DefaultTimeout for the interactive bgprocess Bash tool. */
export const DEFAULT_BASH_TIMEOUT_SECONDS = 5 * 60;
const BG_OUTPUT_PREVIEW_MAX_BYTES = 4096;

/** app_update.go bgOutputPreview (byte-based head/tail). */
export function bgOutputPreview(output: Buffer | string): { preview: string; truncated: boolean } {
  const bytes = Buffer.isBuffer(output) ? output : Buffer.from(output, "utf8");
  if (bytes.length === 0) return { preview: "", truncated: false };
  if (bytes.length <= BG_OUTPUT_PREVIEW_MAX_BYTES) return { preview: bytes.toString("utf8"), truncated: false };
  const half = BG_OUTPUT_PREVIEW_MAX_BYTES / 2;
  return { preview: `${bytes.subarray(0, half).toString("utf8")}\n\n... [truncated ${bytes.length - BG_OUTPUT_PREVIEW_MAX_BYTES} bytes] ...\n\n${bytes.subarray(bytes.length - half).toString("utf8")}`, truncated: true };
}

/** app_update.go bgProcessDoneMsg → the "[BACKGROUND] …" notification text. */
export function formatBackgroundDone(done: BackgroundDone): string {
  let statusDesc: string;
  switch (done.state) {
    case "completed": statusDesc = `Background command completed successfully (exit code ${done.exitCode})`; break;
    case "failed": statusDesc = `Background command failed (exit code ${done.exitCode})`; break;
    default: statusDesc = `Background command ${done.state}`;
  }
  let cmdPreview = done.command;
  if (Buffer.byteLength(cmdPreview) > 120) cmdPreview = `${Buffer.from(cmdPreview).subarray(0, 117).toString("utf8")}...`;
  const outputSection = done.outputPreview !== "" ? `\n\noutput:\n${done.outputPreview}` : "";
  const followup = done.outputTruncated ? ` Output truncated above — call ReadBackgroundCommand with task_id ${JSON.stringify(done.id)} for the full text.` : "";
  return `[BACKGROUND] task_id=${done.id} command=${cmdPreview} status=${done.state}${outputSection}\n\n${statusDesc}.${followup}`;
}

/** Go os/exec ExitError.Error() for a non-zero exit, as stored in ProcessResult.Error. */
const goExitStatus = (code: number, signal: NodeJS.Signals | null) => signal ? `signal: ${signal.toLowerCase().replace(/^sig/, "")}` : `exit status ${code}`;

let stdbufPath: string | undefined;
/** bash_cmd_builder.go detectBufferingMethod: stdbuf -oL -eL when on PATH. */
function detectStdbuf(): string {
  if (stdbufPath !== undefined) return stdbufPath;
  stdbufPath = "";
  for (const dir of (process.env.PATH ?? "").split(":")) {
    if (!dir) continue;
    const candidate = join(dir, "stdbuf");
    if (existsSync(candidate)) { stdbufPath = candidate; break; }
  }
  return stdbufPath;
}
/** executor.go detectShell (unix). */
const detectShell = () => process.env.SHELL || (existsSync("/bin/bash") ? "/bin/bash" : "/bin/sh");

/** time.Duration.String for positive durations used by this tool. */
export function goDuration(seconds: number): string {
  let ns = Math.trunc(seconds * 1e9);
  const h = Math.floor(ns / 3_600_000_000_000); ns -= h * 3_600_000_000_000;
  const m = Math.floor(ns / 60_000_000_000); ns -= m * 60_000_000_000;
  const s = Math.floor(ns / 1_000_000_000); ns -= s * 1_000_000_000;
  let fraction = "";
  if (ns) fraction = `.${String(ns).padStart(9, "0").replace(/0+$/, "")}`;
  if (h) return `${h}h${m}m${s}${fraction}s`;
  if (m) return `${m}m${s}${fraction}s`;
  return `${s}${fraction}s`;
}

const pad2 = (n: number) => String(n).padStart(2, "0");
const goZone = (date: Date) => { const offset = -date.getTimezoneOffset(); return offset === 0 ? "Z" : `${offset > 0 ? "+" : "-"}${pad2(Math.floor(Math.abs(offset) / 60))}:${pad2(Math.abs(offset) % 60)}`; };
const goLocalDate = (date: Date) => `${date.getFullYear()}-${pad2(date.getMonth() + 1)}-${pad2(date.getDate())}`;
const goLocalClock = (date: Date) => `${pad2(date.getHours())}:${pad2(date.getMinutes())}:${pad2(date.getSeconds())}`;
/** time.Now() is local; Format(time.RFC3339) keeps the local offset (no fraction). */
const rfc3339 = (ms: number) => { const d = new Date(ms); return `${goLocalDate(d)}T${goLocalClock(d)}${goZone(d)}`; };
/** time.Time.MarshalJSON: RFC3339Nano, local offset, trailing fraction zeros trimmed (JS has millis). */
const rfc3339Nano = (ms: number) => { const d = new Date(ms); const frac = d.getMilliseconds() ? `.${String(d.getMilliseconds()).padStart(3, "0").replace(/0+$/, "")}` : ""; return `${goLocalDate(d)}T${goLocalClock(d)}${frac}${goZone(d)}`; };
/** encoding/json escapes <, >, & (HTML-safe by default) and U+2028/U+2029; JSON.stringify does not. */
export const goJSON = (value: unknown, indent?: number) => JSON.stringify(value, null, indent)
  .replace(/[<>&\u2028\u2029]/g, (c) => `\\u${c.charCodeAt(0).toString(16).padStart(4, "0")}`);
const jsonIndent = (value: unknown) => goJSON(value, 2);

function regexpError(pattern: string, error: unknown): string {
  // Go uses RE2. Cover its stable parser wording for the malformed forms most
  // often returned by model calls; otherwise preserve the JS parser reason.
  if (pattern === "[") return "error parsing regexp: missing closing ]: `[`";
  if (pattern.endsWith("\\")) return `error parsing regexp: trailing backslash at end of expression: \`${pattern}\``;
  const reason = String((error as Error)?.message ?? error).replace(/^Invalid regular expression: \/.+\/: /, "");
  return `error parsing regexp: ${reason}`;
}

export class SwarmBackgroundProcessManager {
  private readonly processes = new Map<string, ProcessRecord>();
  private doneListeners: Array<(done: BackgroundDone) => void> = [];
  private foreground?: { rec: ProcessRecord; detach: () => void };

  /** manager.go SetCompletionCallback/SetErrorCallback, already shaped like bgProcessDoneMsg. */
  onDone(listener: (done: BackgroundDone) => void): void { this.doneListeners.push(listener); }
  requestBackground(): boolean {
    if (!this.foreground || this.foreground.rec.state !== "running") return false;
    this.foreground.rec.backgrounded = true;
    this.foreground.detach();
    return true;
  }
  private emitDone(done: BackgroundDone): void { for (const listener of this.doneListeners) { try { listener(done); } catch { /* UI side effect */ } } }
  /** manager.go handleStateChange: only backgrounded processes notify. */
  private notifyTerminal(rec: ProcessRecord): void {
    if (!rec.backgrounded) return;
    if (rec.state === "failed") {
      // onError receives result.Error (the exec error), not the output.
      const error = rec.spawnError ? rec.spawnError.message : goExitStatus(rec.exitCode ?? -1, rec.signal ?? null);
      this.emitDone({ id: rec.id, command: rec.command, state: "failed", exitCode: rec.exitCode ?? -1, outputPreview: bgOutputPreview(error).preview, outputTruncated: false });
      return;
    }
    const { preview, truncated } = bgOutputPreview(this.outputBytes(rec));
    this.emitDone({ id: rec.id, command: rec.command, state: rec.state, exitCode: (rec.exitCode ?? -1) >= 0 ? rec.exitCode! : -1, outputPreview: preview, outputTruncated: truncated });
  }
  /** executor.go Wait: every tailed line + "\n", in arrival order. */
  private outputBytes(rec: ProcessRecord): Buffer { return Buffer.from(rec.lines.map(line => `${line.content}\n`).join(""), "utf8"); }

  private newID(): string {
    // Date.now has only millisecond resolution; hrtime supplies the sub-ms
    // digits needed by Go's bg-<UnixNano>-<pid> identifier.
    const nano = BigInt(Date.now()) * 1_000_000n + process.hrtime.bigint() % 1_000_000n;
    return `bg-${nano}-${process.pid}`;
  }

  /**
   * executor.go tailOutputFile/readFileChunk: each 50ms poll reads whatever
   * the child wrote since the last poll and splits THAT chunk into lines, so a
   * partial line and its continuation across polls become two lines. The
   * stderr tailer (started last) consistently reads first on the final flush.
   */
  private poll(rec: ProcessRecord): void {
    for (const stream of ["stderr", "stdout"] as const) {
      const pending = stream === "stdout" ? rec.pendingStdout : rec.pendingStderr;
      if (!pending.length) continue;
      const chunk = Buffer.concat(pending.splice(0));
      this.append(rec, stream, chunk);
    }
  }

  private append(rec: ProcessRecord, stream: "stdout" | "stderr", chunk: Buffer): void {
    const text = chunk.toString("utf8");
    if (stream === "stdout") rec.stdout += text; else rec.stderr += text;
    // Go's tailer turns every read fragment into one or more OutputLines and
    // excludes newline bytes from Size().
    let start = 0;
    for (let i = 0; i < text.length; i++) {
      if (text[i] !== "\n") continue;
      this.addLine(rec, stream, text.slice(start, i));
      start = i + 1;
    }
    if (start < text.length) this.addLine(rec, stream, text.slice(start));
  }

  private addLine(rec: ProcessRecord, stream: "stdout" | "stderr", content: string): void {
    rec.lines.push({ line_number: rec.lines.length + 1, timestamp: rfc3339Nano(Date.now()), stream, content });
    rec.bytesWritten += Buffer.byteLength(content);
  }

  spawn(params: BackgroundBashParams, defaultCwd: string): ProcessRecord {
    const command = typeof params.command === "string" ? params.command : "";
    // executor.go only sets cmd.Dir when WorkDir != "": the process otherwise
    // inherits the TUI's own working directory.
    const cwd = params.cwd ? resolve(params.cwd) : defaultCwd;
    const env = Object.fromEntries(Object.entries(params.env ?? {}).filter((x): x is [string, string] => typeof x[1] === "string"));
    const id = this.newID();
    const startedMs = Date.now();
    const shell = detectShell();
    const stdbuf = detectStdbuf();
    const argv = stdbuf ? [stdbuf, "-oL", "-eL", shell, "-c", command] : [shell, "-c", command];
    const child = spawn(argv[0], argv.slice(1), { cwd, env: { ...process.env, ...env }, stdio: ["ignore", "pipe", "pipe"], detached: process.platform !== "win32" });
    let finish!: () => void;
    const completion = new Promise<void>(r => { finish = r; });
    let settleSpawn!: (error: Error | undefined) => void;
    const spawned = new Promise<Error | undefined>(r => { settleSpawn = r; });
    const rec: ProcessRecord = {
      id, command, cwd: params.cwd ?? "", env, state: "running", child, pid: child.pid ?? 0,
      startedMs, lines: [], bytesWritten: 0, stdout: "", stderr: "", pendingStdout: [], pendingStderr: [], completion, spawned, backgrounded: false,
    };
    this.processes.set(id, rec);
    setRunningWork({ id, kind: "bash", label: params.description || "Background Bash", status: "running", startedAt: startedMs, detail: command });
    child.stdout?.on("data", (d: Buffer) => rec.pendingStdout.push(d));
    child.stderr?.on("data", (d: Buffer) => rec.pendingStderr.push(d));
    const poller = setInterval(() => this.poll(rec), 50);
    poller.unref?.();
    child.once("spawn", () => settleSpawn(undefined));
    child.on("error", (error: NodeJS.ErrnoException) => {
      // Go reports a missing binary/cwd as os.PathError from fork/exec against
      // the resolved executable; the manager drops the record (Spawn fails).
      const reason = error.code === "ENOENT" ? "no such file or directory" : error.code === "EACCES" ? "permission denied" : (error.message ?? String(error));
      rec.spawnError = new Error(`execute [${id}]: failed to start command: fork/exec ${argv[0]}: ${reason}`);
      rec.endedMs = Date.now(); rec.exitCode = -1;
      if (rec.state !== "cancelled") rec.state = "failed";
      this.processes.delete(id);
      clearInterval(poller);
      settleSpawn(rec.spawnError);
      finish();
    });
    child.on("close", (code, signal) => {
      clearInterval(poller);
      this.poll(rec);
      rec.endedMs = Date.now();
      rec.signal = signal;
      rec.exitCode = code ?? (signal ? -1 : 0);
      if (rec.state !== "cancelled") rec.state = rec.exitCode === 0 ? "completed" : "failed";
      setRunningWork({ id: rec.id, kind: "bash", label: rec.command, status: rec.state, startedAt: rec.startedMs, endedAt: rec.endedMs, detail: rec.command, output: rec.lines.slice(-12).map(line => line.content).join("\n") });
      finish();
      this.notifyTerminal(rec);
    });
    return rec;
  }

  /**
   * bash_tool.go ExecuteStreaming — the entry point the SDK agent uses for
   * StreamingTools, so (unlike Execute) a missing timeout_seconds is not an
   * error: it falls back to DefaultTimeout (5 minutes).
   */
  async executeBash(params: BackgroundBashParams, defaultCwd: string, signal?: AbortSignal): Promise<ToolText> {
    if (typeof params?.command !== "string" || params.command === "") throw new Error("command is required");
    let timeoutSec = typeof params?.timeout_seconds === "number" ? params.timeout_seconds : 0;
    if (!(timeoutSec > 0)) timeoutSec = DEFAULT_BASH_TIMEOUT_SECONDS;
    // AllowExplicitBackground is false in sdk_integration.go. The unadvertised
    // background member is consequently ignored, exactly as encoding/json does.
    let rec: ProcessRecord;
    try { rec = this.spawn(params, defaultCwd); }
    catch (e) { throw new Error(`failed to spawn process: ${String((e as Error).message ?? e)}`); }
    const spawnError = await rec.spawned;
    if (spawnError) {
      // manager.go Spawn: onError fires for the failed start (TUI queues a
      // "[BACKGROUND] … status=failed" system notification), then the error.
      this.emitDone({ id: rec.id, command: rec.command, state: "failed", exitCode: -1, outputPreview: bgOutputPreview(spawnError.message).preview, outputTruncated: false });
      throw new Error(`failed to spawn process: ${spawnError.message}`);
    }

    // Explicit background mode returns immediately, matching BackgroundTask's
    // async contract. The process remains tracked by ReadBackgroundCommand and
    // emits its normal completion notification when it exits.
    if (params.background === true) {
      rec.backgrounded = true;
      return { text: goJSON({
        backgrounded: true,
        message: "Command launched in the background. Use ReadBackgroundCommand with task_id to monitor output or cancel it.",
        status: "running",
        task_id: rec.id,
        timeout_seconds: timeoutSec,
      }), details: { task_id: rec.id, background: true, background_reason: "explicit" } };
    }

    const idle = new Promise<"idle">(resolveIdle => {
      let lastSize = -1;
      let lastChange = Date.now();
      const intervalMs = Math.min(1000, timeoutSec * 250);
      const timer = setInterval(() => {
        if (rec.state !== "running") return clearInterval(timer);
        if (lastSize < 0 || rec.bytesWritten > lastSize) {
          lastSize = rec.bytesWritten; lastChange = Date.now(); return;
        }
        if (Date.now() - lastChange >= timeoutSec * 1000) {
          clearInterval(timer); resolveIdle("idle");
        }
      }, intervalMs);
      rec.completion.finally(() => clearInterval(timer));
    });
    const aborted = new Promise<"abort">(r => signal?.addEventListener("abort", () => r("abort"), { once: true }));
    let detach!: () => void;
    const detached = new Promise<"background">(resolve => { detach = () => resolve("background"); });
    this.foreground = { rec, detach };
    const winner = await Promise.race([rec.completion.then(() => "done" as const), idle, aborted, detached]);
    if (this.foreground?.rec === rec) this.foreground = undefined;
    if (winner === "abort") {
      await this.cancelRecord(rec);
      throw new Error("command execution error: context canceled");
    }
    if (winner === "idle" || winner === "background") {
      rec.backgrounded = true;
      const text = goJSON({
        backgrounded: true,
        message: winner === "background" ? "Command backgrounded by Ctrl+B. Use ReadBackgroundCommand with task_id to monitor output or cancel it." : `Command idle for ${goDuration(timeoutSec)} (no output) and was auto-backgrounded. Use ReadBackgroundCommand to check status — look at metadata.seconds_since_last_output to decide if it is still healthy.`,
        status: "running",
        task_id: rec.id,
        timeout_seconds: timeoutSec,
      });
      return { text, details: { task_id: rec.id, background: true, background_reason: winner } };
    }
    // bash_tool.go completedProcessResult: plain text, IsError on non-zero.
    const exitCode = rec.exitCode ?? -1;
    const output = completedOutput(this.outputBytes(rec).toString("utf8"), exitCode);
    if (exitCode !== 0) throw new Error(output);
    return { text: output, details: { exit_code: exitCode, duration_ms: (rec.endedMs ?? Date.now()) - rec.startedMs, command: params.command, ...(params.description ? { description: params.description } : {}) } };
  }

  private get(id: string, op = "format_output"): ProcessRecord {
    const rec = this.processes.get(id);
    if (!rec) throw new Error(`${op} [${id}]: process not found: process not found`);
    return rec;
  }

  private formatted(rec: ProcessRecord, params: ReadBackgroundParams, statusOnly: boolean): Record<string, unknown> {
    const maxLines = statusOnly ? 0 : (typeof params.max_lines === "number" && Math.trunc(params.max_lines) > 0 ? Math.min(Math.trunc(params.max_lines), 1000) : 100);
    const specifiedFrom = typeof params.from_line === "number" && Math.trunc(params.from_line) >= 1;
    const from = specifiedFrom ? Math.trunc(params.from_line!) : Math.max(1, rec.lines.length - maxLines + 1);
    let regex: RegExp | undefined;
    if (params.pattern) {
      try { regex = new RegExp(params.pattern); }
      catch (e) { throw new Error(`output [${rec.id}]: failed to get output lines: invalid pattern: ${regexpError(params.pattern, e)}`); }
    }
    const selected = statusOnly ? [] : rec.lines.slice(from - 1)
      .filter(line => (!params.stream || line.stream === params.stream) && (!regex || regex.test(line.content)))
      .slice(0, maxLines);
    const truncated = maxLines > 0 && rec.lines.length > maxLines;
    let outputFile = "";
    if (truncated && rec.lines.length) {
      outputFile = join(tmpdir(), `bgprocess-output-${rec.id.replace(/[/: ]/g, "-")}.txt`);
      try { writeFileSync(outputFile, rec.lines.map(x => `${x.content}\n`).join(""), { mode: 0o644 }); } catch { outputFile = ""; }
    }
    const last = rec.lines.at(-1);
    const value: Record<string, unknown> = {
      task_id: rec.id,
      status: rec.state,
      command: rec.command,
      ...(rec.pid ? { pid: rec.pid } : {}),
      started_at: rfc3339(rec.startedMs),
      ...(rec.endedMs ? { ended_at: rfc3339(rec.endedMs) } : {}),
      duration_seconds: ((rec.endedMs ?? Date.now()) - rec.startedMs) / 1000,
      ...(rec.exitCode !== undefined ? { exit_code: rec.exitCode } : {}),
      ...(!statusOnly && selected.length ? { output: selected } : {}),
      metadata: {
        cwd: rec.cwd,
        ...(Object.keys(rec.env).length ? { env: rec.env } : {}),
        total_lines: rec.lines.length,
        output_truncated: truncated,
        ...(outputFile ? { output_file: outputFile } : {}),
        ...(last ? {
          last_output_at: rfc3339(Date.parse(last.timestamp)),
          seconds_since_last_output: Math.max(0, (Date.now() - Date.parse(last.timestamp)) / 1000),
        } : {}),
        bytes_written: rec.bytesWritten,
      },
    };
    return value;
  }

  async read(params: ReadBackgroundParams): Promise<ToolText> {
    const action = typeof params?.action === "string" && params.action ? params.action : "output";
    const id = typeof params?.task_id === "string" ? params.task_id : "";
    // ReadBackgroundCommand is a plain (non-streaming) tool, so the registry
    // runs Validate first (registry_impl.go) and wraps its error as
    // sdkerr "validation failed for <tool>: <err>"; Execute's own errors are
    // NewErrorResult text. Bash (streaming) never reaches Validate.
    const validation = (message: string) => new Error(`validation failed for ReadBackgroundCommand: ${message}`);
    if (!["status", "output", "cancel", "list"].includes(action)) throw validation(`invalid action: ${action} (must be status, output, cancel, or list)`);
    if (action !== "list" && !id) throw validation(`task_id is required for action "${action}"`);
    if (action === "list") {
      const processes = [...this.processes.values()].map(rec => ({
        task_id: rec.id,
        command: Buffer.byteLength(rec.command) > 50 ? Buffer.from(rec.command).subarray(0, 50).toString("utf8") + "..." : rec.command,
        status: rec.state,
        started_at: `${goLocalDate(new Date(rec.startedMs))} ${goLocalClock(new Date(rec.startedMs))}`,
        duration: goDuration(Math.round(((rec.endedMs ?? Date.now()) - rec.startedMs) / 1000)),
        ...(rec.exitCode !== undefined ? { exit_code: rec.exitCode } : {}),
      }));
      return { text: jsonIndent({ count: processes.length, processes }) };
    }
    if (action !== "status" && action !== "output" && action !== "cancel") throw new Error(`unknown action: ${action}`);
    if (action === "cancel") {
      const rec = this.get(id, "cancel");
      await this.cancelRecord(rec);
      return { text: `Process ${id} has been cancelled` };
    }
    const rec = this.get(id);
    return { text: jsonIndent(this.formatted(rec, params, action === "status")) };
  }

  private async cancelRecord(rec: ProcessRecord): Promise<void> {
    if (rec.state !== "running") return;
    rec.state = "cancelled";
    try {
      if (process.platform !== "win32" && rec.pid) process.kill(-rec.pid, "SIGTERM");
      else rec.child.kill("SIGTERM");
    } catch { /* process raced to completion */ }
    await Promise.race([rec.completion, new Promise<void>(r => setTimeout(r, 5000))]);
    if (rec.endedMs === undefined) {
      try {
        if (process.platform !== "win32" && rec.pid) process.kill(-rec.pid, "SIGKILL");
        else rec.child.kill("SIGKILL");
      } catch { /* already gone */ }
      await rec.completion;
    }
    rec.state = "cancelled";
  }
}

/** bash_tool.go completedProcessResult over ProcessResult.Output. */
export const completedOutput = (rawOutput: string, exitCode: number) => {
  let output = stripANSI(rawOutput);
  if (!output) output = exitCode === 0 ? "Command completed successfully (no output)" : "Command failed with no output";
  return exitCode === 0 ? output : `Exit code: ${exitCode}\n${output}`;
};
