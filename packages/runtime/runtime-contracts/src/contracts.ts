/** Stable, JSON-safe runtime contracts shared by Pi adapters and hosts. */

export type ID = string & { readonly __brand: "StableID" };
const ID_RE = /^[a-zA-Z0-9][a-zA-Z0-9._:-]{0,127}$/;
export function createId(value: string): ID {
  if (!ID_RE.test(value)) throw new ContractError("invalid_id", `Invalid stable ID: ${JSON.stringify(value)}`);
  return value as ID;
}
export function newId(prefix = "evt"): ID {
  const suffix = globalThis.crypto?.randomUUID?.() ?? `${Date.now().toString(36)}-${Math.random().toString(36).slice(2)}`;
  return createId(`${prefix}:${suffix}`);
}

export const POLICY_CLASSES = ["KEEP", "OPT-IN", "DEFER-DISCOVER", "NEVER-DEFAULT"] as const;
export type PolicyClass = typeof POLICY_CLASSES[number];
export interface Capability { id: ID; aliases?: string[]; policy: PolicyClass; sideEffect?: string; hostBound?: boolean; credentialBound?: boolean; }
export interface CapabilitySet { capabilities: Capability[]; selected: ID[]; }

export interface PolicyContext {
  actorId: ID; sessionId: ID; capabilities: ReadonlySet<ID> | ID[];
  profileId?: ID; workspace?: string; consent?: "granted" | "denied" | "required";
  metadata?: JsonObject;
}

export const EVENT_KINDS = ["started", "progress", "content", "tool_call", "tool_result", "completed", "failed", "cancelled"] as const;
export type EventKind = typeof EVENT_KINDS[number];
export interface RuntimeEvent<T extends JsonValue = JsonValue> { id: ID; sequence: number; kind: EventKind; sessionId: ID; timestamp: string; payload?: T; redacted?: boolean; }
export function normalizeEvent<T extends JsonValue>(event: Omit<RuntimeEvent<T>, "timestamp"> & { timestamp?: string }): RuntimeEvent<T> {
  if (!Number.isSafeInteger(event.sequence) || event.sequence < 0) throw new ContractError("invalid_sequence", "Event sequence must be a non-negative safe integer");
  return { ...event, timestamp: event.timestamp ?? new Date().toISOString() };
}

export const OUTCOMES = ["success", "rejected", "denied", "timeout", "cancelled", "failed"] as const;
export type Outcome = typeof OUTCOMES[number];
export interface Success<T> { ok: true; outcome: "success"; value: T; }
export interface Failure { ok: false; outcome: Exclude<Outcome, "success">; error: RuntimeError; }
export type Result<T> = Success<T> | Failure;
export const ok = <T>(value: T): Success<T> => ({ ok: true, outcome: "success", value });
export const err = (outcome: Failure["outcome"], error: RuntimeError): Failure => ({ ok: false, outcome, error });

export type ErrorCode = "invalid_id" | "invalid_sequence" | "validation" | "not_found" | "denied" | "conflict" | "storage" | "internal";
export interface RuntimeError { code: ErrorCode; message: string; retryable?: boolean; cause?: string; details?: JsonObject; }
export class ContractError extends Error {
  readonly code: ErrorCode;
  constructor(code: ErrorCode, message: string, readonly details?: JsonObject) { super(message); this.name = "ContractError"; this.code = code; }
}

export type AuditOutcome = "success" | "failure" | "denied" | "blocked";
export interface AuditRecord { id: ID; timestamp: string; sessionId: ID; eventType: string; actorId: ID; action: string; resource?: string; outcome: AuditOutcome; details?: JsonObject; }
export interface AuditQuery { sessionId?: ID; actorId?: ID; eventTypes?: string[]; outcomes?: AuditOutcome[]; from?: string; to?: string; limit?: number; }
export interface AuditSink { append(record: AuditRecord): Promise<void>; query?(query: AuditQuery): Promise<AuditRecord[]>; }

export interface Profile { id: ID; provider: string; model: string; systemPrompt?: string; capabilities?: ID[]; limits?: { maxTurns?: number; timeoutMs?: number }; metadata?: JsonObject; }
export interface ProfileStore { get(id: ID): Promise<Profile | undefined>; list(): Promise<Profile[]>; put(profile: Profile): Promise<void>; delete(id: ID): Promise<void>; }
export interface EventStore { append(event: RuntimeEvent): Promise<void>; read(sessionId: ID, afterSequence?: number): Promise<RuntimeEvent[]>; }
export interface RuntimePersistence { events: EventStore; profiles: ProfileStore; audit: AuditSink; }

export type JsonPrimitive = string | number | boolean | null;
export type JsonValue = JsonPrimitive | JsonValue[] | { [key: string]: JsonValue };
export type JsonObject = { [key: string]: JsonValue };
const SECRET = /token|secret|password|api[-_]?key|authorization|credential/i;
export function redact<T extends JsonValue>(value: T): T {
  if (Array.isArray(value)) return value.map(redact) as T;
  if (value && typeof value === "object") {
    const out: JsonObject = {};
    for (const [key, item] of Object.entries(value)) out[key] = SECRET.test(key) ? "[REDACTED]" : redact(item);
    return out as T;
  }
  return value;
}
export function redactRecord(record: AuditRecord): AuditRecord { return { ...record, details: record.details ? redact(record.details) : undefined }; }

/** Redacts nested secrets without mutating the input; suitable for explain/audit surfaces. */
export function redactUnknown(value: unknown): unknown {
  if (Array.isArray(value)) return value.map(redactUnknown);
  if (value && typeof value === "object") {
    const out: Record<string, unknown> = {};
    for (const [key, item] of Object.entries(value)) out[key] = SECRET.test(key) ? "[REDACTED]" : redactUnknown(item);
    return out;
  }
  return value;
}
