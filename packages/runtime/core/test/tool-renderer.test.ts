import { describe, expect, it } from "vitest";
import { withDefaultToolRenderer } from "../src/tool-renderer.ts";

describe("default tool renderer", () => {
  it("renders text and expanded details", () => {
    const tool = withDefaultToolRenderer({ name: "generic" });
    expect(tool.renderResult({ content: [{ type: "text", text: "hello" }], details: { count: 2 } }, {}, {}).render(80)).toEqual(["hello"]);
    expect(tool.renderResult({ content: [{ type: "text", text: "hello" }], details: { count: 2 } }, { expanded: true }, {}).render(80).join("\n")).toContain('"count": 2');
  });

  it("preserves explicit renderers and displays errors and partials", () => {
    const explicit = () => ({ render: () => ["custom"] });
    const tool = withDefaultToolRenderer({ name: "generic", renderResult: explicit });
    expect(tool.renderResult).toBe(explicit);
    const generic = withDefaultToolRenderer({ name: "generic" });
    expect(generic.renderResult({ content: [{ type: "text", text: "bad" }], isError: true }, {}, {}).render(80).join("\n")).toContain("bad");
    expect(generic.renderResult({ content: [] }, { isPartial: true }, {}).render(80).join("\n")).toContain("working");
  });

  it("keeps output bounded and renders image parts descriptively", () => {
    const tool = withDefaultToolRenderer({ name: "generic" });
    const rendered = tool.renderResult({ content: [{ type: "image", mimeType: "image/png", data: "..." }, { type: "text", text: "caption" }] }, {}, {}).render(80).join("\n");
    expect(rendered).toContain("[image: image/png]");
    expect(rendered).toContain("caption");
    expect(tool.renderResult({ content: [{ type: "text", text: "x".repeat(25_000) }] }, {}, {}).render(80).join("\n")).toContain("display truncated");
  });
});
