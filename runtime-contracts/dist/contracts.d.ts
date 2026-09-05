/** Stable, JSON-safe runtime contracts shared by Pi adapters and hosts. */
export type ID = string & {
    readonly __brand: "StableID";
};
export declare function createId(value: string): ID;
export declare function newId(prefix?: string): ID;
export declare const POLICY_CLASSES: readonly ["KEEP", "OPT-IN", "DEFER-DISCOVER", "NEVER-DEFAULT"];
export type PolicyClass = typeof POLICY_CLASSES[number];
export interface Capability {
    id: ID;
    aliases?: string[];
    policy: PolicyClass;
    sideEffect?: string;
    hostBound?: boolean;
    credentialBound?: boolean;
}
export interface CapabilitySet {
    capabilities: Capability[];
    selected: ID[];
}
export interface PolicyContext {
    actorId: ID;
    sessionId: ID;
    capabilities: ReadonlySet<ID> | ID[];
    profileId?: ID;
    workspace?: string;
    consent?: "granted" | "denied" | "required";
    metadata?: JsonObject;
}
export declare const EVENT_KINDS: readonly ["started", "progress", "content", "tool_call", "tool_result", "completed", "failed", "cancelled"];
export type EventKind = typeof EVENT_KINDS[number];
export interface RuntimeEvent<T extends JsonValue = JsonValue> {
    id: ID;
    sequence: number;
    kind: EventKind;
    sessionId: ID;
    timestamp: string;
    payload?: T;
    redacted?: boolean;
}
export declare function normalizeEvent<T extends JsonValue>(event: Omit<RuntimeEvent<T>, "timestamp"> & {
    timestamp?: string;
}): RuntimeEvent<T>;
export declare const OUTCOMES: readonly ["success", "rejected", "denied", "timeout", "cancelled", "failed"];
export type Outcome = typeof OUTCOMES[number];
export interface Success<T> {
    ok: true;
    outcome: "success";
    value: T;
}
export interface Failure {
    ok: false;
    outcome: Exclude<Outcome, "success">;
    error: RuntimeError;
}
export type Result<T> = Success<T> | Failure;
export declare const ok: <T>(value: T) => Success<T>;
export declare const err: (outcome: Failure["outcome"], error: RuntimeError) => Failure;
export type ErrorCode = "invalid_id" | "invalid_sequence" | "validation" | "not_found" | "denied" | "conflict" | "storage" | "internal";
export interface RuntimeError {
    code: ErrorCode;
    message: string;
    retryable?: boolean;
    cause?: string;
    details?: JsonObject;
}
export declare class ContractError extends Error {
    readonly details?: JsonObject | undefined;
    readonly code: ErrorCode;
    constructor(code: ErrorCode, message: string, details?: JsonObject | undefined);
}
export type AuditOutcome = "success" | "failure" | "denied" | "blocked";
export interface AuditRecord {
    id: ID;
    timestamp: string;
    sessionId: ID;
    eventType: string;
    actorId: ID;
    action: string;
    resource?: string;
    outcome: AuditOutcome;
    details?: JsonObject;
}
export interface AuditQuery {
    sessionId?: ID;
    actorId?: ID;
    eventTypes?: string[];
    outcomes?: AuditOutcome[];
    from?: string;
    to?: string;
    limit?: number;
}
export interface AuditSink {
    append(record: AuditRecord): Promise<void>;
    query?(query: AuditQuery): Promise<AuditRecord[]>;
}
export interface Profile {
    id: ID;
    provider: string;
    model: string;
    systemPrompt?: string;
    capabilities?: ID[];
    limits?: {
        maxTurns?: number;
        timeoutMs?: number;
    };
    metadata?: JsonObject;
}
export interface ProfileStore {
    get(id: ID): Promise<Profile | undefined>;
    list(): Promise<Profile[]>;
    put(profile: Profile): Promise<void>;
    delete(id: ID): Promise<void>;
}
export interface EventStore {
    append(event: RuntimeEvent): Promise<void>;
    read(sessionId: ID, afterSequence?: number): Promise<RuntimeEvent[]>;
}
export interface RuntimePersistence {
    events: EventStore;
    profiles: ProfileStore;
    audit: AuditSink;
}
export type JsonPrimitive = string | number | boolean | null;
export type JsonValue = JsonPrimitive | JsonValue[] | {
    [key: string]: JsonValue;
};
export type JsonObject = {
    [key: string]: JsonValue;
};
export declare function redact<T extends JsonValue>(value: T): T;
export declare function redactRecord(record: AuditRecord): AuditRecord;
/** Redacts nested secrets without mutating the input; suitable for explain/audit surfaces. */
export declare function redactUnknown(value: unknown): unknown;
