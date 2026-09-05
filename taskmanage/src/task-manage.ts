export const CATEGORIES = ["researching", "planning", "acting", "verifying", "debugging", "documenting"] as const;
export const PRIORITIES = ["low", "medium", "high"] as const;
export const NOTE_TYPES = ["decision", "blocker", "learning", "milestone", "question", "observation", "other"] as const;
export type Category = typeof CATEGORIES[number];
export type Priority = typeof PRIORITIES[number];
export type Status = "pending" | "in_progress" | "completed" | "deleted";
export type NoteType = typeof NOTE_TYPES[number];
export type Mode = "sequential" | "atomic";
export type Ref = string | { ref: string; field?: "taskId" };

export interface AuditEvent { action: "created" | "updated"; at: string }
export interface TaskNote { text: string; type: NoteType; at: string }
export interface Task {
  id: string; subject: string; description?: string; activeForm?: string; category?: Category; priority: Priority;
  metadata?: Record<string, unknown>; parentTaskId?: string; owner_id?: string; status: Status;
  active?: boolean; dependsOn: string[]; notes: string[]; createdAt: string; updatedAt: string;
  typed_notes?: TaskNote[]; audit_events?: AuditEvent[];
}
export interface Operation {
  key: string; op: "create" | "update" | "get" | "list"; taskId?: Ref; subject?: string;
  description?: string; activeForm?: string; category?: Category; priority?: Priority; metadata?: Record<string, unknown>;
  parentTaskId?: Ref; owner_id?: string; status?: Status; active?: boolean; limit?: number; offset?: number;
  addBlocks?: Ref[]; addBlockedBy?: Ref[]; addNote?: string; noteType?: NoteType; include_audit?: boolean;
}
export interface Params { operations: Operation[]; mode?: Mode }
export interface Failure { code: string; message: string; retryable: boolean }
export interface Result { key: string; op: Operation["op"]; status: "succeeded" | "failed" | "skipped"; data?: unknown; error?: Failure }
export interface Batch { status: "succeeded" | "partial" | "failed"; results: Result[] }
export interface OperationEvent {
  type: "pi-swarm-task-operation";
  data: { mode: Mode; status: Batch["status"]; results: Result[]; at: string };
}
import { replayLatest, snapshot, type VersionedSnapshot } from "./persistence.js";

export type JournalEntry = { type: "pi-swarm-task-state"; data: VersionedSnapshot<State> | State } | OperationEvent;
export interface State { nextId: number; tasks: Task[]; keys: Record<string, string> }
export interface TaskMetrics { mutations: number; reads: number; failures: number; lastRevision: number }

const fail = (code: string, message: string, retryable = false): Failure => ({ code, message, retryable });
const clone = <T>(x: T): T => structuredClone(x);
const isObj = (x: unknown): x is Record<string, unknown> => !!x && typeof x === "object" && !Array.isArray(x);

/** JSON schema is deliberately exported as plain JSON so the module works with every Pi release. */
export const taskManageSchema = {
  type: "object", required: ["operations"], additionalProperties: false,
  properties: {
    mode: { type: "string", enum: ["sequential", "atomic"] },
    operations: { type: "array", minItems: 1, maxItems: 50, items: {
      type: "object", required: ["key", "op"], additionalProperties: false,
      properties: {
        key: { type: "string" }, op: { type: "string", enum: ["create", "update", "get", "list"] },
        taskId: { oneOf: [{ type: "string" }, { type: "object", required: ["ref"], additionalProperties: false, properties: { ref: { type: "string" }, field: { type: "string", enum: ["taskId"] } } }] },
        parentTaskId: { oneOf: [{ type: "string" }, { type: "object", required: ["ref"], additionalProperties: false, properties: { ref: { type: "string" }, field: { type: "string", enum: ["taskId"] } } }] },
        subject: { type: "string" }, description: { type: "string" }, activeForm: { type: "string" },
        category: { type: "string", enum: CATEGORIES }, priority: { type: "string", enum: PRIORITIES }, metadata: { type: "object" }, owner_id: { type: "string" },
        status: { type: "string", enum: ["pending", "in_progress", "completed", "deleted"] }, active: { type: "boolean" },
        limit: { type: "integer", minimum: 1, maximum: 500 }, offset: { type: "integer", minimum: 0 },
        addBlocks: { type: "array", items: { oneOf: [{ type: "string" }, { type: "object", required: ["ref"], additionalProperties: false, properties: { ref: { type: "string" }, field: { type: "string", enum: ["taskId"] } } }] } },
        addBlockedBy: { type: "array", items: { oneOf: [{ type: "string" }, { type: "object", required: ["ref"], additionalProperties: false, properties: { ref: { type: "string" }, field: { type: "string", enum: ["taskId"] } } }] } },
        addNote: { type: "string" },
        noteType: { type: "string", enum: NOTE_TYPES }, include_audit: { type: "boolean" }
      }
    }}
  }
} as const;

type RenderTheme = {
  fg?: (color: string, text: string) => string;
  bold?: (text: string) => string;
  dim?: (text: string) => string;
};
type RenderContext = { isError?: boolean; isPartial?: boolean; expanded?: boolean };
type RenderComponent = { render(width: number): string[] };

