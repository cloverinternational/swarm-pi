/**
 * Port of Swarm's builtin annoyance nudge so `pi -p` emits the same model-
 * visible <system-reminder> after a failed tool call as `swarm -p`.
 *
 * Source of truth (swarm-sdk):
 *   internal/hooks/builtin/annoyance_nudge.go  OnEvent, sanitizeAnnoyanceLabel
 *   internal/hooks/failure.go                  ClassifyToolFailure,
 *                                              ClassifyToolFriction,
 *                                              failureFrictionCategory,
 *                                              frictionFingerprint,
 *                                              normalizeFailureText, boundedText
 *   internal/hooks/format.go                   FormatHookContext, NextReminderSeq
 *   internal/reminder/envelope.go              Wrap
 *   internal/hooks/agentbridge/bridge.go       event Data: error = execErr.Error()
 *   internal/agent/agent_tools.go              hook context → standalone RoleUser
 *                                              message after the tool message
 *
 * Pi tool results have no separate execErr: a failed tool returns
 * `isError: true` with the "Error executing <tool>: ..." text as content, so
 * the evidence string Swarm would hash ("error: " + execErr.Error()) is
 * "error: " + that text with the leading "Error executing <tool>: " removed.
 */
import { createHash } from "node:crypto";

const MAX_FINGERPRINT_MATERIAL_BYTES = 8 << 10;
const PROSE_ELISION = "\n[...]\n";
const MAX_FAILURE_ENTRIES = 1024;

const sha256hex16 = (value: string) => createHash("sha256").update(value, "utf8").digest("hex").slice(0, 32);

/** failure.go normalizeFailureText: lower-case, whitespace collapsed. */
export const normalizeFailureText = (text: string) => text.split(/\s+/).filter(Boolean).join(" ").toLowerCase();

/** failure.go boundedText (byte-based head + tail excerpt). */
export function boundedText(text: string, limit: number): [string, boolean] {
  const bytes = Buffer.from(text, "utf8");
  if (limit <= 0 || bytes.length <= limit) return [text, false];
  const segment = Math.floor(limit / 2);
  return [bytes.subarray(0, segment).toString("utf8") + PROSE_ELISION + bytes.subarray(bytes.length - segment).toString("utf8"), true];
}

/** failure.go ClassifyToolFailure fingerprint for a given joined reason. */
export function failureFingerprint(reason: string): string {
  let material = normalizeFailureText(reason);
  const byteLength = Buffer.byteLength(material);
  const [bounded, excerpted] = boundedText(material, MAX_FINGERPRINT_MATERIAL_BYTES);
  if (excerpted) material = `${bounded}\n[length=${byteLength}]`;
  return sha256hex16(material);
}

/** failure.go failureFrictionCategory. */
export function failureFrictionCategory(reason: string): "timeout" | "output-limit" | "tool-failure" {
  const n = normalizeFailureText(reason);
  if (n.includes("timeout") || n.includes("timed out") || n.includes("deadline exceeded") || n.includes("time limit exceeded")) return "timeout";
  if (n.includes("output limit") || n.includes("maximum output") || n.includes("output exceeds") || n.includes("output is too large") || n.includes("output was too large") || n.includes("response too large")) return "output-limit";
  return "tool-failure";
}

/** failure.go frictionFingerprint. */
export const frictionFingerprint = (category: string, failure: string) => sha256hex16(failure ? `${category}:${failure}` : category);

/** toolclass.go NormalizeToolName. */
export const normalizeToolName = (name: string) => name.replace(/_/g, "").toLowerCase();

/** annoyance_nudge.go sanitizeAnnoyanceLabel (rune-aware, 64-byte cap). */
export function sanitizeAnnoyanceLabel(value: string): string {
  value = value.trim().toLowerCase();
  let out = "";
  let separator = false;
  for (const r of value) {
    if (Buffer.byteLength(out) >= 64) break;
    if (/[\p{L}\p{N}]/u.test(r)) { out += r; separator = false; }
    else if (r === "_" || r === "-" || r === ".") { if (out.length > 0 && !separator) { out += r; separator = true; } }
    else if (out.length > 0 && !separator) { out += "-"; separator = true; }
  }
  const result = out.replace(/^[_.\-]+|[_.\-]+$/g, "");
  return result === "" ? "unknown" : result;
}

