import type { JournalEntry, TaskManager, Task, Operation, Status } from "./task-manage.js";

export type EnforcementMode = "advise" | "block" | "off";
export interface HookConfig {
  enforcementMode?: EnforcementMode;
  nudgeInterval?: number;
  nudgeToolThreshold?: number;
  maintenanceToolThreshold?: number;
  isSubagent?: (ctx: unknown) => boolean;
}
export interface HookEvent { type: string; [key: string]: any }
export interface HookContext { sessionManager?: { getEntries(): readonly unknown[] }; [key: string]: any }
export interface HookPi {
  on(event: string, handler: (event: HookEvent, ctx: HookContext) => any): void;
  appendEntry(type: string, data?: unknown): void;
}
interface HookState {
  turns: number; toolCalls: number; lastNudgeTurn: number; maintenanceAt: number;
  hadError: boolean; skillCalls: number; lastSkillReview: number; audit: AuditRecord[];
}
export interface AuditRecord { tool: string; outcome: "success" | "failure"; summary: string; at: string }

const TASK_TOOLS = new Set(["taskmanage", "taskcreate", "taskupdate", "tasklist", "taskget", "todowrite", "todoread"]);
const PLAN_TOOLS = new Set(["enterplanmode", "exitplanmode"]);
const READ_TOOLS = new Set(["read", "grep", "glob", "find", "ls", "lstat"]);
const INTERACTION_TOOLS = new Set(["askuserquestion", "question", "userquestion"]);
const IGNORED_AUDIT = new Set([...TASK_TOOLS, ...PLAN_TOOLS, ...INTERACTION_TOOLS, "pushagentupdate"]);
const secret = /token|password|secret|credential|api[_-]?key|private[_-]?key/i;
const normalize = (name: string) => name.toLowerCase().replace(/[^a-z0-9]/g, "");
const toolName = (e: HookEvent) => e.toolName ?? e.tool_name ?? "";
const input = (e: HookEvent) => e.input ?? e.params ?? {};
const failed = (e: HookEvent) => e.isError === true || e.error != null ||
  e.result?.isError === true || e.result?.error != null ||
  (typeof e.toolOutput === "string" && /^error\b/i.test(e.toolOutput));
const text = (x: unknown) => typeof x === "string" ? x : "";

function readOnlyBash(command: string): boolean {
  return /^(pwd|ls|find|grep|rg|git\s+(status|log|diff|show|branch)|cat|head|tail|sed|awk|wc|which|type|echo|printf)\b/i.test(command.trim()) &&
    !/[|;&]>/.test(command);
}
function exempt(name: string, args: any): boolean {
  const n = normalize(name);
  return TASK_TOOLS.has(n) || PLAN_TOOLS.has(n) || READ_TOOLS.has(n) || INTERACTION_TOOLS.has(n) ||
    n === "skill" || n === "skillmanage" || (n === "bash" && readOnlyBash(text(args?.command)));
}
function stateFrom(entries: readonly unknown[]): HookState {
  const found = [...entries].reverse().find((e: any) => e?.type === "pi-swarm-task-hooks");
  const d = (found as any)?.data;
  return d ? { turns: d.turns ?? 0, toolCalls: d.toolCalls ?? 0, lastNudgeTurn: d.lastNudgeTurn ?? 0,
    maintenanceAt: d.maintenanceAt ?? 0, hadError: !!d.hadError, skillCalls: d.skillCalls ?? 0,
    lastSkillReview: d.lastSkillReview ?? 0, audit: Array.isArray(d.audit) ? d.audit.slice(-200) : [] } :
    { turns: 0, toolCalls: 0, lastNudgeTurn: 0, maintenanceAt: 0, hadError: false, skillCalls: 0, lastSkillReview: 0, audit: [] };
}
function summary(name: string, args: any): string {
  if (!args || typeof args !== "object") return name;
  for (const key of ["command", "file_path", "path", "pattern", "query", "url", "subject"]) {
    if (typeof args[key] === "string" && args[key]) return `${name}: ${sanitize(args[key])}`.slice(0, 160);
  }
  const parts = Object.keys(args).slice(0, 3).map(k => secret.test(k) ? `${k}=[REDACTED]` :
    typeof args[k] === "string" ? `${k}=${sanitize(args[k])}` : k);
  return `${name}${parts.length ? `: ${parts.join(" ")}` : ""}`.slice(0, 160);
}
function sanitize(value: string): string {
  return value.replace(/(Bearer\s+|(?:token|password|secret|api[_-]?key)\s*[=:]\s*)\S+/ig, "$1[REDACTED]")
    .replace(/\r?\n/g, " ").slice(0, 80);
}