const style = (theme: RenderTheme, color: string, value: string) =>
  theme.fg ? theme.fg(color, value) : value;
const bold = (theme: RenderTheme, value: string) => theme.bold ? theme.bold(value) : value;
const dim = (theme: RenderTheme, value: string) => theme.dim ? theme.dim(value) : value;
const characterWidth = (character: string) => {
  const point = character.codePointAt(0) ?? 0;
  if (/\p{Mark}/u.test(character)) return 0;
  return point >= 0x1100 && (
    point <= 0x115f || point === 0x2329 || point === 0x232a ||
    point >= 0x2e80 && point <= 0xa4cf ||
    point >= 0xac00 && point <= 0xd7a3 ||
    point >= 0xf900 && point <= 0xfaff ||
    point >= 0xfe10 && point <= 0xfe6f ||
    point >= 0xff00 && point <= 0xff60 ||
    point >= 0x1f300 && point <= 0x1faff
  ) ? 2 : 1;
};
const displayWidth = (value: string) => Array.from(value).reduce((width, character) => width + characterWidth(character), 0);
const truncate = (value: string, width: number) => {
  if (width <= 0) return "";
  if (displayWidth(value) <= width) return value;
  const limit = Math.max(0, width - 1);
  let used = 0;
  let result = "";
  for (const character of value) {
    const next = characterWidth(character);
    if (used + next > limit) break;
    result += character;
    used += next;
  }
  return `${result}…`;
};
const component = (lines: string[] | ((width: number) => string[])): RenderComponent => ({
  render: (width: number) => typeof lines === "function" ? lines(width) : lines,
});

type RenderTask = {
  id?: string;
  subject?: string;
  content?: string;
  status?: Status;
  active?: boolean;
  category?: Category;
  parent_id?: string;
  depends_on?: string[];
};

function taskRows(batch: Batch): { tasks: RenderTask[]; errors: string[] } {
  const order: string[] = [];
  const tasks = new Map<string, RenderTask>();
  const errors: string[] = [];
  let anonymous = 0;
  for (const result of batch.results) {
    if (result.error) {
      errors.push(result.error.message || "Task update failed");
      continue;
    }
    if (result.status !== "succeeded" || !isObj(result.data)) continue;
    const data = result.data as { task?: RenderTask; tasks?: RenderTask[] };
    const values = data.task ? [data.task] : Array.isArray(data.tasks) ? data.tasks : [];
    for (const task of values) {
      const key = task.id || `__anonymous_${anonymous++}`;
      if (!tasks.has(key)) order.push(key);
      tasks.set(key, task);
    }
  }
  return { tasks: order.map(key => tasks.get(key)!), errors };
}

function renderTaskResult(task: RenderTask, first: boolean, width: number, theme: RenderTheme): string {
  const prefix = first ? "    ⎿ " : "      ";
  const rawIcon = task.status === "completed" ? "✓" : task.status === "in_progress" ? "◉" :
    task.status === "pending" ? "○" : "?";
  if (width <= displayWidth(prefix) + 2) return truncate(`${prefix}${rawIcon}`, width);
  const icon = task.status === "completed" ? style(theme, "success", rawIcon) :
    task.status === "in_progress" ? style(theme, "warning", rawIcon) : dim(theme, rawIcon);
  const label = `${task.id ? `#${task.id} ` : ""}${task.content || task.subject || "Untitled task"}`;
  const available = Math.max(0, width - displayWidth(prefix) - 2);
  const text = truncate(label, available);
  const rendered = task.status === "in_progress" ? bold(theme, text) :
    task.status === "completed" ? dim(theme, text) : text;
  return `${prefix}${icon} ${rendered}`;
}

export const taskManageRenderers = {
  renderCall(_args: Params, theme: RenderTheme): RenderComponent {
    return component([`${bold(theme, "TaskManage")} ${dim(theme, "Managing tasks…")}`]);
  },

  renderResult(result: any, options: RenderContext, theme: RenderTheme): RenderComponent {
    if (options.isPartial)
      return component([`    ${dim(theme, "⎿")} ${dim(theme, "Managing tasks…")}`]);
    let batch: Batch | undefined;
    const text = result?.content?.find((item: any) => item?.type === "text")?.text;
    try { batch = typeof text === "string" ? JSON.parse(text) : undefined; } catch { /* use fallback */ }
    if (!batch || !Array.isArray(batch.results)) {
      const message = options.isError ? "Task update failed" : "Task state unavailable";
      return component([`    ${dim(theme, "⎿")} ${options.isError ? style(theme, "error", message) : dim(theme, message)}`]);
    }

    const rows = taskRows(batch);
    if (!rows.tasks.length && !rows.errors.length)
      return component([`    ${dim(theme, "⎿")} ${dim(theme, "Tasks unchanged")}`]);
    return component(width => {
      const lines = rows.tasks.map((task, index) => renderTaskResult(task, index === 0, width, theme));
      for (const message of rows.errors) {
        const prefix = lines.length ? "      " : "    ⎿ ";
        lines.push(`${prefix}${style(theme, "error", "✗")} ${truncate(message, Math.max(0, width - displayWidth(prefix) - 2))}`);
      }
      return lines;
    });
  },
};

