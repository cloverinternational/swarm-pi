import { CodeMode, Tool, toolError } from "../../../packages/tools/codemode/src/index.ts";
import { Effect, Schema } from "effect";
import { Type } from "typebox";
import { execFile } from "node:child_process";
import { promisify } from "node:util";
import { readFile, writeFile, mkdir } from "node:fs/promises";
import path from "node:path";
import { randomUUID } from "node:crypto";
import type { JsonSchema } from "../../../packages/tools/codemode/src/tool.ts";
import { wrapToolForHookRows } from "../00-runtime/hooks.ts";
import { dispatchRegisteredHook } from "../../lib/runtime/hook-state.ts";
const execFileAsync = promisify(execFile);
const root = process.cwd();
export const CODEMODE_FOOTER_STATE = Symbol.for("pi-swarm-codemode-footer-state");
type CodeModeFooterState = { enabled: boolean; requestRender?: () => void };
const codeModeFooterState = (): CodeModeFooterState => {
  const g = globalThis as typeof globalThis & { [CODEMODE_FOOTER_STATE]?: CodeModeFooterState };
  return g[CODEMODE_FOOTER_STATE] ?? (g[CODEMODE_FOOTER_STATE] = { enabled: false });
};
// Deliberately mirrors the host-authority model requested here: CodeMode is not a
// filesystem sandbox. Relative paths resolve from cwd; absolute and parent paths work.
const resolve = (p: string) => path.resolve(root, p);
const boundedFailureText = (value: unknown): string => {
  const error = value as any;
  const stderr = typeof error?.stderr === "string" ? error.stderr.trim() : "";
  const stdout = typeof error?.stdout === "string" ? error.stdout.trim() : "";
  const detail = (stderr || stdout).slice(-4000);
  const status = error?.code !== undefined ? ` exit=${String(error.code)}` : error?.signal ? ` signal=${String(error.signal)}` : "";
  return `${detail || (error instanceof Error ? error.message : String(error))}${status}`.trim();
};
const host = (description: string, input: any, run: (value: any) => Promise<unknown>) => Tool.make({ description: `${description} Prefer one CodeMode program that chains related calls with sequential await, then returns one compact structured report. Use Promise.all only for independent calls.`, input, run: (v: any) => Effect.promise(async () => { try { return await run(v); } catch (error) { throw toolError(`Host tool failed: ${boundedFailureText(error)}`); } }) });
const CODEMODE_OUTPUT_LIMIT = 96 * 1024;
const CODEMODE_SPILL_DIR = path.join(root, ".swarm", "codemode-spills");
const result = (value: unknown) => ({ content: [{ type: "text", text: JSON.stringify(value, null, 2) }], details: value });
const spillIfNeeded = async (value: any) => {
  const serialized = JSON.stringify(value, null, 2);
  if (Buffer.byteLength(serialized, "utf8") <= CODEMODE_OUTPUT_LIMIT) return result(value);
  await mkdir(CODEMODE_SPILL_DIR, { recursive: true });
  const file = path.join(CODEMODE_SPILL_DIR, `${Date.now()}-${randomUUID()}.json`);
  await writeFile(file, serialized, "utf8");
  const relative = path.relative(root, file);
  const notice = {
    ok: value?.ok,
    value: `[codemode output spilled to ${relative}; read the file for the complete JSON result]`,
    ...(value?.ok === false ? { error: value.error } : {}),
    ...(value?.logs ? { logs: value.logs } : {}),
    toolCalls: value?.toolCalls ?? [],
    spilled: true,
    spill_path: relative,
    spill_bytes: Buffer.byteLength(serialized, "utf8"),
    spill_message: `Output exceeded the ${CODEMODE_OUTPUT_LIMIT} byte context threshold and was written to ${relative}. Read that file with tools.workspace.read if you need the complete result.`,
  };
  return result(notice);
};
const summarize = (value: unknown, limit = 180) => { const text = typeof value === "string" ? value.replace(/\s+/g, " ").trim() : JSON.stringify(value); return text.length > limit ? text.slice(0, limit - 1) + "…" : text; };
const renderSummary = (value: any, expanded: boolean) => { const calls = Array.isArray(value?.toolCalls) ? value.toolCalls : []; const failed = value?.ok === false; const lines = ["┌─ CODEMODE ───────────────────────────────────────────────", `│ ${failed ? "✗ FAILED" : "✓ COMPLETE"}   calls=${calls.length}${value?.truncated ? "   output=TRUNCATED" : ""}`, "│"]; calls.forEach((call: any, i: number) => lines.push(`│  ${String(i + 1).padStart(2, "0")}  ${call.name}`)); if (failed) lines.push(`│  error  ${summarize(value.error?.message ?? value.error)}`); else if (expanded) lines.push(`│  value  ${summarize(value.value, 900)}`); lines.push("└────────────────────────────────────────────────────────────"); return lines.join("\n"); };
type CodeModeSelection = { mode: "all" | "allowlist"; tools: string[] };
const normalizeToolName = (name: string) => name.trim().replace(/^tools\./, "");
const isSelected = (selection: CodeModeSelection, name: string) => selection.mode === "all" || selection.tools.includes(normalizeToolName(name));
const invokeNestedTool = async (definition: any, name: string, input: unknown) => {
  const toolCallId = `codemode-${Date.now()}-${Math.random().toString(36).slice(2)}`;
  const event = { toolName: name, toolCallId, input };
  const before = await dispatchRegisteredHook("tool_call", event, {});
  if (before?.block === true) throw new Error(before.reason ?? `Tool '${name}' blocked by hook`);
  let output: any;
  let failed = false;
  try { output = await definition.execute(toolCallId, input, undefined, undefined, undefined); }
  catch (error) { failed = true; output = { content: [{ type: "text", text: error instanceof Error ? error.message : String(error) }], isError: true, details: {} }; }
  const after = await dispatchRegisteredHook("tool_result", { ...event, content: output?.content, details: output?.details, isError: failed || output?.isError === true }, {});
  if (after?.content !== undefined) output = { ...output, content: after.content };
  if (after?.details !== undefined) output = { ...output, details: after.details };
  if (after?.isError !== undefined) output = { ...output, isError: after.isError };
  if (failed) throw new Error(output?.content?.[0]?.text ?? `Tool '${name}' failed`);
  return output;
};
const buildPiTools = (pi: any, selection: CodeModeSelection) => { const tree: Record<string, unknown> = {}; for (const direct of pi.codemodeTools ?? []) { if (direct?.name && direct.execute && isSelected(selection, direct.name)) tree[direct.name] = Tool.make({ description: direct.description ?? direct.name, input: direct.parameters ?? { type: "object", properties: {} }, run: (input: unknown) => Effect.promise(() => invokeNestedTool(direct, direct.name, input)) }); } for (const info of pi.getAllTools?.() ?? []) { const name = info?.name; if (typeof name !== "string" || !name || name === "codemode" || !isSelected(selection, name)) continue; const definition = pi.getToolDefinition?.(name); if (!definition?.execute) continue; tree[name] = Tool.make({ description: info.description ?? definition.description ?? name, input: info.parameters ?? definition.parameters ?? { type: "object", properties: {} }, run: (input: unknown) => Effect.promise(() => invokeNestedTool(definition, name, input)) }); } return tree; };

