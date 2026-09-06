import { afterEach, describe, expect, it } from "vitest";
import { mkdtempSync, readFileSync, rmSync, writeFileSync, existsSync } from "node:fs";
import { join } from "node:path";
import { tmpdir } from "node:os";
import { applyPatch, parsePatch, undoFile } from "../../lib/tools/swarm-apply-patch.ts";
import { readImage } from "../../lib/tools/swarm-read-image.ts";
import extension from "../../extensions/30-tools/swarm-fs-tools.ts";
import { PERMISSIVE_PARAMETERS, loadSwarmToolSurface, overlaySwarmToolSchemas } from "../../lib/runtime/swarm-tool-surface.ts";

const roots: string[] = [];
const root = () => { const p = mkdtempSync(join(tmpdir(), "swarm-patch-")); roots.push(p); return p; };
afterEach(() => { while (roots.length) rmSync(roots.pop()!, { recursive: true, force: true }); });

describe("Swarm apply_patch parity", () => {
  it("parses and applies add, update, delete, and move", async () => {
    const ws = root();
    writeFileSync(join(ws, "update.txt"), "one\ntwo\n");
    writeFileSync(join(ws, "delete.txt"), "bye\n");
    writeFileSync(join(ws, "move.txt"), "old\n");
    const patch = `*** Begin Patch
*** Add File: add.txt
+added
*** Update File: update.txt
 one
-two
+changed
*** Delete File: delete.txt
*** Update File: move.txt
*** Move to: moved.txt
-old
+new
*** End Patch`;
    expect(parsePatch(patch)).toHaveLength(4);
    await expect(applyPatch(patch, { workspacePath: ws })).resolves.toBe("Success. Updated the following files:\nA add.txt\nM update.txt\nM moved.txt\nD delete.txt\n");
    expect(readFileSync(join(ws, "add.txt"), "utf8")).toBe("added\n");
    expect(readFileSync(join(ws, "update.txt"), "utf8")).toBe("one\nchanged\n");
    expect(existsSync(join(ws, "delete.txt"))).toBe(false);
    expect(existsSync(join(ws, "move.txt"))).toBe(false);
    expect(readFileSync(join(ws, "moved.txt"), "utf8")).toBe("new\n");
  });

  it("uses @@ anchors to disambiguate repeated context", async () => {
    const ws = root(), file = join(ws, "a.txt");
    writeFileSync(file, "function one() {\n  same();\n}\nfunction two() {\n  same();\n}\n");
    await applyPatch(`*** Begin Patch\n*** Update File: a.txt\n@@ function two() {\n-  same();\n+  changed();\n*** End Patch`, { workspacePath: ws });
    expect(readFileSync(file, "utf8")).toContain("function one() {\n  same();");
    expect(readFileSync(file, "utf8")).toContain("function two() {\n  changed();");
  });

  it("applies anchored repeated context atomically across multiple files", async () => {
    const ws = root();
    const first = join(ws, "first.ts"), second = join(ws, "second.ts");
    const source = "function keep() {\n  same();\n}\nfunction target() {\n  same();\n}\n";
    writeFileSync(first, source);
    writeFileSync(second, source);
    const patch = `*** Begin Patch
*** Update File: first.ts
@@ function target() {
-  same();
+  changed();
*** Update File: second.ts
@@ function target() {
-  same();
+  changed();
*** End Patch`;

    await expect(applyPatch(patch, { workspacePath: ws })).resolves.toContain("M first.ts\nM second.ts\n");
    expect(readFileSync(first, "utf8")).toBe("function keep() {\n  same();\n}\nfunction target() {\n  changed();\n}\n");
    expect(readFileSync(second, "utf8")).toBe("function keep() {\n  same();\n}\nfunction target() {\n  changed();\n}\n");
  });

  it("reports ambiguous and missing context with Swarm text", async () => {
    const ws = root(); writeFileSync(join(ws, "a.txt"), "same\nx\nsame\n");
    await expect(applyPatch("*** Begin Patch\n*** Update File: a.txt\n-same\n+new\n*** End Patch", { workspacePath: ws }))
      .rejects.toThrow("ambiguous context at lines 1, 3");
    await expect(applyPatch("*** Begin Patch\n*** Update File: a.txt\n-missing\n+new\n*** End Patch", { workspacePath: ws }))
      .rejects.toThrow("update a.txt: context not found:\nmissing");
  });

  it("preflights atomically when the second operation fails", async () => {
    const ws = root(); writeFileSync(join(ws, "a.txt"), "old\n");
    await expect(applyPatch("*** Begin Patch\n*** Update File: a.txt\n-old\n+new\n*** Delete File: absent.txt\n*** End Patch", { workspacePath: ws }))
      .rejects.toThrow("absent.txt: file not found");
    expect(readFileSync(join(ws, "a.txt"), "utf8")).toBe("old\n");
  });

  it("Undo restores prior content and consumes the snapshot", async () => {
    const ws = root(), file = join(ws, "a.txt"); writeFileSync(file, "old\n");
    await applyPatch("*** Begin Patch\n*** Update File: a.txt\n-old\n+new\n*** End Patch", { workspacePath: ws });
    await expect(undoFile(file, ws)).resolves.toContain(`Successfully reverted ${file}`);
    expect(readFileSync(file, "utf8")).toBe("old\n");
    await expect(undoFile(file, ws)).rejects.toThrow(`no snapshot found for ${file}`);
  });

  it("Read rejects text with Swarm's exact result message", () => {
    const ws = root(), file = join(ws, "a.txt"); writeFileSync(file, "text");
    expect(readImage(file, ws)).toEqual([{ type: "text", text: `ERROR: Read handles image files only (.gif, .jpeg, .jpg, .png, .webp). For text use the shell, e.g. \`sed -n '1,200p' ${file}\` to view a slice or \`rg PATTERN ${file}\` to search it.` }]);
  });

  it("registers byte-identical fixture descriptions and parameters", () => {
    const tools: any[] = []; extension({ registerTool: (tool: any) => tools.push(tool), getCwd: () => process.cwd() });
    for (const name of ["apply_patch", "Undo", "Read"]) {
      const actual = tools.find(t => t.name === name), expected = loadSwarmToolSurface().get(name)!;
      expect(actual.description).toBe(expected.description);
      expect(actual.parameters).toEqual(PERMISSIVE_PARAMETERS);
      expect(JSON.stringify(overlaySwarmToolSchemas({ tools: [{ type: "function", function: { name, description: actual.description, parameters: actual.parameters } }] })!.tools[0].function.parameters)).toBe(JSON.stringify(expected.parameters));
    }
  });
});
