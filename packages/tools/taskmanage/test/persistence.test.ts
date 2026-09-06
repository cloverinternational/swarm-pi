import { describe, expect, it } from "vitest";
import { CURRENT_SCHEMA_VERSION, replayLatest, snapshot } from "../src/persistence.js";
import { TaskManager, type JournalEntry } from "../src/task-manage.js";

describe("versioned append/replay persistence", () => {
  it("writes a versioned snapshot and replays the highest revision", () => {
    const first = snapshot({ value: "old" }, 1, "2020-01-01T00:00:00.000Z");
    const second = snapshot({ value: "new" }, 2, "2020-01-02T00:00:00.000Z");
    const result = replayLatest([{ type: "state", data: first }, { type: "state", data: second }], "state", (v) => v as { value: string });
    expect(result?.state).toEqual({ value: "new" });
    expect(result?.revision).toBe(2);
    expect(first.schemaVersion).toBe(CURRENT_SCHEMA_VERSION);
  });

  it("migrates legacy unversioned task entries and ignores future versions", () => {
    const legacy = { type: "pi-swarm-task-state", data: { nextId: 2, tasks: [], keys: {} } };
    const future = { type: "pi-swarm-task-state", data: { schemaVersion: 99, revision: 99, state: { nextId: 99, tasks: [], keys: {} } } };
    const manager = new TaskManager();
    manager.rehydrate([legacy as JournalEntry, future as any]);
    expect(manager.snapshot().nextId).toBe(2);
    expect(manager.metrics().lastRevision).toBe(0);
  });

  it("increments durable revisions and exposes useful metrics", () => {
    const entries: JournalEntry[] = [];
    const manager = new TaskManager(entry => entries.push(entry));
    manager.execute({ operations: [{ key: "x", op: "create", subject: "Persist me" }] });
    manager.execute({ operations: [{ key: "read", op: "list" }] });
    expect((entries[0].data as any).schemaVersion).toBe(CURRENT_SCHEMA_VERSION);
    expect((entries[0].data as any).revision).toBe(1);
    expect(manager.metrics()).toMatchObject({ mutations: 1, reads: 1, lastRevision: 1 });
  });
});
