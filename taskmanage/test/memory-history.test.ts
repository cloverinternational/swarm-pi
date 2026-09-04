import { describe, expect, it } from "vitest";
import { MemoryHistory, MEMORY_ENTRY_TYPE, redact, scopeOf } from "../../.pi/extensions/memory-history.ts";

describe("durable memory history", () => {
  it("redacts credentials before persistence", () => {
    expect(redact("use sk-1234567890123456 and api_key=secret-value")).toBe("use [REDACTED] and api_key=[REDACTED]");
  });

  it("round trips versioned entries and isolates namespace/workspace/session", () => {
    const clock = () => new Date("2025-01-01T00:00:00.000Z");
    const history = new MemoryHistory(clock);
    const entry = history.remember("Deploy the worker", { namespace: "team", workspace: "/repo", session: "one" }, ["deploy"]);
    history.load([entry, { type: MEMORY_ENTRY_TYPE, data: { ...entry.data, namespace: "other" } }, { type: MEMORY_ENTRY_TYPE, data: { ...entry.data, version: 99 } }]);
    expect(history.search("deploy", { namespace: "team", workspace: "/repo", session: "one" })).toHaveLength(1);
    expect(history.search("deploy", { namespace: "other", workspace: "/repo", session: "one" })).toHaveLength(1);
    expect(history.search("deploy", { namespace: "team", workspace: "/other", session: "one" })).toHaveLength(0);
    expect(history.all()).toHaveLength(2);
  });

  it("searches tags, limits results, and replays chronological history", () => {
    let n = 0;
    const history = new MemoryHistory(() => new Date(1000 * ++n));
    const scope = scopeOf({ namespace: "n", workspace: "/w", session: "s" });
    history.remember("first decision", scope, ["architecture"]);
    history.remember("second decision", scope, ["release"]);
    history.remember("unrelated", scope);
    expect(history.search("architecture", scope)[0].text).toBe("first decision");
    expect(history.search("decision", scope, 1)[0].text).toBe("second decision");
    expect(history.replay(scope).map(x => x.text)).toEqual(["first decision", "second decision", "unrelated"]);
  });

  it("migrates legacy records into the current scoped schema", () => {
    const history = new MemoryHistory(() => new Date("2025-02-01T00:00:00.000Z"));
    const [entry] = history.migrate([{ type: "memory", data: { text: "password=hunter2: remember this", tags: ["old"] } }], { namespace: "migrated", workspace: "/repo", session: "s" });
    expect(entry.type).toBe(MEMORY_ENTRY_TYPE);
    expect(entry.data.version).toBe(1);
    expect(entry.data.text).toContain("password=[REDACTED]");
    expect(entry.data.source).toBe("migration");
  });
});
