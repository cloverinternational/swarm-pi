import { CustomEditor } from "@earendil-works/pi-coding-agent";
import { truncateToWidth } from "@earendil-works/pi-tui";

/** Pi presentation adapter for the Swarm TUI palette and input prompt. */
const lineHasSwarmPrompt = (line: string) => line.replace(/\x1b\[[0-9;]*m/g, "").trimStart().startsWith("> ");

export default function swarmThemes(pi: any) {
  pi.on("session_start", (_event: any, ctx: any) => {
    // ResourceLoader owns theme parsing/discovery; select the canonical preset
    // only when the user has not already chosen a theme.
    if (ctx.ui?.getAllThemes?.().some((t: any) => t.name === "swarm-swarmcode") && !process.env.PI_THEME) {
      ctx.ui.setTheme?.("swarm-swarmcode");
    }
    // Do not replace Pi's editor component. The installed Pi/TUI build mounts
    // CustomEditor through MouseRegion, and theme invalidation calls a child
    // invalidate method that this extension cannot safely provide. Keeping
    // Pi's native editor avoids breaking /reload and tool-row rendering.
    // The Swarm prompt prefix is cosmetic and is intentionally disabled until
    // the CustomEditor contract is version-pinned and tested.
  });
}
