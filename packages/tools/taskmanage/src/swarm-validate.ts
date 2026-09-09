/**
 * Faithful port of Swarm's TaskManage.Validate (mono/swarm-sdk
 * internal/tools/ii/task_manage.go parseTaskOperations +
 * task_operation.go parseTaskOperation/validateTaskOperationFields/
 * parseTaskTarget/validateTaskOperation). The registry runs Validate BEFORE
 * execution and surfaces failures as a tool ERROR:
 *   "Error executing TaskManage: validation failed for TaskManage: <message> (error_id=…)"
 * rather than the JSON `{"status":"failed",…}` batch that runtime failures use.
 * Returns the Go message, or undefined when the batch is valid.
 */
const MAX_OPERATIONS = 50, DEFAULT_LIST_LIMIT = 50, MAX_LIST_LIMIT = 500;
const MINIMAL_CREATE = `{"key":"<your-key>","op":"create","subject":"<short imperative title>"}`;
const CATEGORIES = new Set(["researching", "planning", "acting", "verifying", "debugging", "documenting"]);
const PRIORITIES = new Set(["low", "medium", "high"]);
const NOTE_TYPES = new Set(["decision", "blocker", "learning", "milestone", "question", "observation", "other"]);
const ALLOWED: Record<string, string[]> = {
  create: ["subject", "description", "activeForm", "category", "metadata", "parentTaskId", "owner_id", "status", "active", "addBlocks", "addBlockedBy"],
  update: ["taskId", "status", "category", "subject", "description", "activeForm", "active", "parentTaskId", "metadata", "addBlocks", "addBlockedBy", "addNote", "noteType"],
  get: ["taskId", "include_audit"],
  list: ["category", "status", "active", "limit", "offset", "subject"],
};

const q = (v: unknown) => JSON.stringify(String(v));
const isObj = (v: unknown): v is Record<string, unknown> => !!v && typeof v === "object" && !Array.isArray(v);
class Failure extends Error {}
const fail = (message: string): never => { throw new Failure(message); };

function requiredString(raw: Record<string, unknown>, field: string): string {
  if (!(field in raw)) fail(`${field} is required`);
  const v = raw[field];
  if (typeof v !== "string" || v.trim() === "") fail(`${field} must be a non-empty string`);
  return v as string;
}
function optionalString(raw: Record<string, unknown>, field: string): string | undefined {
  if (!(field in raw)) return undefined;
  const v = raw[field];
  if (typeof v !== "string") fail(`${field} must be a string`);
  return v as string;
}
function integer(value: unknown, field: string): number {
  if (typeof value !== "number" || !Number.isInteger(value)) fail(`${field} must be an integer`);
  return value as number;
}
function target(value: unknown, field: string): void {
  if (typeof value === "string") { if (value === "" && field !== "parentTaskId") fail(`${field} must not be empty`); return; }
  if (isObj(value)) {
    for (const key of Object.keys(value)) if (key !== "ref" && key !== "field") fail(`${field} reference contains unknown field ${q(key)}`);
    if (!("ref" in value)) fail(`${field} reference requires ref`);
    const ref = value.ref;
    if (typeof ref !== "string" || ref.trim() === "") fail(`${field} reference ref must be a non-empty string`);
    let refField = "taskId";
    if ("field" in value) { if (typeof value.field !== "string") fail(`${field} reference field must be a string`); refField = value.field as string; }
    if (refField !== "taskId") fail(`${field} reference field must be taskId`);
    return;
  }
  fail(`${field} must be a task ID or reference`);
}
function targets(value: unknown, field: string): void {
  if (!Array.isArray(value)) fail(`${field} must be an array`);
  (value as unknown[]).forEach((raw, i) => target(raw, `${field}[${i}]`));
}

