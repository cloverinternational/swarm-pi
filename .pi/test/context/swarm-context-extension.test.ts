import { mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import swarmContextExtension from "../../extensions/10-context/swarm-context.ts";
import { footerSegments } from "../../extensions/50-ui/conversation-metrics.ts";

function harness(cwd: string, options: { models?: string[]; retriever?: (task: string) => string } = {}) {
  const tools = new Map<string, any>(); const commands = new Map<string, any>(); const entries: any[] = []; const notices: any[] = []; const spawned: any[] = [];
  let start: any;
  const pi: any = {
    registerTool: (tool: any) => tools.set(tool.name, tool),
    registerCommand: (name: string, opts: any) => commands.set(name, opts),
    appendEntry: (type: string, data: unknown) => entries.push({ type, data }),
    on: (event: string, handler: any) => { if (event === "session_start") start = handler; },
  };
  if (options.retriever) pi.agents = { spawn: (spec: any) => { spawned.push(spec); return { wait: async () => ({ status: "completed", output: options.retriever!(spec.task) }) }; } };
  swarmContextExtension(pi);
  start?.({}, { cwd, sessionManager: { getSessionFile: () => "session-1", getEntries: () => [] }, models: { list: () => options.models ?? ["anthropic/claude-3-5-haiku-latest"] }, ui: { notify: (text: string, level: string) => notices.push({ text, level }) } });
  const call = async (name: string, params: any) => { const output = await tools.get(name).execute("id", params); return { isError: output.isError === true, details: output.details as any, text: output.content[0].text }; };
  return { tools, commands, entries, notices, spawned, call };
}

const seed = () => { const cwd = mkdtempSync(join(tmpdir(), "swarm-context-")); writeFileSync(join(cwd, "adr.md"), "# Decisions\n## Storage\nWe chose SQLite.\n## Transport\nWe chose HTTP.\n"); return cwd; };

describe("swarm-context extension", () => {
  it("exposes search and outline, not a synthesized-answer tool", () => {
    const { tools, commands } = harness(process.cwd());
    expect([...tools.keys()].sort()).toEqual(["context_delete", "context_index", "context_inspect", "context_outline", "context_reindex", "context_remember", "context_search"]);
    expect(commands.has("swarm-context")).toBe(true);
  });

  it("returns cited evidence chosen by the retrieval agent", async () => {
    const { call, spawned } = harness(seed(), { retriever: task => { const id = JSON.parse(task.slice(task.indexOf("OUTLINE:") + 9))[1].sourceId; return JSON.stringify({ found: true, nodes: [{ sourceId: id, nodeId: "0002", why: "names the store" }] }); } });
    await call("context_index", { operation: "index", path: "adr.md" });
    const { details } = await call("context_search", { operation: "search", query: "which database" });
    expect(details.status).toBe("ok");
    expect(details.untrusted).toBe(true);
    expect(details.evidence[0].excerpt).toContain("SQLite");
    expect(details.evidence[0].citation).toMatchObject({ nodeId: "0002", title: "Storage", line: 2 });
    expect(details.evidence[0].why).toBe("names the store");
    expect(details.searched).toEqual([details.evidence[0].citation.sourceId + "#0002"]);
    expect(spawned[0].model).toBe("anthropic/claude-3-5-haiku-latest");
  });

  it("uses the cheapest model and warns in the footer only when falling back", async () => {
    footerSegments().delete("swarm-context");
    const cheap = harness(seed(), { models: ["anthropic/claude-3-5-haiku-latest"] });
    expect(footerSegments().get("swarm-context")!()).toBeUndefined();
    expect(cheap.notices).toEqual([]);

    const fallback = harness(seed(), { models: ["some/expensive-model"] });
    expect(footerSegments().get("swarm-context")!()).toBe("ctx:session-model");
    expect(fallback.notices[0]).toMatchObject({ level: "warn" });
    expect(fallback.notices[0].text).toContain("session model");
  });

  it("says not-indexed rather than searching an empty index", async () => {
    const { call, spawned } = harness(seed(), { retriever: () => "{}" });
    const { details } = await call("context_search", { operation: "search", query: "anything" });
    expect(details.status).toBe("not-indexed");
    expect(details.next_steps.options[0]).toContain("context_index");
    expect(spawned).toEqual([]);
  });

  it("reports no-result honestly when the retriever finds nothing", async () => {
    const { call } = harness(seed(), { retriever: () => JSON.stringify({ found: false, nodes: [], note: "No section covers deployment." }) });
    await call("context_index", { operation: "index", path: "adr.md" });
    const { details } = await call("context_search", { operation: "search", query: "deployment schedule" });
    expect(details.status).toBe("no-result");
    expect(details.evidence).toEqual([]);
    expect(details.next_steps.options[0]).toBe("No section covers deployment.");
    expect(details.next_steps.options[1]).toContain("rather than answering from general knowledge");
  });

  it("ignores retriever-invented nodeIds instead of fabricating excerpts", async () => {
    const { call } = harness(seed(), { retriever: () => JSON.stringify({ found: true, nodes: [{ sourceId: "made-up", nodeId: "9999", why: "hallucinated" }] }) });
    await call("context_index", { operation: "index", path: "adr.md" });
    const { details } = await call("context_search", { operation: "search", query: "which database" });
    expect(details.status).toBe("no-result");
    expect(details.evidence).toEqual([]);
  });

  it("survives unparseable retriever output", async () => {
    const { call } = harness(seed(), { retriever: () => "I could not comply." });
    await call("context_index", { operation: "index", path: "adr.md" });
    const { details } = await call("context_search", { operation: "search", query: "which database" });
    expect(details.status).toBe("no-result");
  });

  it("degrades to a self-service outline when no retriever exists", async () => {
    const { call } = harness(seed());
    await call("context_index", { operation: "index", path: "adr.md" });
    const { details } = await call("context_search", { operation: "search", query: "which database" });
    expect(details.status).toBe("no-retriever");
    expect(details.outline.map((e: any) => e.title)).toEqual(["Decisions", "Storage", "Transport"]);
  });

  it("refuses paths outside the workspace", async () => {
    const { call } = harness(seed());
    const output = await call("context_index", { operation: "index", path: "../../etc/passwd" });
    expect(output.isError).toBe(true);
    expect(output.text).toContain("outside the workspace");
  });

  it("indexes, inspects staleness, and deletes", async () => {
    const cwd = seed();
    const { call, entries } = harness(cwd);
    const indexed = await call("context_index", { operation: "index", path: "adr.md" });
    expect(entries).toHaveLength(1);
    expect((await call("context_inspect", { operation: "inspect" })).details[0]).toMatchObject({ state: "current", sections: 1, tree: undefined });
    writeFileSync(join(cwd, "adr.md"), "# Decisions\n## Storage\nWe changed our mind.\n");
    expect((await call("context_inspect", { operation: "inspect" })).details[0].state).toBe("stale");
    await call("context_delete", { operation: "delete", sourceId: indexed.details.id });
    expect((await call("context_inspect", { operation: "inspect" })).details).toEqual([]);
  });

  it("captures a note in one call and appends later notes to the same topic", async () => {
    const { call } = harness(seed());
    const first = await call("context_remember", { operation: "remember", note: "We chose SQLite for durability.", topic: "Decisions" });
    expect(first.details.appended).toBe(false);
    const second = await call("context_remember", { operation: "remember", note: "We rejected Postgres for weight.", topic: "Decisions" });
    expect(second.details.appended).toBe(true);
    expect(second.details.sourceId).toBe(first.details.sourceId);
    // The outline exposes titles, never body text; context_remember titles each
    // note from its first line, so both notes are visible as separate sections.
    const titles = (await call("context_outline", { operation: "outline" })).details.outline.map((e: any) => e.title);
    expect(titles.some((t: string) => t.includes("SQLite"))).toBe(true);
    expect(titles.some((t: string) => t.includes("Postgres"))).toBe(true);
  });

  it("rejects an empty note rather than indexing nothing", async () => {
    const { call } = harness(seed());
    expect((await call("context_remember", { operation: "remember", note: "   " })).isError).toBe(true);
  });

  it("summarizes lazily on first search and reuses short prose for free", async () => {
    let summaryCalls = 0;
    const { call, spawned } = harness(seed(), { retriever: task => {
      if (task.startsWith("You describe sections")) { summaryCalls += 1; return JSON.stringify({ description: "Architecture decision record for storage and transport.", summaries: [{ nodeId: "0001", summary: "Covers storage and transport choices." }] }); }
      const id = JSON.parse(task.slice(task.indexOf("OUTLINE:") + 9))[1].sourceId;
      return JSON.stringify({ found: true, nodes: [{ sourceId: id, nodeId: "0002", why: "names the store" }] });
    } });
    await call("context_index", { operation: "index", path: "adr.md" });
    expect(spawned).toEqual([]); // indexing itself costs nothing

    await call("context_search", { operation: "search", query: "which database" });
    expect(summaryCalls).toBe(1);
    const outline = (await call("context_outline", { operation: "outline" })).details.outline;
    expect(outline.find((e: any) => e.nodeId === "0001").summary).toBe("Covers storage and transport choices.");
    // Short leaves reuse their own text, so they never reached the model.
    expect(outline.find((e: any) => e.nodeId === "0002").summary).toContain("SQLite");

    await call("context_search", { operation: "search", query: "which transport" });
    expect(summaryCalls).toBe(1); // cached, not recomputed
  });

  it("still retrieves from titles when summarization fails", async () => {
    const { call } = harness(seed(), { retriever: task => {
      if (task.startsWith("You describe sections")) return "the model refused";
      const id = JSON.parse(task.slice(task.indexOf("OUTLINE:") + 9))[1].sourceId;
      return JSON.stringify({ found: true, nodes: [{ sourceId: id, nodeId: "0002", why: "title match" }] });
    } });
    await call("context_index", { operation: "index", path: "adr.md" });
    const { details } = await call("context_search", { operation: "search", query: "which database" });
    expect(details.status).toBe("ok");
    expect(details.evidence[0].excerpt).toContain("SQLite");
  });

  it("re-indexes a stale file in place and drops its summaries", async () => {
    const cwd = seed();
    const { call } = harness(cwd, { retriever: task => task.startsWith("You describe sections")
      ? JSON.stringify({ summaries: [{ nodeId: "0001", summary: "old" }] })
      : JSON.stringify({ found: true, nodes: [{ sourceId: JSON.parse(task.slice(task.indexOf("OUTLINE:") + 9))[1].sourceId, nodeId: "0002", why: "storage" }] }) });
    const indexed = await call("context_index", { operation: "index", path: "adr.md" });
    await call("context_search", { operation: "search", query: "anything" });
    writeFileSync(join(cwd, "adr.md"), "# Decisions\n## Storage\nWe migrated to Postgres.\n");
    expect((await call("context_inspect", { operation: "inspect" })).details[0].state).toBe("stale");

    const reindexed = await call("context_reindex", { operation: "reindex", sourceId: indexed.details.id });
    expect(reindexed.details.id).toBe(indexed.details.id);
    expect((await call("context_inspect", { operation: "inspect" })).details[0].state).toBe("current");
    expect((await call("context_outline", { operation: "outline" })).details.outline.find((e: any) => e.nodeId === "0001").summary).not.toBe("old");
    // New content is reachable through retrieval, which is where body text lives.
    const search = await call("context_search", { operation: "search", query: "storage" });
    expect(search.details.evidence[0].excerpt).toContain("Postgres");
  });

  it("refuses to re-index a note that has no backing file", async () => {
    const { call } = harness(seed());
    const note = await call("context_remember", { operation: "remember", note: "inline only" });
    const output = await call("context_reindex", { operation: "reindex", sourceId: note.details.sourceId });
    expect(output.isError).toBe(true);
    expect(output.text).toContain("no backing file");
  });

  it("describes each source so the retriever can choose between them", async () => {
    const { call, spawned } = harness(seed(), { retriever: task => task.startsWith("You describe sections")
      ? JSON.stringify({ description: "Architecture decisions for storage and transport.", summaries: [] })
      : JSON.stringify({ found: false, nodes: [], note: "nothing" }) });
    await call("context_index", { operation: "index", path: "adr.md" });
    await call("context_search", { operation: "search", query: "which database" });

    const { sources } = (await call("context_outline", { operation: "outline" })).details;
    expect(sources[0]).toMatchObject({ name: "adr.md", description: "Architecture decisions for storage and transport.", sections: 1 });
    // The retriever is shown the source cards, not just the section list.
    expect(spawned.find((s: any) => !s.task.startsWith("You describe sections")).task).toContain("Architecture decisions for storage");
  });

  it("keeps a description out of context_inspect payloads but shows source text nowhere", async () => {
    const { call } = harness(seed());
    await call("context_index", { operation: "index", path: "adr.md" });
    const [row] = (await call("context_inspect", { operation: "inspect" })).details;
    expect(row.raw).toBeUndefined();
    expect(row.tree).toBeUndefined();
    expect(JSON.stringify(row)).not.toContain("SQLite");
  });

  it("batches a large source and keeps the batches that succeed", async () => {
    const cwd = mkdtempSync(join(tmpdir(), "swarm-context-"));
    const sections = Array.from({ length: 60 }, (_, i) => "## Section " + i + "\n" + "prose ".repeat(200)).join("\n");
    writeFileSync(join(cwd, "big.md"), "# Big\n" + sections + "\n");
    let batch = 0;
    const { call, spawned } = harness(cwd, { retriever: task => {
      if (!task.startsWith("You describe sections")) return JSON.stringify({ found: false, nodes: [] });
      batch += 1;
      if (batch === 2) return "model overran and refused";
      const ids = [...task.matchAll(/"nodeId":"(\d+)"/g)].map(m => m[1]);
      return JSON.stringify({ summaries: ids.map(id => ({ nodeId: id, summary: "summary for " + id })) });
    } });
    await call("context_index", { operation: "index", path: "big.md" });
    await call("context_search", { operation: "search", query: "anything" });

    expect(spawned.filter((s: any) => s.task.startsWith("You describe sections")).length).toBeGreaterThan(1);
    const { outline, next_steps } = (await call("context_outline", { operation: "outline" })).details;
    const summarized = outline.filter((e: any) => e.summary !== undefined).length;
    expect(summarized).toBeGreaterThan(0);
    expect(summarized).toBeLessThan(outline.length);
    // The gap is declared rather than hidden.
    expect(next_steps.options.join(" ")).toContain("no summary yet");
  });
});
