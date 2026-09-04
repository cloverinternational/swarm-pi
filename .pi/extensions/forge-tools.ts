import { access, mkdir, readFile, readdir, writeFile, rename } from "node:fs/promises";
import { constants } from "node:fs";
import { dirname, isAbsolute, relative, resolve, sep } from "node:path";
import { spawn } from "node:child_process";

/** Minimal Pi Component shape, kept local so the adapter stays standalone-testable. */
class ForgeText {
  constructor(private text: string) {}
  setText(text: string) { this.text = text; }
  invalidate() {}
  render(_width: number): string[] { return this.text ? this.text.split("\n") : []; }
}

/** Forge-compatible, workspace-scoped tool adapters. Deliberately independent of Pi internals. */
export interface ForgeToolResult { content: [{ type: "text"; text: string }]; details: Record<string, unknown>; isError?: boolean }
const ok = (text: string, details: Record<string, unknown> = {}): ForgeToolResult => ({ content: [{ type: "text", text }], details });
const fail = (message: string, code = "tool_error"): ForgeToolResult => ({ content: [{ type: "text", text: `[${code}] ${message}` }], details: { code }, isError: true });
function cancelled(signal?: AbortSignal) { return signal?.aborted; }
function pathInWorkspace(workspace: string, input: string): string {
  if (!input || input.includes("\0")) throw new Error("path is required and must not contain NUL");
  const root = resolve(workspace); const candidate = resolve(root, input);
  const rel = relative(root, candidate);
  if (rel === ".." || rel.startsWith(`..${sep}`) || isAbsolute(rel)) throw new Error("path must remain within workspace");
  return candidate;
}
function resultError(error: unknown, signal?: AbortSignal): ForgeToolResult { return fail(cancelled(signal) ? "operation cancelled" : error instanceof Error ? error.message : String(error), cancelled(signal) ? "cancelled" : "execution_error"); }
async function textFile(path: string, signal?: AbortSignal) { if (cancelled(signal)) throw new Error("operation cancelled"); return (await readFile(path)).toString("utf8"); }
async function run(command: string, cwd: string, signal: AbortSignal | undefined, timeout = 30): Promise<{out: string; code: number}> {
  if (cancelled(signal)) throw new Error("operation cancelled");
  return await new Promise((resolvePromise, reject) => {
    const child = spawn("/bin/sh", ["-c", command], { cwd, stdio: ["ignore", "pipe", "pipe"] }); let out = ""; let done = false;
    const finish = (fn: () => void) => { if (!done) { done = true; clearTimeout(timer); signal?.removeEventListener("abort", abort); fn(); } };
    const abort = () => { child.kill("SIGTERM"); finish(() => reject(new Error("operation cancelled"))); };
    const timer = setTimeout(() => { child.kill("SIGTERM"); finish(() => reject(new Error(`command timed out after ${timeout}s`))); }, timeout * 1000);
    child.stdout.on("data", d => { out += d.toString(); }); child.stderr.on("data", d => { out += d.toString(); });
    signal?.addEventListener("abort", abort, { once: true });
    child.on("error", e => finish(() => reject(e))); child.on("close", code => finish(() => resolvePromise({ out, code: code ?? 1 })));
  });
}
const schemasPath = { type: "string", description: "Path relative to the workspace." };
const common = { type: "object", additionalProperties: false };
function tool(name: string, description: string, parameters: unknown, execute: (p: any, signal?: AbortSignal) => Promise<ForgeToolResult>) {
  return {
    name, label: name, description, parameters,
    execute: async (_id: string, p: any, signal: AbortSignal) => { try { return await execute(p, signal); } catch (e) { return resultError(e, signal); } },
    renderCall: (args: any, theme: any) => {
      const target = String(args?.path ?? args?.command ?? args?.pattern ?? "").trim();
      const title = theme?.fg ? theme.fg("toolTitle", theme.bold(name)) : name;
      const detail = target && theme?.fg ? theme.fg("muted", ` ${target}`) : target ? ` ${target}` : "";
      return new ForgeText(`${title}${detail}`);
    },
    renderResult: (result: ForgeToolResult, options: { isPartial?: boolean }, theme: any) => {
      if (options?.isPartial) return new ForgeText(theme?.fg ? theme.fg("warning", "Processing…") : "Processing…");
      const text = result.isError ? result.content[0].text : "✓ Done";
      const color = result.isError ? "error" : "success";
      return new ForgeText(theme?.fg ? theme.fg(color, text) : text);
    },
  };
}

