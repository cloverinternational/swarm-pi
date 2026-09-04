const objectSchema = { type: "object", additionalProperties: false };
const text = (details, message) => ({
    content: [{ type: "text", text: message ?? JSON.stringify(details, null, 2) }],
    details,
});
const failure = (error) => ({
    content: [{ type: "text", text: error instanceof Error ? error.message : String(error) }],
    details: { error: error instanceof Error ? error.message : String(error) },
    isError: true,
});
function executable(run) {
    return async (_toolCallId, params) => {
        try {
            return await run(params);
        }
        catch (error) {
            return failure(error);
        }
    };
}
export function registerScheduleTools(pi, scheduler) {
    pi.registerTool({
        name: "cron_create",
        label: "Create schedule",
        description: "Schedule a prompt to run at a future time, either recurring on a standard five-field cron schedule or once at its next occurrence.",
        promptSnippet: "Create recurring or one-shot prompt schedules",
        executionMode: "parallel",
        parameters: {
            ...objectSchema,
            required: ["prompt", "cron"],
            properties: {
                prompt: { type: "string", minLength: 1, maxLength: 100_000, description: "The prompt to execute on the scheduled agent." },
                cron: { type: "string", minLength: 1, maxLength: 256, description: "Standard five-field cron: minute hour day-of-month month day-of-week." },
                recurring: { type: "boolean", default: false, description: "Keep scheduling after the first occurrence." },
                durable: { type: "boolean", default: false, description: "Persist across process restarts." },
                agent_id: { type: "string", maxLength: 256, description: "Optional target agent identity." },
            },
        },
        execute: executable(async (params) => {
            const task = await scheduler.create({ ...params, agentId: params.agent_id });
            const details = scheduler.describe(task);
            return text(details, JSON.stringify({
                ...details,
                message: `Scheduled ${details.type} task ${task.id} (${details.human_schedule}). ${task.durable ? "persisted" : "session-only"}.`,
            }, null, 2));
        }),
    });
    pi.registerTool({
        name: "cron_list",
        label: "List schedules",
        description: "List scheduled cron jobs with IDs, expressions, schedule type, persistence, and next fire time.",
        promptSnippet: "List active prompt schedules",
        executionMode: "parallel",
        parameters: { ...objectSchema, properties: {} },
        execute: executable(async () => {
            const tasks = scheduler.list().map((task) => scheduler.describe(task));
            const details = { total_tasks: tasks.length, tasks };
            if (!tasks.length)
                return text(details, `No scheduled tasks.\n\n${JSON.stringify(details, null, 2)}`);
            const lines = tasks.map((task) => `  ${task.id}: ${task.human_schedule} (${task.type}, ${task.persistence})\n    Cron: ${task.cron}\n    Next: ${task.next_fire_at}`);
            return text(details, `Scheduled Tasks (${tasks.length} total):\n\n${lines.join("\n\n")}\n\n${JSON.stringify(details, null, 2)}`);
        }),
    });
    pi.registerTool({
        name: "cron_delete",
        label: "Delete schedule",
        description: "Cancel a scheduled cron job by ID, including its persisted record when durable.",
        promptSnippet: "Cancel a prompt schedule by ID",
        executionMode: "parallel",
        parameters: {
            ...objectSchema,
            required: ["id"],
            properties: { id: { type: "string", minLength: 1, description: "Task ID to cancel." } },
        },
        execute: executable(async (params) => {
            await scheduler.remove(params.id);
            return text({ id: params.id, message: `Cancelled scheduled task ${params.id}` });
        }),
    });
    pi.registerTool({
        name: "schedule_wakeup",
        label: "Schedule wakeup",
        description: "Schedule a one-shot prompt after a positive delay. Wakeups are session-only.",
        promptSnippet: "Wake the current agent after a delay",
        executionMode: "parallel",
        parameters: {
            ...objectSchema,
            required: ["prompt", "delay"],
            properties: {
                prompt: { type: "string", minLength: 1, maxLength: 100_000, description: "The prompt to execute on wakeup." },
                delay: { type: "string", minLength: 1, description: "Positive Go-style duration such as 30s, 5m, 1h, or 1h30m." },
            },
        },
        execute: executable(async (params) => {
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
