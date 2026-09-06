export type QuestionKind = "text" | "single" | "multi" | "confirm";
export type InteractionStatus = "answered" | "declined" | "timed_out" | "headless";

/** Option shape compatible with the popular Pi/Claude-style questionnaire extensions. */
export interface QuestionOption { value?: string; label: string; description?: string; recommended?: boolean }
export interface StructuredQuestion {
  id?: string; question: string; header?: string; kind?: QuestionKind;
  choices?: string[]; options?: QuestionOption[]; multiSelect?: boolean;
  required?: boolean; default?: string | string[] | boolean; timeoutMs?: number;
}
export interface QuestionAnswer { id: string; question: string; selected: string[]; customText?: string }
export interface QuestionnaireResponse { status: InteractionStatus; answers: QuestionAnswer[]; }
export interface QuestionResponse { status: InteractionStatus; answer?: string | string[] | boolean; question: string }
export interface ApprovalRequest { id?: string; title: string; reason?: string; timeoutMs?: number }
export interface ApprovalResponse { approved: boolean; status: InteractionStatus; id: string }
export interface AgentUpdate { message: string; level?: "info" | "success" | "warning" | "error"; progress?: number }
export interface FeedbackRequest { message: string; rating?: number; tags?: string[] }

const DEFAULT_TIMEOUT = 5 * 60_000;
const questionKinds = new Set<QuestionKind>(["text", "single", "multi", "confirm"]);
const nonEmpty = (value: unknown): value is string => typeof value === "string" && value.trim().length > 0;
const optionValue = (option: QuestionOption) => option.value?.trim() || option.label.trim();

export function validateQuestion(value: unknown): asserts value is StructuredQuestion {
  if (!value || typeof value !== "object") throw new Error("question must be an object");
  const q = value as StructuredQuestion;
  if (!nonEmpty(q.question)) throw new Error("question.question is required");
  if (q.header !== undefined && (!nonEmpty(q.header) || q.header.length > 12)) throw new Error("question.header must be 1-12 characters");
  const kind = q.kind ?? (q.options || q.choices ? (q.multiSelect ? "multi" : "single") : "text");
  if (!questionKinds.has(kind)) throw new Error(`unsupported question kind: ${String(kind)}`);
  const options = q.options ?? q.choices?.map(label => ({ label }));
  if ((kind === "single" || kind === "multi") && (!options || options.length < 2 || options.length > 4 || options.some(o => !nonEmpty(o.label))))
    throw new Error(`${kind} questions require 2-4 non-empty choices/options`);
  if (options && new Set(options.map(optionValue)).size !== options.length) throw new Error("question option values must be unique");
  if (options?.some(o => /^(other|type something\.?|none)$/i.test(o.label.trim()))) throw new Error("custom options are automatic; omit Other/Type something");
  if (q.choices && q.options) throw new Error("provide choices or options, not both");
  if (q.timeoutMs !== undefined && (!Number.isFinite(q.timeoutMs) || q.timeoutMs <= 0)) throw new Error("question.timeoutMs must be positive");
}

