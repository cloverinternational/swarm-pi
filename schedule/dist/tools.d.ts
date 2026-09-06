import type { Scheduler } from "./scheduler.js";
export declare function goLocalRFC3339(date: Date): string;
export interface ScheduleToolAPI {
    registerTool(tool: unknown): void;
}
export declare function registerScheduleTools(pi: ScheduleToolAPI, scheduler: Scheduler): void;