/** reminder/envelope.go Wrap (html.EscapeString on source). */
export function wrapReminder(source: string, kind: string, seq: number, body: string): string {
  source = source.trim() || "unknown";
  if (!["nudge", "block", "review", "context"].includes(kind)) kind = "context";
  if (seq < 1) seq = 1;
  const esc = source.replace(/&/g, "&amp;").replace(/'/g, "&#39;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&#34;");
  return `<system-reminder source="${esc}" kind="${kind}" seq="${seq}">${body.trim()}</system-reminder>`;
}

/** hooks/format.go reminderKind: inferred from source + content. */
export function reminderKind(source: string, content: string): "review" | "block" | "nudge" | "context" {
  const lower = `${source} ${content}`.toLowerCase();
  if (lower.includes("skill review") || lower.includes("autogenskills")) return "review";
  if (lower.includes("block") || lower.includes("enforcement")) return "block";
  if (lower.includes("nudge") || lower.includes("reminder")) return "nudge";
  return "context";
}

/** hooks/format.go FormatHookContext for plain (non-canonical) hook text. */
export function formatHookContext(hookName: string, content: string): string {
  const trimmed = content.trim();
  if (trimmed === "") return "";
  if (trimmed.startsWith("<system-reminder ") && trimmed.includes(" source=\"") && trimmed.includes(" kind=\"") && trimmed.includes(" seq=\"")) return trimmed;
  const name = hookName || "hook";
  return wrapReminder(name, reminderKind(name, trimmed), nextReminderSeq(name), trimmed);
}

/** hooks/format.go NextReminderSeq — process-local per source. */
const sequences = new Map<string, number>();
export function nextReminderSeq(source: string): number {
  const next = (sequences.get(source) ?? 0) + 1;
  sequences.set(source, next);
  return next;
}
export function resetReminderSequences(): void { sequences.clear(); }

/** annoyance_nudge.go message body (Go %q on already-sanitized labels). */
export function annoyanceMessage(toolName: string, category: string, fingerprint: string): string {
  return `[ANNOYANCE REVIEW]\nTool: "${sanitizeAnnoyanceLabel(toolName)}"\nFriction class: "${sanitizeAnnoyanceLabel(category)}"\nFingerprint: "${sanitizeAnnoyanceLabel(fingerprint)}"\n\nAct as a demanding harness editor, not a complaint generator:\n1. Decide whether this is a real product defect or inefficiency. Skip invalid input, expected test failures, permission denials, and user cancellations.\n2. If actionable, call annoyed ONCE with category, observed behavior, expected behavior, bounded non-secret evidence, and objective acceptance tests.\n3. The acceptance tests must include the exact reproduction shape plus one near-miss that must remain allowed. Do not publish vague frustration or duplicate this fingerprint.`;
}

export interface ToolResultLike { toolName: string; isError?: boolean; content?: unknown; details?: Record<string, unknown> }

/** Text a Pi tool returned, joined like Swarm's execErr.Error() would read. */
export function resultText(content: unknown): string {
  if (typeof content === "string") return content;
  if (Array.isArray(content)) return content.map((b: any) => (b && typeof b === "object" && typeof b.text === "string" ? b.text : "")).join("");
  return "";
}

/**
 * bridge.go sets Data["error"] = execErr.Error(); agent_tools.go renders the
 * model-visible text as "Error executing <tool>: <execErr>". Invert that to
 * recover execErr for the evidence string.
 */
export function evidenceFor(toolName: string, text: string): string {
  const prefix = `Error executing ${toolName}: `;
  return `error: ${text.startsWith(prefix) ? text.slice(prefix.length) : text}`;
}

export interface Friction { toolName: string; category: string; fingerprint: string }

/** ClassifyToolFriction for a Pi tool result; undefined when not friction. */
export function classifyFriction(result: ToolResultLike): Friction | undefined | "clean" {
  const type = (result.details as any)?.error_type ?? (result.details as any)?.errorType;
  if (type === "tool.batch_blocked" || type === "tool.blocked_by_hook") return undefined;
  if (result.isError !== true) return "clean";
  const text = resultText(result.content);
  // Pi renders a tool_call block as createErrorToolResult(reason) with no
  // details, so Swarm's structured tool.blocked_by_hook disposition is only
  // recoverable from the agent_tools.go prefix that hook blocks produce.
  if (text.startsWith(`Tool '${result.toolName}' blocked by hook: `)) return undefined;
  const reason = evidenceFor(result.toolName, text);
  const category = failureFrictionCategory(reason);
  return { toolName: result.toolName, category, fingerprint: frictionFingerprint(category, failureFingerprint(reason)) };
}

/** AnnoyanceNudgeHook state: last fingerprint per (conversation, tool). */
export class AnnoyanceNudgeState {
  private lastFailure = new Map<string, string>();
  /** Returns the model-visible reminder to append, or undefined. */
  onToolResult(conversation: string, result: ToolResultLike): string | undefined {
    const classification = classifyFriction(result);
    if (normalizeToolName(result.toolName) === "annoyed") return undefined;
    const key = `${conversation}\u0000${normalizeToolName(result.toolName)}`;
    if (classification === "clean") { this.lastFailure.delete(key); return undefined; }
    if (!classification) return undefined;
    if (this.lastFailure.get(key) === classification.fingerprint) return undefined;
    if (this.lastFailure.size >= MAX_FAILURE_ENTRIES) this.lastFailure.clear();
    this.lastFailure.set(key, classification.fingerprint);
    const body = annoyanceMessage(classification.toolName, classification.category, classification.fingerprint);
    return wrapReminder("annoyance-nudge", "nudge", nextReminderSeq("annoyance-nudge"), body);
  }
}
