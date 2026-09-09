import { afterEach, describe, expect, it } from "vitest";
import {
  handleRunningWorkInput,
  runningWorkExpanded,
  setRunningWorkExpanded,
  setRunningWork,
  visibleRunningWork,
} from "../../lib/ui/running-work.ts";

describe("running work footer interaction", () => {
  afterEach(() => setRunningWorkExpanded(false));

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
});
