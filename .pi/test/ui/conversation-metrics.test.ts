import { describe, expect, it } from "vitest";
import { clampFooterRow, currentModelLabel, footerIdentityLine, footerMetricsLine, footerVisibleWidth, wrapFooterText } from "../../extensions/50-ui/conversation-metrics.ts";

describe("conversation metrics footer", () => {
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
    expect(footerMetricsLine("1m 02s", 1234, ["Autogen 2/5"], "Ctrl+B background Bash")).toContain("↓ 1,234 tok");
  });

  it("uses a clear fallback when model context is unavailable", () => {
    expect(footerIdentityLine("idle")).toContain("model unavailable");
  });

  it("measures visible width ignoring ANSI styling", () => {
    expect(footerVisibleWidth("\x1b[32m●\x1b[0m  model x")).toBe("●  model x".length);
    expect(footerVisibleWidth("汉字")).toBe(4);
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
