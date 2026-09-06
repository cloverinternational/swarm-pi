import { Text } from "@earendil-works/pi-tui";

export interface HookPresentation {
  hook: string;
  phase: "before" | "after" | "lifecycle";
  outcome: "executed" | "blocked" | "failed" | "skipped";
  reason?: string;
  message?: string;
  output?: string;
}

export function renderHookPresentation(data: HookPresentation, theme: any): Text {
  const marker = data.outcome === "blocked" ? "!" : data.outcome === "failed" ? "×" : "✓";
  const phase = data.phase === "after" ? "post" : "pre";
  const line = `${marker} [${phase}-hook] ${data.hook}${data.outcome === "blocked" || data.outcome === "failed" ? ` (${data.outcome.toUpperCase()}${data.reason || data.message ? `: ${data.reason ?? data.message}` : ""})` : ""}`;
  const output = data.output ? `\n${String(data.output).split("\n").map(line => `    ⎿ ${line}`).join("\n")}` : "";
  return new Text(theme.fg(marker === "✓" ? "success" : marker === "!" ? "warning" : "error", line) + (output ? theme.fg("dim", output) : ""), 2, 0);
}
