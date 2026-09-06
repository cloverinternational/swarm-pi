import type { Scheduler } from "./scheduler.js";

interface ToolResult {
  content: Array<{ type: "text"; text: string }>;
  details: unknown;
  isError?: boolean;
}

const objectSchema = { type: "object" } as const;
const text = (details: unknown, message?: string): ToolResult => ({
  content: [{ type: "text", text: message ?? JSON.stringify(details, null, 2) }],
  details,
});
const failure = (error: unknown): ToolResult => ({
  content: [{ type: "text", text: error instanceof Error ? error.message : String(error) }],
  details: { error: error instanceof Error ? error.message : String(error) },
  isError: true,
});

function executable<T>(run: (params: T) => Promise<ToolResult>) {
  return async (_toolCallId: string, params: T): Promise<ToolResult> => {
    try {
      return await run(params);
    } catch (error) {
      return failure(error);
    }
  };
}

export interface ScheduleToolAPI {
  registerTool(tool: unknown): void;
}

export function registerScheduleTools(pi: ScheduleToolAPI, scheduler: Scheduler): void {
  pi.registerTool({
    name: "CronCreate",
    label: "Create schedule",
    description: "Schedule a prompt to run at a future time - either recurring on a cron schedule, or once at a specific time. Uses standard 5-field cron. Pass durable: true to persist to disk; otherwise session-only.",
    promptSnippet: "Create recurring or one-shot prompt schedules",
    executionMode: "parallel",
    parameters: {
      ...objectSchema,
      required: ["prompt", "cron"],
      properties: {
        prompt: { type: "string", description: "The prompt to execute on the scheduled agent." },
        cron: { type: "string", description: "Cron expression (5-field: minute hour day-of-month month day-of-week). Examples: '*/5 * * * *' every 5min, '0 9 * * 1-5' weekdays at 9am, '0 0 1 * *' monthly." },
        recurring: { type: "boolean", description: "Recurring job? (default: false for one-shot)" },
        durable: { type: "boolean", description: "Persist to disk? (default: false, session-only)" },
        agent_id: { type: "string", description: "Specific agent to use for execution" },
      },
    },
    execute: executable(async (params: { prompt: string; cron: string; recurring?: boolean; durable?: boolean; agent_id?: string }) => {
      const task = await scheduler.create({ ...params, agentId: params.agent_id });
      const details = scheduler.describe(task);
      return text(details, JSON.stringify({
        ...details,
        message: `Scheduled ${details.type} task ${task.id} (${details.human_schedule}). ${task.durable ? "persisted" : "session-only"}.`,
      }, null, 2));
    }),
  });

  pi.registerTool({
    name: "CronList",
    label: "List schedules",
    description: "List all scheduled cron jobs. Shows task ID, cron expression, schedule type (recurring/one-shot), and persistence mode (durable/session-only).",
    promptSnippet: "List active prompt schedules",
    executionMode: "parallel",
    parameters: { ...objectSchema, properties: {} },
    execute: executable(async () => {
      const tasks = scheduler.list().map((task) => scheduler.describe(task));
      const details = { total_tasks: tasks.length, tasks };
      if (!tasks.length) return text(details, `No scheduled tasks.\n\n${JSON.stringify(details, null, 2)}`);
      const lines = tasks.map((task) =>
        `  ${task.id}: ${task.human_schedule} (${task.type}, ${task.persistence})\n    Cron: ${task.cron}\n    Next: ${task.next_fire_at}`,
      );
      return text(details, `Scheduled Tasks (${tasks.length} total):\n\n${lines.join("\n\n")}\n\n${JSON.stringify(details, null, 2)}`);
    }),
  });

  pi.registerTool({
    name: "CronDelete",
    label: "Delete schedule",
    description: "Cancel a scheduled cron job by ID. Removes it from the scheduler and from disk if it was a durable (persisted) task.",
    promptSnippet: "Cancel a prompt schedule by ID",
    executionMode: "parallel",
    parameters: {
      ...objectSchema,
      required: ["id"],
      properties: { id: { type: "string", description: "Task ID to cancel" } },
    },
    execute: executable(async (params: { id: string }) => {
      await scheduler.remove(params.id);
      return text({ id: params.id, message: `Cancelled scheduled task ${params.id}` });
    }),
  });

  pi.registerTool({
    name: "ScheduleWakeup",
    label: "Schedule wakeup",
    description: "Schedule a prompt to run after a delay. Used for dynamic scheduling where the model decides when to resume.",
    promptSnippet: "Wake the current agent after a delay",
    executionMode: "parallel",
    parameters: {
      ...objectSchema,
      required: ["prompt", "delay"],
      properties: {
        prompt: { type: "string", description: "The prompt to execute on wakeup." },
        delay: { type: "string", description: "Delay before wakeup (e.g., '5m', '1h')." },
      },
    },
    execute: executable(async (params: { prompt: string; delay: string }) => {
      const task = await scheduler.scheduleWakeup(params);
      return text({
        id: task.id,
        delay: task.delay,
        fire_time: task.nextFireAt.toISOString(),
        message: `Scheduled wakeup ${task.id} in ${task.delay} (at ${task.nextFireAt.toISOString()}).`,
      });
    }),
  });
}
