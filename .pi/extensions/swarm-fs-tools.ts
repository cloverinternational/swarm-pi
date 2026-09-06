import { applyPatch, undoFile } from "../lib/swarm-apply-patch.ts";
import { readImage } from "../lib/swarm-read-image.ts";
import { newErrorID } from "../lib/swarm-bash.ts";
import { applySwarmSurface, loadSwarmToolSurface } from "../lib/swarm-tool-surface.ts";

type Pi = any;
const registrations = new WeakSet<object>();
// Pi flags a tool result as failed only when execute() throws; the message
// becomes the result content verbatim (docs/extensions.md "Signaling errors").
const error = (name: string, message: string): never => {
  throw new Error(`Error executing ${name}: ${message} (error_id=${newErrorID()})`);
};

export function registerSwarmFSTools(pi: Pi): void {
  if (registrations.has(pi as object)) return;
  registrations.add(pi as object);
  // checkpoint.go SnapshotContext provenance, published by the transport
  // parity extension (conversation id + the user turn as the model saw it).
  const provenance = () => ({
    conversationId: (globalThis as any)[Symbol.for("pi-swarm-conversation-id")] as string | undefined,
    userMessage: (globalThis as any)[Symbol.for("pi-swarm-last-user-message")] as string | undefined,
  });
  // Load eagerly so a missing/corrupt parity fixture fails extension startup.
  loadSwarmToolSurface();
  // Swarm's headless broker (--approval-mode auto) approves every file-read
  // path; the interactive TUI would prompt the user, which Pi cannot mirror,
  // so only headless sessions skip FSRead's workspace boundary.
  let headless = false;
  pi.on?.("session_start", (_event: unknown, ctx: any) => { headless = ctx?.hasUI === false; });
  // registry_impl.go: Validate failures are sdkerr-wrapped a second time.
  const validation = (name: string, message: string): never =>
    error(name, `validation failed for ${name}: ${message} (error_id=${newErrorID()})`);
  pi.registerTool?.(applySwarmSurface({
    name: "apply_patch", label: "apply_patch", description: "", parameters: {},
    async execute(_id: string, params: any, signal: AbortSignal | undefined, _update: unknown, ctx: any) {
      try {
        const text = await applyPatch(params?.input, { workspacePath: ctx?.cwd ?? pi.getCwd?.() ?? process.cwd(), cwd: params?.cwd, signal, ...provenance() });
        return { content: [{ type: "text", text }], details: {} };
      } catch (e) { return error("apply_patch", (e as Error).message); }
    },
  }));
  pi.registerTool?.(applySwarmSurface({
    name: "Undo", label: "Undo", description: "", parameters: {},
    async execute(_id: string, params: any, _signal: AbortSignal | undefined, _update: unknown, ctx: any) {
      try {
        const text = await undoFile(params?.path, ctx?.cwd ?? pi.getCwd?.() ?? process.cwd());
        return { content: [{ type: "text", text }], details: {} };
      } catch (e) { return error("Undo", (e as Error).message); }
    },
  }));
  pi.registerTool?.(applySwarmSurface({
    name: "Read", label: "Read", description: "", parameters: {},
    async execute(_id: string, params: any, _signal: AbortSignal | undefined, _update: unknown, ctx: any) {
      const filePath = params?.file_path ?? params?.file ?? params?.path ?? params?.filename;
      if (filePath === undefined || filePath === null) return validation("Read", "file_path is required");
      if (typeof filePath !== "string") return validation("Read", "file_path must be a string");
      try {
        const content = readImage(filePath, ctx?.cwd ?? pi.getCwd?.() ?? process.cwd(), headless);
        return { content, details: {} };
      } catch (e) { return error("Read", (e as Error).message); }
    },
  }));
}

export default function swarmFSToolsExtension(pi: Pi): void { registerSwarmFSTools(pi); }
