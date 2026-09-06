import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

describe("Pi hooks extension", () => {
  it("does not re-register Pi built-in tools", () => {
    const source = readFileSync(
      fileURLToPath(new URL("../../.pi/extensions/hooks.ts", import.meta.url)),
      "utf8",
    );

    expect(source).not.toContain("registerTool");
    expect(source).not.toMatch(/create(?:Read|Bash|Edit|Write|Grep|Find|Ls|PowerShell)ToolDefinition/);
    expect(source).toContain('pi.on("session_start"');
    expect(source).toContain('pi.registerShortcut?.("ctrl+h"');
    expect(source).toContain('pi.registerCommand?.("hooks"');
  });
});
