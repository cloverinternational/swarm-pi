export const CATEGORIES = ["researching", "planning", "acting", "verifying", "debugging", "documenting"] as const;
export const NOTE_TYPES = ["decision", "blocker", "learning", "milestone", "question", "observation", "other"] as const;
export type Category = typeof CATEGORIES[number];
export type Status = "pending" | "in_progress" | "completed" | "deleted";
export type NoteType = typeof NOTE_TYPES[number];
export type Mode = "sequential" | "atomic";
export type Ref = string | { ref: string; field?: "taskId" };

export interface AuditEvent { action: "created" | "updated"; at: string }
export interface TaskNote { text: string; type: NoteType; at: string }
export interface Task {
  id: string; subject: string; description?: string; activeForm?: string; category?: Category;
  metadata?: Record<string, unknown>; parentTaskId?: string; owner_id?: string; status: Status;
  active?: boolean; dependsOn: string[]; notes: TaskNote[]; createdAt: string; updatedAt: string;
  audit?: AuditEvent[];
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
        addBlocks: { type: "array", items: { oneOf: [{ type: "string" }, { type: "object", required: ["ref"], additionalProperties: false, properties: { ref: { type: "string" }, field: { type: "string", enum: ["taskId"] } } }] } },
        addBlockedBy: { type: "array", items: { oneOf: [{ type: "string" }, { type: "object", required: ["ref"], additionalProperties: false, properties: { ref: { type: "string" }, field: { type: "string", enum: ["taskId"] } } }] } },
        addNote: { type: "string" },
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
      create: ["key","op","subject","description","activeForm","category","metadata","parentTaskId","owner_id","status","active","addBlocks","addBlockedBy"],
      update: ["key","op","taskId","subject","description","activeForm","category","metadata","owner_id","status","active","parentTaskId","addBlocks","addBlockedBy","addNote","noteType"],
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
      if (refs !== undefined && (!Array.isArray(refs) || refs.some(ref => this.validateRef(ref, field)))) {
        return fail("validation_failed", `operation ${op.key}: ${field} must contain only task IDs or {ref, field:"taskId"} references`);
      }
    }
    if (op.op === "create" && (!op.subject || !op.subject.trim())) return fail("validation_failed", `operation ${op.key}: subject must not be blank`);
    if (op.limit !== undefined && (!Number.isInteger(op.limit) || op.limit < 1 || op.limit > 500)) return fail("validation_failed", `operation ${op.key}: limit must be 1..500`);
    if (op.offset !== undefined && (!Number.isInteger(op.offset) || op.offset < 0)) return fail("validation_failed", `operation ${op.key}: offset must be non-negative`);
    return undefined;
  }
  private validateRef(value: unknown, field: string): Failure | undefined {
    if (value === undefined || typeof value === "string") return undefined;
    if (!isObj(value) || typeof value.ref !== "string" || !value.ref.trim() ||
      (value.field !== undefined && value.field !== "taskId") ||
      Object.keys(value).some(key => key !== "ref" && key !== "field"))
      return fail("validation_failed", `${field} must be a task ID or {ref, field:"taskId"} reference`);
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
      const operationBefore = this.snapshot(), localBefore = { ...local };
      const result = this.run(op, local);
      results.push(result);
      if (result.status === "succeeded" && op.op !== "update" || (result.status === "succeeded" && op.status !== "deleted")) {
        const produced = result.data as any;
        if (produced?.task?.id) { this.state.keys[op.key] = produced.task.id; local[op.key] = produced.task.id; }
      }
      if (result.status === "failed") {
        stopped = true;
        this.restore(params.mode === "atomic" ? before : operationBefore);
        for (const key of Object.keys(local)) delete local[key];
        Object.assign(local, localBefore);
        if (params.mode === "atomic") this.restore(before);
      }
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
      const deps = [...(op.addBlockedBy ?? [])].map(target);
      if (deps.some(x=>typeof x!=="string")) return {key:op.key,op:op.op,status:"failed",error:deps.find(x=>typeof x!=="string") as Failure};
      for (const d of deps as string[]) if (!this.find(d)) return {key:op.key,op:op.op,status:"failed",error:fail("not_found",`dependency task ${d} not found`)};
      const blocks = [...(op.addBlocks ?? [])].map(target);
      if (blocks.some(x=>typeof x!=="string")) return {key:op.key,op:op.op,status:"failed",error:blocks.find(x=>typeof x!=="string") as Failure};
      for (const d of blocks as string[]) if (!this.find(d)) return {key:op.key,op:op.op,status:"failed",error:fail("not_found",`task ${d} not found`)};
      const now = new Date().toISOString(), task: Task = { id:String(this.state.nextId++), subject:op.subject!.trim(), description:op.description, activeForm:op.activeForm, category:op.category ?? this.inferCategory(`${op.subject} ${op.description ?? ""}`), metadata:op.metadata&&clone(op.metadata), parentTaskId:parentId, owner_id:op.owner_id, status:op.status === "in_progress" || op.status === "completed" ? op.status : "pending", active:op.active ?? op.status === "in_progress", dependsOn:[...new Set(deps as string[])], notes:[], audit:[{action:"created",at:now}], createdAt:now, updatedAt:now };
      this.state.tasks.push(task); this.state.keys[op.key]=task.id; local[op.key]=task.id;
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
    if (op.status === "deleted") {
      if (this.state.tasks.some(t => t.parentTaskId === id))
        return {key:op.key,op:op.op,status:"failed",error:fail("validation_failed",`cannot delete task ${id}: child task still exists`)};
      if (this.state.tasks.some(t => t.id !== id && t.dependsOn.includes(id)))
        return {key:op.key,op:op.op,status:"failed",error:fail("validation_failed",`cannot delete task ${id}: another task depends on it`)};
      this.state.tasks = this.state.tasks.filter(t => t.id !== id);
      for (const key of Object.keys(this.state.keys)) if (this.state.keys[key] === id) delete this.state.keys[key];
      return {key:op.key,op:op.op,status:"succeeded",data:{}};
    }
    const deps = [...task.dependsOn]; for (const r of op.addBlockedBy??[]) { const d=target(r); if(typeof d!=="string") return {key:op.key,op:op.op,status:"failed",error:d}; if(!this.find(d)) return {key:op.key,op:op.op,status:"failed",error:fail("not_found",`dependency task ${d} not found`)}; if(d===id || this.reaches(d,id)) return {key:op.key,op:op.op,status:"failed",error:fail("cycle",`dependency would create a cycle`)}; if(!deps.includes(d)) deps.push(d); }
    for (const r of op.addBlocks??[]) { const d=target(r); if(typeof d!=="string") return {key:op.key,op:op.op,status:"failed",error:d}; const other=this.find(d); if(!other) return {key:op.key,op:op.op,status:"failed",error:fail("not_found",`task ${d} not found`)}; if(d===id || this.reaches(id,d)) return {key:op.key,op:op.op,status:"failed",error:fail("cycle","dependency would create a cycle")}; if(!other.dependsOn.includes(id)) other.dependsOn.push(id); }
    if (op.parentTaskId !== undefined) {
      const p = target(op.parentTaskId);
      if (typeof p !== "string") return {key:op.key,op:op.op,status:"failed",error:p};
      if (!this.find(p)) return {key:op.key,op:op.op,status:"failed",error:fail("not_found",`parent task ${p} not found`)};
      if (p === id || this.parentReaches(p, id)) return {key:op.key,op:op.op,status:"failed",error:fail("cycle","parent would create a cycle")};
    }
    if (op.status === "in_progress") {
      for (const dependency of deps) {
        const dependencyTask = this.find(dependency);
        if (dependencyTask?.status !== "completed")
          return {key:op.key,op:op.op,status:"failed",error:fail("validation_failed",`cannot set task ${id} to in_progress: dependency ${dependency} is not completed`)};
      }
    }
    if (op.status === "completed") {
      const child = this.state.tasks.find(t => t.parentTaskId === id && t.status !== "completed");
      if (child) return {key:op.key,op:op.op,status:"failed",error:fail("validation_failed",`cannot complete task ${id}: child task ${child.id} is not completed`)};
    }
    const mergedMetadata: Record<string, unknown> | undefined = op.metadata === undefined ? task.metadata : { ...(task.metadata ?? {}), ...clone(op.metadata) };
    if (op.metadata) for (const [key, value] of Object.entries(op.metadata)) if (value === null) delete (mergedMetadata as Record<string, unknown>)[key];
    Object.assign(task, { subject:op.subject?.trim()||task.subject, description:op.description??task.description, activeForm:op.activeForm??task.activeForm, category:op.category??task.category, metadata:mergedMetadata, owner_id:op.owner_id??task.owner_id, status:op.status??task.status, active:op.active??task.active, parentTaskId:op.parentTaskId === undefined ? task.parentTaskId : (target(op.parentTaskId) as string), dependsOn:deps, updatedAt:new Date().toISOString() });
    if (op.status === "in_progress") task.active = true;
    const updatedAt = new Date().toISOString();
    if(op.addNote) task.notes.push({text:op.addNote,type:op.noteType??"other",at:updatedAt});
    (task.audit ??= []).push({action:"updated",at:updatedAt});
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
    if (task.dependsOn.length) result.depends_on = [...task.dependsOn];
    const blocks = this.blockedBy(task.id); if (blocks.length) result.blocks = blocks;
    if (task.owner_id) result.owner_id = task.owner_id;
    return result;
  }
  private blockedBy(id: string): string[] { return this.state.tasks.filter(t => t.dependsOn.includes(id)).map(t => t.id); }
  private outputTask(task: Task, includeAudit = false): Task {
    const result = clone(task);
    if (!includeAudit) delete result.audit;
    return result;
  }
  private reaches(from:string, to:string, visited=new Set<string>()):boolean { if(visited.has(from)) return false; visited.add(from); const t=this.find(from); return !!t && (t.dependsOn.includes(to)||t.dependsOn.some(d=>this.reaches(d,to,visited))); }
  private parentReaches(from:string, to:string, visited=new Set<string>()): boolean {
    if (from === to) return true; if (visited.has(from)) return false; visited.add(from);
    const parent = this.find(from)?.parentTaskId; return !!parent && this.parentReaches(parent, to, visited);
  }
}

export function registerTaskManage(pi: { registerTool(tool: unknown): void; appendEntry(type: string, data: unknown): void; on(event: string, handler: (event: unknown, ctx: {sessionManager?: {getEntries(): readonly unknown[]}})=>void): void }): TaskManager {
  const manager = new TaskManager(entry => pi.appendEntry(entry.type, entry.data));
  pi.on("session_start", (_event, ctx) => manager.rehydrate((ctx.sessionManager?.getEntries() ?? []) as JournalEntry[]));
  pi.registerTool({ name:"TaskManage", label:"Manage tasks", description:"Manage ordered tasks. sequential commits the successful prefix; atomic commits all or rolls back.", parameters:taskManageSchema,
    execute: async (_id:string, params:Params, signal?:AbortSignal) => ({ content:[{type:"text",text:JSON.stringify(manager.execute(params,signal))}] }) });
  return manager;
}