function categoryLetter(category?: Category): string {
  return category === "researching" ? "R" : category === "planning" ? "P" :
    category === "verifying" ? "V" : category === "debugging" ? "D" :
    category === "documenting" ? "X" : "A";
}

function orderedOpenTasks(tasks: Task[]): Array<{ task: Task; depth: number }> {
  const open = tasks.filter(task => task.status === "pending" || task.status === "in_progress");
  const visible = new Set(open.map(task => task.id));
  const children = new Map<string, Task[]>();
  const roots: Task[] = [];
  for (const task of open) {
    if (task.parentTaskId && visible.has(task.parentTaskId))
      children.set(task.parentTaskId, [...(children.get(task.parentTaskId) ?? []), task]);
    else roots.push(task);
  }
  const rank = (task: Task) => task.active ? 0 : task.status === "in_progress" ? 1 : 2;
  const output: Array<{ task: Task; depth: number }> = [];
  const visit = (task: Task, depth: number) => {
    output.push({ task, depth });
    for (const child of (children.get(task.id) ?? []).sort((a, b) => rank(a) - rank(b))) visit(child, depth + 1);
  };
  for (const task of roots.sort((a, b) => rank(a) - rank(b))) visit(task, 0);
  return output;
}

export function taskWidgetRenderer(tasks: Task[], theme: RenderTheme): RenderComponent {
  const current = tasks.filter(task => task.status !== "deleted");
  const completed = current.filter(task => task.status === "completed").length;
  const ordered = orderedOpenTasks(current);
  return component(width => {
    const header = `Tasks   ${completed}/${current.length} done`;
    const lines = [displayWidth(header) > width ? truncate(header, width) :
      `${bold(theme, dim(theme, "Tasks"))}${dim(theme, `   ${completed}/${current.length} done`)}`];
    for (const { task, depth } of ordered) {
      const blocked = task.dependsOn.some(id => current.find(candidate => candidate.id === id)?.status !== "completed");
      const glyph = task.active ? style(theme, "accent", "●") :
        task.status === "in_progress" ? style(theme, "accent", "◐") :
        blocked ? style(theme, "warning", "⧗") : dim(theme, "○");
      const indent = `  ${"  ".repeat(depth)}`;
      const badge = dim(theme, `[${categoryLetter(task.category)}]`);
      const available = Math.max(0, width - displayWidth(indent) - 6);
      const subject = truncate(task.subject, available);
      lines.push(width <= displayWidth(indent) + 6
        ? truncate(`${indent}${task.active ? "●" : task.status === "in_progress" ? "◐" : blocked ? "⧗" : "○"} [${categoryLetter(task.category)}]`, width)
        : `${indent}${glyph} ${badge} ${task.active ? bold(theme, subject) : dim(theme, subject)}`);
    }
    return lines;
  });
}

