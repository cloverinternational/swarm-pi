/** Generic, deterministic hook runtime contracts for Pi extensions.
 *
 * This deliberately has no Pi dependency: adapters normalize host payloads at
 * the edge, while this coordinator owns ordering, policy, terminal de-duping,
 * budgets, routing, and durable audit records.
 */
export type HookPhase = "before" | "after" | "lifecycle";
export type HookFailureMode = "continue" | "block" | "throw";
export type HookAction = "continue" | "block";

export interface RuntimeEvent {
  id?: string; type: string; phase: HookPhase; at: string;
  sessionId?: string; conversationId?: string; tool?: string; toolCallId?: string;
  scope?: string; subagent?: boolean; input?: unknown; output?: unknown;
  failed?: boolean; [key: string]: unknown;
}
export interface HookDecision { action?: HookAction; message?: string; metadata?: Record<string, unknown> }
export interface RuntimeHookContext { sessionId?: string; isSubagent?: boolean; [key: string]: unknown }
export interface HookDefinition {
  name: string; priority?: number; events?: string[]; scopes?: string[];
  phase?: HookPhase; timeoutMs?: number; failureMode?: HookFailureMode;
  budgetKey?: string; maxInvocations?: number;
  filter?: (event: RuntimeEvent) => boolean;
  routeSubagent?: boolean;
  handle: (event: RuntimeEvent, context: RuntimeHookContext) => HookDecision | void | Promise<HookDecision | void>;
}
export interface HookAudit { hook: string; event: string; outcome: "executed" | "skipped" | "blocked" | "failed"; at: string; message?: string; tool?: string; toolCallId?: string }
export interface HookRuntimeOptions { defaultTimeoutMs?: number; auditLimit?: number; appendAudit?: (audit: HookAudit) => void; routeSubagent?: (event: RuntimeEvent, context: RuntimeHookContext) => RuntimeHookContext | Promise<RuntimeHookContext>; }

const aliases: Record<string, string> = {
  before_tool: "tool.before_execute", tool_call: "tool.before_execute", beforetool: "tool.before_execute",
  tool_result: "tool.after_execute", tool_execution_end: "tool.after_execute", after_tool: "tool.after_execute",
  session_start: "agent.started", session_shutdown: "agent.stopped", shutdown: "agent.stopped",
  turn_start: "agent.turn_started", turn_end: "agent.turn_ended", before_agent_start: "agent.before_start",
};
const phaseFor = (type: string): HookPhase => type.startsWith("tool.before") || type.startsWith("agent.before") ? "before" : type.startsWith("tool.after") ? "after" : "lifecycle";
const text = (x: unknown) => typeof x === "string" ? x : undefined;

/** Converts Pi/Claude-style payloads into the stable event vocabulary. */
export function normalizeHookEvent(payload: Record<string, unknown> = {}, eventName?: string): RuntimeEvent {
  const raw = text(eventName) ?? text(payload.type) ?? "unknown";
  const type = aliases[raw.toLowerCase()] ?? raw;
  const result = payload.result ?? payload.toolResult ?? payload.tool_output ?? payload.output;
  return {
    ...payload, id: text(payload.id) ?? text(payload.eventId), type, phase: phaseFor(type),
    at: text(payload.at) ?? text(payload.timestamp) ?? new Date().toISOString(),
    sessionId: text(payload.sessionId) ?? text(payload.session_id),
    conversationId: text(payload.conversationId) ?? text(payload.conversation_id),
    tool: text(payload.tool) ?? text(payload.toolName) ?? text(payload.tool_name),
    toolCallId: text(payload.toolCallId) ?? text(payload.tool_call_id),
    input: payload.input ?? payload.params ?? payload.tool_input, output: result,
    failed: payload.isError === true || payload.error != null || (result as any)?.isError === true,
    subagent: payload.subagent === true || payload.isSubagent === true,
  };
}
const matches = (pattern: string, value: string) => pattern === "*" || pattern === value ||
  (pattern.endsWith("*") && value.startsWith(pattern.slice(0, -1))) ||
  (pattern.startsWith("*") && value.endsWith(pattern.slice(1)));
const fingerprint = (e: RuntimeEvent) => JSON.stringify({ type: e.type, tool: e.tool, input: e.input, output: e.output, failed: e.failed });