class CodeModeToolPicker {
  private index = 0;
  private offset = 0;
  private readonly names: string[];
  private readonly chosen: Set<string>;
  constructor(names: string[], selection: CodeModeSelection, private readonly done: (value: CodeModeSelection | undefined) => void, private readonly theme: any) {
    this.names = [...new Set(names)].sort();
    this.chosen = new Set(selection.mode === "all" ? this.names : selection.tools);
  }
  private visibleRows(): number { return 12; }
  private move(delta: number): void {
    if (!this.names.length) return;
    this.index = Math.max(0, Math.min(this.names.length - 1, this.index + delta));
    const rows = this.visibleRows();
    if (this.index < this.offset) this.offset = this.index;
    if (this.index >= this.offset + rows) this.offset = this.index - rows + 1;
  }
  render(width: number): string[] {
    const inner = Math.max(1, width - 2);
    const fit = (value: string) => value.length > inner ? value.slice(0, Math.max(0, inner - 1)) + "…" : value.padEnd(inner, " ");
    const rows = this.visibleRows();
    const end = Math.min(this.names.length, this.offset + rows);
    const border = (left: string, fill: string, right: string) => left + fill.repeat(Math.max(0, inner)) + right;
    const lines = [border("╭", "─", "╮"), "│" + fit(" CodeMode tools") + "│", "│" + fit(" Space toggle · ↑/↓ scroll · A all · N none · Enter apply") + "│", "│" + fit(" " + this.chosen.size + "/" + this.names.length + " selected") + "│", border("├", "─", "┤")];
    for (let i = this.offset; i < end; i++) { const mark = this.chosen.has(this.names[i]) ? "[✓]" : "[ ]"; const cursor = i === this.index ? "❯" : " "; const text = " " + cursor + " " + mark + " " + this.names[i]; const row = "│" + fit(text) + "│"; lines.push(this.theme?.fg ? this.theme.fg(i === this.index ? "accent" : this.chosen.has(this.names[i]) ? "success" : "dim", row) : row); }
    if (this.offset > 0 || end < this.names.length) lines.push("│" + fit(" " + (this.offset + 1) + "–" + end + " of " + this.names.length + "  ↕") + "│");
    lines.push(border("╰", "─", "╯")); return lines;
  }
  invalidate() {}
  handleInput(data: string): void {
    if (data === "\u001b" || data === "q") return this.done(undefined);
    if (data === "\r" || data === "\n") return this.done({ mode: this.chosen.size === this.names.length ? "all" : "allowlist", tools: [...this.chosen] });
    if (data === " ") { const name = this.names[this.index]; if (name) { if (this.chosen.has(name)) this.chosen.delete(name); else this.chosen.add(name); } return; }
    if (data === "a" || data === "A") { this.names.forEach(name => this.chosen.add(name)); return; }
    if (data === "n" || data === "N") { this.chosen.clear(); return; }
    if (data === "\u001b[A" || data === "k") this.move(-1);
    else if (data === "\u001b[B" || data === "j") this.move(1);
    else if (data === "\u001b[5~") this.move(-this.visibleRows());
    else if (data === "\u001b[6~") this.move(this.visibleRows());
  }
}

