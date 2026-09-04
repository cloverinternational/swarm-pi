export type QuestionKind = "text" | "single" | "multi" | "confirm";
export type InteractionStatus = "answered" | "declined" | "timed_out" | "headless";

export interface StructuredQuestion {
  id?: string;
  question: string;
  kind?: QuestionKind;
  choices?: string[];
  default?: string | string[] | boolean;
  timeoutMs?: number;
}
export interface QuestionResponse { status: InteractionStatus; answer?: string | string[] | boolean; question: string }
export interface ApprovalRequest { id?: string; title: string; reason?: string; timeoutMs?: number }
export interface ApprovalResponse { approved: boolean; status: InteractionStatus; id: string }
export interface AgentUpdate { message: string; level?: "info" | "success" | "warning" | "error"; progress?: number }
export interface FeedbackRequest { message: string; rating?: number; tags?: string[] }

const DEFAULT_TIMEOUT = 5 * 60_000;
const questionKinds = new Set<QuestionKind>(["text", "single", "multi", "confirm"]);
const nonEmpty = (value: unknown): value is string => typeof value === "string" && value.trim().length > 0;

export function validateQuestion(value: unknown): asserts value is StructuredQuestion {
  if (!value || typeof value !== "object") throw new Error("question must be an object");
  const q = value as StructuredQuestion;
  if (!nonEmpty(q.question)) throw new Error("question.question is required");
  const kind = q.kind ?? "text";
  if (!questionKinds.has(kind)) throw new Error(`unsupported question kind: ${String(kind)}`);
  if ((kind === "single" || kind === "multi") && (!Array.isArray(q.choices) || q.choices.length < 1 || q.choices.some(c => !nonEmpty(c))))
    throw new Error(`${kind} questions require a non-empty choices array`);
  if (q.choices && new Set(q.choices).size !== q.choices.length) throw new Error("question choices must be unique");
  if (q.timeoutMs !== undefined && (!Number.isFinite(q.timeoutMs) || q.timeoutMs <= 0)) throw new Error("question.timeoutMs must be positive");
}

export function validateApproval(value: unknown): asserts value is ApprovalRequest {
  if (!value || typeof value !== "object") throw new Error("approval must be an object");
  const a = value as ApprovalRequest;
  if (!nonEmpty(a.title)) throw new Error("approval.title is required");
  if (a.timeoutMs !== undefined && (!Number.isFinite(a.timeoutMs) || a.timeoutMs <= 0)) throw new Error("approval.timeoutMs must be positive");
}

const TIMEOUT = Symbol("interaction-timeout");
function withTimeout<T>(promise: Promise<T>, timeoutMs: number, signal?: AbortSignal): Promise<T | typeof TIMEOUT> {
  return new Promise(resolve => {
    let done = false;
    const finish = (value: T | typeof TIMEOUT) => { if (done) return; done = true; clearTimeout(timer); signal?.removeEventListener("abort", abort); resolve(value); };
    const timer = setTimeout(() => finish(TIMEOUT), timeoutMs);
    const abort = () => finish(TIMEOUT);
    signal?.addEventListener("abort", abort, { once: true });
    promise.then(finish, () => finish(TIMEOUT));
  });
}

export interface InteractionHost {
  ui?: { input?: (title: string, options?: { defaultValue?: string }) => Promise<string | undefined>; select?: (title: string, options: string[]) => Promise<string | undefined>; confirm?: (title: string, options?: { defaultValue?: boolean }) => Promise<boolean> };
  sendMessage?: (message: { customType: string; content: string; display: boolean; details?: unknown }, options?: { triggerTurn?: boolean }) => void;
}

