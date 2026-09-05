import { readFileSync } from "node:fs";

/**
 * Vendored 1:1 copy of the Swarm TUI SwarmForge system prompt.
 *
 * The prompt text is owned by this package as plain-text assets. Nothing here
 * parses Go source or markdown at runtime: `scripts/sync.mjs` regenerates the
 * assets from upstream, and `test/index.test.ts` fails if they drift.
 *
 * Upstream: upstream/swarm-sdk/swarm-tui/internal/chat/settings/system_prompt.go
 */
const asset = (name: string): string =>
  readFileSync(new URL(`../assets/${name}`, import.meta.url), "utf8");

/** Verbatim body of `forgeSwarmSystemPrompt` (system_prompt.go:408). */
export const forgeSwarmSystemPrompt: string = asset("forge-swarm.txt");

/** Verbatim body of `swarmForgeDelegationAddendum` (system_prompt.go:561). */
export const swarmForgeDelegationAddendum: string = asset("delegation-addendum.txt");

/** `swarmForgeSystemPrompt = forgeSwarmSystemPrompt + swarmForgeDelegationAddendum` (system_prompt.go:628). */
export const swarmForgeSystemPrompt: string =
  forgeSwarmSystemPrompt + swarmForgeDelegationAddendum;

/** Provenance reference recorded by prompt-assembling extensions. */
export const UPSTREAM_SOURCE =
  "upstream/swarm-sdk/swarm-tui/internal/chat/settings/system_prompt.go";

export interface SwarmPromptPreset {
  /** Display name, matching the TUI's builtin prompt entry. */
  name: string;
  content: string;
  /** Whether the TUI prepends the runtime <system_information> block. */
  workspaceContext: boolean;
  /** `name` of the originating Go constant. */
  constant: string;
}

/**
 * The two workspace-context builtins registered by the TUI
 * (system_prompt.go:72-84), in upstream declaration order.
 */
export const swarmPromptPresets: readonly SwarmPromptPreset[] = Object.freeze([
  Object.freeze({
    name: "Accumulated Context Engineering",
    content: forgeSwarmSystemPrompt,
    workspaceContext: true,
    constant: "forgeSwarmSystemPrompt",
  }),
  Object.freeze({
    name: "SwarmForge",
    content: swarmForgeSystemPrompt,
    workspaceContext: true,
    constant: "swarmForgeSystemPrompt",
  }),
]);
