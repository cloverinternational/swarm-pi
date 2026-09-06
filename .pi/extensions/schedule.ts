import { resolve } from "node:path";
import { registerScheduleTools, Scheduler, type SchedulerOptions } from "../../packages/tools/schedule/src/index.ts";
import { withDefaultToolRenderer } from "../lib/swarm-tool-renderer.ts";

export interface ScheduleExtensionAPI {
  getCwd?(): string;
  registerTool(tool: unknown): void;
  sendUserMessage(content: string, options?: { deliverAs?: "steer" | "followUp" }): void | Promise<void>;
  on?(event: string, handler: (...args: any[]) => unknown): void;
}

export interface ScheduleExtensionOptions extends Omit<SchedulerOptions, "sink" | "workDir"> {
  workDir?: string;
}

export async function registerScheduleExtension(
  pi: ScheduleExtensionAPI,
  options: ScheduleExtensionOptions = {},
): Promise<Scheduler> {
  const workDir = resolve(options.workDir ?? pi.getCwd?.() ?? process.cwd());
  const scheduler = new Scheduler({
    ...options,
    workDir,
    sink: async (prompt) => {
      await pi.sendUserMessage(prompt, { deliverAs: "followUp" });
    },
  });
  registerScheduleTools({ ...pi, registerTool: (tool: unknown) => pi.registerTool(withDefaultToolRenderer(tool as any)) }, scheduler);
  await scheduler.start();
  pi.on?.("session_shutdown", () => scheduler.stop());
  return scheduler;
}

export default async function scheduleExtension(pi: ScheduleExtensionAPI): Promise<void> {
  await registerScheduleExtension(pi);
}
