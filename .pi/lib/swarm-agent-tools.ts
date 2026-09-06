import { randomUUID } from "node:crypto";
import { closeSync, mkdirSync, openSync } from "node:fs";
import { appendFile, mkdir, readFile } from "node:fs/promises";
import { homedir } from "node:os";
import { dirname, join } from "node:path";
import type { AgentManager, AgentResult, BackgroundHandle } from "../../agents/src/index.ts";

export type ToolResult = { text: string; isError?: boolean; details?: unknown };
export type AgentToolParams = Record<string, any>;
type Status = "running" | "completed" | "failed" | "cancelled";
type Question = { id: string; question: string; context: string; askedAt: number; answer?: string; resolve?: (answer: string) => void };
type Entry = {
  id: string; task: string; startedAt: number; outputFile: string; handle: BackgroundHandle;
  done: Promise<AgentResult>; result?: AgentResult; delegate?: boolean; question?: Question;
};

export const AGENT_MANAGER_SYMBOL = Symbol.for("pi-swarm.agent-manager");
export const AGENT_TOOLS_SYMBOL = Symbol.for("pi-swarm.agent-tools");
const MAX_OUTPUT = 8 * 1024 * 1024;
const divider = "───────────────────────────────────────────────────────────────\n";
const goDuration = (ms: number) => ms < 1000 ? `${Math.max(0, Math.trunc(ms))}ms` : `${(ms / 1000).toFixed(ms % 1000 ? 3 : 0).replace(/0+$/, "").replace(/\.$/, "")}s`;
const json = (v: unknown) => JSON.stringify(v, null, 2);
const outputPath = (id: string) => join(process.env.XDG_CACHE_HOME || join(homedir(), ".cache"), "swarm", "tasks", `${id}.output`);
const terminal = (s: Status) => s !== "running";
const id8 = () => randomUUID().slice(0, 8);

function parseChunk(data: Buffer): string {
  const parts: string[] = [];
  for (const raw of data.toString("utf8").trim().split("\n")) {
    if (!raw.trim()) continue;
    try {
      const ev = JSON.parse(raw);
      if (ev.type === "final") {
        if (ev.content) parts.push(String(ev.content));
        if (ev.error) parts.push(`[error] ${ev.error}`);
      } else if (ev.type === "content" || ev.type === "chunk") parts.push(String(ev.content ?? ev.text ?? ""));
      else if (ev.type === "tool_result") parts.push(String(ev.output ?? ""));
    } catch { parts.push(raw); }
  }
  return parts.join("");
}

/** subagent.go getAllAvailableAgents builtin ids (custom definitions are merged in and sorted). */
export const BUILTIN_AGENT_IDS = ["general-assistant", "code-reviewer", "research-agent", "explore", "background-worker", "agent_constructor", "code_formatter", "text_summarizer", "data_validator", "error_analyzer", "question_answerer"];
export class AgentToolValidationError extends Error {}
export class SwarmAgentTools {
  availableAgents(): string[] {
    const custom = typeof (this.manager as any).profileNames === "function" ? (this.manager as any).profileNames() as string[] : [];
    return [...new Set([...custom, ...BUILTIN_AGENT_IDS])].sort((a, b) => (a < b ? -1 : a > b ? 1 : 0));
  }
  private entries = new Map<string, Entry>();
  constructor(readonly manager: AgentManager) {}

  private fail(message: string): never { throw new Error(message); }
  /** Errors Swarm raises from the tool's Validate() (registry_impl.go wraps them as "validation failed for X: …"). */
  private invalid(message: string): never { throw new AgentToolValidationError(message); }
  private entry(id: string): Entry | undefined { return this.entries.get(id); }
  private status(e: Entry): Status { return (e.result?.status ?? "running") as Status; }

