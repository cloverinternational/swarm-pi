import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ScheduleStore, Scheduler } from "../src/index.js";

const roots: string[] = [];
let id = 0;
async function fixture(sink: (prompt: string) => unknown = () => undefined) {
  const root = await mkdtemp(join(tmpdir(), "pi-scheduler-"));
  roots.push(root);
  const store = new ScheduleStore(join(root, ".swarm", "scheduled_tasks.json"));
  const scheduler = new Scheduler({
    store,
    sink,
    idFactory: (kind) => `${kind}-0000000${++id}`,
    onError: () => undefined,
  });
  await scheduler.start();
  return { root, store, scheduler };
}

beforeEach(() => {
  id = 0;
  vi.useFakeTimers();
  vi.setSystemTime(new Date(2026, 0, 1, 0, 0, 0));
});
afterEach(async () => {
  vi.useRealTimers();
  await Promise.all(roots.splice(0).map((root) => rm(root, { recursive: true, force: true })));
});

describe("Scheduler", () => {
  it("creates, sorts, and deletes tasks", async () => {
    const { scheduler } = await fixture();
    const later = await scheduler.create({ prompt: "later", cron: "10 * * * *" });
    const sooner = await scheduler.create({ prompt: "sooner", cron: "5 * * * *", recurring: true });
    expect(scheduler.list().map((task) => task.id)).toEqual([sooner.id, later.id]);
    await scheduler.remove(sooner.id);
    expect(scheduler.list().map((task) => task.id)).toEqual([later.id]);
    await expect(scheduler.remove("task-does-not-exist")).rejects.toThrow("not found");
  });

  it("restores durable tasks after restart but not session tasks", async () => {
    const { store, scheduler } = await fixture();
    const durable = await scheduler.create({ prompt: "keep", cron: "5 * * * *", durable: true });
    await scheduler.create({ prompt: "drop", cron: "10 * * * *" });
    await scheduler.stop();

    const restored = new Scheduler({ store, sink: () => undefined, idFactory: (kind) => `${kind}-99999999` });
    await restored.start();
    expect(restored.list().map((task) => task.id)).toEqual([durable.id]);
    await restored.stop();
  });

  it("fires a wakeup once and removes it before delivery", async () => {
    const calls: Array<{ prompt: string; visible: number }> = [];
    let scheduler!: Scheduler;
    ({ scheduler } = await fixture((message) => calls.push({ prompt: message, visible: scheduler.list().length })));
    const wakeup = await scheduler.scheduleWakeup({ prompt: "resume", delay: "2s" });
    expect(wakeup.nextFireAt).toEqual(new Date(2026, 0, 1, 0, 0, 2));

    await vi.advanceTimersByTimeAsync(1_999);
    await scheduler.idle();
    expect(calls).toEqual([]);
    await vi.advanceTimersByTimeAsync(1);
    await scheduler.idle();
    expect(calls).toEqual([{ prompt: "resume", visible: 0 }]);
    await vi.advanceTimersByTimeAsync(10_000);
    expect(calls).toHaveLength(1);
  });

  it("claims each recurring occurrence once and advances its next fire", async () => {
    const calls: string[] = [];
    const { scheduler } = await fixture((message) => calls.push(message));
    await scheduler.create({ prompt: "tick", cron: "* * * * *", recurring: true, durable: true });

    await vi.advanceTimersByTimeAsync(60_000);
    await scheduler.idle();
    expect(calls).toEqual(["tick"]);
    expect(scheduler.list()[0].nextFireAt).toEqual(new Date(2026, 0, 1, 0, 2, 0));
    await vi.advanceTimersByTimeAsync(30_000);
    await scheduler.idle();
    expect(calls).toHaveLength(1);
    await vi.advanceTimersByTimeAsync(30_000);
    await scheduler.idle();
    expect(calls).toEqual(["tick", "tick"]);
  });

  it("removes a one-shot even when its sink rejects", async () => {
    const errors: unknown[] = [];
    const root = await mkdtemp(join(tmpdir(), "pi-scheduler-"));
    roots.push(root);
    const scheduler = new Scheduler({
      store: new ScheduleStore(join(root, ".swarm", "scheduled_tasks.json")),
      sink: async () => { throw new Error("offline"); },
      onError: (error) => errors.push(error),
      idFactory: (kind) => `${kind}-12345678`,
    });
    await scheduler.start();
    await scheduler.scheduleWakeup({ prompt: "once", delay: "1s" });
    await vi.advanceTimersByTimeAsync(1_000);
    await scheduler.idle();
    expect(scheduler.list()).toEqual([]);
    expect(errors).toHaveLength(1);
  });

  it("returns defensive copies", async () => {
    const { scheduler } = await fixture();
    await scheduler.create({ prompt: "safe", cron: "5 * * * *" });
    const listed = scheduler.list();
    listed[0].prompt = "mutated";
    listed[0].nextFireAt.setFullYear(2030);
    expect(scheduler.list()[0].prompt).toBe("safe");
    expect(scheduler.list()[0].nextFireAt.getFullYear()).toBe(2026);
  });
});
