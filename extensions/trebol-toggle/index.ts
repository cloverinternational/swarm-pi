import { ExtensionRunner, type ExtensionAPI } from "@earendil-works/pi-coding-agent";
import { installTrebolShortcut, trebolShortcut } from "../../.pi/lib/runtime/trebol-shortcut.ts";
import { setTrebolIdleProbe, setTrebolMode, trebolIdle, trebolMode } from "../../.pi/lib/runtime/trebol-toggle.ts";

const FOOTER_KEY = Symbol.for("pi-swarm-footer-segments");
const REGISTERED = Symbol.for("pi-swarm-trebol-loader-registered");
const MODE = Symbol.for("pi-swarm-trebol-mode");
const METRICS = Symbol.for("pi-swarm-conversation-metrics");

type ToggleShared = { busy: boolean; generation: number; active?: { pi: any; ctx: any }; previous?: "on" | "off" };
const globals = globalThis as any;
const state: ToggleShared = globals[REGISTERED] ?? (globals[REGISTERED] = { busy: false, generation: 0 });
globals[MODE] ??= "on";

setTrebolIdleProbe(() => {
  try { return state.active?.ctx?.isIdle?.(); } catch { return false; }
});

function segments(): Map<string, () => string | undefined> {
  return globals[FOOTER_KEY] ?? (globals[FOOTER_KEY] = new Map());
}

export default function trebolLoader(pi: ExtensionAPI): void {
  if (state.active?.pi === pi) return;
  let releaseShortcut: (() => void) | undefined = installTrebolShortcut(ExtensionRunner);
  // Runtime replacement delivers a fresh ExtensionAPI; clear the completed
  // transition latch before exposing the command in that replacement.
  if (state.active && state.active.pi !== pi) state.busy = false;
  const controller = { pi, ctx: undefined as any };
  state.active = controller;
  const existingMode = trebolMode();
  const updateFooter = (mode = trebolMode()) => {
    const map = segments();
    // On is the default: reserve footer space for exceptional state only.
    map.delete("trebol-mode");
    const ctx = controller.ctx;
    try {
      const metrics = globals[METRICS];
      if (metrics && mode === "off") metrics.footer = undefined;
    } catch { /* metrics disposal is not a guarantee that the slot resets */ }
    try { ctx?.ui?.setStatus?.("trebol", mode === "off" ? "Trebol OFF [Ctrl+N]" : undefined); } catch { /* replaced session */ }
    try { ctx?.ui?.requestRender?.(); } catch { /* replaced session */ }
  };

  pi.on("session_start", (_event, ctx) => {
    releaseShortcut ??= installTrebolShortcut(ExtensionRunner);
    controller.ctx = ctx;
    updateFooter(existingMode);
  });
  pi.on("session_shutdown", () => { releaseShortcut?.(); releaseShortcut = undefined; if (state.active === controller) state.active = undefined; });

  const toggle = async (ctx: any) => {
      if (state.busy) { ctx.ui.notify("Trebol toggle is already in progress", "warning"); return; }
      let isIdle = false;
      try { isIdle = ctx.isIdle?.() === true; } catch { isIdle = false; }
      if (!isIdle || ctx.hasPendingMessages() || !trebolIdle()) { ctx.ui.notify("Toggle Trebol only while the agent is idle with no queued messages", "warning"); return; }
      state.busy = true;
      state.previous = trebolMode();
      const target = state.previous === "on" ? "off" : "on";
      const generation = ++state.generation;
      setTrebolMode(target);
      updateFooter(target);
      try {
        await ctx.reload();
        // `ctx.reload()` replaces ExtensionAPI/contexts. This old handler may not
        // dereference ctx after awaiting it; the resident factory initializes
        // the replacement state on its next session_start.
        if (state.generation === generation) state.busy = false;
      } catch (error) {
        if (state.generation === generation) {
          const restored = state.previous ?? "on";
          setTrebolMode(restored);
          // Re-render the footer for the restored mode; the on state contributes no segment.
          updateFooter(restored);
          state.busy = false;
          try { ctx.ui.notify(`Trebol reload failed: ${error instanceof Error ? error.message : String(error)}`, "error"); } catch { /* ctx may already be invalid */ }
        }
        throw error;
      }
    };
  pi.registerCommand("trebol-toggle", {
    description: "Toggle Trebol extensions and reload the runtime",
    handler: async (_args, ctx) => toggle(ctx),
  });
  const shortcut = Object.assign(toggle, { [trebolShortcut]: true });
  pi.registerShortcut("ctrl+n", {
    description: "Toggle Trebol extensions",
    handler: shortcut,
  });
}

