import { describe, expect, it } from "vitest";
import { ContextIndex, scopeOf } from "../../lib/context/page-index-memory.ts";
import { RetrievalSession, chooseRetrievalModel } from "../../lib/context/context-retrieval.ts";

const SCOPE = { workspace: "/repo", session: "s" };
function seeded() {
  const index = new ContextIndex();
  const entry = index.index("adr.md", "# Decisions\n## Storage\nWe chose SQLite.\n## Transport\nWe chose HTTP.\n", SCOPE);
  return { index, session: new RetrievalSession(index, scopeOf(SCOPE)), sourceId: entry.data.id };
}

describe("retrieval model choice", () => {
  it("prefers the cheapest capable model", () => {
    expect(chooseRetrievalModel(["openai/gpt-4o", "anthropic/claude-3-5-haiku-latest"])).toMatchObject({ model: "anthropic/claude-3-5-haiku-latest", fallback: false });
  });
  it("falls back to the session model and flags it", () => {
    const choice = chooseRetrievalModel(["some/big-model"]);
    expect(choice.fallback).toBe(true);
    expect(choice.model).toBeUndefined();
    expect(choice.reason).toContain("session model");
  });
  it("honours an explicit override", () => expect(chooseRetrievalModel([], "custom/model")).toMatchObject({ model: "custom/model", fallback: false }));
});

describe("RetrievalSession", () => {
  it("returns titles and nodeIds without body text", () => {
    const { session } = seeded();
    const { outline, next_steps } = session.outline();
    expect(outline.map(e => e.title)).toEqual(["Decisions", "Storage", "Transport"]);
    expect(JSON.stringify(outline)).not.toContain("SQLite");
    expect(outline[1]).toMatchObject({ depth: 1, nodeId: "0002" });
    expect(next_steps.options.join(" ")).toContain("do NOT use general knowledge");
  });

  it("reports an empty index honestly instead of returning nothing", () => {
    const session = new RetrievalSession(new ContextIndex(), scopeOf(SCOPE));
    const { outline, next_steps } = session.outline();
    expect(outline).toEqual([]);
    expect(next_steps.summary).toBe("Nothing is indexed");
    expect(next_steps.options[0]).toContain("context_index");
  });

  it("reads only sections the outline offered", () => {
    const { session, sourceId } = seeded();
    session.outline();
    const { evidence } = session.read(sourceId, ["0002"]);
    expect(evidence).toHaveLength(1);
    expect(evidence[0].excerpt).toContain("SQLite");
    expect(evidence[0].citation).toMatchObject({ nodeId: "0002", title: "Storage", line: 2 });
  });

  it("refuses nodeIds that were never listed in an outline", () => {
    const { session, sourceId } = seeded();
    const { evidence, next_steps } = session.read(sourceId, ["0002"]);
    expect(evidence).toEqual([]);
    expect(next_steps.options[0]).toContain("Call context_outline first");
  });

  it("enforces the read budget and says what went unread", () => {
    const { session, sourceId } = seeded();
    session.outline();
    const budgeted = new RetrievalSession(seeded().index, scopeOf(SCOPE), { maxReads: 1, maxChars: 24000 });
    budgeted.outline();
    const first = budgeted.read(sourceId, ["0002", "0003"]);
    expect(first.evidence).toHaveLength(1);
    expect(first.budget.exhausted).toBe(true);
    expect(first.next_steps.options[0]).toContain("budget reached");
  });

  it("truncates against the character budget rather than overflowing", () => {
    const index = new ContextIndex();
    const entry = index.index("big.md", "# Big\n" + "y".repeat(5000), SCOPE);
    const session = new RetrievalSession(index, scopeOf(SCOPE), { maxReads: 5, maxChars: 100 });
    session.outline();
    const { evidence } = session.read(entry.data.id, ["0001"]);
    expect(evidence[0].excerpt).toContain("truncated by retrieval budget");
    expect(evidence[0].excerpt.length).toBeLessThan(200);
  });
});
