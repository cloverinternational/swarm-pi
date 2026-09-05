import type { JournalEntry, TaskManager, Task, Operation, Status } from "./task-manage.js";
function registerHook(pi: HookPi, group: string, event: string, handler: any) {
  const globalRegister = (globalThis as any).__piSwarmRegisterHook;
  if (typeof globalRegister === "function") return globalRegister(pi, group, event, handler);
  pi.on(event, handler);
}

export type EnforcementMode = "advise" | "block" | "off";
export interface HookConfig {
  enforcementMode?: EnforcementMode;
  nudgeInterval?: number;
  nudgeToolThreshold?: number;
  maintenanceToolThreshold?: number;
  isSubagent?: (ctx: unknown) => boolean;
}
export interface HookEvent { type: string; [key: string]: any }
export interface HookContext { sessionManager?: { getEntries(): readonly unknown[]; getBranch?(): readonly unknown[] }; [key: string]: any }
export interface HookPi {
  on(event: string, handler: (event: HookEvent, ctx: HookContext) => any): void;
  appendEntry(type: string, data?: unknown): void;
}
interface HookState {
  turns: number; toolCalls: number; lastNudgeTurn: number; maintenanceAt: number;
  maintenanceAtByTask: Record<string, number>;
  hadError: boolean; skillCalls: number; lastSkillReview: number; audit: AuditRecord[];
  focusTaskId?: string; completedCalls: string[]; nudgeBudget: number;
}
export interface AuditRecord { tool: string; outcome: "success" | "failure"; summary: string; at: string }

const TASK_TOOLS = new Set(["taskmanage", "taskcreate", "taskupdate", "tasklist", "taskget", "todowrite", "todoread", "todo"]);
const PLAN_TOOLS = new Set(["enterplanmode", "exitplanmode", "plan", "planmode"]);
const SKILL_TOOLS = new Set(["skill", "skillmanage", "skillreview", "patchskill"]);
const READ_TOOLS = new Set(["read", "readfile", "grep", "glob", "find", "ls", "listdir", "lstat", "readdir", "readbackgroundcommand", "historysearch", "historyget", "recall", "lsp", "lspsymbols"]);
const INTERACTION_TOOLS = new Set(["askuserquestion", "question", "userquestion", "pushagentupdate", "annoyed"]);
const RESEARCH_TOOLS = new Set(["websearch", "search", "webfetch", "web", "browser", "xsearch", "xaiwebsearch", "fetch"]);
const IGNORED_AUDIT = new Set([...TASK_TOOLS, ...PLAN_TOOLS, ...SKILL_TOOLS, ...INTERACTION_TOOLS]);
// One vocabulary covers object keys and values embedded in commands.
const secretName = String.raw`(?:token|password|passwd|secret|credential|authorization|cookie|api(?:[_-]?key)|private(?:[_-]?key)|access(?:[_-]?(?:key|token)))`;
const secret = new RegExp(secretName, "i");
const normalize = (name: string) => name.toLowerCase().replace(/[^a-z0-9]/g, "");
const toolName = (e: HookEvent) => e.toolName ?? e.tool_name ?? "";
const input = (e: HookEvent) => e.input ?? e.params ?? {};
const failed = (e: HookEvent) => e.isError === true || e.error != null ||
  e.result?.isError === true || e.result?.error != null ||
  (typeof e.toolOutput === "string" && /^error\b/i.test(e.toolOutput));
const text = (x: unknown) => typeof x === "string" ? x : "";

