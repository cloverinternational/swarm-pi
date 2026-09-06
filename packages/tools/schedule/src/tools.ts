import { randomBytes } from "node:crypto";
import type { Scheduler } from "./scheduler.js";
import { withDefaultToolRenderer } from "../../../../.pi/lib/swarm-tool-renderer.ts";

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
// Go map[string]any → MarshalIndent with sorted keys.
const sorted = (value: Record<string, unknown>) => Object.fromEntries(Object.keys(value).sort().map((key) => [key, value[key]]));
/** Go time.Format(layout) in the local zone: "2006-01-02T15:04:05Z07:00" (RFC3339) or "15:04:05". */
const pad = (n: number) => String(n).padStart(2, "0");
export function goLocalRFC3339(date: Date): string {
  const offset = -date.getTimezoneOffset();
  const zone = offset === 0 ? "Z" : `${offset > 0 ? "+" : "-"}${pad(Math.floor(Math.abs(offset) / 60))}:${pad(Math.abs(offset) % 60)}`;
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}${zone}`;
}
const goClock = (date: Date) => `${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}`;

/** Swarm tools return sdkerr failures as tool errors: "Error executing <tool>: <msg> (error_id=…)". */
function executable<T>(name: string, run: (params: T) => Promise<ToolResult>) {
  return async (_toolCallId: string, params: T): Promise<ToolResult> => {
    try {
      return await run(params);
    } catch (error) {
      const message = error instanceof Error ? error.message : String(error);
      throw new Error(`Error executing ${name}: ${message} (error_id=err_${randomBytes(10).toString("hex")})`);
    }
  };
}

export interface ScheduleToolAPI {
  registerTool(tool: unknown): void;
}

export function registerScheduleTools(pi: ScheduleToolAPI, scheduler: Scheduler): void {
  pi.registerTool(withDefaultToolRenderer({
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
    execute: executable("CronCreate", async (params: { prompt: string; cron: string; recurring?: boolean; durable?: boolean; agent_id?: string }) => {
      const task = await scheduler.create({ ...params, agentId: params.agent_id });
      const details = scheduler.describe(task);
      // cron_create.go: persistence is reported as "session-only"/"persisted"
      // here (CronList says "session"/"durable"); no next_fire_at on the wire.
      const persistence = task.durable ? "persisted" : "session-only";
      return text(details, JSON.stringify(sorted({
        id: task.id, cron: task.cron, human_schedule: details.human_schedule, type: details.type, persistence,
        message: `Scheduled ${details.type} task ${task.id} (${details.human_schedule}). ${persistence}.`,
      }), null, 2));
    }),
  }));

  pi.registerTool(withDefaultToolRenderer({
    name: "CronList",
    label: "List schedules",
    description: "List all scheduled cron jobs. Shows task ID, cron expression, schedule type (recurring/one-shot), and persistence mode (durable/session-only).",
    promptSnippet: "List active prompt schedules",
    executionMode: "parallel",
    parameters: { ...objectSchema, properties: {} },
    execute: executable("CronList", async () => {
      // Swarm cron_list.go Run: text block + "\n" + MarshalIndent of a
      // map[string]any (keys sorted at every level; no next_fire_at).
      const tasks = scheduler.list().map((task) => scheduler.describe(task));
      const taskInfos = tasks.map(({ next_fire_at: _next, ...info }) => sorted(info));
      const details = sorted({ total_tasks: tasks.length, tasks: taskInfos });
      let output = "";
      if (!tasks.length) output += "No scheduled tasks.\n";
      else {
        output += `Scheduled Tasks (${tasks.length} total):\n\n`;
        for (const info of taskInfos) {
          output += `  ${info.id}: ${info.human_schedule} (${info.type}, ${info.persistence})\n    Cron: ${info.cron}\n`;
          if (info.agent_id !== undefined) output += `    Agent: ${info.agent_id}\n`;
          output += "\n";
        }
      }
      return text({ total_tasks: tasks.length, tasks }, `${output}\n${JSON.stringify(details, null, 2)}`);
    }),
  }));

  pi.registerTool(withDefaultToolRenderer({
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
    execute: executable("CronDelete", async (params: { id: string }) => {
      await scheduler.remove(params.id);
      return text(sorted({ id: params.id, message: `Cancelled scheduled task ${params.id}` }));
    }),
  }));

  pi.registerTool(withDefaultToolRenderer({
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
    execute: executable("ScheduleWakeup", async (params: { prompt: string; delay: string }) => {
      const task = await scheduler.scheduleWakeup(params);
      // schedule_wakeup.go: fire_time = time.Now().Add(delay).Format(RFC3339)
      // in the local zone; the message uses the "15:04:05" clock layout.
      return text(sorted({
        id: task.id,
        delay: task.delay,
        fire_time: goLocalRFC3339(task.nextFireAt),
        message: `Scheduled wakeup ${task.id} in ${task.delay} (at ${goClock(task.nextFireAt)}).`,
      }));
    }),
  }));
}
