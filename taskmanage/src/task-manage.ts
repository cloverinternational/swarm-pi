export const CATEGORIES = ["researching", "planning", "acting", "verifying", "debugging", "documenting"] as const;
export const NOTE_TYPES = ["decision", "blocker", "learning", "milestone", "question", "observation", "other"] as const;
export type Category = typeof CATEGORIES[number];
export type Status = "pending" | "in_progress" | "completed" | "deleted";
export type NoteType = typeof NOTE_TYPES[number];
export type Mode = "sequential" | "atomic";
export type Ref = string | { ref: string; field?: "taskId" };

export interface TaskNote { text: string; type: NoteType; at: string }
export interface Task {
  id: string; subject: string; description?: string; activeForm?: string; category?: Category;
  metadata?: Record<string, unknown>; parentTaskId?: string; owner_id?: string; status: Status;
  active?: boolean; dependsOn: string[]; notes: TaskNote[]; createdAt: string; updatedAt: string;
}
export interface Operation {
  key: string; op: "create" | "update" | "get" | "list"; taskId?: Ref; subject?: string;
  description?: string; activeForm?: string; category?: Category; metadata?: Record<string, unknown>;
  parentTaskId?: Ref; owner_id?: string; status?: Status; active?: boolean; limit?: number; offset?: number;
  addBlocks?: Ref[]; addBlockedBy?: Ref[]; addNote?: string; noteType?: NoteType; include_audit?: boolean;
}
export interface Params { operations: Operation[]; mode?: Mode }
export interface Failure { code: string; message: string; retryable: boolean }
export interface Result { key: string; op: Operation["op"]; status: "succeeded" | "failed" | "skipped"; data?: unknown; error?: Failure }
export interface Batch { status: "succeeded" | "partial" | "failed"; results: Result[] }
export interface JournalEntry { type: "pi-swarm-task-state"; data: State }
interface State { nextId: number; tasks: Task[]; keys: Record<string, string> }

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
        category: { type: "string", enum: CATEGORIES }, metadata: { type: "object" }, owner_id: { type: "string" },
        status: { type: "string", enum: ["pending", "in_progress", "completed", "deleted"] }, active: { type: "boolean" },
        limit: { type: "integer", minimum: 1, maximum: 500 }, offset: { type: "integer", minimum: 0 },
        addBlocks: { type: "array" }, addBlockedBy: { type: "array" }, addNote: { type: "string" },
        noteType: { type: "string", enum: NOTE_TYPES }, include_audit: { type: "boolean" }
      }
    }}
  }
} as const;

