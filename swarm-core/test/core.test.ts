import { describe, expect, it } from "vitest";
import { createEventId, createIdentity, MemoryEventJournal, SwarmRuntime } from "../src/index.js";

describe("swarm-core baseline contracts", () => {
  it("creates deterministic sortable IDs for a supplied clock/random source", () => {
    expect(createEventId(1, 0)).toBe("0000000001-0000000");
    expect(createEventId(2, 0.5)).toMatch(/^0000000002-/);
  });
  it("rejects blank identity fields", () => {
    expect(() => createIdentity("  ")).toThrow("workspace");
    expect(() => createIdentity("/work", new Date(0), " ")).toThrow("sessionId");
  });
  it("correlates events to the active session and invalidates on replacement", () => {
    const journal = new MemoryEventJournal();
    const runtime = new SwarmRuntime(journal);
    runtime.start(createIdentity("/one", new Date(0), "s1"));
    runtime.emit("turn_start", { turnId: "t1" });
    runtime.replace(createIdentity("/two", new Date(1), "s2"));
    const entries = runtime.entries();
    expect(entries.map(event => event.type)).toEqual(["session_start", "turn_start", "session_shutdown", "session_start"]);
    expect(entries[1].sessionId).toBe("s1");
    expect(entries[3].sessionId).toBe("s2");
    expect(runtime.identity?.workspace).toBe("/two");
  });
  it("rejects duplicate event IDs and does not expose mutable journal state", () => {
    const journal = new MemoryEventJournal();
    const event = { id: "e", type: "x", sessionId: "s", at: new Date(0).toISOString(), data: { value: 1 } };
    journal.append(event);
    expect(() => journal.append(event)).toThrow("duplicate");
    const entries = journal.entries();
    (entries[0].data as { value: number }).value = 9;
    expect((journal.entries()[0].data as { value: number }).value).toBe(1);
  });
});
