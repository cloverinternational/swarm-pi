import { describe, expect, it } from "vitest";
import { mkdtemp, rm } from "node:fs/promises";
import { join } from "node:path";
import { tmpdir } from "node:os";
import { registerVault, registerVaultTool } from "../../.pi/extensions/vault.ts";

describe("vault extension", () => {
  it("registers a unified tool and stores global credentials", async () => {
    const root = await mkdtemp(join(tmpdir(), "pi-vault-extension-"));
    try {
      const tools: any[] = []; let command: any;
      const pi = { registerTool: (tool: any) => tools.push(tool), registerCommand: (_name: string, spec: any) => { command = spec; } };
      registerVaultTool(pi, { path: join(root, "credentials.json") });
      registerVault(pi, { path: join(root, "credentials.json") });
      expect(tools.map(tool => tool.name)).toEqual(["vault"]);
      const added = await tools[0].execute("call", { action: "add", id: "demo", kind: "password", secret: "plain-value" });
      expect(added.details).toMatchObject({ success: true, scope: "global" });
      const listed = await tools[0].execute("call", { action: "list" });
      expect(JSON.stringify(listed)).not.toContain("plain-value");
      expect(command.description).toContain("transparent");
    } finally { await rm(root, { recursive: true, force: true }); }
  });
});