export default function codemodeExtension(pi: any) {
  const tools = { workspace: {
    read: host("Read a UTF-8 text file in the workspace. For unfamiliar or potentially large sources, first read a small bounded sample or targeted slice, inspect its literal shape and encoding, then expand deliberately.", Schema.Struct({ path: Schema.String }), async ({ path: p }: any) => readFile(resolve(p), "utf8")),
    list: host("List workspace files. Treat the listing as reconnaissance: inspect a bounded sample before assuming file types, layout, or parsability.", Schema.Struct({ path: Schema.String }), async ({ path: p }: any) => { const { stdout } = await execFileAsync("find", [resolve(p), "-maxdepth", "2", "-type", "f"]); return stdout.trim().split(/\r?\n/).filter(Boolean).map((x: string) => path.relative(root, x)); }),
    grep: host("Search text in files; paths may be absolute or outside the cwd. Use a narrow taste query first to establish whether the source contains the expected markers, format, and scope before broad extraction.", Schema.Struct({ query: Schema.String, path: Schema.optional(Schema.String) }), async ({ query, path: p }: any) => { const { stdout } = await execFileAsync("grep", ["-R", "-n", "-F", "--", query, resolve(p ?? ".")], { maxBuffer: 1024 * 1024 }); return stdout; }),
    edit: host("Perform an exact text replacement in any accessible file.", Schema.Struct({ path: Schema.String, oldText: Schema.String, newText: Schema.String }), async ({ path: p, oldText, newText }: any) => { const f = resolve(p); const before = await readFile(f, "utf8"); const count = before.split(oldText).length - 1; if (count !== 1) throw new Error(`Expected exactly one match, found ${count}`); await writeFile(f, before.replace(oldText, newText)); return { path: f, replacements: 1 }; }),
    write: host("Write a UTF-8 text file at any accessible path.", Schema.Struct({ path: Schema.String, content: Schema.String }), async ({ path: p, content }: any) => { const f = resolve(p); await mkdir(path.dirname(f), { recursive: true }); await writeFile(f, content); return { path: f, written: true }; }),
    delete: host("Delete any accessible file.", Schema.Struct({ path: Schema.String }), async ({ path: p }: any) => { const { unlink } = await import("node:fs/promises"); await unlink(resolve(p)); return { path: resolve(p), deleted: true }; }),
    bash: host("Run an unrestricted shell command with the host user's permissions. For unfamiliar APIs, JSON, logs, or command output, sample first with a cheap bounded probe, inspect the actual response and exit status, then run the bulk command only after the shape is confirmed.", Schema.Struct({ command: Schema.String }), async ({ command }: any) => { const { stdout, stderr } = await execFileAsync("/bin/sh", ["-c", command], { cwd: root, maxBuffer: 4 * 1024 * 1024 }); return { stdout, stderr }; }),
  }};
  // Pi action methods are unavailable while extensions are loading. Do not
  // touch the action API until session_start has fired; this also makes reload
  // safe on hosts that eagerly evaluate extension factories.
  let runtimeReady = false;
  let enabled = false;
  let selection: CodeModeSelection = { mode: "all", tools: [] };
  const normalizeNames = (names: string[]) => [...new Set(names.flatMap(name => name.split(",")).map(normalizeToolName).filter(Boolean))];
  const availableNames = () => ["workspace.read", "workspace.list", "workspace.grep", "workspace.edit", "workspace.write", "workspace.delete", "workspace.bash", ...(pi.getAllTools?.() ?? []).map((tool: any) => tool?.name).filter((name: any) => typeof name === "string" && name !== "codemode")];
  const saveSelection = () => pi.appendEntry?.("pi-swarm-codemode-tools", selection);
  const loadSelection = (ctx: any) => { const entries = ctx?.sessionManager?.getBranch?.() ?? ctx?.sessionManager?.getEntries?.() ?? []; const data = [...entries].reverse().find((entry: any) => entry?.type === "pi-swarm-codemode-tools" || (entry?.type === "custom" && entry?.customType === "pi-swarm-codemode-tools"))?.data; if (data?.mode === "all" || data?.mode === "allowlist") selection = { mode: data.mode, tools: normalizeNames(Array.isArray(data.tools) ? data.tools : []) }; };
  const selectionText = () => selection.mode === "all" ? "all tools" : selection.tools.length + " selected tools";
  const setSelection = (mode: "all" | "allowlist", names: string[]) => { selection = { mode, tools: normalizeNames(names) }; saveSelection(); };
  // Registration is safe during extension loading, but changing the host's active
  // tool set is not. CodeMode must be opt-in: automatically hiding Pi's native
  // tools at session_start leaves a fresh conversation with only `codemode`, and
  // makes the first turn depend on the confined interpreter being perfectly
  // instructed. Rehydrate configuration here; `/codemode on` performs the
  // explicit activation after startup.
  pi.on?.("session_start", (_event: any, ctx: any) => { loadSelection(ctx); runtimeReady = true; updateCodeStatus(ctx); });
  const refreshRuntime = () => { const selectedTools = selection.mode === "all" ? tools : Object.fromEntries(Object.entries(tools).filter(([namespace]) => selection.tools.some(name => name === namespace || name.startsWith(namespace + ".")))); return CodeMode.make({ tools: { ...selectedTools, ...(runtimeReady ? buildPiTools(pi, selection) : {}) } as any, limits: { timeoutMs: 30_000, maxToolCalls: 50, maxOutputBytes: 128 * 1024 } }); };
  const originalTools = () => runtimeReady ? (pi.getActiveTools?.() ?? []) : [];
  let savedTools: string[] | undefined;
  const activate = () => { if (!runtimeReady) return; if (!enabled) { savedTools ??= originalTools(); pi.setActiveTools?.(["codemode"]); enabled = true; updateCodeStatus(pi); } };
  const deactivate = () => { if (runtimeReady && enabled) pi.setActiveTools?.(savedTools ?? []); enabled = false; updateCodeStatus(pi); };
  const updateCodeStatus = (ctx: any) => { const state = codeModeFooterState(); state.enabled = enabled; state.requestRender?.(); const ui = ctx?.ui ?? pi; ui?.setStatus?.("codemode", enabled ? " CODE " : " code "); };

  pi.registerTool(wrapToolForHookRows({ name: "codemode", label: "Code Mode", description: "Run one bounded CodeMode program over workspace tools and registered Pi-Swarm tools such as Agent and AgentControl. Every tool is rooted at `tools`: use `tools.workspace.read(...)` or `tools.Agent(...)` only when listed; bare `workspace(...)`, `workspace.read(...)`, `Agent(...)`, and `AgentControl(...)` are invalid. Prefer one chained workflow: taste/read context, edit, prove the diff, verify, and return one compact report. Calls must be awaited; use Promise.all only for independent work. Spill oversized output instead of flooding context.", parameters: Type.Object({ code: Type.String({ description: `Use the tools root only. Exact examples: tools.workspace.read({ path: "AGENTS.md" }), tools.workspace.bash({ command: "git diff --check" }), and tools.Agent(...) only if listed. Bare workspace, Agent, and AgentControl identifiers are invalid. Compose one workflow: taste, harvest, edit, prove the diff, verify, and return concise evidence.` }) }), executionMode: "sequential", async execute(_id: string, params: { code: string }, signal: AbortSignal) { if (signal?.aborted) return result({ ok: false, error: "Execution cancelled" }); const output = await Effect.runPromise(refreshRuntime().execute(params.code) as any); return await spillIfNeeded(output); } }));
  pi.registerCommand?.("codemode", {
    description: "Open CodeMode tool picker",
    handler: async (args: string, ctx: any) => {
      const parts = args.trim().split(/\s+/).filter(Boolean);
      const action = (parts.shift() ?? "picker").toLowerCase();
      const names = normalizeNames(parts);
      if (action === "picker" || action === "select" || action === "configure") {
        const picked = await ctx.ui?.custom?.((_tui: any, theme: any, _keys: any, done: any) => new CodeModeToolPicker(availableNames(), selection, done, theme), { overlay: true, overlayOptions: { anchor: "right-center", width: "55%", minWidth: 48, maxHeight: "80%", margin: 2 } });
        if (picked) { setSelection(picked.mode, picked.tools); ctx.ui?.notify?.("CodeMode now exposes " + selectionText(), "info"); }
        return;
      }
      if (action === "off") deactivate();
      else if (action === "on") activate();
      else if (action === "list") ctx.ui?.notify?.("CodeMode tools (" + selectionText() + "):\n" + availableNames().sort().join("\n"), "info");
      else if (action === "status") ctx.ui?.notify?.("CodeMode " + (enabled ? "enabled" : "disabled") + "; exposing " + selectionText(), "info");
      else if (action === "reset" || action === "all") { setSelection("all", []); ctx.ui?.notify?.("CodeMode exposes all available tools.", "info"); }
      else { enabled ? deactivate() : activate(); ctx.ui?.notify?.(enabled ? "CodeMode enabled: native tools hidden." : "CodeMode disabled: native tools restored.", "info"); }
    },
  });
}
