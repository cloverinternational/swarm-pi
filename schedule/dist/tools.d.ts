import type { Scheduler } from "./scheduler.js";
export interface ScheduleToolAPI {
    registerTool(tool: unknown): void;
}
export declare function registerScheduleTools(pi: ScheduleToolAPI, scheduler: Scheduler): void;