export class TaskManager {
  private state: State = { nextId: 1, tasks: [], keys: {} };
  private revision = 0;
  private stats: TaskMetrics = { mutations: 0, reads: 0, failures: 0, lastRevision: 0 };
  constructor(
    private readonly persist?: (entry: JournalEntry) => void,
    private readonly emitOperation?: (event: OperationEvent) => void,
  ) {}
  snapshot(): State { return clone(this.state); }
  metrics(): TaskMetrics { return { ...this.stats }; }
  restore(state: State): void {
    const restored = clone(state);
    // Legacy journals may contain malformed or pre-priority state. Normalize
    // at the persistence boundary instead of allowing replay to crash.
    if (!restored || !Array.isArray(restored.tasks) || typeof restored.nextId !== "number" || !restored.keys || typeof restored.keys !== "object") {
      this.state = { nextId: 1, tasks: [], keys: {} };
      return;
    }
    for (const task of restored.tasks) {
      if (!PRIORITIES.includes(task.priority)) task.priority = "medium";
    }
    this.state = restored;
  }
  rehydrate(entries: readonly JournalEntry[]): void {
    this.state = { nextId: 1, tasks: [], keys: {} };
    this.revision = 0;
    this.stats = { mutations: 0, reads: 0, failures: 0, lastRevision: 0 };
    const replayed = replayLatest<State>(entries, "pi-swarm-task-state", (value) => value as State);
    if (replayed) {
      this.restore(replayed.state);
      this.revision = replayed.revision;
      this.stats.lastRevision = this.revision;
    }
  }
  private commit(): void {
    this.revision++;
    this.stats.mutations++;
    this.stats.lastRevision = this.revision;
    this.persist?.({ type: "pi-swarm-task-state", data: snapshot(this.snapshot(), this.revision) });
  }
  private find(id: string): Task | undefined { return this.state.tasks.find(t => t.id === id); }
  private resolve(ref: Ref | undefined, local: Record<string, string>): string | Failure {
    if (typeof ref === "string") return ref;
    if (!ref) return fail("validation_failed", "taskId is required");
    if (ref.field && ref.field !== "taskId") return fail("validation_failed", "reference field must be taskId");
    const id = local[ref.ref] ?? this.state.keys[ref.ref];
    return id ?? fail("reference_failed", `reference ${ref.ref} is not available`);
  }
  private inferCategory(text: string): Category {
    const value = text.toLowerCase();
    const rules: [Category, string[]][] = [
      ["planning", ["plan", "design", "architect", "strategy", "specification", "specify", "outline", "draft", "proposal", "tech spec", "write spec", "create spec"]],
      ["researching", ["research", "explore", "investigate", "search", "find", "analyze", "read", "understand", "study", "review docs", "check docs", "examine", "discover"]],
      ["debugging", ["debug", "troubleshoot", "trace error", "trace bug", "resolve error", "resolve issue", "investigate error", "fix bug", "fix error", "fix crash", "fix panic", "stack trace", "segfault", "deadlock"]],
      ["verifying", ["test", "verify", "validate", "check", "confirm", "assert", "ensure", "prove", "inspect"]],
      ["documenting", ["document", "write docs", "add docs", "update docs", "readme", "changelog", "comment", "comments", "documentation", "docstring", "javadoc", "godoc"]],
    ];
    for (const [category, words] of rules) if (words.some(word => value.includes(word))) return category;
    return "acting";
  }
  private validate(op: Operation, index: number): Failure | undefined {
    if (!isObj(op) || typeof op.key !== "string" || !op.key || !["create","update","get","list"].includes(op.op))
      return fail("validation_failed", `operation ${index} must contain a valid key and op`);
    const allowed: Record<Operation["op"], string[]> = {
      create: ["key","op","subject","description","activeForm","category","priority","metadata","parentTaskId","owner_id","status","active","addBlocks","addBlockedBy"],
      update: ["key","op","taskId","subject","description","activeForm","category","priority","metadata","status","active","parentTaskId","addBlocks","addBlockedBy","addNote","noteType"],
      get: ["key","op","taskId","include_audit"],
      list: ["key","op","subject","category","status","active","limit","offset"],
    };
    for (const field of Object.keys(op as object)) if (!allowed[op.op]?.includes(field))
      return fail("validation_failed", `operation ${op.key}: field ${field} is not valid for ${op.op}`);
    for (const field of ["taskId", "parentTaskId"] as const) {
      const error = this.validateRef((op as Record<string, unknown>)[field], field);
      if (error) return error;
    }
    for (const field of ["addBlocks", "addBlockedBy"] as const) {
      const refs = (op as Record<string, unknown>)[field];
      if (refs !== undefined && (!Array.isArray(refs) || refs.some(ref => ref === undefined || this.validateRef(ref, field)))) {
        return fail("validation_failed", `operation ${op.key}: ${field} must contain only task IDs or {ref, field:"taskId"} references`);
      }
    }
    const stringFields = ["subject", "description", "activeForm", "owner_id", "addNote", "noteType", "category", "priority", "status"] as const;
    for (const field of stringFields) {
      if ((op as Record<string, unknown>)[field] !== undefined && typeof (op as Record<string, unknown>)[field] !== "string")
        return fail("validation_failed", `operation ${op.key}: ${field} must be a string`);
    }
    if (op.metadata !== undefined && (!isObj(op.metadata) || !this.isJSONValue(op.metadata)))
      return fail("validation_failed", `operation ${op.key}: metadata must be a JSON-compatible object`);
    for (const field of ["active", "include_audit"] as const) {
      if ((op as Record<string, unknown>)[field] !== undefined && typeof (op as Record<string, unknown>)[field] !== "boolean")
        return fail("validation_failed", `operation ${op.key}: ${field} must be a boolean`);
    }
    if (op.op === "create" && (!op.subject || !op.subject.trim())) return fail("validation_failed", `operation ${op.key}: subject must not be blank`);
    if (op.active === true && (op.op === "create" || op.status !== undefined) && op.status !== "in_progress")
      return fail("validation_failed", `operation ${op.key}: active task must be in_progress`);
    if (op.category !== undefined && !CATEGORIES.includes(op.category)) return fail("validation_failed", `operation ${op.key}: invalid category ${op.category}`);
    if (op.priority !== undefined && !PRIORITIES.includes(op.priority)) return fail("validation_failed", `operation ${op.key}: invalid priority ${op.priority}`);
    if (op.status !== undefined && !["pending","in_progress","completed","deleted"].includes(op.status)) return fail("validation_failed", `operation ${op.key}: invalid status ${op.status}`);
    if (op.noteType !== undefined && !NOTE_TYPES.includes(op.noteType)) return fail("validation_failed", `operation ${op.key}: invalid noteType ${op.noteType}`);
    if (op.limit !== undefined && (!Number.isInteger(op.limit) || op.limit < 1 || op.limit > 500)) return fail("validation_failed", `operation ${op.key}: limit must be 1..500`);
    if (op.offset !== undefined && (!Number.isInteger(op.offset) || op.offset < 0)) return fail("validation_failed", `operation ${op.key}: offset must be non-negative`);
    return undefined;
  }
  private isJSONValue(value: unknown, seen = new Set<unknown>()): boolean {
    if (value === null || typeof value === "string" || typeof value === "boolean") return true;
    if (typeof value === "number") return Number.isFinite(value);
    if (typeof value !== "object" || seen.has(value)) return false;
    seen.add(value);
    const valid = Array.isArray(value)
      ? value.every(item => this.isJSONValue(item, seen))
      : isObj(value) && (Object.getPrototypeOf(value) === Object.prototype || Object.getPrototypeOf(value) === null) &&
        Object.values(value).every(item => this.isJSONValue(item, seen));
    seen.delete(value);
    return valid;
  }
  private validateRef(value: unknown, field: string): Failure | undefined {
    if (value === undefined) return undefined;
    if (typeof value === "string") {
      return value.trim() ? undefined : fail("validation_failed", `${field} must be a task ID or reference`);
    }
    if (!isObj(value) || typeof value.ref !== "string" || !value.ref.trim() ||
      (value.field !== undefined && value.field !== "taskId") ||
      Object.keys(value).some(key => key !== "ref" && key !== "field"))
      return fail("validation_failed", `${field} must be a task ID or {ref, field:"taskId"} reference`);
    return undefined;
  }
  execute(params: Params, signal?: AbortSignal): Batch {
    if (!isObj(params) || Object.keys(params).some(key => key !== "operations" && key !== "mode"))
      return { status: "failed", results: [{ key: "batch", op: "list", status: "failed", error: fail("validation_failed", "unknown batch field") }] };
    const ops = params.operations;
    if (Array.isArray(ops)) this.stats.reads += ops.filter(op => op?.op === "get" || op?.op === "list").length;
    if (!Array.isArray(ops) || !ops.length || ops.length > 50) return { status: "failed", results: [{ key: "batch", op: "list", status: "failed", error: fail("validation_failed", "operations must contain 1..50 operations") }] };
    if (params.mode && params.mode !== "sequential" && params.mode !== "atomic") return { status: "failed", results: [{ key: "batch", op: "list", status: "failed", error: fail("validation_failed", "unsupported mode") }] };
    const seen = new Map<string, "create"|"update"|"get"|"list">();
    for (let i=0;i<ops.length;i++) {
      const e = this.validate(ops[i], i); if (e) return { status: "failed", results: [{ key: ops[i]?.key ?? "batch", op: ops[i]?.op ?? "list", status: "failed", error: e }] };
      const prior = seen.get(ops[i].key);
      if (prior && (prior !== "create" || ops[i].op === "create")) return { status: "failed", results: [{ key: ops[i].key, op: ops[i].op, status: "failed", error: fail("validation_failed", `duplicate operation key ${ops[i].key}`) }] };
      seen.set(ops[i].key, prior ?? ops[i].op);
    }
    const before = this.snapshot(), local: Record<string,string> = {}, results: Result[] = [];
    let stopped = false;
    for (const op of ops) {
      if (stopped) { results.push({ key: op.key, op: op.op, status: "skipped" }); continue; }
      if (signal?.aborted) { results.push({ key: op.key, op: op.op, status: "failed", error: fail("cancelled", "operation cancelled") }); stopped = true; continue; }
      const operationBefore = this.snapshot(), localBefore = { ...local };
      const result = this.run(op, local);
      results.push(result);
      if (result.status === "succeeded" &&
        (op.op === "create" || (op.op === "update" && op.status !== "deleted"))) {
        const produced = result.data as any;
        if (produced?.task?.id) { this.state.keys[op.key] = produced.task.id; local[op.key] = produced.task.id; }
      }
      if (result.status === "failed") {
        stopped = true;
        this.restore(params.mode === "atomic" ? before : operationBefore);
        for (const key of Object.keys(local)) delete local[key];
        Object.assign(local, localBefore);
        if (params.mode === "atomic") this.restore(before);
      } else if (result.status === "succeeded" && params.mode !== "atomic" &&
        (op.op === "create" || op.op === "update")) {
        // Journal successful mutations only. Reads do not change durable state.
        this.commit();
      }
    }
    if (params.mode === "atomic" && !stopped &&
      ops.some((op, i) => (op.op === "create" || op.op === "update") && results[i]?.status === "succeeded")) this.commit();
    if (params.mode === "atomic" && stopped) {
      const failedIndex = results.findIndex(r => r.status === "failed");
      for (let i = 0; i < failedIndex; i++) {
        results[i] = { ...results[i], status: "failed", data: undefined,
          error: fail("atomic_rollback", "operation was rolled back because the atomic batch failed") };
      }
    }
    const status = params.mode === "atomic" && stopped ? "failed" :
      stopped ? (results.some(r => r.status === "succeeded") ? "partial" : "failed") : "succeeded";
    this.stats.failures += results.filter(result => result.status === "failed").length;
    const batch = { status, results } as Batch;
    this.emitOperation?.({ type: "pi-swarm-task-operation", data: {
      mode: params.mode ?? "sequential", status, results: clone(results), at: new Date().toISOString(),
    } });
    return batch;
  }
  private run(op: Operation, local: Record<string,string>): Result {
    const target = (r?: Ref) => this.resolve(r, local);
    if (op.op === "create") {
      // Upstream permits active on create, but TodoItem validation requires
      // an active task to be in progress. Creation then transfers sole focus.
      if (op.active === true && op.status !== "in_progress")
        return {key:op.key,op:op.op,status:"failed",error:fail("validation_failed",`cannot create task ${op.subject}: active task must be in_progress`)};
      const parent = op.parentTaskId === undefined ? undefined : target(op.parentTaskId); if (typeof parent !== "string" && op.parentTaskId) return { key:op.key,op:op.op,status:"failed",error:parent };
      const parentId = typeof parent === "string" ? parent : undefined;
      if (parentId && !this.find(parentId)) return {key:op.key,op:op.op,status:"failed",error:fail("not_found",`task ${parentId} not found`)};
      const deps = [...(op.addBlockedBy ?? [])].map(target);
      if (deps.some(x=>typeof x!=="string")) return {key:op.key,op:op.op,status:"failed",error:deps.find(x=>typeof x!=="string") as Failure};
      for (const d of deps as string[]) {
        const dependency = this.find(d);
        if (!dependency) return {key:op.key,op:op.op,status:"failed",error:fail("not_found",`dependency task ${d} not found`)};
        if (op.status === "in_progress" && dependency.status !== "completed")
          return {key:op.key,op:op.op,status:"failed",error:fail("validation_failed",`cannot create task ${op.subject}: dependency ${d} is not completed`)};
      }
      const blocks = [...(op.addBlocks ?? [])].map(target);
      if (blocks.some(x=>typeof x!=="string")) return {key:op.key,op:op.op,status:"failed",error:blocks.find(x=>typeof x!=="string") as Failure};
      for (const d of blocks as string[]) if (!this.find(d)) return {key:op.key,op:op.op,status:"failed",error:fail("not_found",`task ${d} not found`)};
      const now = new Date().toISOString(), task: Task = { id:String(this.state.nextId++), subject:op.subject!.trim(), description:op.description, activeForm:op.activeForm, category:op.category ?? this.inferCategory(`${op.subject} ${op.description ?? ""}`), priority:op.priority ?? "medium", metadata:op.metadata&&clone(op.metadata), parentTaskId:parentId, owner_id:op.owner_id, status:op.status === "in_progress" || op.status === "completed" ? op.status : "pending", active:op.status === "in_progress" || op.active === true, dependsOn:[...new Set(deps as string[])], notes:[], audit_events:[{action:"created",at:now}], createdAt:now, updatedAt:now };
      this.state.tasks.push(task);
      if (task.status === "in_progress") for (const other of this.state.tasks) if (other.id !== task.id) other.active = false;
      for (const d of blocks as string[]) {
        const other = this.find(d)!;
        if (d === task.id || this.reaches(task.id, d) || this.reaches(d, task.id)) return {key:op.key,op:op.op,status:"failed",error:fail("cycle","dependency would create a cycle")};
        if (!other.dependsOn.includes(task.id)) other.dependsOn.push(task.id);
      }
      return {key:op.key,op:op.op,status:"succeeded",data:{task:this.createAck(task)}};
    }
    if (op.op === "list") {
      const filtered = this.state.tasks.filter(t =>
        (!op.subject || t.subject.toLowerCase().includes(op.subject.toLowerCase())) &&
        (op.category === undefined || t.category === op.category) &&
        (op.status === undefined || t.status === op.status) &&
        (op.active === undefined || t.active === op.active));
      const offset=op.offset??0, limit=op.limit??50; return {key:op.key,op:op.op,status:"succeeded",data:{tasks:filtered.slice(offset,offset+limit).map(t=>this.summary(t)),pagination:{total:filtered.length,offset,limit,more:offset+limit<filtered.length}}};
    }
    const id = op.taskId === undefined
      ? (local[op.key] ?? this.state.keys[op.key] ?? fail("reference_failed", `operation key ${op.key} is not available`))
      : target(op.taskId);
    if (typeof id !== "string") return {key:op.key,op:op.op,status:"failed",error:id};
    const task=this.find(id); if (!task) return {key:op.key,op:op.op,status:"failed",error:fail("not_found",`task ${id} not found`)};
    if (op.op==="get") return {key:op.key,op:op.op,status:"succeeded",data:{task:this.outputTask(task, op.include_audit)}};
    if (op.active === true && op.status === undefined && task.status !== "in_progress")
      return {key:op.key,op:op.op,status:"failed",error:fail("validation_failed",`cannot focus task ${id}: active task must be in_progress`)};
    if (op.status === "deleted") {
      if (this.state.tasks.some(t => t.parentTaskId === id))
        return {key:op.key,op:op.op,status:"failed",error:fail("validation_failed",`cannot delete task ${id}: child task still exists`)};
      if (this.state.tasks.some(t => t.id !== id && t.dependsOn.includes(id)))
        return {key:op.key,op:op.op,status:"failed",error:fail("validation_failed",`cannot delete task ${id}: another task depends on it`)};
      this.state.tasks = this.state.tasks.filter(t => t.id !== id);
      for (const key of Object.keys(this.state.keys)) if (this.state.keys[key] === id) delete this.state.keys[key];
      for (const key of Object.keys(local)) if (local[key] === id) delete local[key];
      return {key:op.key,op:op.op,status:"succeeded",data:{}};
    }
    const deps = [...task.dependsOn]; for (const r of op.addBlockedBy??[]) { const d=target(r); if(typeof d!=="string") return {key:op.key,op:op.op,status:"failed",error:d}; if(!this.find(d)) return {key:op.key,op:op.op,status:"failed",error:fail("not_found",`dependency task ${d} not found`)}; if(d===id || this.reaches(d,id)) return {key:op.key,op:op.op,status:"failed",error:fail("cycle",`dependency would create a cycle`)}; if(!deps.includes(d)) deps.push(d); }
    const resultingStatus = op.status ?? task.status;
    if (resultingStatus === "in_progress") {
      for (const dependency of deps) {
        const dependencyTask = this.find(dependency);
        if (dependencyTask?.status !== "completed")
          return {key:op.key,op:op.op,status:"failed",error:fail("validation_failed",`cannot set task ${id} to in_progress: dependency ${dependency} is not completed`)};
      }
    }
    for (const r of op.addBlocks??[]) { const d=target(r); if(typeof d!=="string") return {key:op.key,op:op.op,status:"failed",error:d}; const other=this.find(d); if(!other) return {key:op.key,op:op.op,status:"failed",error:fail("not_found",`task ${d} not found`)}; if(d===id || this.reaches(id,d)) return {key:op.key,op:op.op,status:"failed",error:fail("cycle","dependency would create a cycle")}; if(!other.dependsOn.includes(id)) other.dependsOn.push(id); }
    if (op.parentTaskId !== undefined) {
      const p = target(op.parentTaskId);
      if (typeof p !== "string") return {key:op.key,op:op.op,status:"failed",error:p};
      if (!this.find(p)) return {key:op.key,op:op.op,status:"failed",error:fail("not_found",`parent task ${p} not found`)};
      if (p === id || this.parentReaches(p, id)) return {key:op.key,op:op.op,status:"failed",error:fail("cycle","parent would create a cycle")};
    }
    if (op.status === "completed") {
      const child = this.state.tasks.find(t => t.parentTaskId === id && t.status !== "completed");
      if (child) return {key:op.key,op:op.op,status:"failed",error:fail("validation_failed",`cannot complete task ${id}: child task ${child.id} is not completed`)};
    }
    const mergedMetadata: Record<string, unknown> | undefined = op.metadata === undefined ? task.metadata : { ...(task.metadata ?? {}), ...clone(op.metadata) };
    if (op.metadata) for (const [key, value] of Object.entries(op.metadata)) if (value === null) delete (mergedMetadata as Record<string, unknown>)[key];
    Object.assign(task, { subject:op.subject?.trim()||task.subject, description:op.description??task.description, activeForm:op.activeForm??task.activeForm, category:op.category??task.category, priority:op.priority??task.priority, metadata:mergedMetadata, status:op.status??task.status, active:op.active??task.active, parentTaskId:op.parentTaskId === undefined ? task.parentTaskId : (target(op.parentTaskId) as string), dependsOn:deps, updatedAt:new Date().toISOString() });
    if (op.status === "in_progress") {
      for (const other of this.state.tasks) other.active = other.id === id;
      // Explicit false is applied after the focus transition.
      if (op.active === false) task.active = false;
    } else if (op.status !== undefined) {
      task.active = false;
    } else if (op.active === true) {
      for (const other of this.state.tasks) other.active = other.id === id;
    }
    const updatedAt = new Date().toISOString();
    if(op.addNote) {
      task.notes.push(op.addNote);
      if (op.noteType !== undefined) (task.typed_notes ??= []).push({text:op.addNote,type:op.noteType,at:updatedAt});
    }
    (task.audit_events ??= []).push({action:"updated",at:updatedAt});
    task.updatedAt = updatedAt;
    return {key:op.key,op:op.op,status:"succeeded",data:{task:this.updateAck(op, task)}};
  }
  private createAck(task: Task): Record<string, unknown> {
    return {id:task.id, subject:task.subject, status:task.status, active:task.active, parent_id:task.parentTaskId ?? ""};
  }
  private updateAck(op: Operation, task: Task): Record<string, unknown> {
    const ack: Record<string, unknown> = {...this.createAck(task)};
    if (op.description !== undefined) ack.description = task.description;
    if (op.activeForm !== undefined) ack.active_form = task.activeForm;
    if (op.category !== undefined) ack.category = task.category;
    if (op.priority !== undefined) ack.priority = task.priority;
    if (op.metadata !== undefined) ack.metadata = task.metadata;
    if ((op.addBlocks?.length ?? 0) > 0) ack.blocks = this.blockedBy(task.id);
    if ((op.addBlockedBy?.length ?? 0) > 0) ack.depends_on = [...task.dependsOn];
    if (op.addNote) ack.note_added = true;
    return ack;
  }
  private summary(task: Task): Record<string, unknown> {
    const result: Record<string, unknown> = {...this.createAck(task)};
    if (task.activeForm) result.active_form = task.activeForm;
    if (task.category) result.category = task.category;
    if (task.priority) result.priority = task.priority;
    if (task.dependsOn.length) result.depends_on = [...task.dependsOn];
    const blocks = this.blockedBy(task.id); if (blocks.length) result.blocks = blocks;
    if (task.owner_id) result.owner_id = task.owner_id;
    return result;
  }
  private blockedBy(id: string): string[] { return this.state.tasks.filter(t => t.dependsOn.includes(id)).map(t => t.id); }
  private outputTask(task: Task, includeAudit = false): Record<string, unknown> {
    const result: Record<string, unknown> = {
      id: task.id, content: task.subject, status: task.status,
      priority: task.priority,
      ...(task.active ? {active: true} : {}),
      ...(task.description !== undefined ? {description: task.description} : {}),
      ...(task.activeForm !== undefined ? {active_form: task.activeForm} : {}),
      ...(task.metadata !== undefined ? {metadata: clone(task.metadata)} : {}),
      ...(task.category !== undefined ? {category: task.category} : {}),
      ...(task.dependsOn.length ? {depends_on: [...task.dependsOn]} : {}),
      ...(this.blockedBy(task.id).length ? {blocks: this.blockedBy(task.id)} : {}),
      ...(task.owner_id !== undefined ? {owner_id: task.owner_id} : {}),
      ...(task.parentTaskId !== undefined ? {parent_id: task.parentTaskId} : {}),
      ...(task.notes.length ? {notes: [...task.notes]} : {}),
      created_at: task.createdAt, updated_at: task.updatedAt,
    };
    if (includeAudit) {
      if (task.audit_events) result.audit_events = task.audit_events.map(event => ({
        type: event.action, timestamp: event.at,
      }));
      if (task.typed_notes) result.typed_notes = task.typed_notes.map(note => ({
        type: note.type, content: note.text, created_at: note.at,
      }));
    }
    return result;
  }
  private reaches(from:string, to:string, visited=new Set<string>()):boolean { if(visited.has(from)) return false; visited.add(from); const t=this.find(from); return !!t && (t.dependsOn.includes(to)||t.dependsOn.some(d=>this.reaches(d,to,visited))); }
  private parentReaches(from:string, to:string, visited=new Set<string>()): boolean {
    if (from === to) return true; if (visited.has(from)) return false; visited.add(from);
    const parent = this.find(from)?.parentTaskId; return !!parent && this.parentReaches(parent, to, visited);
  }
}