/** A single ordered coordinator. Only enforcement can return a blocking decision. */
export class TaskHooksCoordinator {
  readonly config: Required<Pick<HookConfig, "nudgeInterval" | "nudgeToolThreshold" | "maintenanceToolThreshold">> & { enforcementMode: EnforcementMode };
  private readonly isSubagent?: (ctx: unknown) => boolean;
  private state: HookState = stateFrom([]);
  private prompt = "";
  constructor(private readonly manager: TaskManager, private readonly pi: HookPi, config: HookConfig = {}) {
    this.config = { enforcementMode: config.enforcementMode ?? "advise", nudgeInterval: config.nudgeInterval ?? 5,
      nudgeToolThreshold: config.nudgeToolThreshold ?? 2, maintenanceToolThreshold: config.maintenanceToolThreshold ?? 8 };
    this.isSubagent = config.isSubagent;
  }
  private persist() { this.pi.appendEntry("pi-swarm-task-hooks", this.state); }
  auditSnapshot(): readonly AuditRecord[] { return this.state.audit.map(x => ({ ...x })); }
  private message(message: string) { return { message: `[TASK HOOK] ${message}` }; }
  private tasks(): Task[] { return this.manager.snapshot().tasks.filter(t => t.status !== "deleted"); }
  private focus(): Task | undefined { return this.tasks().find(t => t.status === "in_progress" && t.active); }
  private taskOps(e: HookEvent): Operation[] {
    const p = input(e); return Array.isArray(p?.operations) ? p.operations : [];
  }
  private resultSucceeded(e: HookEvent) {
    const r = e.result ?? e.toolResult ?? e.tool_output;
    if (failed(e)) return false;
    return r !== undefined ? !(typeof r === "string" && /^error\b/i.test(r)) : true;
  }
  on(event: HookEvent, ctx: HookContext = {}): any {
    if (event.type === "session_start") {
      this.state = stateFrom(ctx.sessionManager?.getEntries?.() ?? []);
      this.prompt = ""; // never carry prompt/context across sessions
      return;
    }
    if (event.type === "shutdown" || event.type === "session_shutdown") { this.persist(); return; }
    if (event.type === "input" || event.type === "before_agent_start") { this.prompt = text(event.text ?? event.prompt); return; }
    if (event.type === "turn_start") { this.state.turns++; this.persist(); return; }
    if (event.type === "tool_call") return this.gate(event, ctx);
    if (event.type === "tool_execution_update") return; // progress is observed, never audited
    if (event.type === "tool_result" || event.type === "tool_execution_end") return this.outcome(event, ctx);
    if (event.type === "turn_end") return this.endTurn(ctx);
  }
  private gate(e: HookEvent, ctx: HookContext) {
    if (this.config.enforcementMode === "off" || this.isSubagent?.(ctx) ||
      (ctx as any)?.isSubagent === true || (ctx as any)?.agentType === "subagent" ||
      e.isSubagent === true) return;
    const name = toolName(e), n = normalize(name);
    if (!name || exempt(name, input(e))) return;
    if (this.focus()) return;
    const reason = "No active task is focused; create or update a task with status \"in_progress\" before acting.";
    if (this.config.enforcementMode === "block") return { block: true, reason };
    return this.message(reason);
  }
  private outcome(e: HookEvent, ctx: HookContext) {
    const name = toolName(e), n = normalize(name);
    const isFailure = failed(e);
    this.state.toolCalls++;
    if (isFailure) this.state.hadError = true;
    if (n === "skill" || n === "skillmanage") { this.state.skillCalls++; this.state.lastSkillReview = this.state.toolCalls; }
    if (!IGNORED_AUDIT.has(n)) {
      this.state.audit.push({ tool: name, outcome: isFailure ? "failure" : "success", summary: summary(name, input(e)), at: new Date().toISOString() });
      this.state.audit = this.state.audit.slice(-200);
    }
    this.persist();
    if (!isFailure && this.state.hadError) { this.state.hadError = false; return this.message("An error was resolved; preserve any reusable learning in an existing skill."); }
    if (n === "taskmanage" && this.resultSucceeded(e)) return this.guidance(this.taskOps(e), e);
    return;
  }
  private guidance(ops: Operation[], event?: HookEvent) {
    const mutation = ops.filter(o => o.op === "create" || o.op === "update").at(-1);
    if (!mutation) return;
    const snap = this.manager.snapshot();
    let task = mutation.op === "create" ? snap.tasks.at(-1) : snap.tasks.find(t => t.id === (typeof mutation.taskId === "string" ? mutation.taskId : ""));
    if (mutation.op === "create") {
      if (mutation.status === "in_progress") return this.message(`Task #${task?.id ?? "?"} is now ACTIVE. You can proceed with tools.`);
      if (this.focus()) return;
      return this.message("Task created successfully. Activate it with status: \"in_progress\" before proceeding.");
    }
    // References are normalized by TaskManage in its result boundary.
    const returned = event?.result ?? event?.toolResult;
    const rows = Array.isArray(returned?.results) ? returned.results : [];
    const row = rows.find((r: any) => r?.key === mutation.key);
    const returnedID = row?.data?.task?.id;
    if (returnedID) task = snap.tasks.find(t => t.id === returnedID);
    if (mutation.status === "in_progress") return this.message(`Task #${task?.id ?? "?"} is now ACTIVE. You can proceed with tools.`);
    if (mutation.status === "completed") {
      const active = snap.tasks.find(t => t.status === "in_progress" && t.active);
      const pending = snap.tasks.filter(t => t.status === "pending");
      return this.message(`Task #${task?.id ?? "?"} completed. ${active ? `Continue with task #${active.id}: ${active.subject}` :
        pending.length ? `${pending.length} pending task(s) remain; activate the next one.` : "All tasks done; create a task for your next objective."}`);
    }
  }
  private endTurn(ctx: HookContext) {
    if (this.tasks().length === 0 && this.state.toolCalls >= this.config.nudgeToolThreshold &&
      this.state.turns > 1 && this.state.turns - this.state.lastNudgeTurn >= this.config.nudgeInterval &&
      !/^(continue|keep going|go ahead|proceed|resume|run |just run |show |cat |ls |check |build |test )/i.test(this.prompt.trim())) {
      this.state.lastNudgeTurn = this.state.turns; this.persist(); return this.message("Multi-step work detected with no tasks; consider TaskManage.");
    }
    const focus = this.focus();
    if (focus && this.state.toolCalls - this.state.maintenanceAt >= this.config.maintenanceToolThreshold) {
      this.state.maintenanceAt = this.state.toolCalls; this.persist();
      const open = this.tasks().filter(t => t.status === "pending" && t.dependsOn.some(id =>
        this.tasks().some(d => d.id === id && d.status !== "completed"))).slice(0, 3);
      const blockers = open.length ? ` Open blockers: ${open.map(t => `#${t.id} ${t.subject}`).join(", ")}.` : "";
      return this.message(`You've used ${this.state.toolCalls} tools on ${focus.subject}. Check whether scope expanded and mark it completed when done.${blockers}`);
    }
    if (this.state.toolCalls - this.state.lastSkillReview >= 10 && this.state.toolCalls > 0) {
      this.state.lastSkillReview = this.state.toolCalls; this.persist();
      return this.message("Review reusable learning: patch an existing skill or record a no-mutation review.");
    }
  }
}

export function registerTaskHooks(pi: HookPi, manager: TaskManager, config?: HookConfig): TaskHooksCoordinator {
  const coordinator = new TaskHooksCoordinator(manager, pi, config);
  for (const event of ["session_start", "shutdown", "session_shutdown", "input", "before_agent_start", "turn_start", "tool_call", "tool_execution_update", "tool_result", "tool_execution_end", "turn_end"])
    pi.on(event, (e, ctx) => coordinator.on(e, ctx));
  return coordinator;
}
