import { hookGroups, isHookEnabled, persistHookState, renderHookLines, restoreHookState, setHookPi, setHookVisibility, toggleHook } from "../hook-state.ts";
import { Text } from "@earendil-works/pi-tui";

export default function hooksExtension(pi: any) {
  setHookPi(pi);
  // Hook rows are TUI-only entries. Do not use sendMessage here: Pi custom
  // messages are deliberately delivered to the model, even when display=true.
  // Swarm's hook stream is presentation/telemetry, not conversation context.
  pi.registerEntryRenderer?.("swarm-hook-event", (entry: any, _options: any, theme: any) => {
    const data = entry.data ?? {};
    const marker = data.outcome === "blocked" ? "!" : data.outcome === "failed" ? "×" : "✓";
    const color = marker === "✓" ? "success" : marker === "!" ? "warning" : "error";
    const phase = data.phase === "after" ? "post" : "pre";
    const lines = [`${marker} [${phase}-hook] ${data.hookName ?? data.group ?? "hook"}`];
    if (data.reason) lines[0] += ` (${data.outcome?.toUpperCase()}: ${data.reason})`;
    if (data.output) lines.push(...String(data.output).split("\\n").map((line: string) => `    ⎿ ${line}`));
    return new Text(theme.fg(color, lines[0]) + (lines.length > 1 ? `\\n${theme.fg("dim", lines.slice(1).join("\\n"))}` : ""), 2, 0);
  });
  pi.on("session_start", (_e: any, ctx: any) => { restoreHookState(ctx.sessionManager?.getBranch?.() ?? ctx.sessionManager?.getEntries?.() ?? []); ctx.ui?.setWidget?.("swarm-hooks", undefined); });
  pi.registerShortcut?.("ctrl+h", { description: "Toggle inline pre/post-tool hook visibility", handler: (ctx: any) => { const visible = setHookVisibility(); persistHookState(pi); ctx.ui?.notify?.(`Inline hook visibility ${visible ? "on" : "off"}`, "info"); } });
  pi.registerCommand?.("hooks", { description: "Inspect and toggle Swarm hooks", handler: async (args: string, ctx: any) => { const value = args.trim(); if (value) { const [name, mode] = value.split(/\s+/, 2); if (!hookGroups().includes(name as any)) { ctx.ui?.notify?.(`Unknown hook group: ${name}`, "error"); return; } toggleHook(name, mode === "on" ? true : mode === "off" ? false : undefined); persistHookState(pi); } ctx.ui?.notify?.(renderHookLines().join("\n"), "info"); } });
}
