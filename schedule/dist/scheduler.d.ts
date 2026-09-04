import { ScheduleStore } from "./store.js";
import type { CreateScheduleInput, PromptSink, ScheduleClock, ScheduledTask, WakeupInput } from "./types.js";
export interface SchedulerOptions {
    workDir?: string;
    store?: ScheduleStore;
    sink: PromptSink;
    clock?: ScheduleClock;
    onError?: (error: unknown) => void;
    idFactory?: (kind: "task" | "wakeup") => string;
}
export declare class Scheduler {
    private readonly tasks;
    private readonly store;
    private readonly clock;
    private readonly sink;
    private readonly onError;
    private readonly idFactory;
    private timer?;
    private started;
    private mutation;
    private firing;
    constructor(options: SchedulerOptions);
    start(): Promise<void>;
    stop(): Promise<void>;
    create(input: CreateScheduleInput): Promise<ScheduledTask>;
    scheduleWakeup(input: WakeupInput): Promise<ScheduledTask & {
        delay: string;
    }>;
    remove(id: string): Promise<ScheduledTask>;
    list(): ScheduledTask[];
    /** Wait until pending persistence and prompt delivery have settled. */
    idle(): Promise<void>;
    describe(task: ScheduledTask): Record<string, unknown>;
    private assertStarted;
    private uniqueId;
    private persist;
    private arm;
    private fireDue;
    private exclusive;
}
