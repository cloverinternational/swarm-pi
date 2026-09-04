import type { ScheduledTask } from "./types.js";
export declare class ScheduleStore {
    readonly path: string;
    constructor(path: string);
    load(): Promise<ScheduledTask[]>;
    save(tasks: readonly ScheduledTask[]): Promise<void>;
}
