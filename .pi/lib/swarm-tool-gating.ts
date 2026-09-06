/**
 * Which tools Swarm exposes to the model, and under which conditions, so Pi
 * advertises the same surface. Mirrors swarm-tui/internal/chat/sdk_integration.go:
 *
 *  - the 28 always-on tools captured in tools/parity/fixtures/swarm-tools.json
 *  - ask_user_question          only when a QuestionBroker exists (interactive TUI)
 *  - enter_plan_mode/exit_plan_mode only when a PlanBroker exists (interactive TUI)
 *  - x_search / xai_web_search  only when xaitools.HasCredentials():
 *        ~/.swarm/config/oauth/xai.json token (unexpired or refreshable)
 *        or XAI_API_KEY set  (internal/tools/xai/responses.go:63)
 *
 * Anything Pi registers beyond this list is Pi-only and is hidden from the
 * model surface unless PI_SWARM_TOOL_SURFACE=all.
 */
import { existsSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { swarmToolNames } from "./swarm-tool-surface.ts";

export const INTERACTIVE_ONLY_TOOLS = ["ask_user_question", "enter_plan_mode", "exit_plan_mode"] as const;
export const XAI_TOOLS = ["x_search", "xai_web_search"] as const;

export interface GatingEnvironment {
  interactive: boolean;
  home?: string;
  env?: NodeJS.ProcessEnv;
  now?: () => number;
}

/** internal/tools/xai/responses.go HasCredentials + oauth_config.go IsExpired. */
export function xaiHasCredentials(home = process.env.HOME ?? "", env = process.env, now = () => Date.now()): boolean {
  const file = join(home, ".swarm", "config", "oauth", "xai.json");
  if (existsSync(file)) {
    try {
      const token = JSON.parse(readFileSync(file, "utf8"))?.token;
      if (token && typeof token === "object") {
        const expiresAt = Number(token.expires_at ?? 0);
        const expired = expiresAt !== 0 && Math.floor(now() / 1000) > expiresAt - 60;
        if (!expired) return true;
        if (typeof token.refresh_token === "string" && token.refresh_token !== "") return true;
      }
    } catch { /* unreadable config counts as absent, like Go's error path */ }
  }
  return (env.XAI_API_KEY ?? "").trim() !== "";
}

/** The set of tool names Swarm would register in this environment. */
export function swarmSurfaceFor(environment: GatingEnvironment): Set<string> {
  const names = new Set<string>(swarmToolNames());
  if (environment.interactive) for (const name of INTERACTIVE_ONLY_TOOLS) names.add(name);
  if (xaiHasCredentials(environment.home, environment.env, environment.now)) for (const name of XAI_TOOLS) names.add(name);
  return names;
}

/**
 * Filter Pi's active tool names down to the Swarm surface. Returns undefined
 * when nothing would change. Tools Swarm has but Pi lacks are simply absent —
 * they are reported separately by the parity probe.
 */
export function gateActiveTools(active: readonly string[], environment: GatingEnvironment, env = process.env): string[] | undefined {
  if ((env.PI_SWARM_TOOL_SURFACE ?? "").toLowerCase() === "all") return undefined;
  const allowed = swarmSurfaceFor(environment);
  const next = active.filter((name) => allowed.has(name));
  return next.length === active.length ? undefined : next;
}
