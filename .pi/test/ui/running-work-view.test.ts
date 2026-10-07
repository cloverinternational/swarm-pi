import { afterEach, describe, expect, it } from "vitest";
import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { visibleWidth } from "@earendil-works/pi-tui";
import { RunningWorkView, openRunningWorkView, parseSubagentConversation, readSubagentConversation } from "../../lib/ui/running-work-view.ts";
import { handleRunningWorkInput, removeRunningWork, runningWorkSnapshot, setRunningWork } from "../../lib/ui/running-work.ts";

const theme = { fg: (_: string, text: string) => text, bg: (_: string, text: string) => text, bold: (text: string) => text };
const tui = { terminal: { rows: 32 }, requestRender: () => {} };
const agent = { id: "a", kind: "subagent" as const, label: "Agent a", status: "running" as const, detail: "Summarize changes", startedAt: 1 };
afterEach(() => { for (const item of runningWorkSnapshot()) removeRunningWork(item.id); });

describe("focused work browser", () => {
  it("projects conversation roles and tools while omitting system/thinking and malformed records", () => {
    const lines = [
      JSON.stringify({ type: "message", message: { role: "system", content: [{ type: "text", text: "private system" }] } }),
      JSON.stringify({ type: "message", id: "1", message: { role: "user", content: [{ type: "text", text: "Find tests" }] } }),
      JSON.stringify({ type: "message", id: "2", message: { role: "assistant", content: [{ type: "thinking", thinking: "internal" }, { type: "toolCall", name: "Bash" }] } }),
      JSON.stringify({ type: "message", id: "3", message: { role: "toolResult", toolName: "Bash", content: [{ type: "text", text: "2 passed" }] } }),
      "{partial",
    ];
    const result = parseSubagentConversation(lines);
    expect(result.map(row => row.kind)).toEqual(["user", "assistant", "tool"]);
    expect(result[1].title).toContain("Bash");
    expect(JSON.stringify(result)).not.toMatch(/private system|internal|partial/);
    expect(readSubagentConversation("/definitely/missing.jsonl")).toBeUndefined();
    expect(parseSubagentConversation([JSON.stringify({ type: "message", message: { role: "assistant", content: [{ type: "text", text: "x".repeat(9000) }] } })])[0].body.length).toBeLessThan(3100);
  });
  it("reads Pi session entries as a navigable bounded timeline", () => {
    const dir = mkdtempSync(join(tmpdir(), "work-view-"));
    const path = join(dir, "child.jsonl");
    try {
      writeFileSync(path, [
        JSON.stringify({ type: "session", id: "child" }),
        JSON.stringify({ type: "message", id: "user-1", message: { role: "user", content: [{ type: "text", text: "Find files" }] } }),
        JSON.stringify({ type: "message", id: "assistant-1", message: { role: "assistant", content: [{ type: "text", text: "Found three files" }] } }),
        "{incomplete",
      ].join("\n"));
      const steps = readSubagentConversation(path)!;
      expect(steps.map(row => row.id)).toEqual(["user-1", "assistant-1"]);
      setRunningWork({ ...agent, transcriptPath: path });
      const view = new RunningWorkView(tui, theme, () => {});
      view.handleInput("\r");
      expect(view.render(60).join("\n")).toContain("Find files");
      view.handleInput("\x1b[B");
      expect(view.render(60).join("\n")).toContain("Found three files");
      view.dispose();
    } finally { rmSync(dir, { recursive: true, force: true }); }
  });
  it("replaces the input through Pi custom UI, browses steps, and closes through done", async () => {
    setRunningWork(agent);
    let view: RunningWorkView | undefined;
    let doneCount = 0;
    let options: unknown;
    const ctx = { mode: "tui", ui: { custom: (factory: any, opts: unknown) => {
      options = opts;
      view = factory(tui, theme, {}, () => { doneCount++; });
      return Promise.resolve();
    } } };
    expect(handleRunningWorkInput("\x1b[B", { ...ctx, editor: { getText: () => "" } })).toBe(true);
    expect(options).toBeUndefined(); // non-overlay: Pi replaces the editor
    const compact = view?.render(42) ?? [];
    expect(compact.join("\n")).toContain("Running work");
    expect(compact.every(row => visibleWidth(row) <= 42)).toBe(true);
    view?.handleInput("\r");
    const detail = view?.render(42) ?? [];
    expect(detail.join("\n")).toContain("Conversation unavailable");
    expect(detail.every(row => visibleWidth(row) <= 42)).toBe(true);
    view?.handleInput("\x1b");
    expect(doneCount).toBe(0);
    view?.handleInput("\x1b");
    expect(doneCount).toBe(1);
    view?.dispose();
    await Promise.resolve();
  });
  it("does not claim a custom surface where Pi cannot show one", () => {
    setRunningWork(agent);
    expect(openRunningWorkView({ mode: "print", ui: { custom: () => { throw Error("should not run"); } } })).toBe(false);
  });
  it("opens a completed item using Pi's normalized Down sequence", async () => {
    setRunningWork({ ...agent, kind: "bash", status: "completed", output: "finished output" });
    let opened = 0;
    const ctx = { mode: "tui", editor: { getText: () => "" }, ui: { custom: (factory: any) => {
      opened++;
      const view = factory(tui, theme, {}, () => {});
      expect(view.render(60).join("\n")).toContain("completed");
      view.dispose();
      return Promise.resolve();
    } } };
    expect(handleRunningWorkInput("\x1b[1;1B", ctx)).toBe(true);
    expect(opened).toBe(1);
    await Promise.resolve();
  });
  it("leaves Down to the editor when text exists or the work list is empty", () => {
    const ctx = { mode: "tui", editor: { getText: () => "draft" }, ui: { custom: () => { throw Error("should not open"); } } };
    setRunningWork({ ...agent, status: "completed" });
    expect(handleRunningWorkInput("\x1b[1;1B", ctx)).toBe(false);
    removeRunningWork(agent.id);
    ctx.editor.getText = () => "";
    expect(handleRunningWorkInput("\x1b[1;1B", ctx)).toBe(false);
  });
});
