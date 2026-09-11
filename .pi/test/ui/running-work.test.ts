import { afterEach, describe, expect, it } from "vitest";
import {
  handleRunningWorkInput,
  moveRunningWorkSelection,
  removeRunningWork,
  runningWorkExpanded,
  runningWorkSelection,
  runningWorkSnapshot,
  selectedRunningWork,
  setRunningWorkExpanded,
  setRunningWork,
  visibleRunningWork,
} from "../../lib/ui/running-work.ts";

describe("running work footer interaction", () => {
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
});