  private spawn(task: string, opts: AgentToolParams, id: string, delegate = false): Entry {
    const file = outputPath(id);
    mkdirSync(dirname(file), { recursive: true });
    closeSync(openSync(file, "a"));
    const handle = this.manager.spawn({
      id, task, profile: opts.agent_id || opts.preset, preset: opts.preset,
      model: opts.model, background: true,
    });
    const entry: Entry = { id, task, startedAt: Date.now(), outputFile: file, handle, delegate, done: undefined! };
    entry.done = handle.wait().then(async result => {
      entry.result = result;
      const record = result.status === "completed"
        ? { type: "final", ts: Date.now(), content: result.output ?? "" }
        : { type: "final", ts: Date.now(), error: result.error ?? (result.status === "cancelled" ? "cancelled" : "") };
      await mkdir(dirname(file), { recursive: true });
      await appendFile(file, JSON.stringify(record) + "\n");
      return result;
    });
    this.entries.set(id, entry);
    return entry;
  }

  async backgroundTask(p: AgentToolParams): Promise<ToolResult> {
    if (typeof p.task !== "string" || p.task === "") this.invalid("task parameter is required");
    const id = p.agent_id || `bg-${process.hrtime.bigint()}`;
    const e = this.spawn(p.task, p, id);
    return { text: json({
      status: "async_launched", agent_id: id, description: p.task, output_file: e.outputFile,
      message: `Background agent launched (agent_id: ${id}). Do not duplicate this agent's work — avoid the same files or topics. Work on non-overlapping tasks, or tell the user what you launched and end your response. Use TaskOutput with agent_id=${JSON.stringify(id)} and action="result" to check progress or retrieve output. You can also Read the output_file directly for a live tail while the agent is running.`,
      can_read_output: true,
    }) };
  }

  async subagent(p: AgentToolParams): Promise<ToolResult> {
    // subagent.go Validate()
    if (typeof p.task !== "string" || p.task === "") this.invalid("task parameter is required");
    if (p.agent_id && p.preset) this.invalid("Cannot specify both 'agent_id' and 'preset'. The 'preset' parameter is deprecated - use 'agent_id' instead.");
    if (p.run_in_background && Number(p.auto_background_seconds) > 0) this.invalid("Cannot specify both 'run_in_background' and 'auto_background_seconds'. Choose one background mode.");
    if (Number(p.auto_background_seconds) < 0) this.invalid("auto_background_seconds must be positive");
    if (p.agent_id === "agent_constructor" && (p.run_in_background || Number(p.auto_background_seconds) > 0)) {
      this.invalid("agent_constructor cannot run in background mode: its output must be parsed and persisted synchronously. Remove 'run_in_background' or 'auto_background_seconds'.");
    }
    // subagent.go Execute: unknown agent ids list every custom + builtin id, sorted.
    if (typeof p.agent_id === "string" && p.agent_id !== "" && !this.availableAgents().includes(p.agent_id)) {
      this.fail(`Agent '${p.agent_id}' not found. Available agents: ${this.availableAgents().join(", ")}`);
    }
    const prefix = p.agent_id || p.preset || "subagent";
    const e = this.spawn(p.task, p, `${prefix}-${id8()}`);
    if (p.run_in_background) return this.backgroundLaunch(e);
    if (Number(p.auto_background_seconds) > 0) {
      try {
        const result = await Promise.race([
          e.done,
          new Promise<undefined>(resolve => setTimeout(resolve, Number(p.auto_background_seconds) * 1000)),
        ]);
        if (!result) return this.backgroundLaunch(e);
        return { text: result.status === "completed" ? (result.output ?? "") : `Sub-agent execution failed: ${result.error ?? "unknown error"}` };
      } catch (err) { return { text: `Sub-agent execution failed: ${String(err)}` }; }
    }
    const result = await e.done;
    let out = result.status === "completed" ? (result.output ?? "") : `Sub-agent execution failed: ${result.error ?? "unknown error"}`;
    if (Buffer.byteLength(out) > MAX_OUTPUT) {
      const head = Buffer.from(out).subarray(0, MAX_OUTPUT).toString("utf8");
      out = `[${Math.trunc((Buffer.byteLength(out) - Buffer.byteLength(head)) / 1024)}KB of output truncated — sub-agent produced more than the 8MB result limit]\n\n${head}`;
    }
    return { text: out };
  }

