import { Absurd, type TaskContext } from "absurd-sdk";
import { type GeneralAgentInput, type GeneralAgentOutput, type GeneralAgentRuntime } from "../../runtime-contracts/dist/general-agent.js";
/** Runtime construction is deliberately injected: production can construct a Pi
 * Agent/Session, while tests can record calls without a provider or Postgres. */
export type GeneralAgentRuntimeFactory = (input: GeneralAgentInput, ctx: TaskContext, checkpoint: GeneralAgentRuntime["checkpoint"]) => Promise<GeneralAgentRuntime> | GeneralAgentRuntime;
export declare function createGeneralAgentHandler(createRuntime: GeneralAgentRuntimeFactory): (raw: GeneralAgentInput, ctx: TaskContext) => Promise<GeneralAgentOutput>;
export declare function registerGeneralAgentTask(absurd: Absurd, createRuntime: GeneralAgentRuntimeFactory): void;
export type PiContinuationApi = {
    runAgentLoopContinue: (...args: never[]) => Promise<unknown>;
};
export declare function assertPiContinuationApi(api: Partial<PiContinuationApi>): void;
