import { describe, expect, it } from "vitest";
import { currentModelLabel, footerIdentityLine, footerMetricsLine, wrapFooterText } from "../../extensions/50-ui/conversation-metrics.ts";

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
});
