import { CustomEditor } from "@earendil-works/pi-coding-agent";
import { join } from "node:path";

/** Pi presentation adapter for the Swarm TUI palette and input prompt. */
const lineHasSwarmPrompt = (line: string) => line.replace(/\x1b\[[0-9;]*m/g, "").trimStart().startsWith("> ");

export default function swarmThemes(pi: any) {
  pi.on("resources_discover", () => ({ themePaths: [join(process.cwd(), ".pi", "themes")] }));
  pi.on("session_start", (_event: any, ctx: any) => {
    // ResourceLoader owns theme parsing/discovery; select the canonical preset
    // only when the user has not already chosen a theme.
    if (ctx.ui?.getAllThemes?.().some((t: any) => t.name === "swarm-swarmcode") && !process.env.PI_THEME) {
      ctx.ui.setTheme?.("swarm-swarmcode");
    }
    ctx.ui?.setEditorComponent?.((tui: any, theme: any, keybindings: any) => {
      class SwarmPromptEditor extends CustomEditor {
        render(width: number): string[] {
          const lines = super.render(width);
          // Pi's editor keeps the prompt out of the submitted text. Prefix the
          // visible first input row only, preserving autocomplete and history.
          const row = lines.findIndex((line) => !line.includes("─") && (line.includes("\x1b[7m") || line.trim().length > 0));
          if (row >= 0 && !lineHasSwarmPrompt(lines[row] ?? "")) lines[row] = `${theme.borderColor("> ")}${lines[row]}`;
          return lines;
        }
      }
      return new SwarmPromptEditor(tui, theme, keybindings);
    });
  });
}
