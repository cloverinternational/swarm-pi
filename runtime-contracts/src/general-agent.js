/** Contracts for the first durable general-agent task.
 *
 * This module is intentionally runtime-neutral: Absurd is a Postgres-backed
 * durable task runner and Pi owns the model/session loop. It does not provide
 * a SQLite implementation or hide either dependency behind `any`.
 */
export const GENERAL_AGENT_TASK = "pi-swarm.general-agent";
export const GENERAL_AGENT_TOOLS = ["general_agent_status", "general_agent_cancel"];
export function validateGeneralAgentInput(input) {
    if (!isRecord(input) || !nonEmpty(input.prompt) || !nonEmpty(input.workspace) ||
        !nonEmpty(input.sessionId) || !nonEmpty(input.conversationId) ||
        !nonEmpty(input.provider) || !nonEmpty(input.model)) {
        throw new Error("invalid general-agent input: required strings are missing");
    }
    if (input.maxTurns !== undefined && (!safePositiveInt(input.maxTurns) || input.maxTurns > 1000)) {
        throw new Error("invalid general-agent input: maxTurns");
    }
    if (input.timeoutSeconds !== undefined && (!safePositiveInt(input.timeoutSeconds) || input.timeoutSeconds > 86400)) {
        throw new Error("invalid general-agent input: timeoutSeconds");
    }
    return { prompt: input.prompt, workspace: input.workspace, sessionId: input.sessionId,
        conversationId: input.conversationId, provider: input.provider, model: input.model,
        ...(input.maxTurns === undefined ? {} : { maxTurns: input.maxTurns }),
        ...(input.timeoutSeconds === undefined ? {} : { timeoutSeconds: input.timeoutSeconds }) };
}
function isRecord(value) { return !!value && typeof value === "object" && !Array.isArray(value); }
function nonEmpty(value) { return typeof value === "string" && value.trim().length > 0; }
function safePositiveInt(value) { return typeof value === "number" && Number.isSafeInteger(value) && value > 0; }