  private backgroundLaunch(e: Entry): ToolResult {
    return { text: json({
      status: "async_launched", agent_id: e.id, description: e.task, output_file: e.outputFile,
      message: `Background agent launched (agent_id: ${e.id}). Do not duplicate this agent's work. Use TaskOutput with agent_id=${JSON.stringify(e.id)} to retrieve results.`,
      can_read_output: true,
    }) };
  }

  async taskOutput(p: AgentToolParams): Promise<ToolResult> {
    const action = p.action || "status";
    if (!["status", "result", "cancel"].includes(action)) this.fail(`invalid action: ${action} (valid: status, result, cancel)`);
    if (action === "status") {
      if (!p.agent_id) {
        if (!this.entries.size) return { text: "No background agents currently tracked." };
        const agents = [...this.entries.values()].map(e => ({ agent_id: e.id, task: e.task, status: this.status(e), duration: goDuration(Date.now() - e.startedAt), output_file: e.outputFile }));
        return { text: json({ total_agents: agents.length, agents }) };
      }
      const e = this.entry(p.agent_id);
      if (!e) return { text: `Agent not found: ${p.agent_id}` };
      const s = this.status(e);
      return { text: json({
        agent_id: e.id, status: s, start_time: new Date(e.startedAt).toISOString(),
        duration: goDuration(e.result?.durationMs ?? Date.now() - e.startedAt), output_file: e.outputFile,
        message: s === "running" ? `Agent is running (elapsed: ${goDuration(Date.now() - e.startedAt)}). Call action='result' to read partial output.`
          : s === "completed" ? "Agent completed. Call action='result' to retrieve output."
          : s === "failed" ? "Agent failed. Call action='result' to retrieve partial output."
          : "Agent was cancelled. Call action='result' to retrieve partial output.",
        ...(e.result?.error ? { error: e.result.error } : {}),
      }) };
    }
    if (!p.agent_id) return { text: `Error: agent_id is required for ${action} action` };
    const e = this.entry(p.agent_id);
    if (!e) return { text: `Agent not found: ${p.agent_id}` };
    if (action === "cancel") {
      const ok = e.handle.cancel();
      return ok ? { text: json({ agent_id: e.id, status: "cancelled", message: "Agent cancellation requested." }) }
        : { text: `Failed to cancel agent '${e.id}': agent already finished` };
    }
    return this.readResult(e, Number.isInteger(p.offset) ? p.offset : 0);
  }

  private async readResult(e: Entry, requestedOffset: number): Promise<ToolResult> {
    let data = Buffer.alloc(0);
    try { data = await readFile(e.outputFile); } catch {}
    if (!data.length) {
      const s = this.status(e);
      return { text: `agent_id: ${e.id}\nstatus:   ${s}\n${s === "running" ? `elapsed: ${goDuration(Date.now() - e.startedAt)}\nAgent is still running. Check back later or use action='status' to monitor.` : divider + (e.result?.output || (e.result?.error ? `error: ${e.result.error}\n` : "(no output produced)"))}` };
    }
    const offset = Math.max(0, requestedOffset);
    const chunk = data.subarray(Math.min(offset, data.length), Math.min(data.length, offset + MAX_OUTPUT));
    const newOffset = Math.min(offset, data.length) + chunk.length;
    const s = this.status(e);
    return { text: `agent_id:    ${e.id}\nstatus:      ${s}\noutput_file: ${e.outputFile}\noffset:      ${offset}\nnew_offset:  ${newOffset}\ntotal_bytes: ${data.length}\n${terminal(s) ? `duration:    ${goDuration(e.result?.durationMs ?? Date.now() - e.startedAt)}\n` : `elapsed:     ${goDuration(Date.now() - e.startedAt)}\n`}${divider}${chunk.length ? parseChunk(chunk) : (s === "running" ? "(agent is running — no output yet at this offset)\n" : "(no output at this offset)\n")}` };
  }

