import { afterEach, describe, expect, it } from "vitest";
import {
  handleRunningWorkInput,
  isRunningWorkEnter,
  safeInspectionText,
  moveRunningWorkSelection,
  removeRunningWork,
  runningWorkExpanded,
  runningWorkSelection,
  runningWorkSnapshot,
  selectedRunningWork,
  setRunningWorkExpanded,
  setRunningWork,
  visibleRunningWork,
  runningWorkListLabel,
  runningWorkFooterHints,
  browsableRunningWork,
  RECENT_WORK_MS,
  scopeRunningWorkToSession,
} from "../../lib/ui/running-work.ts";

describe("running work footer interaction", () => {
  it("shows compact sanitized bash previews while preserving subagent labels", () => {
    expect(runningWorkListLabel({ id: "b", kind: "bash", label: "ignored", status: "running", startedAt: 0, detail: "  echo   hello\nworld  " })).toBe("bash · echo hello world");
    expect(runningWorkListLabel({ id: "b", kind: "bash", label: "ignored", status: "running", startedAt: 0, detail: "x".repeat(50) })).toBe(`bash · ${"x".repeat(35)}…`);
    expect(runningWorkListLabel({ id: "a", kind: "subagent", label: "Research", status: "running", startedAt: 0, detail: "research task" })).toBe("subagent Research");
    expect(runningWorkListLabel({ id: "b", kind: "bash", label: "ignored", status: "running", startedAt: 0, detail: "echo\x1b]8;;https://secret.test\x07link\x1b]8;;\x07" })).toBe("bash · echolink");
  });

  it("documents navigation, inspection, dismissal, and detach keys", () => {
    expect(runningWorkFooterHints()).toContain("↑/↓ select");
    expect(runningWorkFooterHints()).not.toContain("Ctrl+B");
  });

  it("advertises Ctrl+B only when a background detach action is registered", () => {
    const key = Symbol.for("pi-swarm-background-bash-detach");
    const globals = globalThis as any;
    globals[key] = () => true;
    try {
      expect(runningWorkFooterHints()).toContain("Ctrl+B detach/wait");
    } finally {
      delete globals[key];
    }
  });
  afterEach(() => {
    setRunningWorkExpanded(false);
    for (const item of runningWorkSnapshot()) removeRunningWork(item.id);
  });

  it("collapses when the user resumes editing the prompt", () => {
    setRunningWork({ id: "bash-1", kind: "bash", label: "a long command", status: "running", startedAt: Date.now(), detail: "a long command" });
    setRunningWorkExpanded(true);

    expect(handleRunningWorkInput("x", { editor: { getText: () => "" } })).toBe(false);
    expect(runningWorkExpanded()).toBe(false);
  });

  it("keeps only the latest six commands in the navigable drawer", () => {
    const items = Array.from({ length: 8 }, (_, index) => ({
      id: `bash-${index}`,
      kind: "bash" as const,
      label: `command-${index}`,
      status: "running" as const,
      startedAt: index,
      detail: `command-${index}`,
    }));

    expect(visibleRunningWork(items).map((item) => item.id)).toEqual([
      "bash-2", "bash-3", "bash-4", "bash-5", "bash-6", "bash-7",
    ]);
  });

  it("counts only recent completed work but leaves long-running work visible", () => {
    const now = Date.now();
    setRunningWork({ id: "old", kind: "bash", label: "old", status: "completed", startedAt: 1, endedAt: now - RECENT_WORK_MS, detail: "old" });
    setRunningWork({ id: "recent", kind: "bash", label: "recent", status: "completed", startedAt: 2, endedAt: now - 1000, detail: "recent" });
    setRunningWork({ id: "live", kind: "bash", label: "live", status: "running", startedAt: 3, detail: "live" });
    expect(browsableRunningWork(now).map(item => item.id)).toEqual(["recent", "live"]);
  });

  it("retains completed work on same-session reload, clears it on session switch", () => {
    scopeRunningWorkToSession("work-session-one");
    setRunningWork({ id: "done", kind: "bash", label: "done", status: "completed", startedAt: Date.now(), endedAt: Date.now(), detail: "echo done" });
    scopeRunningWorkToSession("work-session-one");
    expect(browsableRunningWork().map(item => item.id)).toContain("done");
    scopeRunningWorkToSession("work-session-two");
    expect(browsableRunningWork()).toEqual([]);
  });

  it("shows live output for a running bash entry on Enter", () => {
    let live = "line 1";
    setRunningWork({ id: "bash-live", kind: "bash", label: "bash", status: "running", startedAt: Date.now(), detail: "while true; do date; done", readOutput: () => live });
    setRunningWorkExpanded(true);
    const opened: string[] = [];
    const ctx = { ui: { editor: (_title: string, body: string) => { opened.push(body); return Promise.resolve(undefined); } } };

    expect(handleRunningWorkInput("\r", ctx)).toBe(true);
    expect(opened[0]).toContain("line 1");
    expect(opened[0]).toContain("while true; do date; done");

    live = "line 1\nline 2";
    setRunningWorkExpanded(true);
    handleRunningWorkInput("\r", ctx);
    expect(opened[1]).toContain("line 2");
  });

  it("shows a placeholder instead of a blank view when there is no output yet", () => {
    setRunningWork({ id: "bash-empty", kind: "bash", label: "bash", status: "running", startedAt: Date.now(), detail: "sleep 60", readOutput: () => "" });
    setRunningWorkExpanded(true);
    const opened: string[] = [];
    handleRunningWorkInput("\r", { ui: { editor: (_t: string, body: string) => { opened.push(body); return Promise.resolve(undefined); } } });
    expect(opened[0]).toContain("(no output yet)");
  });

  it("clamps a stale selection after items are removed instead of losing Enter", () => {
    for (let index = 0; index < 3; index++) {
      setRunningWork({ id: `bash-${index}`, kind: "bash", label: `command-${index}`, status: "running", startedAt: index, detail: `command-${index}` });
    }
    setRunningWorkExpanded(true);
    moveRunningWorkSelection(2);
    expect(runningWorkSelection()).toBe(2);

    removeRunningWork("bash-1");
    removeRunningWork("bash-2");
    expect(runningWorkSelection()).toBe(0);
    expect(selectedRunningWork()?.id).toBe("bash-0");

    const opened: string[] = [];
    expect(handleRunningWorkInput("\r", { ui: { notify: (body: string) => opened.push(body) } })).toBe(true);
    expect(opened[0]).toContain("command-0");
  });

  it("Enter on an empty drawer is a no-op without crashing", () => {
    expect(selectedRunningWork()).toBeUndefined();
    expect(handleRunningWorkInput("\r", {})).toBe(false);
  });

  it("accepts Kitty CSI-u Enter sequences used by enhanced terminal input", () => {
    expect(isRunningWorkEnter("\x1b[13u")).toBe(true);
    expect(isRunningWorkEnter("\x1b[13;2u")).toBe(true);
    expect(isRunningWorkEnter("x")).toBe(false);
  });

  it("falls back cleanly when a child transcript was removed before Enter", () => {
    setRunningWork({
      id: "agent-missing-transcript",
      kind: "subagent",
      label: "Agent missing-transcript",
      status: "completed",
      startedAt: Date.now(),
      detail: "finished child",
      transcriptPath: "/definitely/missing/child.jsonl",
      output: "final buffered result",
    });
    setRunningWorkExpanded(true);
    const opened: string[] = [];
    handleRunningWorkInput("\r", { ui: { editor: (_title: string, body: string) => { opened.push(body); return Promise.resolve(undefined); } } });
    expect(opened[0]).toContain("final buffered result");
    expect(opened[0]).not.toContain("Unable to read child conversation");
    expect(opened[0]).not.toContain("ENOENT");
  });

  it("wraps hostile long output before handing it to the editor", () => {
    const longLine = "x".repeat(500);
    expect(safeInspectionText(longLine, 181).split("\n").every(line => line.length <= 120)).toBe(true);
    expect(safeInspectionText("\x1b]8;;https://example.test\x07linked\x1b]8;;\x07", 80)).toBe("linked");
  });

  it("wraps by display cells without splitting a surrogate pair", () => {
    const wrapped = safeInspectionText("🙂".repeat(100), 10).split("\n");
    // Five double-width emoji fill a ten-cell budget, and no chunk may end in a lone surrogate.
    expect(wrapped.every(line => [...line].length === 5)).toBe(true);
    expect(wrapped.every(line => !/[\uD800-\uDBFF]$/.test(line))).toBe(true);
  });

  it("survives a readOutput callback that throws after its buffer is disposed", () => {
    setRunningWork({ id: "bash-disposed", kind: "bash", label: "Bash", status: "running", startedAt: Date.now(), detail: "sleep 1", output: "buffered tail", readOutput: () => { throw new Error("buffer disposed"); } });
    setRunningWorkExpanded(true);
    const opened: string[] = [];
    expect(() => handleRunningWorkInput("\r", { ui: { editor: (_t: string, body: string) => { opened.push(body); return Promise.resolve(undefined); } } })).not.toThrow();
    expect(opened[0]).toContain("buffered tail");
  });

  it("catches editor failures instead of creating an unhandled rejection", async () => {
    setRunningWork({ id: "agent-editor-fails", kind: "subagent", label: "Agent", status: "running", startedAt: Date.now(), detail: "task", output: "output" });
    setRunningWorkExpanded(true);
    const notices: string[] = [];
    expect(() => handleRunningWorkInput("\r", { ui: { editor: () => Promise.reject(new Error("view closed")), notify: (text: string) => notices.push(text) } })).not.toThrow();
    await Promise.resolve();
    await Promise.resolve();
    expect(notices).toContain("Unable to open work inspection");
  });
});
