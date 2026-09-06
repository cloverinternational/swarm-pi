/** Contracts for the first durable general-agent task.
 *
 * This module is intentionally runtime-neutral: Absurd is a Postgres-backed
 * durable task runner and Pi owns the model/session loop. It does not provide
 * a SQLite implementation or hide either dependency behind `any`.
 */
export declare const GENERAL_AGENT_TASK: "pi-swarm.general-agent";
export declare const GENERAL_AGENT_TOOLS: readonly ["general_agent_status", "general_agent_cancel"];
export type GeneralAgentToolName = typeof GENERAL_AGENT_TOOLS[number];
export type GeneralAgentInput = {
    prompt: string;
    workspace: string;
    sessionId: string;
    conversationId: string;
    provider: string;
    model: string;
    maxTurns?: number;
    timeoutSeconds?: number;
};
export type GeneralAgentOutput = {
    status: "completed" | "failed" | "cancelled";
    sessionId: string;
    conversationId: string;
    assistantMessageCount: number;
    lastMessageId?: string;
    errorCode?: "aborted" | "timeout" | "provider" | "tool" | "checkpoint" | "unknown";
};
export type GeneralAgentCheckpoint = {
    kind: "message_end";
    messageId: string;
    role: "user" | "assistant" | "toolResult";
    sequence: number;
} | {
    kind: "turn_end";
    sequence: number;
} | {
    kind: "agent_end";
    sequence: number;
};
export interface GeneralAgentRuntime {
    readonly sessionId: string;
    readonly conversationId: string;
    run(prompt: string, signal: AbortSignal): Promise<void>;
    continue(signal: AbortSignal): Promise<void>;
    checkpoint(checkpoint: GeneralAgentCheckpoint): Promise<void>;
}
export type GeneralAgentStatusResult = {
    taskId: string;
    state: "pending" | "running" | "completed" | "failed" | "cancelled";
    attempt: number;
    output?: GeneralAgentOutput;
};
export type GeneralAgentToolRequest = {
    tool: "general_agent_status";
    taskId: string;
} | {
    tool: "general_agent_cancel";
    taskId: string;
};
export type GeneralAgentToolResponse = {
    ok: true;
    tool: "general_agent_status";
    status: GeneralAgentStatusResult;
} | {
    ok: true;
    tool: "general_agent_cancel";
    cancelled: boolean;
} | {
    ok: false;
    tool: GeneralAgentToolName;
    error: "invalid_request" | "not_found" | "denied";
};
export declare function validateGeneralAgentInput(input: unknown): GeneralAgentInput;