  async waitForAgent(p: AgentToolParams): Promise<ToolResult> {
    if (!p.agent_id) this.fail("agent_id parameter is required");
    const timeout = p.timeout_seconds === undefined ? 600 : Number(p.timeout_seconds);
    if (timeout < 0) this.fail("timeout_seconds must be zero or greater");
    const e = this.entry(p.agent_id);
    if (!e) this.fail(`agent '${p.agent_id}' not found: agent '${p.agent_id}' not found`);
    const result = await this.wait(e, timeout);
    if (!result) return { text: json({ wait_status: "timeout", timeout_seconds: timeout, agent: { agent_id: e.id, status: this.status(e) }, message: "Wait timeout; agent execution was not cancelled." }) };
    return { text: json(this.resultObject(result, e.outputFile)) };
  }

  async multiWait(p: AgentToolParams): Promise<ToolResult> {
    // multi_agent_wait.go Validate()
    if (!("agent_ids" in p)) this.invalid("agent_ids parameter is required");
    if (!Array.isArray(p.agent_ids)) this.invalid("agent_ids must be an array");
    if (!p.agent_ids.length) this.invalid("agent_ids array cannot be empty");
    p.agent_ids.forEach((id: unknown, i: number) => { if (typeof id !== "string") this.invalid(`agent_ids[${i}] must be a string`); });
    const timeout = p.timeout_seconds === undefined ? 600 : Number(p.timeout_seconds);
    if (timeout < 0) this.fail("timeout_seconds must be zero or greater");
    const entries = p.agent_ids.map((id: string) => {
      const e = this.entry(id); if (!e) this.fail(`agent '${id}' not found: agent '${id}' not found`); return e;
    });
    const all = Promise.all(entries.map(e => e.done));
    const results = timeout === 0 ? await all : await Promise.race([all, new Promise<undefined>(r => setTimeout(r, timeout * 1000))]);
    const waitStatus = results ? "completed" : "timeout";
    const states = entries.map(e => e.result ? this.resultObject(e.result, e.outputFile, false) : ({ agent_id: e.id, status: "running" }));
    const completed = entries.filter(e => e.result).length;
    return { text: json({ wait_status: waitStatus, timeout_seconds: timeout, agent_count: states.length, completed_count: completed, agents: states, agents_cancelled: false, message: `Wait ${waitStatus} with ${completed} of ${states.length} agent(s) in a terminal state; the wait did not cancel agent execution.` }) };
  }

  private wait(e: Entry, seconds: number): Promise<AgentResult | undefined> {
    return seconds === 0 ? e.done : Promise.race([e.done, new Promise<undefined>(r => setTimeout(r, seconds * 1000))]);
  }
  private resultObject(r: AgentResult, file: string, includeResult = true): Record<string, unknown> {
    const o: Record<string, unknown> = { agent_id: r.id, status: r.status, duration: goDuration(r.durationMs) };
    if (r.status === "completed") Object.assign(o, includeResult ? { result: r.output ?? "" } : {}, { tokens_used: 0, cost_usd: 0, turns: 0 });
    else if (r.status === "failed") Object.assign(o, { error: r.error ?? "unknown error" });
    else if (r.status === "cancelled") Object.assign(o, { message: "Agent was cancelled before completion." });
    if (file) o.output_file = file;
    return o;
  }