export class HookRuntimeCoordinator {
  private definitions: HookDefinition[] = [];
  private audit: HookAudit[] = [];
  private terminal = new Set<string>();
  private anonymous = new Set<string>();
  private usage = new Map<string, number>();
  private budget: Map<string, number>;
  readonly options: Required<Pick<HookRuntimeOptions, "defaultTimeoutMs" | "auditLimit">>;
  constructor(private readonly optionsIn: HookRuntimeOptions = {}, sharedBudget?: Map<string, number>) {
    this.options = { defaultTimeoutMs: optionsIn.defaultTimeoutMs ?? 10_000, auditLimit: optionsIn.auditLimit ?? 200 };
    this.budget = sharedBudget ?? new Map();
  }
  register(definition: HookDefinition): () => void {
    if (!definition.name || typeof definition.handle !== "function") throw new Error("hook name and handler are required");
    this.definitions.push(definition); this.definitions.sort((a, b) => (b.priority ?? 50) - (a.priority ?? 50));
    return () => { this.definitions = this.definitions.filter(h => h !== definition); };
  }
  auditSnapshot() { return this.audit.map(a => ({ ...a })); }
  private record(audit: HookAudit) { this.audit.push(audit); this.audit = this.audit.slice(-this.options.auditLimit); this.optionsIn.appendAudit?.(audit); }
  async dispatch(payload: Record<string, unknown>, eventName?: string, context: RuntimeHookContext = {}): Promise<{ event: RuntimeEvent; blocked?: HookAudit }> {
    let event = normalizeHookEvent(payload, eventName);
    if (event.phase === "after") {
      const id = event.toolCallId;
      if (id && this.terminal.has(id)) return { event };
      if (!id) { const fp = fingerprint(event); if (this.anonymous.has(fp)) return { event }; this.anonymous.add(fp); if (this.anonymous.size > 50) this.anonymous.delete(this.anonymous.values().next().value!); }
      if (id) this.terminal.add(id);
    }
    let routed = context;
    if (event.subagent && this.optionsIn.routeSubagent) routed = await this.optionsIn.routeSubagent(event, context);
    for (const hook of this.definitions) {
      if (hook.phase && hook.phase !== event.phase || hook.events && !hook.events.some(p => matches(p, event.type)) || hook.scopes && (!event.scope || !hook.scopes.includes(event.scope)) || hook.filter && !hook.filter(event)) {
        this.record({ hook: hook.name, event: event.type, outcome: "skipped", at: new Date().toISOString(), tool: event.tool, toolCallId: event.toolCallId }); continue;
      }
      if (event.subagent && hook.routeSubagent === false) { this.record({ hook: hook.name, event: event.type, outcome: "skipped", at: new Date().toISOString(), message: "subagent route excluded" }); continue; }
      const key = hook.budgetKey ?? hook.name, used = this.usage.get(key) ?? 0, remaining = this.budget.get(key);
      if (hook.maxInvocations != null && used >= hook.maxInvocations || remaining != null && remaining <= 0) { this.record({ hook: hook.name, event: event.type, outcome: "skipped", at: new Date().toISOString(), message: "budget exhausted" }); continue; }
      this.usage.set(key, used + 1); if (remaining != null) this.budget.set(key, remaining - 1);
      try {
        const decision = await withTimeout(Promise.resolve(hook.handle(event, { ...routed, isSubagent: event.subagent || routed.isSubagent })), hook.timeoutMs ?? this.options.defaultTimeoutMs);
        if (decision?.action === "block") { const a = { hook: hook.name, event: event.type, outcome: "blocked" as const, at: new Date().toISOString(), message: decision.message, tool: event.tool, toolCallId: event.toolCallId }; this.record(a); return { event, blocked: a }; }
        this.record({ hook: hook.name, event: event.type, outcome: "executed", at: new Date().toISOString(), message: decision?.message, tool: event.tool, toolCallId: event.toolCallId });
      } catch (error) {
        const message = error instanceof Error ? error.message : String(error), outcome = hook.failureMode === "block" ? "blocked" : "failed";
        const a = { hook: hook.name, event: event.type, outcome: outcome as "blocked" | "failed", at: new Date().toISOString(), message, tool: event.tool, toolCallId: event.toolCallId }; this.record(a);
        if (hook.failureMode === "block") return { event, blocked: a };
        if (hook.failureMode === "throw") throw error;
      }
    }
    return { event };
  }
}
function withTimeout<T>(promise: Promise<T>, ms: number): Promise<T> {
  if (ms <= 0) return Promise.reject(new Error("hook timeout"));
  return new Promise((resolve, reject) => { const timer = setTimeout(() => reject(new Error(`hook timeout after ${ms}ms`)), ms); promise.then(resolve, reject).finally(() => clearTimeout(timer)); });
}

/** Built-in families kept as names so adapters can register implementations independently. */
export const BUILTIN_HOOK_FAMILIES = ["session", "prompt", "permission", "steering", "tool", "findings", "metrics", "task", "skill", "subagent"] as const;
export type BuiltinHookFamily = typeof BUILTIN_HOOK_FAMILIES[number];
