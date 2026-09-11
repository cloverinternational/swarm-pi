import { vaultAdd, vaultGet, vaultList, vaultRemove, type VaultRuntime } from "../../lib/tools/swarm-vault-tools.ts";
import { withDefaultToolRenderer } from "../../../packages/runtime/core/src/tool-renderer.ts";

type UI = { input?: (title: string, initial?: string) => Promise<string | undefined>; notify?: (message: string, type?: string) => void };
type Pi = { registerCommand?: (name: string, spec: { description: string; handler: (args: string, ctx: { ui?: UI }) => Promise<void> }) => void; registerTool?: (tool: unknown) => void };

const parameters = {
  type: "object",
  properties: {
    action: { type: "string", enum: ["add", "list", "get", "remove"], description: "Use get for one credential, list to discover available credentials, add to store a credential, or remove to delete one." },
    id: { type: "string", description: "Credential identifier. Required for get and remove; use the id returned by list." },
    kind: { type: "string", description: "Credential kind, such as password, api_key, or ssh_key." },
    query: { type: "string", description: "Optional case-insensitive search across credential id, name, and kind. Prefer query/list before asking the user to provide a credential." },
    scope: { type: "string", description: "Optional credential scope filter." },
    tags: { type: "array", items: { type: "string" }, description: "Optional tags that every result must contain." },
    limit: { type: "integer", minimum: 1, maximum: 100, default: 20 },
    cursor: { type: "string", description: "Cursor returned by a previous list operation." },
    details: { type: "boolean", default: false, description: "Include nonessential metadata such as tags and allowed hosts." },
    secret: { type: "string", description: "Value to store for this entry." },
    name: { type: "string" },
    target: { type: "string", description: "Optional environment variable or file target associated with this entry." },
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
    description: "Manage stored connection and authentication entries for autonomous work. Use action=list with optional query, kind, scope, or tags to find an entry, action=get to retrieve one, action=add to store one, and action=remove to delete one. Use the selected entry with the tool or service that needs it, and use action=remove only when requested.",
    parameters,
    async execute(_id: string, input: any) {
      if (input?.action === "add") return text(await vaultAdd({ ...input, scope: "global" }, vault));
      if (input?.action === "list" || input?.action === "get") {
        if (input.action === "get" && !input.id) return text({ success: false, error: "id is required for get" });
        if (input.action === "get") return text(await vaultGet(input, vault));
        return text(await vaultList({ ...input }, vault));
      }
      if (input?.action === "remove") return text(await vaultRemove(input, vault));
      return text({ success: false, error: "action must be add, list, get, or remove" });
    },
  }));
}

export function registerVault(pi: Pi, vault?: VaultRuntime): void {
  pi.registerCommand?.("vault", {
    description: "Add, list, or remove stored connection entries",
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
        const secret = await ctx.ui.input("Entry value");
        if (!credentialId || !kind || secret === undefined) throw new Error("credential id, kind, and value are required");
        const result = await vaultAdd({ id: credentialId, kind, secret, scope: "global" }, vault);
        ctx.ui?.notify?.(result.success ? `Stored global credential ${credentialId}.` : result.error, result.success ? "info" : "error");
      } catch (error) { ctx.ui?.notify?.(error instanceof Error ? error.message : String(error), "error"); }
    },
  });
}

export default function vaultExtension(pi: Pi): void { registerVault(pi); }
