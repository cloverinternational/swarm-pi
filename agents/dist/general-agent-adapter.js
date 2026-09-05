import { GENERAL_AGENT_TASK, validateGeneralAgentInput, } from "../../runtime-contracts/dist/general-agent.js";
export function createGeneralAgentHandler(createRuntime) {
    return async (raw, ctx) => {
        const input = validateGeneralAgentInput(raw);
        let sequence = 0;
        // Pi's session subscriber calls this only after message_end. Absurd's
        // step makes that boundary idempotent across worker retries.
        const checkpoint = async (value) => {
            const n = ++sequence;
            await ctx.step(`pi.${value.kind}.${n}`, async () => ({ kind: value.kind, sequence: n, messageId: "messageId" in value ? value.messageId : undefined }));
        };
        const runtime = await createRuntime(input, ctx, checkpoint);
        try {
            if (hasResumableContext(runtime))
                await runtime.continue(signalFor(ctx, input));
            else
                await runtime.run(input.prompt, signalFor(ctx, input));
            return { status: "completed", sessionId: input.sessionId, conversationId: input.conversationId, assistantMessageCount: sequence };
        }
        catch (error) {
            const aborted = error instanceof Error && (error.name === "AbortError" || error.message === "aborted");
            return { status: aborted ? "cancelled" : "failed", sessionId: input.sessionId, conversationId: input.conversationId, assistantMessageCount: sequence, errorCode: aborted ? "aborted" : "provider" };
        }
    };
}
export function registerGeneralAgentTask(absurd, createRuntime) {
    absurd.registerTask({ name: GENERAL_AGENT_TASK }, createGeneralAgentHandler(createRuntime));
}
/** Factories may expose this without adding it to the narrow runtime contract. */
function hasResumableContext(runtime) {
    return runtime.resumable === true;
}
function signalFor(_ctx, input) {
    const controller = new AbortController();
    if (input.timeoutSeconds)
        setTimeout(() => controller.abort(), input.timeoutSeconds * 1000).unref?.();
    return controller.signal;
}
export function assertPiContinuationApi(api) {
    if (typeof api.runAgentLoopContinue !== "function")
        throw new Error("Pi runAgentLoopContinue API is required");
}
