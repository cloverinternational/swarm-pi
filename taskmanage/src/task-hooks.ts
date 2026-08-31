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
  focusTaskId?: string; completedCalls: string[];
}
export interface AuditRecord { tool: string; outcome: "success" | "failure"; summary: string; at: string }

const TASK_TOOLS = new Set(["taskmanage", "taskcreate", "taskupdate", "tasklist", "taskget", "todowrite", "todoread", "todo"]);
const PLAN_TOOLS = new Set(["enterplanmode", "exitplanmode", "plan", "planmode"]);
const SKILL_TOOLS = new Set(["skill", "skillmanage", "skillreview", "patchskill"]);
const READ_TOOLS = new Set(["read", "readfile", "grep", "glob", "find", "ls", "listdir", "lstat", "readdir", "readbackgroundcommand", "historysearch", "historyget", "recall", "lsp", "lspsymbols"]);
const INTERACTION_TOOLS = new Set(["askuserquestion", "question", "userquestion", "pushagentupdate", "annoyed"]);
const RESEARCH_TOOLS = new Set(["websearch", "search", "webfetch", "web", "browser", "xsearch", "xaiwebsearch", "fetch"]);
const IGNORED_AUDIT = new Set([...TASK_TOOLS, ...PLAN_TOOLS, ...SKILL_TOOLS, ...INTERACTION_TOOLS]);
const secret = /token|password|secret|credential|api[_-]?key|private[_-]?key|access[_-]?key|authorization|cookie|passwd/i;
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
    SKILL_TOOLS.has(n) || RESEARCH_TOOLS.has(n) || n === "lsp" || n.startsWith("lsp") ||
    n === "recall" || n.endsWith("recall") || (n === "bash" && readOnlyBash(text(args?.command)));
}
function stateFrom(entries: readonly unknown[]): HookState {
  const found = [...entries].reverse().find((e: any) => e?.type === "pi-swarm-task-hooks");
  const d = (found as any)?.data;
  return d ? { turns: d.turns ?? 0, toolCalls: d.toolCalls ?? 0, lastNudgeTurn: d.lastNudgeTurn ?? 0,
    maintenanceAt: d.maintenanceAt ?? 0, hadError: !!d.hadError, skillCalls: d.skillCalls ?? 0,
    lastSkillReview: d.lastSkillReview ?? 0, audit: Array.isArray(d.audit) ? d.audit.slice(-200) : [],
    focusTaskId: typeof d.focusTaskId === "string" ? d.focusTaskId : undefined,
    completedCalls: Array.isArray(d.completedCalls) ? d.completedCalls.slice(-500) : [] } :
    { turns: 0, toolCalls: 0, lastNudgeTurn: 0, maintenanceAt: 0, hadError: false, skillCalls: 0, lastSkillReview: 0, audit: [], completedCalls: [] };
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
  return value.replace(/(Bearer\s+|(?:token|password|secret|credential|private[_-]?key|api[_-]?key|authorization|cookie|passwd)\s*[=:]\s*)[^\s,'"]+/ig, "$1[REDACTED]")
    .replace(/(--?(?:token|password|secret|credential|private[_-]?key|api[_-]?key|authorization|cookie|passwd)(?:[=\s]+))[^\s,'"]+/ig, "$1[REDACTED]")
    .replace(/([?&](?:token|password|secret|credential|api[_-]?key|private[_-]?key|authorization|access_token)=)[^&#\s]+/ig, "$1[REDACTED]")
    .replace(/(["']?(?:token|password|secret|credential|private[_-]?key|api[_-]?key|authorization|cookie|passwd)["']?\s*:\s*["']?)[^"',}\s]+/ig, "$1[REDACTED]")
    .replace(/((?:https?:\/\/|file:\/\/)[^?\s]*\/)([^\/\s]*(?:token|secret|credential|private|password)[^\/\s]*)/ig, "$1[REDACTED]")
    .replace(/\r?\n/g, " ").slice(0, 80);
}

interface SharedNudgeState { lastNudgeTurn: number; budget: number; }
const sharedByPi = new WeakMap<object, SharedNudgeState>();
const sharedBySession = new WeakMap<object, SharedNudgeState>();

/** A single ordered coordinator. Only enforcement can return a blocking decision. */
export class TaskHooksCoordinator {
  readonly config: Required<Pick<HookConfig, "nudgeInterval" | "nudgeToolThreshold" | "maintenanceToolThreshold">> & { enforcementMode: EnforcementMode };
  private readonly isSubagent?: (ctx: unknown) => boolean;
  private state: HookState = stateFrom([]);
  private prompt = "";
  private shared: SharedNudgeState;
  private sessionOwner?: object;
  constructor(private readonly manager: TaskManager, private readonly pi: HookPi, config: HookConfig = {}) {
    this.config = { enforcementMode: config.enforcementMode ?? "advise", nudgeInterval: config.nudgeInterval ?? 5,
      nudgeToolThreshold: config.nudgeToolThreshold ?? 2, maintenanceToolThreshold: config.maintenanceToolThreshold ?? 8 };
    this.isSubagent = config.isSubagent;
    const owner = pi as object;
    this.shared = sharedByPi.get(owner) ?? { lastNudgeTurn: 0, budget: 1 };
    sharedByPi.set(owner, this.shared);
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
    const batch = this.batchResult(e);
    return !!batch && batch.status === "succeeded";
  }
  private batchResult(e: HookEvent): any {
    const candidates = [e.result, e.toolResult, e.tool_output, e.content, e.details];
    const find = (value: any): any => {
      if (!value) return undefined;
      if (typeof value === "string") {
        try { return find(JSON.parse(value)); } catch { return undefined; }
      }
      if (Array.isArray(value)) {
        for (const item of value) { const found = find(item?.text ?? item); if (found) return found; }
        return undefined;
      }
      if (typeof value === "object") {
        if (typeof value.status === "string" && Array.isArray(value.results)) return value;
        return find(value.content) ?? find(value.details) ?? find(value.result);
      }
      return undefined;
    };
    return failed(e) ? undefined : candidates.map(find).find(Boolean);
  }
  on(event: HookEvent, ctx: HookContext = {}): any {
    if (event.type === "session_start") {
      this.state = stateFrom(ctx.sessionManager?.getEntries?.() ?? []);
      if (ctx.sessionManager && typeof ctx.sessionManager === "object") {
        this.sessionOwner = ctx.sessionManager;
        this.shared = sharedBySession.get(this.sessionOwner) ?? { lastNudgeTurn: this.state.lastNudgeTurn, budget: 1 };
        sharedBySession.set(this.sessionOwner, this.shared);
      } else {
        this.shared = { lastNudgeTurn: this.state.lastNudgeTurn, budget: 1 };
      }
      this.prompt = ""; // never carry prompt/context across sessions
      return;
    }
    if (event.type === "shutdown" || event.type === "session_shutdown") { this.persist(); return; }
    if (event.type === "input" || event.type === "before_agent_start") { this.prompt = text(event.text ?? event.prompt); return; }
    if (event.type === "turn_start") { this.state.turns++; this.persist(); return; }
    if (event.type === "tool_call") return this.gate(event, ctx);
    if (event.type === "tool_execution_update") return; // progress is observed, never audited
    if (event.type === "tool_result" || event.type === "tool_execution_end") {
      const id = event.toolCallId ?? event.tool_call_id;
      if (id && this.state.completedCalls.includes(String(id))) return;
      // Pi emits tool_result before tool_execution_end. The first terminal event
      // is authoritative; the stable call id prevents the second from replaying it.
      if (id) {
        this.state.completedCalls.push(String(id));
        this.state.completedCalls = this.state.completedCalls.slice(-500);
      }
      return this.outcome(event, ctx);
    }
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
    // Task/plan bookkeeping is not productive tool use for maintenance
    // cadence. Completion de-duplication is done before reaching this method.
    if (!TASK_TOOLS.has(n) && !PLAN_TOOLS.has(n)) this.state.toolCalls++;
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
    const batch = event ? this.batchResult(event) : undefined;
    if (!batch || batch.status !== "succeeded") return;
    const successful = new Set((batch.results ?? []).filter((r: any) => r?.status === "succeeded").map((r: any) => r.key));
    const mutation = ops.filter(o => (o.op === "create" || o.op === "update") && successful.has(o.key)).at(-1);
    if (!mutation) return;
    const snap = this.manager.snapshot();
    const row = (batch.results ?? []).find((r: any) => r?.key === mutation.key && r?.status === "succeeded");
    const returnedTask = row?.data?.task;
    let task = typeof returnedTask?.id === "string" ? snap.tasks.find(t => t.id === returnedTask.id) : undefined;
    if (!task && mutation.op === "update" && typeof mutation.taskId === "string")
      task = snap.tasks.find(t => t.id === mutation.taskId);
    if (mutation.op === "create") {
      if (mutation.status === "in_progress") return this.message(`Task #${task?.id ?? "?"} is now ACTIVE. You can proceed with tools.`);
      if (this.focus()) return;
      return this.message("Task created successfully. Activate it with status: \"in_progress\" before proceeding.");
    }
    if (mutation.status === "in_progress") return this.message(`Task #${task?.id ?? "?"} is now ACTIVE. You can proceed with tools.`);
    if (mutation.status === "completed") {
      const active = snap.tasks.find(t => t.status === "in_progress" && t.active);
      const pending = snap.tasks.filter(t => t.status === "pending");
      return this.message(`Task #${task?.id ?? "?"} completed. ${active ? `Continue with task #${active.id}: ${active.subject}` :
        pending.length ? `${pending.length} pending task(s) remain; activate the next one.` : "All tasks done; create a task for your next objective."}`);
    }
  }
  private endTurn(ctx: HookContext) {
    const focus = this.focus();
    const focusId = focus?.id;
    if (focusId !== this.state.focusTaskId) {
      this.state.focusTaskId = focusId;
      this.state.maintenanceAt = this.state.toolCalls;
      this.persist();
    }
    if (this.tasks().length === 0 && this.state.toolCalls >= this.config.nudgeToolThreshold &&
      this.state.turns > 1 && this.state.turns - this.state.lastNudgeTurn >= this.config.nudgeInterval &&
      this.state.turns - this.shared.lastNudgeTurn >= this.config.nudgeInterval && this.shared.budget > 0 &&
      !/^(continue|keep going|go ahead|proceed|resume|run |just run |show |cat |ls |check |build |test )/i.test(this.prompt.trim())) {
      this.state.lastNudgeTurn = this.state.turns; this.shared.lastNudgeTurn = this.state.turns; this.shared.budget--;
      this.persist(); return this.message("Multi-step work detected with no tasks; consider TaskManage.");
    }
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
