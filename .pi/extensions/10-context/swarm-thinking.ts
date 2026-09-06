type ThinkingLevel = "off" | "minimal" | "low" | "medium" | "high" | "xhigh";

const levels: ThinkingLevel[] = ["off", "minimal", "low", "medium", "high", "xhigh"];

interface ThinkingUI {
  select(title: string, options: string[]): Promise<string | undefined>;
  notify(message: string, type?: "info" | "warning" | "error"): void;
}

interface ThinkingContext {
  ui: ThinkingUI;
}

interface ThinkingAPI {
  getThinkingLevel(): ThinkingLevel;
  setThinkingLevel(level: ThinkingLevel): void;
  registerShortcut(
    shortcut: string,
    options: { description: string; handler: (ctx: ThinkingContext) => Promise<void> },
  ): void;
  registerCommand(
    name: string,
    options: { description: string; handler: (args: string, ctx: ThinkingContext) => Promise<void> },
  ): void;
}

function label(level: ThinkingLevel, current: ThinkingLevel): string {
  return `${level === current ? "●" : "○"} ${level[0].toUpperCase()}${level.slice(1)}${level === current ? "  · current" : ""}`;
}

export async function openThinkingSettings(pi: ThinkingAPI, ctx: ThinkingContext): Promise<void> {
  const current = pi.getThinkingLevel();
  const selected = await ctx.ui.select(
    "Thinking settings · choose reasoning level",
    levels.map(level => label(level, current)),
  );
  if (!selected) return;

  const index = levels.findIndex(level => selected.startsWith(`● ${level[0].toUpperCase()}${level.slice(1)}`) ||
    selected.startsWith(`○ ${level[0].toUpperCase()}${level.slice(1)}`));
  if (index < 0) return;
  const next = levels[index];
  pi.setThinkingLevel(next);
  ctx.ui.notify(
    next === "off" ? "Thinking is off." : `Thinking level: ${next}.`,
    "info",
  );
}

export default function swarmThinkingExtension(pi: ThinkingAPI): void {
  const open = (ctx: ThinkingContext) => openThinkingSettings(pi, ctx);
  // Pi owns /thinking and ctrl+alt+t. Use Swarm-specific identifiers so this
  // extension remains visible in autocomplete and does not shadow Pi controls.
  pi.registerShortcut("ctrl+alt+shift+t", {
    description: "Open Swarm thinking settings",
    handler: open,
  });
  pi.registerCommand("swarm-thinking", {
    description: "Open Swarm thinking settings",
    handler: async (_args, ctx) => open(ctx),
  });
}
