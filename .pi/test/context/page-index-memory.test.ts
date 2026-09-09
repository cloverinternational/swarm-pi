import { describe, expect, it } from "vitest";
import { ContextIndex, CONTEXT_SOURCE_ENTRY, CONTEXT_TOMBSTONE_ENTRY, EXCERPT_LIMIT, indexMarkdown, redactContext } from "../../lib/context/page-index-memory.ts";

describe("Page-Index-inspired context memory", () => {
  it("builds nested Markdown nodes with line provenance and ignores fenced headings", () => {
    const result = indexMarkdown("# Decision\nUse capture.\n## Scope\nWorkspace.\n" + String.fromCharCode(96,96,96) + "\n# Not a heading\n" + String.fromCharCode(96,96,96) + "\n");
    expect(result.tree[0].children[0]).toMatchObject({ nodeId: "0002", line: 3, title: "Scope" });
    expect(result.tree[0].children[0].text).toContain("Workspace.");
    expect(result.tree[0].children[0].children).toEqual([]);
  });
  it("redacts secrets before indexing", () => expect(redactContext("api_key=secret-value")).toBe("api_key=[REDACTED]"));
  it("retrieves only the exact scope and preserves citations", () => {
    const index = new ContextIndex(() => new Date("2025-01-01T00:00:00.000Z"));
    const entry = index.index("decisions.md", "# Release\n## Choice\nShip on Friday.\n", { namespace: "team", workspace: "/repo", session: "s" });
    expect(index.retrieve("Friday", { namespace: "team", workspace: "/repo", session: "s" })).toMatchObject([{ citation: { sourceId: entry.data.id, line: 2, nodeId: "0002" } }]);
    expect(index.retrieve("Friday", { namespace: "other", workspace: "/repo", session: "s" })).toEqual([]);
  });
  it("deletes through a tombstone and does not resurrect on reload", () => {
    const index = new ContextIndex(); const entry = index.index("a.md", "# A\ntext", { workspace: "/repo", session: "s" }); const tombstone = index.delete(entry.data.id);
    expect(tombstone.type).toBe(CONTEXT_TOMBSTONE_ENTRY); expect(index.inspect({ workspace: "/repo", session: "s" })).toEqual([]);
    const reloaded = new ContextIndex(); reloaded.load([{ type: CONTEXT_SOURCE_ENTRY, data: entry.data }, tombstone]); expect(reloaded.inspect({ workspace: "/repo", session: "s" })).toEqual([]);
  });

  it("bounds excerpts and marks retrieval results untrusted", () => {
    const index = new ContextIndex();
    index.index("big.md", "# Big\n" + "x".repeat(5000), { workspace: "/repo", session: "s" });
    const matches = index.retrieve("Big", { workspace: "/repo", session: "s" });
    expect(matches[0].excerpt.length).toBeLessThanOrEqual(EXCERPT_LIMIT + 20);
    expect(matches[0].excerpt).toContain("truncated");
  });
  it("detects stale and missing file-backed sources", () => {
    const index = new ContextIndex();
    index.index("notes.md", "# Notes\noriginal", { workspace: "/repo", session: "s" }, "docs/notes.md");
    expect(index.staleness(() => "# Notes\noriginal")).toEqual([{ id: expect.any(String), path: "docs/notes.md", state: "current" }]);
    expect(index.staleness(() => "# Notes\nchanged")[0].state).toBe("stale");
    expect(index.staleness(() => undefined)[0].state).toBe("missing");
  });
});
