import path from "node:path";
import { registerHook } from "../hook-state.ts";

const MUTATING_TOOLS = /^(?:write|edit|apply_patch|delete|remove|move|rename|mkdir|touch|rm|cp|mv|patch)$/i;

/** Vendored read-only reference trees live under vendor/ (formerly upstream/). */
const VENDOR_DIR = "vendor";

function containsUpstreamTarget(value: unknown, root: string): boolean {
  if (typeof value !== "string") return false;
  const upstream = path.resolve(root, VENDOR_DIR);
  const candidates = value.match(/(?:^|[\s"'`=])((?:\.\/)?vendor(?:[\/][^\s"'`;&|]*)?|\/(?:[^\s"'`;&|]*[\/])vendor(?:[\/][^\s"'`;&|]*)?)/g) ?? [];
  return candidates.some(match => {
    const candidate = match.trim().replace(/^['"`]/, "");
    const resolved = path.resolve(root, candidate);
    return resolved === upstream || resolved.startsWith(`${upstream}${path.sep}`);
  });
}

export default function upstreamReadonlyExtension(pi: any): void {
  registerHook(pi, "disk-hooks", "tool_call", (event: any, ctx: any) => {
    const root = path.resolve(ctx?.cwd ?? pi.getCwd?.() ?? process.cwd());
    const tool = String(event?.toolName ?? event?.tool_name ?? "");
    const input = event?.input ?? event?.arguments ?? event?.tool_input ?? {};
    const fields = input && typeof input === "object"
      ? Object.entries(input).filter(([key]) => /path|file|dir|target|cwd|command|script/i.test(key)).map(([, value]) => value)
      : [];
    const command = input && typeof input === "object" ? (input as any).command : undefined;
    const target = fields.some(value => containsUpstreamTarget(value, root)) || containsUpstreamTarget(command, root);
    if (target && (MUTATING_TOOLS.test(tool) || /^(?:bash|shell|execute|run)/i.test(tool))) {
      return { block: true, reason: "vendor/ is read-only; make changes in a local path instead." };
    }
    return undefined;
  });
}
