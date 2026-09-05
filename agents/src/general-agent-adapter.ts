import { Absurd, type TaskContext } from "absurd-sdk";
import {
  GENERAL_AGENT_TASK,
  validateGeneralAgentInput,
  type GeneralAgentInput,
  type GeneralAgentOutput,
  type GeneralAgentRuntime,
} from "../../runtime-contracts/dist/general-agent.js";

/** Runtime construction is deliberately injected: production can construct a Pi
 * Agent/Session, while tests can record calls without a provider or Postgres. */
export type GeneralAgentRuntimeFactory = (input: GeneralAgentInput, ctx: TaskContext, checkpoint: GeneralAgentRuntime["checkpoint"]) => Promise<GeneralAgentRuntime> | GeneralAgentRuntime;

export function createGeneralAgentHandler(createRuntime: GeneralAgentRuntimeFactory) {
  return async (raw: GeneralAgentInput, ctx: TaskContext): Promise<GeneralAgentOutput> => {
      const input = validateGeneralAgentInput(raw);
      let sequence = 0;
      // Pi's session subscriber calls this only after message_end. Absurd's
      // step makes that boundary idempotent across worker retries.
      const checkpoint = async (value: Parameters<GeneralAgentRuntime["checkpoint"]>[0]) => {
        const n = ++sequence;
        await ctx.step(`pi.${value.kind}.${n}`, async () => ({ kind: value.kind, sequence: n, messageId: "messageId" in value ? value.messageId : undefined }));
      };
      const runtime = await createRuntime(input, ctx, checkpoint);
      try {
        if (hasResumableContext(runtime)) await runtime.continue(signalFor(ctx, input));
        else await runtime.run(input.prompt, signalFor(ctx, input));
        return { status: "completed", sessionId: input.sessionId, conversationId: input.conversationId, assistantMessageCount: sequence };
      } catch (error) {
        const aborted = error instanceof Error && (error.name === "AbortError" || error.message === "aborted");
        return { status: aborted ? "cancelled" : "failed", sessionId: input.sessionId, conversationId: input.conversationId, assistantMessageCount: sequence, errorCode: aborted ? "aborted" : "provider" };
      }
  };
}

export function registerGeneralAgentTask(absurd: Absurd, createRuntime: GeneralAgentRuntimeFactory): void {
  absurd.registerTask<GeneralAgentInput, GeneralAgentOutput>({ name: GENERAL_AGENT_TASK }, createGeneralAgentHandler(createRuntime));
}

/** Factories may expose this without adding it to the narrow runtime contract. */
function hasResumableContext(runtime: GeneralAgentRuntime): boolean {
  return (runtime as GeneralAgentRuntime & { resumable?: boolean }).resumable === true;
}

function signalFor(_ctx: TaskContext, input: GeneralAgentInput): AbortSignal {
  const controller = new AbortController();
  if (input.timeoutSeconds) setTimeout(() => controller.abort(), input.timeoutSeconds * 1000).unref?.();
  return controller.signal;
}

export type PiContinuationApi = {
  runAgentLoopContinue: (...args: never[]) => Promise<unknown>;
};

export function assertPiContinuationApi(api: Partial<PiContinuationApi>): void {
  if (typeof api.runAgentLoopContinue !== "function") throw new Error("Pi runAgentLoopContinue API is required");
}
