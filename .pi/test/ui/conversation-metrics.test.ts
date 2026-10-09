import { describe, expect, it } from "vitest";
import { clampFooterRow, currentModelLabel, footerIdentityLine, footerMetricsLine, footerVisibleWidth, runningWorkHint, wrapFooterText } from "../../../extensions/conversation-metrics/extension.ts";

describe("conversation metrics footer", () => {
  it("advertises only active work with a compact Down hint", () => {
    expect(runningWorkHint([])).toBeUndefined();
    expect(runningWorkHint([{ status: "completed" }, { status: "failed" }])).toBeUndefined();
    expect(runningWorkHint([{ status: "running" }, { status: "queued" }, { status: "completed" }])).toBe("↓ Tasks running (2)");
  });
  it("reports provider and model", () => {
    expect(currentModelLabel({ model: { provider: "openai", id: "gpt-test" } })).toBe("openai/gpt-test");
  });

  it("wraps long footer content to the terminal width", () => {
    const lines = wrapFooterText("provider/model · output tokens · running", 12);
    expect(lines.length).toBeGreaterThan(1);
    expect(lines.every((line) => line.length <= 12)).toBe(true);
  });

  it("builds a two-row identity and metrics layout", () => {
    expect(footerIdentityLine("running", "openai/gpt-test")).toBe("●  model openai/gpt-test");
    expect(footerMetricsLine("1m 02s", 1234, ["Autogen 2/5"])).toContain("↓ 1,234 tok");
    // The Ctrl+B affordance is rendered by the in-flight bash row, not the footer.
    expect(footerMetricsLine("1m 02s", 1234, ["Autogen 2/5"])).not.toContain("Ctrl+B");
  });

  it("uses a clear fallback when model context is unavailable", () => {
    expect(footerIdentityLine("idle")).toContain("model unavailable");
  });

  it("measures visible width ignoring ANSI styling", () => {
    expect(footerVisibleWidth("\x1b[32m●\x1b[0m  model x")).toBe("●  model x".length);
    expect(footerVisibleWidth("汉字")).toBe(4);
    // Combining marks and ZWJ sequences render on the preceding cell.
    expect(footerVisibleWidth("e\u0301")).toBe(1);
    expect(footerVisibleWidth("👩\u200d💻")).toBe(4);
    expect(clampFooterRow("e\u0301abc", 3)).toBe("e\u0301ab");
  });

  it("clamps styled rows to the terminal width without splitting escapes", () => {
    const styled = "\x1b[32m● running some long styled footer row\x1b[0m";
    const clamped = clampFooterRow(styled, 10);
    expect(footerVisibleWidth(clamped)).toBeLessThanOrEqual(10);
    expect(clamped.startsWith("\x1b[32m")).toBe(true);
    expect(clamped.endsWith("\x1b[0m")).toBe(true);
  });

  it("clamps wide characters as two cells and never throws on tiny widths", () => {
    expect(footerVisibleWidth(clampFooterRow("汉字汉字汉字", 5))).toBeLessThanOrEqual(5);
    expect(clampFooterRow("anything", 0)).toBe("");
    expect(clampFooterRow("anything", -3)).toBe("");
  });

  it("leaves rows that already fit untouched", () => {
    expect(clampFooterRow("short", 80)).toBe("short");
  });
});
