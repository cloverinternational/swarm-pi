import { mkdtemp, readFile, stat, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterEach, describe, expect, it } from "vitest";
import { ScheduleStore, type ScheduledTask } from "../src/index.js";

const roots: string[] = [];
async function fixture() {
  const root = await mkdtemp(join(tmpdir(), "pi-schedule-store-"));
  roots.push(root);
  return { root, path: join(root, ".swarm", "scheduled_tasks.json") };
}
afterEach(async () => {
  const { rm } = await import("node:fs/promises");
  await Promise.all(roots.splice(0).map((root) => rm(root, { recursive: true, force: true })));
});

function task(overrides: Partial<ScheduledTask> = {}): ScheduledTask {
  return {
    id: "task-12345678",
    prompt: "do work",
    cron: "*/5 * * * *",
    recurring: true,
    durable: true,
    createdAt: new Date("2026-01-01T00:00:00.000Z"),
    nextFireAt: new Date("2026-01-01T00:05:00.000Z"),
    ...overrides,
  };
}

describe("ScheduleStore", () => {
  it("round-trips upstream-compatible durable task JSON with private permissions", async () => {
    const { path } = await fixture();
    const store = new ScheduleStore(path);
    await store.save([task({ lastFiredAt: new Date("2026-01-01T00:00:00.000Z"), agentId: "worker" })]);

    const raw = JSON.parse(await readFile(path, "utf8"));
    expect(raw).toEqual([expect.objectContaining({
      id: "task-12345678",
      created_at: "2026-01-01T00:00:00.000Z",
      last_fired_at: "2026-01-01T00:00:00.000Z",
      next_fire_at: "2026-01-01T00:05:00.000Z",
      agent_id: "worker",
    })]);
    expect((await stat(path)).mode & 0o777).toBe(0o600);
    expect(await store.load()).toEqual([task({
      lastFiredAt: new Date("2026-01-01T00:00:00.000Z"),
      agentId: "worker",
    })]);
  });

  it("does not persist session-only tasks and removes an empty store", async () => {
    const { path } = await fixture();
    const store = new ScheduleStore(path);
    await store.save([task()]);
    await store.save([task({ durable: false })]);
    await expect(readFile(path, "utf8")).rejects.toMatchObject({ code: "ENOENT" });
  });

  it("fails closed on corrupt or invalid persisted data", async () => {
    const { path } = await fixture();
    const store = new ScheduleStore(path);
    await store.save([task()]);
    await writeFile(path, "{bad json", "utf8");
    await expect(store.load()).rejects.toThrow("not valid JSON");
    await writeFile(path, JSON.stringify([{ ...JSON.parse(JSON.stringify({
      id: "bad",
      prompt: "x",
      cron: "* * * * *",
      recurring: true,
      durable: true,
      created_at: "2026-01-01T00:00:00Z",
      next_fire_at: "2026-01-01T00:01:00Z",
    })) }]), "utf8");
    await expect(store.load()).rejects.toThrow("invalid id");
  });
});
