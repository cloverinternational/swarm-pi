/** Stable, JSON-safe runtime contracts shared by Pi adapters and hosts. */
const ID_RE = /^[a-zA-Z0-9][a-zA-Z0-9._:-]{0,127}$/;
export function createId(value) {
    if (!ID_RE.test(value))
        throw new ContractError("invalid_id", `Invalid stable ID: ${JSON.stringify(value)}`);
    return value;
}
export function newId(prefix = "evt") {
    const suffix = globalThis.crypto?.randomUUID?.() ?? `${Date.now().toString(36)}-${Math.random().toString(36).slice(2)}`;
    return createId(`${prefix}:${suffix}`);
}
export const POLICY_CLASSES = ["KEEP", "OPT-IN", "DEFER-DISCOVER", "NEVER-DEFAULT"];
export const EVENT_KINDS = ["started", "progress", "content", "tool_call", "tool_result", "completed", "failed", "cancelled"];
export function normalizeEvent(event) {
    if (!Number.isSafeInteger(event.sequence) || event.sequence < 0)
        throw new ContractError("invalid_sequence", "Event sequence must be a non-negative safe integer");
    return { ...event, timestamp: event.timestamp ?? new Date().toISOString() };
}
export const OUTCOMES = ["success", "rejected", "denied", "timeout", "cancelled", "failed"];
export const ok = (value) => ({ ok: true, outcome: "success", value });
export const err = (outcome, error) => ({ ok: false, outcome, error });
export class ContractError extends Error {
    details;
    code;
    constructor(code, message, details) {
        super(message);
        this.details = details;
        this.name = "ContractError";
        this.code = code;
    }
}
const SECRET = /token|secret|password|api[-_]?key|authorization|credential/i;
export function redact(value) {
    if (Array.isArray(value))
        return value.map(redact);
    if (value && typeof value === "object") {
        const out = {};
        for (const [key, item] of Object.entries(value))
            out[key] = SECRET.test(key) ? "[REDACTED]" : redact(item);
        return out;
    }
    return value;
}
export function redactRecord(record) { return { ...record, details: record.details ? redact(record.details) : undefined }; }
/** Redacts nested secrets without mutating the input; suitable for explain/audit surfaces. */
export function redactUnknown(value) {
    if (Array.isArray(value))
        return value.map(redactUnknown);
    if (value && typeof value === "object") {
        const out = {};
        for (const [key, item] of Object.entries(value))
            out[key] = SECRET.test(key) ? "[REDACTED]" : redactUnknown(item);
        return out;
    }
    return value;
}