export function validateQuestionnaire(value: unknown): asserts value is { questions: StructuredQuestion[] } {
  if (!value || typeof value !== "object" || !Array.isArray((value as any).questions) || (value as any).questions.length < 1 || (value as any).questions.length > 4)
    throw new Error("questions must contain 1-4 questions");
  const seen = new Set<string>();
  for (const q of (value as any).questions) { validateQuestion(q); const id = q.id ?? q.question; if (seen.has(id)) throw new Error("question ids must be unique"); seen.add(id); }
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
    const q = { ...input, kind: input.kind ?? (input.options || input.choices ? (input.multiSelect ? "multi" : "single") : "text") };
    if (this.headless() || (!this.host.ui?.input && !this.host.ui?.select && !this.host.ui?.confirm)) return { status: "headless", ...(q.default !== undefined ? { answer: q.default } : {}), question: q.question };
    const ui = this.host.ui!;
    const options: QuestionOption[] = q.options ?? q.choices?.map(label => ({ label })) ?? [];
    const answer = await withTimeout((async () => {
      if (q.kind === "confirm") return (await ui.confirm?.(q.question, { defaultValue: typeof q.default === "boolean" ? q.default : false })) ?? undefined;
      if (q.kind === "single") return await ui.select?.(`${q.header ? `[${q.header}] ` : ""}${q.question}`, options.map(o => `${o.label}${o.description ? ` — ${o.description}` : ""}`));
      if (q.kind === "multi") return await ui.input?.(`${q.question} (comma-separated: ${options.map(o => o.label).join(", ")})`);
      return await ui.input?.(q.question, { defaultValue: typeof q.default === "string" ? q.default : undefined });
    })(), this.timeout(q.timeoutMs), signal);
    if (answer === TIMEOUT || answer === undefined) return { status: answer === TIMEOUT ? "timed_out" : "declined", question: q.question };
    const normalized = q.kind === "multi" && typeof answer === "string" ? answer.split(",").map(x => x.trim()).filter(Boolean) : answer;
    return { status: "answered", answer: normalized as any, question: q.question };
  }
  async askQuestionnaire(input: { questions: StructuredQuestion[] }, signal?: AbortSignal): Promise<QuestionnaireResponse> {
    validateQuestionnaire(input);
    const answers: QuestionAnswer[] = [];
    for (const q of input.questions) {
      const result = await this.ask(q, signal);
      if (result.status !== "answered") return { status: result.status, answers };
      const selected = Array.isArray(result.answer) ? result.answer.map(String) : result.answer === undefined || typeof result.answer === "boolean" ? [] : [String(result.answer)];
      answers.push({ id: q.id ?? q.question, question: q.question, selected });
    }
    return { status: "answered", answers };
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

export const askUserQuestionSchema = { type: "object", additionalProperties: false, oneOf: [{ required: ["question"], properties: { question: { type: "string", minLength: 1 }, kind: { type: "string", enum: ["text", "single", "multi", "confirm"] }, choices: { type: "array", items: { type: "string" } }, options: { type: "array", minItems: 2, maxItems: 4, items: { type: "object", required: ["label"], additionalProperties: false, properties: { value: { type: "string" }, label: { type: "string", minLength: 1 }, description: { type: "string" }, recommended: { type: "boolean" } } } }, header: { type: "string", maxLength: 12 }, multiSelect: { type: "boolean" }, required: { type: "boolean" }, default: {}, timeoutMs: { type: "number", exclusiveMinimum: 0 } } }, { required: ["questions"], properties: { questions: { type: "array", minItems: 1, maxItems: 4 } } }] } as const;
export const requestApprovalSchema = { type: "object", required: ["title"], additionalProperties: false, properties: { id: { type: "string" }, title: { type: "string", minLength: 1 }, reason: { type: "string" }, timeoutMs: { type: "number", exclusiveMinimum: 0 } } } as const;
export const agentUpdateSchema = { type: "object", required: ["message"], additionalProperties: false, properties: { message: { type: "string", minLength: 1 }, level: { type: "string", enum: ["info", "success", "warning", "error"] }, progress: { type: "number", minimum: 0, maximum: 1 } } } as const;
export const feedbackSchema = { type: "object", required: ["message"], additionalProperties: false, properties: { message: { type: "string", minLength: 1 }, rating: { type: "integer", minimum: 1, maximum: 5 }, tags: { type: "array", items: { type: "string" } } } } as const;

export function registerInteractionTools(pi: InteractionHost & { registerTool(tool: unknown): void }, options?: { headless?: boolean; timeoutMs?: number }) {
  const broker = new InteractionBroker(pi, options);
  const result = (value: unknown) => ({ content: [{ type: "text", text: JSON.stringify(value) }], details: value });
  pi.registerTool({ name: "ask_user_question", label: "Ask user", description: "Ask 1-4 validated structured questions. Supports single choice, multi-select, free text, confirmation, stable option values, and cancellation. Use for meaningful decisions; do not guess when user input is required.", parameters: askUserQuestionSchema, async execute(_id: string, p: StructuredQuestion | { questions: StructuredQuestion[] }, _signal: AbortSignal) { try { if ("questions" in p) return result(await broker.askQuestionnaire(p, _signal)); return result(await broker.ask(p, _signal)); } catch (e) { return result({ status: "declined", error: e instanceof Error ? e.message : String(e) }); } } });
  pi.registerTool({ name: "RequestApproval", label: "Request approval", description: "Request explicit approval; timeout and headless mode fail closed.", parameters: requestApprovalSchema, async execute(_id: string, p: ApprovalRequest, _signal: AbortSignal) { try { return result(await broker.approve(p, _signal)); } catch (e) { return result({ approved: false, status: "declined", error: e instanceof Error ? e.message : String(e) }); } } });
  pi.registerTool({ name: "PushAgentUpdate", label: "Agent update", description: "Publish a bounded progress update to the user.", parameters: agentUpdateSchema, async execute(_id: string, p: AgentUpdate) { try { return result(broker.update(p)); } catch (e) { return result({ error: e instanceof Error ? e.message : String(e) }); } } });
  pi.registerTool({ name: "SubmitFeedback", label: "Submit feedback", description: "Record validated user feedback for the current run.", parameters: feedbackSchema, async execute(_id: string, p: FeedbackRequest) { try { return result(broker.feedback(p)); } catch (e) { return result({ error: e instanceof Error ? e.message : String(e) }); } } });
  return broker;
}