export default function forgeToolsExtension(pi: any) {
  // Pi's ExtensionAPI does not expose getCwd(); project extensions are loaded
  // with the project as the process working directory. Keep the optional shim
  // for standalone hosts and tests that explicitly provide a workspace.
  const workspace = resolve(typeof pi.getCwd === "function" ? pi.getCwd() : process.cwd());
  const within = (p: string) => pathInWorkspace(workspace, p);
  const registered = new Map<string, any>();
  const register = (definition: any) => { registered.set(definition.name, definition); pi.registerTool(definition); };
  register(tool("forge_read", "Read bounded UTF-8 text from a workspace file.", { ...common, required: ["path"], properties: { path: schemasPath, offset: { type: "number", minimum: 1 }, limit: { type: "number", minimum: 1 } } }, async (p, s) => { const path = within(p.path); const lines = (await textFile(path, s)).split("\n"); const start = Math.max(0, (p.offset ?? 1) - 1); if (start >= lines.length) throw new Error("offset is beyond end of file"); const selected = lines.slice(start, p.limit ? start + p.limit : undefined); return ok(selected.map((x: string, i: number) => `${start + i + 1}\t${x}`).join("\n"), { path, startLine: start + 1, lineCount: selected.length }); }));
  register(tool("forge_write", "Create a workspace file; refuses overwrite by default.", { ...common, required: ["path", "content"], properties: { path: schemasPath, content: { type: "string" }, overwrite: { type: "boolean", default: false } } }, async (p, s) => { const path = within(p.path); if (cancelled(s)) throw new Error("operation cancelled"); try { await access(path, constants.F_OK); if (!p.overwrite) throw new Error("file exists; set overwrite=true"); } catch (e: any) { if (e?.code !== "ENOENT") throw e; } await mkdir(dirname(path), { recursive: true }); const tmp = `${path}.pi-tmp-${process.pid}-${Date.now()}`; await writeFile(tmp, p.content, "utf8"); await rename(tmp, path); return ok(`Wrote ${relative(workspace, path)} (${Buffer.byteLength(p.content)} bytes)`, { path, bytes: Buffer.byteLength(p.content) }); }));
  register(tool("forge_edit", "Replace an exact string in a workspace file.", { ...common, required: ["path", "old_string", "new_string"], properties: { path: schemasPath, old_string: { type: "string", minLength: 1 }, new_string: { type: "string" }, replace_all: { type: "boolean", default: false } } }, async (p, s) => { const path = within(p.path); const before = await textFile(path, s); const count = before.split(p.old_string).length - 1; if (!count) throw new Error("old_string was not found"); if (count > 1 && !p.replace_all) throw new Error(`old_string matched ${count} times; set replace_all=true`); const after = p.replace_all ? before.split(p.old_string).join(p.new_string) : before.replace(p.old_string, p.new_string); await writeFile(path, after, "utf8"); return ok(`Edited ${relative(workspace, path)} (${p.replace_all ? count : 1} replacement)`, { path, replacements: p.replace_all ? count : 1 }); }));
  register(tool("forge_ls", "List workspace directory entries.", { ...common, properties: { path: schemasPath } }, async (p) => { const path = within(p.path ?? "."); const entries = await readdir(path, { withFileTypes: true }); return ok(entries.sort((a, b) => a.name.localeCompare(b.name)).map(e => `${e.isDirectory() ? "d" : "f"}\t${e.name}`).join("\n"), { path, count: entries.length }); }));
  register(tool("forge_find", "Find files below a workspace directory.", { ...common, properties: { path: schemasPath, name: { type: "string" }, max_results: { type: "number", minimum: 1, maximum: 1000 } } }, async (p, s) => { const root = within(p.path ?? "."); const found: string[] = []; async function walk(dir: string): Promise<void> { if (cancelled(s)) throw new Error("operation cancelled"); for (const e of await readdir(dir, { withFileTypes: true })) { if (e.name === ".git" || e.name === "node_modules") continue; const full = resolve(dir, e.name); if (e.isDirectory()) await walk(full); else if (!p.name || e.name === p.name || e.name.includes(p.name)) { found.push(relative(workspace, full)); if (found.length >= (p.max_results ?? 200)) return; } if (found.length >= (p.max_results ?? 200)) return; } } await walk(root); return ok(found.join("\n"), { count: found.length }); }));
  register(tool("forge_grep", "Search text in workspace files.", { ...common, required: ["pattern"], properties: { pattern: { type: "string" }, path: schemasPath, glob: { type: "string" }, max_results: { type: "number", minimum: 1, maximum: 1000 } } }, async (p, s) => { const base = within(p.path ?? "."); const cmd = `grep -RInI --exclude-dir=.git --exclude-dir=node_modules ${shellQuote(p.pattern)} ${shellQuote(base)}`; const r = await run(cmd, workspace, s, 30); const lines = r.out.trim().split("\n").filter(Boolean).slice(0, p.max_results ?? 200); return ok(lines.join("\n"), { count: lines.length, truncated: lines.length >= (p.max_results ?? 200) }); }));
  register(tool("forge_bash", "Run a shell command in the workspace with a required timeout.", { ...common, required: ["command"], properties: { command: { type: "string" }, timeout: { type: "number", minimum: 1, maximum: 300 } } }, async (p, s) => { const r = await run(p.command, workspace, s, p.timeout ?? 30); return r.code === 0 ? ok(r.out, { exitCode: r.code }) : fail(`${r.out}\n(exit ${r.code})`, "command_failed"); }));
  register(tool("forge_diff", "Show a unified diff between two workspace files.", { ...common, required: ["a", "b"], properties: { a: schemasPath, b: schemasPath } }, async (p, s) => { const r = await run(`diff -u ${shellQuote(within(p.a))} ${shellQuote(within(p.b))} || test $? -eq 1`, workspace, s, 30); return ok(r.out, { different: r.out.length > 0 }); }));
  register(tool("forge_protected_write", "Write only on non-protected git branches unless explicitly approved.", { ...common, required: ["path", "content"], properties: { path: schemasPath, content: { type: "string" }, overwrite: { type: "boolean" }, allow_protected: { type: "boolean", description: "Explicit approval to write on main/master." } } }, async (p, s) => { const branch = (await run("git branch --show-current", workspace, s, 10)).out.trim(); if ((branch === "main" || branch === "master") && !p.allow_protected) throw new Error(`protected branch ${branch}; set allow_protected=true only with explicit approval`); return (await registered.get("forge_write")?.execute("protected-write", p, s)) ?? fail("forge_write is unavailable"); }));
}
function shellQuote(value: string) { return `'${value.replaceAll("'", "'\\''")}'`; }
