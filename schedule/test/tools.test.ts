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
    expect([...tools.keys()]).toEqual(["cron_create", "cron_list", "cron_delete", "schedule_wakeup"]);

    const created = await tools.get("cron_create").execute("call-1", {
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

    const listed = await tools.get("cron_list").execute("call-2", {});
    expect(listed.details.total_tasks).toBe(1);
    const deleted = await tools.get("cron_delete").execute("call-3", { id: "task-12345678" });
    expect(deleted.details.message).toContain("Cancelled");
  });

  it("reports validation failures as tool errors", async () => {
    const root = await mkdtemp(join(tmpdir(), "pi-schedule-tools-"));
    roots.push(root);
    const scheduler = new Scheduler({ store: new ScheduleStore(join(root, "tasks.json")), sink: () => undefined });
    await scheduler.start();
    const tools = new Map<string, any>();
    registerScheduleTools({ registerTool: (tool: any) => tools.set(tool.name, tool) }, scheduler);
    const result = await tools.get("schedule_wakeup").execute("call", { prompt: "x", delay: "-1m" });
    expect(result.isError).toBe(true);
    expect(result.content[0].text).toContain("invalid delay");
  });
});