function parseOperation(raw: Record<string, unknown>, index: number): { key: string; kind: string } {
  let key = "";
  try { key = requiredString(raw, "key"); } catch (e) { if (e instanceof Failure) fail(`operation ${index}: ${e.message}`); throw e; }
  // Field-level failures are prefixed with the operation key; target
  // (taskId/parentTaskId/addBlocks/addBlockedBy) failures are returned as-is.
  const opScoped = (fn: () => void) => { try { fn(); } catch (e) { if (e instanceof Failure) fail(`operation ${q(key)}: ${e.message}`); throw e; } };
  let kind = "";
  opScoped(() => { kind = requiredString(raw, "op"); });
  opScoped(() => {
    const fields = ALLOWED[kind];
    if (!fields) fail(`unsupported op ${q(kind)}`);
    const common = new Set(["key", "op", ...fields]);
    for (const field of Object.keys(raw)) {
      if (common.has(field)) continue;
      if (kind === "create" && (field === "addNote" || field === "noteType"))
        fail(`field ${q(field)} is not valid for create; create the task first, then add the note with an update operation targeting taskId:{"ref":${q(key)}}`);
      fail(`field ${q(field)} is not valid for ${kind}`);
    }
  });
  if ("taskId" in raw) target(raw.taskId, "taskId");
  if ("parentTaskId" in raw) target(raw.parentTaskId, "parentTaskId");
  if ("addBlocks" in raw) targets(raw.addBlocks, "addBlocks");
  if ("addBlockedBy" in raw) targets(raw.addBlockedBy, "addBlockedBy");
  let subject = "", category = "", priority = "", status = "", noteType = "", limit = DEFAULT_LIST_LIMIT, offset = 0;
  opScoped(() => {
    subject = optionalString(raw, "subject") ?? "";
    optionalString(raw, "description"); optionalString(raw, "activeForm");
    category = optionalString(raw, "category") ?? "";
    priority = optionalString(raw, "priority") ?? "";
    if ("metadata" in raw && !isObj(raw.metadata)) fail("metadata must be an object");
    optionalString(raw, "owner_id");
    status = optionalString(raw, "status") ?? "";
    if ("active" in raw && typeof raw.active !== "boolean") fail("active must be a boolean");
    if ("limit" in raw) limit = integer(raw.limit, "limit");
    if ("offset" in raw) offset = integer(raw.offset, "offset");
    optionalString(raw, "addNote");
    noteType = optionalString(raw, "noteType") ?? "";
    if ("include_audit" in raw && typeof raw.include_audit !== "boolean") fail("include_audit must be a boolean");
  });
  // validateTaskOperation
  if (kind === "create" && subject.trim() === "") fail(`operation ${q(key)}: op:"create" requires a non-blank "subject". A minimal valid create is ${MINIMAL_CREATE} — "description" is optional and is never required. Retry this operation with "subject" set to a short imperative title.`);
  if (category !== "" && !CATEGORIES.has(category)) fail(`operation ${q(key)}: invalid category ${q(category)}`);
  if (priority !== "" && !PRIORITIES.has(priority)) fail(`operation ${q(key)}: invalid priority ${q(priority)}`);
  if (status !== "" && !["pending", "in_progress", "completed", "deleted"].includes(status)) fail(`operation ${q(key)}: invalid status ${q(status)}`);
  if (noteType !== "" && !NOTE_TYPES.has(noteType)) fail(`operation ${q(key)}: invalid noteType ${q(noteType)}`);
  if (kind === "list") {
    if (limit < 1 || limit > MAX_LIST_LIMIT) fail(`operation ${q(key)}: limit must be between 1 and ${MAX_LIST_LIMIT}`);
    if (offset < 0) fail(`operation ${q(key)}: offset must be non-negative`);
  }
  return { key, kind };
}

export function swarmValidateTaskManageParams(params: unknown): string | undefined {
  try {
    const raw = isObj(params) ? params : {};
    for (const field of Object.keys(raw)) if (field !== "operations" && field !== "mode") fail(`unknown top-level field ${q(field)}`);
    const ops = raw.operations;
    if (!Array.isArray(ops) || ops.length === 0) fail("operations must contain at least one operation");
    if ((ops as unknown[]).length > MAX_OPERATIONS) fail(`operations exceeds maximum of ${MAX_OPERATIONS}`);
    let mode = "sequential";
    if ("mode" in raw) { if (typeof raw.mode !== "string") fail("mode must be a string"); mode = raw.mode as string; }
    if (mode !== "sequential" && mode !== "atomic") fail(`unsupported mode ${q(mode)}`);
    const first = new Map<string, string>();
    (ops as unknown[]).forEach((item, index) => {
      if (!isObj(item)) fail(`operation ${index} must be an object`);
      const op = parseOperation(item as Record<string, unknown>, index);
      const seen = first.get(op.key);
      if (seen !== undefined) {
        const rawOp = (ops as unknown[])[index] as Record<string, unknown>;
        const sameCreateFollowup = seen === "create" && op.kind !== "create";
        const explicitTargetFollowup = op.kind === "update" && ("taskId" in rawOp);
        if (!sameCreateFollowup && !explicitTargetFollowup) fail(`duplicate operation key ${q(op.key)}: keys must be unique per operation`);
      } else first.set(op.key, op.kind);
    });
    return undefined;
  } catch (e) {
    if (e instanceof Failure) return e.message;
    throw e;
  }
}

/** Go time.Now().MarshalJSON(): RFC3339 with nanoseconds, trailing zeros trimmed. */
export function goNow(): string {
  const ms = Date.now();
  const iso = new Date(ms).toISOString(); // ...sss'Z'
  const sub = String(Number(process.hrtime.bigint() % 1_000_000n)).padStart(6, "0");
  const frac = (iso.slice(20, 23) + sub).replace(/0+$/, "");
  return `${iso.slice(0, 19)}${frac ? `.${frac}` : ""}Z`;
}
