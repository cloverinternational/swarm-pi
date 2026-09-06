import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterEach, describe, expect, it } from "vitest";
import { registerScheduleTools, ScheduleStore, Scheduler } from "../src/index.js";

const roots: string[] = [];
afterEach(async () => {
  await Promise.all(roots.splice(0).map((root) => rm(root, { recursive: true, force: true })));
});

describe("schedule tools", () => {
  it("registers the Swarm tool surface and returns structured results", async () => {
    const root = await mkdtemp(join(tmpdir(), "pi-schedule-tools-"));
    roots.push(root);
    const scheduler = new Scheduler({
      store: new ScheduleStore(join(root, "tasks.json")),
      sink: () => undefined,
      idFactory: (kind) => `${kind}-12345678`,
    });
    await scheduler.start();
    const tools = new Map<string, any>();
    registerScheduleTools({ registerTool: (tool: any) => tools.set(tool.name, tool) }, scheduler);
    expect([...tools.keys()]).toEqual(["CronCreate", "CronList", "CronDelete", "ScheduleWakeup"]);

    const created = await tools.get("CronCreate").execute("call-1", {
      prompt: "run",
      cron: "*/5 * * * *",
      recurring: true,
      durable: true,
    });
    expect(created.isError).toBeUndefined();
    expect(created.details).toEqual(expect.objectContaining({
      id: "task-12345678",
      type: "recurring",
      persistence: "durable",
    }));

    const listed = await tools.get("CronList").execute("call-2", {});
    expect(listed.details.total_tasks).toBe(1);
    const deleted = await tools.get("CronDelete").execute("call-3", { id: "task-12345678" });
    expect(deleted.details.message).toContain("Cancelled");
  });

  it("reports validation failures as tool errors", async () => {
    const root = await mkdtemp(join(tmpdir(), "pi-schedule-tools-"));
    roots.push(root);
    const scheduler = new Scheduler({ store: new ScheduleStore(join(root, "tasks.json")), sink: () => undefined });
    await scheduler.start();
    const tools = new Map<string, any>();
    registerScheduleTools({ registerTool: (tool: any) => tools.set(tool.name, tool) }, scheduler);
    // Pi flags a tool result as failed only when execute() throws; the
    // message is Swarm's sdkerr-wrapped text (schedule_wakeup.go).
    await expect(tools.get("ScheduleWakeup").execute("call", { prompt: "x", delay: "-1m" })).rejects.toThrow(/^Error executing ScheduleWakeup: invalid delay format: -1m \(error_id=err_[0-9a-f]{20}\)$/);
    await expect(tools.get("CronDelete").execute("call", { id: "nope" })).rejects.toThrow("Error executing CronDelete: task 'nope' not found (error_id=");
  });
});
