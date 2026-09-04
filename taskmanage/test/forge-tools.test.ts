import { describe, expect, it } from "vitest";
import { mkdtemp, readFile, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import forgeToolsExtension from "../../.pi/extensions/forge-tools.ts";

describe("Forge tool adapters", () => {
  async function tools() {
    const cwd = await mkdtemp(join(tmpdir(), "forge-tools-"));
    const registered = new Map<string, any>();
    forgeToolsExtension({ getCwd: () => cwd, registerTool: (tool: any) => registered.set(tool.name, tool) });
    return { cwd, registered };
  }
  it("keeps file operations inside the workspace and renders line numbers", async () => {
    const { cwd, registered } = await tools();
    await writeFile(join(cwd, "a.txt"), "one\ntwo\n");
    const result = await registered.get("forge_read").execute("1", { path: "a.txt", offset: 2 }, new AbortController().signal);
    expect(result.isError).toBeUndefined();
    expect(result.content[0].text).toContain("2\ttwo");
    const escaped = await registered.get("forge_read").execute("2", { path: "../a.txt" }, new AbortController().signal);
    expect(escaped.isError).toBe(true);
  });
  it("refuses accidental overwrite and supports exact edits", async () => {
    const { cwd, registered } = await tools();
    await writeFile(join(cwd, "a.txt"), "old\n");
    const refused = await registered.get("forge_write").execute("1", { path: "a.txt", content: "new" }, new AbortController().signal);
    expect(refused.isError).toBe(true);
    const edited = await registered.get("forge_edit").execute("2", { path: "a.txt", old_string: "old", new_string: "new" }, new AbortController().signal);
    expect(edited.isError).toBeUndefined();
    expect(await readFile(join(cwd, "a.txt"), "utf8")).toBe("new\n");
  });
  it("cancels recursive search", async () => {
    const { registered } = await tools();
    const controller = new AbortController(); controller.abort();
    const result = await registered.get("forge_find").execute("1", {}, controller.signal);
    expect(result.isError).toBe(true);
    expect(result.details.code).toBe("cancelled");
  });
});