  async delegate(p: AgentToolParams): Promise<ToolResult> {
    if (typeof p.task !== "string" || p.task === "") this.fail("task cannot be empty");
    const kind = p.agent_id || "general-assistant";
    const id = `delegate-${kind}-${process.hrtime.bigint()}`;
    const e = this.spawn(p.task, p, id, true);
    return { text: `Delegate launched: agent_id=${id}\nstatus=async_launched\noutput_file=${e.outputFile}\ntask=${p.task}\n\nUse DelegateOutput with this agent_id to poll, answer questions, check status, or retrieve the result.` };
  }

  /** Pi has no child-to-parent tool channel; adapters/tests call this to model ask_parent. */
  askParent(agentId: string, question: string, context = ""): Promise<string> {
    const e = this.entry(agentId);
    if (!e?.delegate) return Promise.reject(new Error(`delegate ${agentId} not found`));
    return new Promise(resolve => { e.question = { id: `q-${id8()}`, question, context, askedAt: Date.now(), resolve }; });
  }

  async delegateOutput(p: AgentToolParams): Promise<ToolResult> {
    if (!p.agent_id) this.fail("agent_id is required");
    const e = this.entry(p.agent_id);
    if (!e?.delegate) return { text: `Delegate not found: ${p.agent_id}\nIt may have expired or the agent_id is incorrect.`, isError: true };
    const action = String(p.action || "").trim().toLowerCase();
    if (!["status", "poll", "answer", "result", "cancel"].includes(action)) this.fail(`unknown action ${JSON.stringify(p.action)}: must be 'status', 'poll', 'answer', 'result', or 'cancel'`);
    if (action === "result") return this.readResult(e, Number.isInteger(p.offset) ? p.offset : 0);
    if (action === "cancel") { e.handle.cancel(); this.entries.delete(e.id); return { text: `Delegate ${e.id} cancelled.\nIf it had a pending question, it will time out.` }; }
    if (action === "answer") {
      if (!p.answer) return { text: "Error: answer cannot be empty. Provide your answer in the 'answer' parameter.", isError: true };
      const q = e.question;
      if (!q) return { text: `No pending question for delegate ${e.id}.\nThe delegate may have already received an answer or completed.` };
      if (p.question_id && p.question_id !== q.id) return { text: `Question ID mismatch. Pending question_id is ${JSON.stringify(q.id)}.\nCall again with question_id=${JSON.stringify(q.id)} or omit question_id to answer any pending question.` };
      q.answer = p.answer; q.resolve?.(p.answer); e.question = undefined;
      return { text: `Answer delivered to delegate ${e.id}.\nThe delegate has been unblocked and will continue working.\n\nPoll for next question: DelegateOutput(agent_id="${e.id}", action="poll")` };
    }
    if (action === "poll" && !e.question && !e.result) await Promise.race([e.done, new Promise(r => setTimeout(r, 10_000))]);
    if (e.question) return { text: this.formatQuestion(e) };
    if (e.result && action === "poll") return { text: `agent_id: ${e.id}\nstatus:   ${e.result.status}\nelapsed:  ${goDuration(Date.now() - e.startedAt)}\n${e.result.status === "completed" && e.result.output ? `\n─── RESULT ───\n${e.result.output}\n` : ""}\nGet full output: DelegateOutput(agent_id="${e.id}", action="result")\n` };
    return { text: `agent_id:  ${e.id}\nstatus:    ${this.status(e)}\nelapsed:   ${goDuration(Date.now() - e.startedAt)}\ntask:      ${e.task}\n\n(no pending questions)\n` };
  }

  private formatQuestion(e: Entry): string {
    const q = e.question!;
    return `─── DELEGATE QUESTION ───\nagent_id:    ${e.id}\nquestion_id: ${q.id}\nquestion:    ${q.question}\n${q.context ? `context:     ${q.context}\n` : ""}asked_at:    ${goDuration(Date.now() - q.askedAt)} ago\n\nThe delegate is BLOCKED waiting for your answer.\nAnswer: DelegateOutput(agent_id=${JSON.stringify(e.id)}, action="answer", answer="...", question_id=${JSON.stringify(q.id)})\n`;
  }
}
