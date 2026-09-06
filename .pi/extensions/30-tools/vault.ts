import { vaultAdd, vaultList, vaultRemove, type VaultRuntime } from "../../lib/tools/swarm-vault-tools.ts";
import { withDefaultToolRenderer } from "../../lib/runtime/swarm-tool-renderer.ts";

type UI = { input?: (title: string, initial?: string) => Promise<string | undefined>; notify?: (message: string, type?: string) => void };
type Pi = { registerCommand?: (name: string, spec: { description: string; handler: (args: string, ctx: { ui?: UI }) => Promise<void> }) => void; registerTool?: (tool: unknown) => void };

const parameters = {
  type: "object",
  properties: {
    action: { type: "string", enum: ["add", "list", "remove"] },
    id: { type: "string", description: "Credential identifier." },
    kind: { type: "string", description: "Credential kind, such as password, api_key, or ssh_key." },
    secret: { type: "string", description: "Credential value. Stored transparently in the global local vault." },
    name: { type: "string" },
    target: { type: "string", description: "Environment variable or file target used by vault_exec." },
  },
  required: ["action"],
};

const text = (value: unknown) => ({ content: [{ type: "text", text: JSON.stringify(value) }], details: value });
const toolRegistrations = new WeakSet<object>();

export function registerVaultTool(pi: Pi, vault?: VaultRuntime): void {
  if (toolRegistrations.has(pi as object)) return;
  toolRegistrations.add(pi as object);
  pi.registerTool?.(withDefaultToolRenderer({
    name: "vault",
    label: "Credential vault",
    description: "Store, list, or remove global credentials in the transparent local vault. Secrets are not encrypted; the user assumes all risk.",
    parameters,
    async execute(_id: string, input: any) {
      if (input?.action === "add") return text(await vaultAdd({ ...input, scope: "global" }, vault));
      if (input?.action === "list") return text(await vaultList({ kind: input.kind }, vault));
      if (input?.action === "remove") return text(await vaultRemove(input, vault));
      return text({ success: false, error: "action must be add, list, or remove" });
    },
  }));
}

export function registerVault(pi: Pi, vault?: VaultRuntime): void {
  pi.registerCommand?.("vault", {
    description: "Add, list, or remove credentials in the transparent global vault",
    handler: async (args, ctx) => {
      const [action = "list", id] = args.trim().split(/\s+/, 2);
      try {
        if (action === "list") {
          const result = await vaultList({}, vault);
          ctx.ui?.notify?.(result.credentials.length ? result.credentials.map((c: any) => `${c.id} · ${c.kind}`).join("\n") : "No credentials stored.", "info");
          return;
        }
        if (action === "remove" && id) {
          const result = await vaultRemove({ id }, vault);
          ctx.ui?.notify?.(result.success ? `Removed credential ${id}.` : result.error, result.success ? "info" : "error");
          return;
        }
        if (action !== "add" || !ctx.ui?.input) throw new Error("usage: /vault add [id] | /vault list | /vault remove <id>");
        const credentialId = id || (await ctx.ui.input("Credential id"))?.trim();
        const kind = (await ctx.ui.input("Credential kind", "password"))?.trim();
        const secret = await ctx.ui.input("Credential value (stored transparently; user assumes all risk)");
        if (!credentialId || !kind || secret === undefined) throw new Error("credential id, kind, and value are required");
        const result = await vaultAdd({ id: credentialId, kind, secret, scope: "global" }, vault);
        ctx.ui?.notify?.(result.success ? `Stored global credential ${credentialId}.` : result.error, result.success ? "info" : "error");
      } catch (error) { ctx.ui?.notify?.(error instanceof Error ? error.message : String(error), "error"); }
    },
  });
}

export default function vaultExtension(pi: Pi): void { registerVault(pi); }
