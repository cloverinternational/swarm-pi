import type { ID } from "./contracts.js";
export type GoalState = "queued" | "running" | "paused" | "completed";
export type LoopState = "queued" | "running" | "paused" | "stopped";
export interface ContinuationBudget {
    maxIterations?: number;
    maxTokens?: number;
    maxCost?: number;
    maxNoProgress?: number;
}
export interface GoalRecord {
    id: ID;
    description: string;
    doneWhen: string;
    doneWhenReviewed: boolean;
    state: GoalState;
    createdAt: string;
    updatedAt: string;
    taskId?: ID;
}
export interface LoopRecord {
    id: ID;
    goalId?: ID;
    prompt: string;
    cadence: string;
    continuation?: ContinuationBudget;
    state: LoopState;
    createdAt: string;
    updatedAt: string;
    scheduleId?: string;
}
export interface GoalCreateInput {
    description: string;
    doneWhen: string;
    doneWhenReviewed: boolean;
    idempotencyKey?: string;
}
export interface LoopCreateInput {
    prompt: string;
    cadence: string;
    goalId?: ID;
    continuation?: ContinuationBudget;
    idempotencyKey?: string;
}
/** Authoritative durable workflow boundary. Implementations own persistence; adapters never do. */
export interface GoalLoopControlPlane {
    goalCreate(input: GoalCreateInput): Promise<GoalRecord>;
    goalStatus(id: ID): Promise<GoalRecord>;
    goalPause(id: ID): Promise<GoalRecord>;
    goalResume(id: ID): Promise<GoalRecord>;
    goalComplete(id: ID): Promise<GoalRecord>;
    loopCreate(input: LoopCreateInput): Promise<LoopRecord>;
    loopStatus(id: ID): Promise<LoopRecord>;
    loopPause(id: ID): Promise<LoopRecord>;
    loopResume(id: ID): Promise<LoopRecord>;
    loopStop(id: ID): Promise<LoopRecord>;
}
export declare const goalLoopTools: {
    readonly goal_create: {
        readonly required: readonly ["description", "doneWhen", "doneWhenReviewed"];
        readonly properties: {
            readonly description: {
                readonly type: "string";
                readonly minLength: 1;
            };
            readonly doneWhen: {
                readonly type: "string";
                readonly minLength: 1;
            };
            readonly doneWhenReviewed: {
                readonly type: "boolean";
                readonly const: true;
            };
            readonly idempotencyKey: {
                readonly type: "string";
                readonly minLength: 1;
            };
        };
    };
    readonly goal_status: {
        readonly required: readonly ["id"];
        readonly properties: {
            readonly id: {
                readonly type: "string";
                readonly minLength: 1;
            };
        };
    };
    readonly goal_pause: {
        readonly required: readonly ["id"];
        readonly properties: {
            readonly id: {
                readonly type: "string";
                readonly minLength: 1;
            };
        };
    };
    readonly goal_resume: {
        readonly required: readonly ["id"];
        readonly properties: {
            readonly id: {
                readonly type: "string";
                readonly minLength: 1;
            };
        };
    };
    readonly goal_complete: {
        readonly required: readonly ["id"];
        readonly properties: {
            readonly id: {
                readonly type: "string";
                readonly minLength: 1;
            };
        };
    };
    readonly loop_create: {
        readonly required: readonly ["prompt", "cadence", "continuation"];
        readonly properties: {
            readonly prompt: {
                readonly type: "string";
                readonly minLength: 1;
            };
            readonly cadence: {
                readonly type: "string";
                readonly minLength: 1;
            };
            readonly goalId: {
                readonly type: "string";
                readonly minLength: 1;
            };
            readonly continuation: {
                readonly type: "object";
                readonly additionalProperties: false;
                readonly properties: {
                    readonly maxIterations: {
                        readonly type: "integer";
                        readonly minimum: 1;
                    };
                    readonly maxTokens: {
                        readonly type: "integer";
                        readonly minimum: 1;
                    };
                    readonly maxCost: {
                        readonly type: "number";
                        readonly exclusiveMinimum: 0;
                    };
                    readonly maxNoProgress: {
                        readonly type: "integer";
                        readonly minimum: 1;
                    };
                };
            };
        };
    };
    readonly loop_status: {
        readonly required: readonly ["id"];
        readonly properties: {
            readonly id: {
                readonly type: "string";
                readonly minLength: 1;
            };
        };
    };
    readonly loop_pause: {
        readonly required: readonly ["id"];
        readonly properties: {
            readonly id: {
                readonly type: "string";
                readonly minLength: 1;
            };
        };
    };
    readonly loop_resume: {
        readonly required: readonly ["id"];
        readonly properties: {
            readonly id: {
                readonly type: "string";
                readonly minLength: 1;
            };
        };
    };
    readonly loop_stop: {
        readonly required: readonly ["id"];
        readonly properties: {
            readonly id: {
                readonly type: "string";
                readonly minLength: 1;
            };
        };
    };
};
/** Register model-callable workflow controls. Creation is always queued; resume is the explicit start. */
export declare function registerGoalLoopTools(pi: {
    registerTool(tool: unknown): void;
}, control: GoalLoopControlPlane): void;
export declare function registerGoalLoopCommands(pi: {
    registerCommand?(name: string, options: {
        description: string;
        handler: (args: string) => Promise<unknown>;
    }): void;
}, control: GoalLoopControlPlane): void;
export declare function registerGoalLoop(pi: {
    registerTool(tool: unknown): void;
    registerCommand?(name: string, options: {
        description: string;
        handler: (args: string) => Promise<unknown>;
    }): void;
}, control: GoalLoopControlPlane): void;