function readOnlyBash(command: string): boolean {
  // This is deliberately an argv allowlist, not a shell parser. Quoted
  // arguments are refused so quoted callbacks/options cannot bypass it.
  if (!command.trim()) return false;
  if (/[^\x20-\x7e]|["'`$;&|<>()[\]{}\\*?]/.test(command)) return false;
  const words = command.trim().split(/\s+/);
  const executable = words.shift()!.toLowerCase();
  const args = words;
  const safeArg = (arg: string) => /^[A-Za-z0-9_./:@%+=,-]+$/.test(arg);
  if (!args.every(safeArg)) return false;
  if (!/^(pwd|ls|find|grep|rg|git|cat|head|tail|wc|which|type|echo|printf)$/.test(executable)) return false;
  if (executable === "pwd" && args.length) return false;
  if ((executable === "which" || executable === "type") && args.length !== 1) return false;
  if (executable === "cat" && args.some(a => a.startsWith("-"))) return false;
  if ((executable === "echo" || executable === "printf") && args.some(a => a.startsWith("-"))) return false;
  if ((executable === "head" || executable === "tail") &&
      args.some(a => a.startsWith("-") && !/^(?:-[0-9]+|-[nmc][0-9]+|-[nmc]|-f)$/.test(a))) return false;
  if (executable === "ls" && args.some(a => a.startsWith("-") && !/^(?:-[A-Za-z]+|--[a-z-]+)$/.test(a))) return false;
  if ((executable === "grep" || executable === "rg") &&
      args.some(a => a.startsWith("-") && !/^(?:-[A-Za-z]+|--[a-z-]+(?:=[A-Za-z0-9_.-]+)?)$/.test(a))) return false;
  if (executable === "find" && args.some(a => a.startsWith("-") &&
      !/^(?:-type|-[0-9]+|--maxdepth|--mindepth|-[a-z]+)$/i.test(a))) return false;
  if (executable === "find" && args.some(a => /^(?:-?(?:delete|exec|execdir|ok|okdir|fls|fprint|fprint0|fprintf|d|i))(?:=|$)/i.test(a))) return false;
  if (executable === "git") {
    const subcommand = args.shift()?.toLowerCase();
    if (!subcommand || !/^(?:status|log|diff|show|branch)$/.test(subcommand)) return false;
    if (subcommand === "branch" && args.some(a => a.startsWith("-"))) return false;
    if (subcommand === "diff" && args.some(a => /^(?:--output(?:=|$)|--no-index$)/i.test(a))) return false;
  }
  return true;
}
function exempt(name: string, args: any): boolean {
  const n = normalize(name);
  return TASK_TOOLS.has(n) || PLAN_TOOLS.has(n) || READ_TOOLS.has(n) || INTERACTION_TOOLS.has(n) ||
    SKILL_TOOLS.has(n) || RESEARCH_TOOLS.has(n) || (n === "bash" && readOnlyBash(text(args?.command)));
}
function stateFrom(entries: readonly unknown[]): HookState {
  const found = [...entries].reverse().find((e: any) => e?.type === "pi-swarm-task-hooks");
  const d = (found as any)?.data;
  return d ? { turns: d.turns ?? 0, toolCalls: d.toolCalls ?? 0, lastNudgeTurn: d.lastNudgeTurn ?? 0,
    maintenanceAt: d.maintenanceAt ?? 0, maintenanceAtByTask: d.maintenanceAtByTask && typeof d.maintenanceAtByTask === "object" ? { ...d.maintenanceAtByTask } : {},
    hadError: !!d.hadError, skillCalls: d.skillCalls ?? 0,
    lastSkillReview: d.lastSkillReview ?? 0, audit: Array.isArray(d.audit) ? d.audit.slice(-200) : [],
    focusTaskId: typeof d.focusTaskId === "string" ? d.focusTaskId : undefined,
    completedCalls: Array.isArray(d.completedCalls) ? d.completedCalls.slice(-500) : [],
    nudgeBudget: typeof d.nudgeBudget === "number" ? Math.max(0, d.nudgeBudget) : 1 } :
    { turns: 0, toolCalls: 0, lastNudgeTurn: 0, maintenanceAt: 0, maintenanceAtByTask: {}, hadError: false, skillCalls: 0, lastSkillReview: 0, audit: [], completedCalls: [], nudgeBudget: 1 };
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
  const secretValue = `(?:"[^"]*"|'[^']*'|[^\\s,'"]+)`;
  return value.replace(new RegExp(`(Bearer\\s+|${secretName}\\s*[=:]\\s*)${secretValue}`, "ig"), "$1[REDACTED]")
    .replace(new RegExp(`(--?${secretName}(?:[=\\s]+))${secretValue}`, "ig"), "$1[REDACTED]")
    .replace(new RegExp(`([?&]${secretName}=)[^&#\\s]+`, "ig"), "$1[REDACTED]")
    .replace(new RegExp(`([\"']?${secretName}[\"']?\\s*:\\s*[\"']?)[^\"',}\\s]+`, "ig"), "$1[REDACTED]")
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
  private anonymousTerminals: { fingerprint: string; type: string }[] = [];
  constructor(private readonly manager: TaskManager, private readonly pi: HookPi, config: HookConfig = {}) {
    this.config = { enforcementMode: config.enforcementMode ?? "advise", nudgeInterval: config.nudgeInterval ?? 5,
      nudgeToolThreshold: config.nudgeToolThreshold ?? 2, maintenanceToolThreshold: config.maintenanceToolThreshold ?? 8 };
    this.isSubagent = config.isSubagent;
    const owner = pi as object;
    this.shared = sharedByPi.get(owner) ?? { lastNudgeTurn: 0, budget: this.state.nudgeBudget };
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
    return !failed(e) && !!batch && batch.status === "succeeded";
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
    return candidates.map(find).find(Boolean);
  }
  on(event: HookEvent, ctx: HookContext = {}): any {
    if (event.type === "session_start") {
      this.state = stateFrom(ctx.sessionManager?.getBranch?.() ?? ctx.sessionManager?.getEntries?.() ?? []);
      if (ctx.sessionManager && typeof ctx.sessionManager === "object") {
        this.sessionOwner = ctx.sessionManager;
        const existing = sharedBySession.get(this.sessionOwner);
        this.shared = existing ?? { lastNudgeTurn: this.state.lastNudgeTurn, budget: this.state.nudgeBudget };
        // A journal read by a fresh coordinator is authoritative for consumed
        // budget; never let an in-memory default restore an exhausted session.
        this.shared.budget = Math.min(this.shared.budget, this.state.nudgeBudget);
        sharedBySession.set(this.sessionOwner, this.shared);
      } else {
        this.shared = { lastNudgeTurn: this.state.lastNudgeTurn, budget: this.state.nudgeBudget };
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
      if (!id) {
        const fingerprint = this.anonymousTerminalFingerprint(event);
        const inverse = event.type === "tool_result" ? "tool_execution_end" : "tool_result";
        const duplicate = this.anonymousTerminals.findIndex(x => x.type === inverse && x.fingerprint === fingerprint);
        if (duplicate >= 0) {
          this.anonymousTerminals.splice(duplicate, 1);
          return;
        }
        this.anonymousTerminals.push({ fingerprint, type: event.type });
        this.anonymousTerminals = this.anonymousTerminals.slice(-20);
      }
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
  private anonymousTerminalFingerprint(e: HookEvent): string {
    const stable = (value: unknown): string => {
      if (value === undefined) return "";
      if (value === null || typeof value !== "object") return JSON.stringify(value);
      if (Array.isArray(value)) return `[${value.map(stable).join(",")}]`;
      return `{${Object.keys(value as object).sort().map(k =>
        `${JSON.stringify(k)}:${stable((value as any)[k])}`).join(",")}}`;
    };
    return stable({ name: toolName(e), input: input(e), failed: failed(e), batch: this.batchResult(e) });
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
    const batch = n === "taskmanage" ? this.batchResult(e) : undefined;
    const isFailure = failed(e) || (!!batch && batch.status !== "succeeded");
    // Task/plan bookkeeping is not productive tool use for maintenance
    // cadence. Completion de-duplication is done before reaching this method.
    if (!TASK_TOOLS.has(n) && !PLAN_TOOLS.has(n)) this.state.toolCalls++;
    if (isFailure) this.state.hadError = true;
    const resolvedError = !isFailure && this.state.hadError;
    if (resolvedError) this.state.hadError = false;
    // Failed or partial skill calls do not count as successful reusable-skill
    // usage and must not make the next review appear complete.
    if (SKILL_TOOLS.has(n) && !isFailure) {
      this.state.skillCalls++;
      this.state.lastSkillReview = this.state.toolCalls;
    }
    // Keep routine bookkeeping out of the audit, but retain anomalous
    // TaskManage terminal results so partial/failed Pi payloads are visible.
    if (!IGNORED_AUDIT.has(n) || (n === "taskmanage" && isFailure)) {
      this.state.audit.push({ tool: name, outcome: isFailure ? "failure" : "success", summary: summary(name, input(e)), at: new Date().toISOString() });
      this.state.audit = this.state.audit.slice(-200);
    }
    this.persist();
    if (resolvedError) return this.message("An error was resolved; preserve any reusable learning in an existing skill.");
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
      if (this.state.focusTaskId) this.state.maintenanceAtByTask[this.state.focusTaskId] = this.state.maintenanceAt;
      this.state.focusTaskId = focusId;
      this.state.maintenanceAt = focusId ? (this.state.maintenanceAtByTask[focusId] ?? this.state.toolCalls) : this.state.toolCalls;
      this.persist();
    }
    if (this.tasks().length === 0 && this.state.toolCalls >= this.config.nudgeToolThreshold &&
      this.state.turns > 1 && this.state.turns - this.state.lastNudgeTurn >= this.config.nudgeInterval &&
      this.state.turns - this.shared.lastNudgeTurn >= this.config.nudgeInterval && this.shared.budget > 0 &&
      !/^(continue|keep going|go ahead|proceed|resume|run |just run |show |cat |ls |check |build |test )/i.test(this.prompt.trim())) {
      this.state.lastNudgeTurn = this.state.turns; this.shared.lastNudgeTurn = this.state.turns; this.shared.budget--;
      this.state.nudgeBudget = this.shared.budget;
      this.persist(); return this.message("Multi-step work detected with no tasks; consider TaskManage.");
    }
    if (focus && this.state.toolCalls - this.state.maintenanceAt >= this.config.maintenanceToolThreshold) {
      this.state.maintenanceAt = this.state.toolCalls;
      this.state.maintenanceAtByTask[focus.id] = this.state.maintenanceAt;
      this.persist();
      const open = this.tasks().filter(t => t.status === "pending" && t.dependsOn.some(id =>
        this.tasks().some(d => d.id === id && d.status !== "completed"))).slice(0, 3);
      const blockers = open.length ? ` Open blockers: ${open.map(t => `#${t.id} ${t.subject}`).join(", ")}.` : "";
      return this.message(`You've used ${this.state.toolCalls} tools on ${focus.subject}. Check whether scope expanded and mark it completed when done.${blockers}`);
    }
    if (this.state.skillCalls > 0 && this.state.toolCalls - this.state.lastSkillReview >= 10) {
      this.state.lastSkillReview = this.state.toolCalls; this.persist();
      return this.message("Review reusable learning: patch an existing skill or record a no-mutation review.");
    }
  }
}

export function registerTaskHooks(pi: HookPi, manager: TaskManager, config?: HookConfig): TaskHooksCoordinator {
  const coordinator = new TaskHooksCoordinator(manager, pi, config);
  for (const event of ["session_start", "shutdown", "session_shutdown", "input", "before_agent_start", "turn_start", "tool_call", "tool_execution_update", "tool_result", "tool_execution_end", "turn_end"])
    // Pi passes the event name to `on`, but its payload's `type` field is not
    // part of the runtime contract for every lifecycle event. Normalize it at
    // the adapter boundary so the coordinator also works with Pi's live
    // payloads, not only with unit-test fixtures that add `type` manually.
    registerHook(pi, "taskmanage", event, (e: HookEvent, ctx: HookContext) => coordinator.on({ ...e, type: event }, ctx));
  return coordinator;
}
