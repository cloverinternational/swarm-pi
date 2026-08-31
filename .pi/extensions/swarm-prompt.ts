import { readFileSync } from "node:fs";

const promptSource = new URL(
  "../../upstream/swarm-sdk/swarm-tui/docs/FORGE_SWARM_SYSTEM_PROMPT.md",
  import.meta.url,
);
const promptDocument = readFileSync(promptSource, "utf8");
const promptMatch = promptDocument.match(/```text\n([\s\S]*?)\n```/);
if (!promptMatch) {
  throw new Error("Forge system prompt document does not contain a text block");
}
const forgeSwarmSystemPrompt = promptMatch[1];
const forgeMarker = "## Core Principles:";

export interface PromptExtensionEvent {
  systemPrompt: string;
  systemPromptOptions?: { cwd?: string };
}

export interface PromptExtensionAPI {
  on(
    event: "before_agent_start",
    handler: (event: PromptExtensionEvent, ctx: unknown) => unknown,
  ): void;
}

function workspaceContext(cwd: string | undefined): string {
  const location = cwd || process.cwd();
  return [
    "<system_information>",
    `Current working directory: ${location}`,
    `Operating system: ${process.platform}`,
    `Shell: ${process.env.SHELL || "unknown"}`,
    "</system_information>",
  ].join("\n");
}

export function registerSwarmPrompt(pi: PromptExtensionAPI): void {
  pi.on("before_agent_start", (event) => {
    if (event.systemPrompt.includes(forgeMarker)) return;
    const base = event.systemPrompt.trim();
    const cwd = event.systemPromptOptions?.cwd;
    return {
      systemPrompt: [
        base,
        workspaceContext(cwd),
        forgeSwarmSystemPrompt,
      ].filter(Boolean).join("\n\n"),
    };
  });
}

export default function swarmPromptExtension(pi: PromptExtensionAPI): void {
  registerSwarmPrompt(pi);
}
