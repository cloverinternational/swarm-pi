import { hookGroups, isHookEnabled, persistHookState, renderHookLines, restoreHookState, setHookPi, setHookVisibility, toggleHook } from "../hook-state.ts";

export default function hooksExtension(pi: any) {
  setHookPi(pi);
  pi.on("session_start", (_e: any, ctx: any) => { restoreHookState(ctx.sessionManager?.getEntries?.() ?? []); pi.ui?.setWidget?.("swarm-hooks", undefined); });
  pi.registerShortcut?.("ctrl+h", { description: "Toggle inline pre/post-tool hook visibility", handler: (ctx: any) => { const visible = setHookVisibility(); persistHookState(pi); ctx.ui?.notify?.(`Inline hook visibility ${visible ? "on" : "off"}`, "info"); } });
  pi.registerCommand?.("hooks", { description: "Inspect and toggle Swarm hooks", handler: async (args: string, ctx: any) => { const value = args.trim(); if (value) { const [name, mode] = value.split(/\s+/, 2); if (!hookGroups().includes(name as any)) { ctx.ui?.notify?.(`Unknown hook group: ${name}`, "error"); return; } toggleHook(name, mode === "on" ? true : mode === "off" ? false : undefined); persistHookState(pi); } ctx.ui?.notify?.(renderHookLines().join("\n"), "info"); } });
}