export class InteractionBroker {
  constructor(private readonly host: InteractionHost, private readonly options: { headless?: boolean; timeoutMs?: number } = {}) {}
  private timeout(value?: number) { return value ?? this.options.timeoutMs ?? DEFAULT_TIMEOUT; }
  private headless() { return this.options.headless || process.env.PI_HEADLESS === "1" || process.env.CI === "true"; }
  async ask(input: StructuredQuestion, signal?: AbortSignal): Promise<QuestionResponse> {
    validateQuestion(input);
    const q = { ...input, kind: input.kind ?? "text" };
    if (this.headless() || (!this.host.ui?.input && !this.host.ui?.select && !this.host.ui?.confirm))
      return { status: "headless", ...(q.default !== undefined ? { answer: q.default } : {}), question: q.question };
    const ui = this.host.ui!;
    const answer = await withTimeout((async () => {
      if (q.kind === "confirm") return (await ui.confirm?.(q.question, { defaultValue: typeof q.default === "boolean" ? q.default : false })) ?? undefined;
      if (q.kind === "single") return await ui.select?.(q.question, q.choices!);
      if (q.kind === "multi") return await ui.input?.(`${q.question} (comma-separated: ${q.choices!.join(", ")})`);
      return await ui.input?.(q.question, { defaultValue: typeof q.default === "string" ? q.default : undefined });
    })(), this.timeout(q.timeoutMs), signal);
    if (answer === TIMEOUT || answer === undefined) return { status: answer === TIMEOUT ? "timed_out" : "declined", question: q.question };
    const normalized = q.kind === "multi" && typeof answer === "string" ? answer.split(",").map(x => x.trim()).filter(Boolean) : answer;
    return { status: "answered", answer: normalized as any, question: q.question };
  }
  async approve(input: ApprovalRequest, signal?: AbortSignal): Promise<ApprovalResponse> {
    validateApproval(input); const id = input.id ?? `approval-${Date.now().toString(36)}`;
    if (this.headless() || !this.host.ui?.confirm) return { id, approved: false, status: "headless" };
    const answer = await withTimeout(this.host.ui.confirm(`${input.title}${input.reason ? `\n${input.reason}` : ""}`), this.timeout(input.timeoutMs), signal);
    return { id, approved: answer === true, status: answer === TIMEOUT ? "timed_out" : "answered" };
  }
  update(input: AgentUpdate): AgentUpdate { if (!nonEmpty(input.message)) throw new Error("agent update message is required"); if (input.progress !== undefined && (!Number.isFinite(input.progress) || input.progress < 0 || input.progress > 1)) throw new Error("progress must be between 0 and 1"); this.host.sendMessage?.({ customType: "swarm-agent-update", content: input.message, display: true, details: input }); return input; }
  feedback(input: FeedbackRequest): FeedbackRequest { if (!nonEmpty(input.message)) throw new Error("feedback message is required"); if (input.rating !== undefined && (!Number.isInteger(input.rating) || input.rating < 1 || input.rating > 5)) throw new Error("rating must be an integer from 1 to 5"); return input; }
}

export const askUserQuestionSchema = { type: "object", required: ["question"], additionalProperties: false, properties: { question: { type: "string", minLength: 1 }, kind: { type: "string", enum: ["text", "single", "multi", "confirm"] }, choices: { type: "array", items: { type: "string" } }, default: {}, timeoutMs: { type: "number", exclusiveMinimum: 0 } } } as const;
export const requestApprovalSchema = { type: "object", required: ["title"], additionalProperties: false, properties: { id: { type: "string" }, title: { type: "string", minLength: 1 }, reason: { type: "string" }, timeoutMs: { type: "number", exclusiveMinimum: 0 } } } as const;
export const agentUpdateSchema = { type: "object", required: ["message"], additionalProperties: false, properties: { message: { type: "string", minLength: 1 }, level: { type: "string", enum: ["info", "success", "warning", "error"] }, progress: { type: "number", minimum: 0, maximum: 1 } } } as const;
export const feedbackSchema = { type: "object", required: ["message"], additionalProperties: false, properties: { message: { type: "string", minLength: 1 }, rating: { type: "integer", minimum: 1, maximum: 5 }, tags: { type: "array", items: { type: "string" } } } } as const;

export function registerInteractionTools(pi: InteractionHost & { registerTool(tool: unknown): void }, options?: { headless?: boolean; timeoutMs?: number }) {
  const broker = new InteractionBroker(pi, options);
  const result = (value: unknown) => ({ content: [{ type: "text", text: JSON.stringify(value) }], details: value });
  pi.registerTool({ name: "AskUserQuestion", label: "Ask user", description: "Ask a validated structured question; headless runs return a non-blocking status.", parameters: askUserQuestionSchema, async execute(_id: string, p: StructuredQuestion, _signal: AbortSignal) { try { return result(await broker.ask(p, _signal)); } catch (e) { return result({ status: "declined", error: e instanceof Error ? e.message : String(e) }); } } });
  pi.registerTool({ name: "RequestApproval", label: "Request approval", description: "Request explicit approval; timeout and headless mode fail closed.", parameters: requestApprovalSchema, async execute(_id: string, p: ApprovalRequest, _signal: AbortSignal) { try { return result(await broker.approve(p, _signal)); } catch (e) { return result({ approved: false, status: "declined", error: e instanceof Error ? e.message : String(e) }); } } });
  pi.registerTool({ name: "PushAgentUpdate", label: "Agent update", description: "Publish a bounded progress update to the user.", parameters: agentUpdateSchema, async execute(_id: string, p: AgentUpdate) { try { return result(broker.update(p)); } catch (e) { return result({ error: e instanceof Error ? e.message : String(e) }); } } });
  pi.registerTool({ name: "SubmitFeedback", label: "Submit feedback", description: "Record validated user feedback for the current run.", parameters: feedbackSchema, async execute(_id: string, p: FeedbackRequest) { try { return result(broker.feedback(p)); } catch (e) { return result({ error: e instanceof Error ? e.message : String(e) }); } } });
  return broker;
}