export class TaskManager {
  private state: State = { nextId: 1, tasks: [], keys: {} };
  constructor(private readonly persist?: (entry: JournalEntry) => void) {}
  snapshot(): State { return clone(this.state); }
  restore(state: State): void { this.state = clone(state); }
  rehydrate(entries: readonly JournalEntry[]): void {
    const last = [...entries].reverse().find(e => e.type === "pi-swarm-task-state");
    if (last) this.restore(last.data);
  }
  private commit(): void { this.persist?.({ type: "pi-swarm-task-state", data: this.snapshot() }); }
  private find(id: string): Task | undefined { return this.state.tasks.find(t => t.id === id); }
  private resolve(ref: Ref | undefined, local: Record<string, string>): string | Failure {
    if (typeof ref === "string") return ref;
    if (!ref) return fail("validation_failed", "taskId is required");
    if (ref.field && ref.field !== "taskId") return fail("validation_failed", "reference field must be taskId");
    const id = local[ref.ref] ?? this.state.keys[ref.ref];
    return id ?? fail("reference_failed", `reference ${ref.ref} is not available`);
  }
  private validate(op: Operation, index: number): Failure | undefined {
    if (!isObj(op) || typeof op.key !== "string" || !op.key || !["create","update","get","list"].includes(op.op))
      return fail("validation_failed", `operation ${index} must contain a valid key and op`);
    if (op.op === "create" && (!op.subject || !op.subject.trim())) return fail("validation_failed", `operation ${op.key}: subject must not be blank`);
    if (op.limit !== undefined && (!Number.isInteger(op.limit) || op.limit < 1 || op.limit > 500)) return fail("validation_failed", `operation ${op.key}: limit must be 1..500`);
    if (op.offset !== undefined && (!Number.isInteger(op.offset) || op.offset < 0)) return fail("validation_failed", `operation ${op.key}: offset must be non-negative`);
    return undefined;
  }
  execute(params: Params, signal?: AbortSignal): Batch {
    const ops = params?.operations;
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
      const result = this.run(op, local);
      results.push(result);
      if (result.status === "failed") { stopped = true; if (params.mode === "atomic") this.restore(before); }
    }
    if (params.mode === "atomic" && !stopped) this.commit();
    else if (params.mode !== "atomic" && results.some(r => r.status === "succeeded")) this.commit();
    if (params.mode === "atomic" && stopped) {
      const failedIndex = results.findIndex(r => r.status === "failed");
      for (let i = 0; i < failedIndex; i++) {
        results[i] = { ...results[i], status: "failed", data: undefined,
          error: fail("atomic_rollback", "operation was rolled back because the atomic batch failed") };
      }
    }
    const status = params.mode === "atomic" && stopped ? "failed" :
      stopped ? (results.some(r => r.status === "succeeded") ? "partial" : "failed") : "succeeded";
    return { status, results };
  }
  private run(op: Operation, local: Record<string,string>): Result {
    const target = (r?: Ref) => this.resolve(r, local);
    if (op.op === "create") {
      const parent = op.parentTaskId === undefined ? undefined : target(op.parentTaskId); if (typeof parent !== "string" && op.parentTaskId) return { key:op.key,op:op.op,status:"failed",error:parent };
      const parentId = typeof parent === "string" ? parent : undefined;
      if (parentId && !this.find(parentId)) return {key:op.key,op:op.op,status:"failed",error:fail("not_found",`task ${parentId} not found`)};
      const deps = [...(op.addBlockedBy ?? [])].map(target); if (deps.some(x=>typeof x!=="string")) return {key:op.key,op:op.op,status:"failed",error:deps.find(x=>typeof x!=="string") as Failure};
      const now = new Date().toISOString(), task: Task = { id:String(this.state.nextId++), subject:op.subject!.trim(), description:op.description, activeForm:op.activeForm, category:op.category, metadata:op.metadata&&clone(op.metadata), parentTaskId:parentId, owner_id:op.owner_id, status:"pending", active:false, dependsOn:deps as string[], notes:[], createdAt:now, updatedAt:now };
      this.state.tasks.push(task); this.state.keys[op.key]=task.id; local[op.key]=task.id;
      return {key:op.key,op:op.op,status:"succeeded",data:{task:clone(task)}};
    }
    if (op.op === "list") {
      const filtered = op.subject ? this.state.tasks.filter(t=>t.subject.includes(op.subject!)) : this.state.tasks;
      const offset=op.offset??0, limit=op.limit??50; return {key:op.key,op:op.op,status:"succeeded",data:{tasks:clone(filtered.slice(offset,offset+limit)),pagination:{total:filtered.length,offset,limit,more:offset+limit<filtered.length}}};
    }
    const id=target(op.taskId ?? op.key); if (typeof id !== "string") return {key:op.key,op:op.op,status:"failed",error:id};
    const task=this.find(id); if (!task) return {key:op.key,op:op.op,status:"failed",error:fail("not_found",`task ${id} not found`)};
    if (op.op==="get") return {key:op.key,op:op.op,status:"succeeded",data:{task:clone(task)}};
    const deps = [...task.dependsOn]; for (const r of op.addBlockedBy??[]) { const d=target(r); if(typeof d!=="string") return {key:op.key,op:op.op,status:"failed",error:d}; if(d===id || this.reaches(d,id)) return {key:op.key,op:op.op,status:"failed",error:fail("cycle",`dependency would create a cycle`)}; if(!deps.includes(d)) deps.push(d); }
    for (const r of op.addBlocks??[]) { const d=target(r); if(typeof d!=="string") return {key:op.key,op:op.op,status:"failed",error:d}; const other=this.find(d); if(!other) return {key:op.key,op:op.op,status:"failed",error:fail("not_found",`task ${d} not found`)}; if(d===id || this.reaches(id,d)) return {key:op.key,op:op.op,status:"failed",error:fail("cycle","dependency would create a cycle")}; if(!other.dependsOn.includes(id)) other.dependsOn.push(id); }
    Object.assign(task, { subject:op.subject?.trim()||task.subject, description:op.description??task.description, activeForm:op.activeForm??task.activeForm, category:op.category??task.category, metadata:op.metadata??task.metadata, owner_id:op.owner_id??task.owner_id, status:op.status??task.status, active:op.active??task.active, dependsOn:deps, updatedAt:new Date().toISOString() });
    if(op.addNote) task.notes.push({text:op.addNote,type:op.noteType??"other",at:new Date().toISOString()});
    return {key:op.key,op:op.op,status:"succeeded",data:{task:clone(task)}};
  }
  private reaches(from:string, to:string, visited=new Set<string>()):boolean { if(visited.has(from)) return false; visited.add(from); const t=this.find(from); return !!t && (t.dependsOn.includes(to)||t.dependsOn.some(d=>this.reaches(d,to,visited))); }
}

export function registerTaskManage(pi: { registerTool(tool: unknown): void; appendEntry(type: string, data: unknown): void; on(event: string, handler: (event: unknown, ctx: {sessionManager?: {getEntries(): readonly unknown[]}})=>void): void }): TaskManager {
  const manager = new TaskManager(entry => pi.appendEntry(entry.type, entry.data));
  pi.on("session_start", (_event, ctx) => manager.rehydrate((ctx.sessionManager?.getEntries() ?? []) as JournalEntry[]));
  pi.registerTool({ name:"TaskManage", label:"Manage tasks", description:"Manage ordered tasks. sequential commits the successful prefix; atomic commits all or rolls back.", parameters:taskManageSchema,
    execute: async (_id:string, params:Params, signal?:AbortSignal) => ({ content:[{type:"text",text:JSON.stringify(manager.execute(params,signal))}] }) });
  return manager;
}