export function registerTaskManage(pi: { registerTool(tool: unknown): void; appendEntry(type: string, data: unknown): void; on(event: string, handler: (event: unknown, ctx: {sessionManager?: {getEntries(): readonly unknown[]; getBranch?(): readonly unknown[]}; ui?: {setWidget(key: string, content: unknown): void}})=>void): void }, presentation = taskManageRenderers): TaskManager {
  const manager = new TaskManager(
    entry => pi.appendEntry(entry.type, entry.data),
    event => pi.appendEntry(event.type, event.data),
  );
  let ui: {setWidget(key: string, content: unknown): void} | undefined;
  const refreshWidget = (ctx?: {ui?: {setWidget(key: string, content: unknown): void}}) => {
    ui = ctx?.ui ?? ui;
    if (!ui) return;
    const tasks = manager.snapshot().tasks.filter(task => task.status !== "deleted");
    const visible = tasks.some(task => task.status === "pending" || task.status === "in_progress");
    ui.setWidget("swarm-tasks", visible
      ? ((_tui: unknown, theme: RenderTheme) => taskWidgetRenderer(tasks, theme))
      : undefined);
  };
  pi.on("session_start", (_event, ctx) => {
    manager.rehydrate((ctx.sessionManager?.getBranch?.() ?? ctx.sessionManager?.getEntries() ?? []) as JournalEntry[]);
    refreshWidget(ctx);
  });
  pi.registerTool({ name:"TaskManage", label:"Manage tasks", description:"Manage ordered tasks. sequential commits the successful prefix; atomic commits all or rolls back.", parameters:taskManageSchema,
    renderShell: "self",
    promptSnippet: "TaskManage: track multi-step work with durable ordered tasks.",
    promptGuidelines: ["Use TaskManage for multi-step work and keep exactly one active task when working sequentially."],
    renderCall: presentation.renderCall,
    renderResult: presentation.renderResult,
    execute: async (_id:string, params:Params, signal?:AbortSignal, _onUpdate?: unknown, ctx?: {ui?: {setWidget(key: string, content: unknown): void}}) => {
      const batch = manager.execute(params,signal);
      refreshWidget(ctx);
      return { content:[{type:"text",text:JSON.stringify(batch)}] };
    } });
  return manager;
}
