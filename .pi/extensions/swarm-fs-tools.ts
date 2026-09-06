import { applyPatch, undoFile } from "../lib/swarm-apply-patch.ts";
import { readImage } from "../lib/swarm-read-image.ts";
import { newErrorID } from "../lib/swarm-bash.ts";
import { applySwarmSurface, loadSwarmToolSurface } from "../lib/swarm-tool-surface.ts";

type Pi = any;
const registrations = new WeakSet<object>();
const error = (name: string, message: string) => {
  const text = `Error executing ${name}: ${message} (error_id=${newErrorID()})`;
  return { content: [{ type: "text", text }], details: { error: text }, isError: true };
};

export function registerSwarmFSTools(pi: Pi): void {
  if (registrations.has(pi as object)) return;
  registrations.add(pi as object);
  // Load eagerly so a missing/corrupt parity fixture fails extension startup.
  loadSwarmToolSurface();
  pi.registerTool?.(applySwarmSurface({
    name: "apply_patch", label: "apply_patch", description: "", parameters: {},
    async execute(_id: string, params: any, signal: AbortSignal | undefined, _update: unknown, ctx: any) {
      try {
        const text = await applyPatch(params?.input, { workspacePath: ctx?.cwd ?? pi.getCwd?.() ?? process.cwd(), cwd: params?.cwd, signal });
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
      try {
        const filePath = params?.file_path ?? params?.file ?? params?.path ?? params?.filename;
        const content = readImage(filePath, ctx?.cwd ?? pi.getCwd?.() ?? process.cwd());
        return { content, details: {} };
      } catch (e) { return error("Read", (e as Error).message); }
    },
  }));
}

export default function swarmFSToolsExtension(pi: Pi): void { registerSwarmFSTools(pi); }
