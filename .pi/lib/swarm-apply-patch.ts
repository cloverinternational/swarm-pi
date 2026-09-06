import {
  chmodSync,
  existsSync,
  lstatSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  realpathSync,
  renameSync,
  rmSync,
  statSync,
  writeFileSync,
} from "node:fs";
import { dirname, basename, isAbsolute, join, relative, resolve, sep } from "node:path";
import { tmpdir } from "node:os";

export type PatchKind = "add" | "update" | "delete";
export interface PatchChunk { ctxOffset: number; del: string[]; ins: string[] }
export interface PatchHunk { anchors: string[]; context: string[]; chunks: PatchChunk[]; eof: boolean }
export interface PatchOp { kind: PatchKind; path: string; moveTo: string; addBody: string; hunks: PatchHunk[] }
export interface ApplyPatchOptions { workspacePath?: string; cwd?: string; signal?: AbortSignal }

const BEGIN = "*** Begin Patch", END = "*** End Patch", EOF = "*** End of File";
const UPDATE = "*** Update File: ", DELETE = "*** Delete File: ", ADD = "*** Add File: ", MOVE = "*** Move to: ";
const section = (line: string) => line.startsWith(UPDATE) || line.startsWith(DELETE) || line.startsWith(ADD);

function parseHunkBody(lines: string[], start: number, path: string) {
  const context: string[] = [], chunks: PatchChunk[] = [];
  let del: string[] = [], ins: string[] = [], mode = "keep", i = start, eof = false;
  const flush = () => {
    if (del.length || ins.length) chunks.push({ ctxOffset: context.length - del.length, del, ins });
    del = []; ins = [];
  };
  while (i < lines.length) {
    const raw = lines[i];
    if (raw.startsWith("@@") || section(raw)) break;
    if (raw === EOF) { i++; eof = true; break; }
    if (raw.startsWith("***")) throw new Error(`update ${path}: invalid line ${JSON.stringify(raw)}`);
    i++;
    const line = raw === "" ? " " : raw;
    const last = mode;
    if (line[0] === "+") mode = "add";
    else if (line[0] === "-") mode = "delete";
    else if (line[0] === " ") mode = "keep";
    else throw new Error(`update ${path}: hunk line must start with '+', '-', or ' ': ${JSON.stringify(raw)}`);
    if (mode === "keep" && last !== mode) flush();
    const content = line.slice(1);
    if (mode === "delete") { del.push(content); context.push(content); }
    else if (mode === "add") ins.push(content);
    else context.push(content);
  }
  flush();
  return { context, chunks, next: i, eof };
}

function parseHunks(lines: string[], start: number, path: string): [PatchHunk[], number] {
  const hunks: PatchHunk[] = [];
  let i = start;
  while (i < lines.length && !section(lines[i])) {
    if (lines[i].trim() === "") { i++; continue; }
    const anchors: string[] = [];
    while (i < lines.length && lines[i].startsWith("@@")) {
      let anchor = lines[i].slice(2);
      if (anchor.startsWith(" ")) anchor = anchor.slice(1);
      if (anchor.trim() !== "") anchors.push(anchor);
      i++;
    }
    const body = parseHunkBody(lines, i, path);
    if (!body.context.length && !body.chunks.length && !anchors.length) break;
    hunks.push({ anchors, context: body.context, chunks: body.chunks, eof: body.eof });
    i = body.next;
  }
  return [hunks, i];
}

/** Parse the Codex V4A envelope exactly as Swarm's forge parser does. */
export function parsePatch(input: string): PatchOp[] {
  input = input.replace(/\r\n/g, "\n");
  let lines = input.trim().split("\n");
  if (lines.length >= 4) {
    const first = lines[0].trim(), last = lines.at(-1)!.trim();
    if ((first === "<<EOF" || first === "<<'EOF'" || first === '<<"EOF"') && last.endsWith("EOF")) lines = lines.slice(1, -1);
  }
  if (!lines.length || lines[0].trim() !== BEGIN) throw new Error(`The first line of the patch must be ${JSON.stringify(BEGIN)}`);
  if (lines.at(-1)!.trim() !== END) throw new Error(`The last line of the patch must be ${JSON.stringify(END)}`);
  lines = lines.slice(1, -1);
  const ops: PatchOp[] = [];
  for (let i = 0; i < lines.length;) {
    const raw = lines[i], header = raw.trim();
    if (header === "") { i++; continue; }
    if (header.startsWith(UPDATE)) {
      const path = header.slice(UPDATE.length).trim();
      if (!path) throw new Error("update: missing path");
      const op: PatchOp = { kind: "update", path, moveTo: "", addBody: "", hunks: [] };
      i++;
      if (i < lines.length && lines[i].trim().startsWith(MOVE)) {
        op.moveTo = lines[i].trim().slice(MOVE.length).trim();
        if (!op.moveTo) throw new Error(`update ${path}: empty move target`);
        i++;
      }
      [op.hunks, i] = parseHunks(lines, i, path);
      if (!op.hunks.length) throw new Error(`update ${path}: no hunks`);
      ops.push(op);
    } else if (header.startsWith(DELETE)) {
      const path = header.slice(DELETE.length).trim();
      if (!path) throw new Error("delete: missing path");
      ops.push({ kind: "delete", path, moveTo: "", addBody: "", hunks: [] }); i++;
    } else if (header.startsWith(ADD)) {
      const path = header.slice(ADD.length).trim();
      if (!path) throw new Error("add: missing path");
      const body: string[] = []; i++;
      while (i < lines.length && !section(lines[i].trim())) {
        if (!lines[i].startsWith("+")) throw new Error(`add ${path}: line must start with '+': ${JSON.stringify(lines[i])}`);
        body.push(lines[i].slice(1)); i++;
      }
      if (!body.length) throw new Error(`add ${path}: file hunk is empty`);
      ops.push({ kind: "add", path, moveTo: "", addBody: body.join("\n") + "\n", hunks: [] });
    } else throw new Error(`unknown patch line: ${JSON.stringify(raw)}`);
  }
  return ops;
}

const normalize = (s: string) => s.trim().replace(/[\u2010-\u2015\u2212]/g, "-")
  .replace(/[\u2018-\u201b]/g, "'").replace(/[\u201c-\u201f]/g, '"')
  .replace(/[\u00a0\u2002-\u200a\u202f\u205f\u3000]/g, " ");
function lineEqual(a: string, b: string, tier: number) {
  if (tier === 0) return a === b;
  if (tier === 1) return a.replace(/[ \t]+$/, "") === b.replace(/[ \t]+$/, "");
  if (tier === 2) return a.trim() === b.trim();
  return normalize(a) === normalize(b);
}
function matchAt(file: string[], context: string[], at: number, tier: number) {
  return at >= 0 && at + context.length <= file.length && context.every((line, j) => lineEqual(file[at + j], line, tier));
}
function findContext(file: string[], context: string[], cursor: number, eof: boolean, anchored: boolean): [number, number] {
  if (!context.length) return [cursor, 0];
  if (eof) {
    const end = file.length - context.length;
    for (let tier = 0; tier <= 3; tier++) if (end >= cursor && matchAt(file, context, end, tier)) return [end, tier];
  }
  for (let tier = 0; tier <= 3; tier++) {
    const found: number[] = [];
    for (let i = cursor; i + context.length <= file.length; i++) if (matchAt(file, context, i, tier)) found.push(i);
    if (anchored && found.length) return [found[0], tier];
    if (found.length > 1) throw new Error(`ambiguous context at lines ${found.map(i => i + 1).join(", ")} for:\n${context.join("\n")}\nAdd an @@ class/function anchor or more surrounding lines`);
    if (found.length === 1) return [found[0], tier];
  }
  throw new Error(`context not found:\n${context.join("\n")}`);
}
function anchorMatches(line: string, snippet: string, tier: number) {
  if (tier === 0) return line === snippet;
  if (tier === 1) return line.replace(/[ \t]+$/, "") === snippet.replace(/[ \t]+$/, "");
  if (tier === 2) return line.trim() === snippet.trim();
  return line.includes(snippet);
}
function findAnchor(file: string[], anchor: string, cursor: number): [number, number] {
  const snippets = anchor.split("\\n");
  for (let tier = 0; tier <= 3; tier++) {
    const found: number[] = [];
    for (let i = cursor; i + snippets.length <= file.length; i++)
      if (snippets.every((s, n) => anchorMatches(file[i + n], s, tier))) found.push(i);
    if (found.length > 1) throw new Error(`ambiguous @@ anchor at lines ${found.map(i => i + 1).join(", ")}: ${JSON.stringify(anchor)}; use a longer or multiline anchor`);
    if (found.length === 1) return [found[0], tier];
  }
  throw new Error(`@@ anchor not found from line ${cursor + 1}: ${JSON.stringify(anchor)}`);
}

function repairBlankContext(file: string[], hunk: PatchHunk, cursor: number): [PatchHunk, number] | undefined {
  for (let tier = 0; tier <= 3; tier++) {
    const found: Array<{ at: number; insert: number[]; span: number }> = [];
    for (let at = cursor; at < file.length; at++) {
      let f = at; const insert: number[] = []; let ok = true;
      for (let c = 0; c < hunk.context.length; c++) {
        while (f < file.length && file[f] === "" && hunk.context[c] !== "") { insert.push(c); f++; }
        if (f >= file.length || !lineEqual(file[f], hunk.context[c], tier)) { ok = false; break; }
        f++;
      }
      if (ok && insert.length) found.push({ at, insert, span: f - at });
      if (hunk.anchors.length && found.length) break;
    }
    if (found.length === 1 || (hunk.anchors.length && found.length)) {
      const a = found[0], context: string[] = [];
      for (let c = 0, n = 0; c <= hunk.context.length; c++) {
        while (n < a.insert.length && a.insert[n] === c) { context.push(""); n++; }
        if (c < hunk.context.length) context.push(hunk.context[c]);
      }
      const chunks = hunk.chunks.map(ch => ({
        ...ch, ctxOffset: ch.ctxOffset + a.insert.filter(pos => pos <= ch.ctxOffset).length,
      }));
      const repaired = { ...hunk, context, chunks };
      if (context.length === a.span && matchAt(file, context, a.at, 0)) return [repaired, a.at];
      return;
    }
    if (found.length > 1) return;
  }
}

function applyHunks(original: string, hunks: PatchHunk[], path: string): string {
  const crlf = original.includes("\r\n"), file = original.replace(/\r\n/g, "\n").split("\n");
  const out: string[] = []; let cursor = 0;
  for (let hunk of hunks) {
    for (const anchor of hunk.anchors) {
      let idx: number;
      try { [idx] = findAnchor(file, anchor, cursor); }
      catch (e) { throw new Error(`update ${path}: ${(e as Error).message}`); }
      out.push(...file.slice(cursor, idx)); cursor = idx;
    }
    let at: number;
    try { [at] = findContext(file, hunk.context, cursor, hunk.eof, hunk.anchors.length > 0); }
    catch (e) {
      const repaired = repairBlankContext(file, hunk, cursor);
      if (!repaired) throw new Error(`update ${path}: ${(e as Error).message}`);
      [hunk, at] = repaired;
    }
    const pureAdd = hunk.context.length === 0 && hunk.chunks.length > 0 && hunk.chunks.every(c => !c.del.length);
    if (pureAdd) at = hunk.anchors.length ? cursor : file.length - (file.at(-1) === "" ? 1 : 0);
    out.push(...file.slice(cursor, at));
    let ctxCursor = at;
    for (const chunk of hunk.chunks) {
      const chunkAt = at + chunk.ctxOffset;
      if (chunkAt < ctxCursor) throw new Error(`update ${path}: overlapping hunk chunks`);
      out.push(...file.slice(ctxCursor, chunkAt), ...chunk.ins);
      ctxCursor = chunkAt + chunk.del.length;
    }
    out.push(...file.slice(ctxCursor, at + hunk.context.length));
    cursor = at + hunk.context.length;
  }
  out.push(...file.slice(cursor));
  let result = out.join("\n");
  if (result && !result.endsWith("\n")) result += "\n";
  return crlf ? result.replace(/\n/g, "\r\n") : result;
}

function within(root: string, target: string) {
  const rel = relative(root, target);
  return rel === "" || (rel !== ".." && !rel.startsWith(`..${sep}`) && !isAbsolute(rel));
}
function canonicalProspective(path: string) {
  let parent = resolve(path);
  while (!existsSync(parent)) {
    const next = dirname(parent);
    if (next === parent) throw new Error(`no existing parent for ${path}`);
    parent = next;
  }
  return resolve(realpathSync(parent), relative(parent, resolve(path)));
}
function gitCommonDir(dir: string): string {
  let current = resolve(dir);
  for (;;) {
    const dot = join(current, ".git");
    if (existsSync(dot)) {
      if (lstatSync(dot).isDirectory()) return realpathSync(dot);
      const line = readFileSync(dot, "utf8").trim();
      if (line.startsWith("gitdir:")) {
        let git = line.slice(7).trim();
        if (!isAbsolute(git)) git = resolve(dirname(dot), git);
        const parent = dirname(git);
        return realpathSync.native?.(basename(parent) === "worktrees" ? dirname(parent) : git) ?? realpathSync(basename(parent) === "worktrees" ? dirname(parent) : git);
      }
    }
    const parent = dirname(current); if (parent === current) return ""; current = parent;
  }
}
function resolvePatchPath(workspace: string, base: string, requested: string) {
  if (!requested) throw new Error(`cannot resolve ${JSON.stringify(requested)}: empty path`);
  let abs = isAbsolute(requested) ? resolve(requested) : resolve(base, requested);
  if (workspace && !existsSync(abs)) {
    const prefix = join(resolve(workspace), basename(resolve(workspace))) + sep;
    if (abs.startsWith(prefix)) {
      const healed = join(resolve(workspace), abs.slice(prefix.length));
      if (existsSync(healed)) abs = healed;
    }
  }
  let canonical: string;
  try { canonical = canonicalProspective(abs); }
  catch (e) { throw new Error(`resolve ${JSON.stringify(requested)}: ${(e as Error).message}`); }
  const ws = workspace ? realpathSync(resolve(workspace)) : "";
  if (ws && !within(ws, canonical) && !(base !== workspace && gitCommonDir(ws) && gitCommonDir(ws) === gitCommonDir(canonical)))
    throw new Error(`path ${JSON.stringify(requested)} resolves to ${canonical}, which is outside the workspace ${ws}.\nUse a workspace-relative path, or pass cwd=<dir> to target a linked git worktree of the same repository.`);
  return canonical;
}

interface State { exists: boolean; isDir: boolean; content: string; mode: number }
interface Change { op: PatchOp; absPath: string; absDest: string; oldContent: string; oldMode: number; newContent: string; destExisted: boolean; destOldContent: string; destOldMode: number }
function state(path: string): State {
  if (!existsSync(path)) return { exists: false, isDir: false, content: "", mode: 0o644 };
  const st = lstatSync(path);
  return { exists: true, isDir: st.isDirectory(), content: st.isDirectory() ? "" : readFileSync(path, "utf8"), mode: st.mode & 0o777 };
}
function conflictBlocks(s: string) {
  let blocks = 0, conflict = false, separator = false;
  for (const line of s.replace(/\r\n/g, "\n").split("\n")) {
    if (line.startsWith("<<<<<<<")) { conflict = true; separator = false; }
    else if (conflict && line.startsWith("=======")) separator = true;
    else if (conflict && separator && line.startsWith(">>>>>>>")) { blocks++; conflict = separator = false; }
  }
  return blocks;
}
function atomicWrite(path: string, content: string, mode: number) {
  mkdirSync(dirname(path), { recursive: true, mode: 0o755 });
  const dir = mkdtempSync(join(dirname(path), ".apply-patch-"));
  const temp = join(dir, "file");
  try { writeFileSync(temp, content, { mode }); chmodSync(temp, mode); renameSync(temp, path); }
  finally { rmSync(dir, { recursive: true, force: true }); }
}

let snapshotCounter = 0n;
const snapshotName = (path: string) => `${basename(path)}-${BigInt(Date.now()) * 1_000_000n + process.hrtime.bigint() % 1_000_000n + snapshotCounter++}.bak`;
function snapshot(workspace: string, path: string, existed: boolean, content: string) {
  if (!workspace) return;
  const dir = join(workspace, ".swarm", "snapshots");
  mkdirSync(dir, { recursive: true, mode: 0o700 }); chmodSync(dir, 0o700);
  const ignore = join(dir, ".gitignore");
  if (!existsSync(ignore)) writeFileSync(ignore, "# Auto-generated by swarm: agent file snapshots — not for version control.\n*\n", { mode: 0o644 });
  const name = snapshotName(path), bak = join(dir, name);
  writeFileSync(bak, content, { mode: 0o600 });
  writeFileSync(bak.slice(0, -4) + ".ctx.json", JSON.stringify({
    tool_name: "apply_patch", file_path: path, timestamp: new Date().toISOString(), ...(existed ? {} : { new_file: true }),
  }, null, 2), { mode: 0o644 });
}

/** Preflight and transactionally apply every operation in a V4A patch. */
export async function applyPatch(input: string, options: ApplyPatchOptions = {}): Promise<string> {
  if (options.signal?.aborted) throw options.signal.reason ?? new Error("context canceled");
  if (!input.trim()) throw new Error("input is required");
  const workspace = resolve(options.workspacePath ?? process.cwd());
  const cwd = options.cwd?.trim() ? resolve(options.cwd.trim()) : workspace;
  if (options.cwd?.trim()) {
    if (!existsSync(cwd)) throw new Error(`cwd ${JSON.stringify(options.cwd.trim())} is not usable: stat ${options.cwd.trim()}: no such file or directory`);
    if (!statSync(cwd).isDirectory()) throw new Error(`cwd ${JSON.stringify(options.cwd.trim())} is not a directory`);
  }
  const ops = parsePatch(input);
  if (!ops.length) throw new Error("No files were modified.");
  const virtual = new Map<string, State>(), load = (p: string) => virtual.get(p) ?? (() => { const s = state(p); virtual.set(p, s); return s; })();
  const changes: Change[] = [];
  for (const op of ops) {
    const absPath = resolvePatchPath(workspace, cwd, op.path), source = load(absPath);
    const ch: Change = { op, absPath, absDest: absPath, oldContent: "", oldMode: 0o644, newContent: "", destExisted: false, destOldContent: "", destOldMode: 0 };
    if (op.kind === "add") {
      if (source.isDir) throw new Error(`${op.path} is a directory`);
      ch.destExisted = source.exists;
      if (source.exists) Object.assign(ch, { oldContent: source.content, oldMode: source.mode, destOldContent: source.content, destOldMode: source.mode });
      ch.newContent = op.addBody;
      virtual.set(absPath, { exists: true, isDir: false, content: ch.newContent, mode: ch.oldMode });
    } else {
      if (!source.exists) throw new Error(`${op.path}: file not found`);
      if (source.isDir) throw new Error(`${op.path} is a directory`);
      ch.oldContent = source.content; ch.oldMode = source.mode;
      if (op.kind === "delete") virtual.set(absPath, { ...source, exists: false });
      else {
        ch.newContent = applyHunks(ch.oldContent, op.hunks, op.path);
        if (op.moveTo) {
          const dest = resolvePatchPath(workspace, cwd, op.moveTo);
          if (dest !== absPath) {
            const ds = load(dest);
            if (ds.isDir) throw new Error(`move target ${op.moveTo} is a directory`);
            Object.assign(ch, { absDest: dest, destExisted: ds.exists, destOldContent: ds.content, destOldMode: ds.mode });
            virtual.set(absPath, { ...source, exists: false });
            virtual.set(dest, { exists: true, isDir: false, content: ch.newContent, mode: source.mode });
          } else virtual.set(absPath, { ...source, content: ch.newContent });
        } else virtual.set(absPath, { ...source, content: ch.newContent });
      }
    }
    if (op.kind !== "delete" && conflictBlocks(ch.newContent) > conflictBlocks(ch.oldContent))
      throw new Error(`${op.path} introduces unresolved merge conflict markers`);
    changes.push(ch);
  }
  const seen = new Set<string>();
  for (const ch of changes) {
    const take = (p: string, existed: boolean, body: string) => { if (!seen.has(p)) { snapshot(workspace, p, existed, body); seen.add(p); } };
    if (ch.op.kind === "add") take(ch.absDest, ch.destExisted, ch.destOldContent);
    else { take(ch.absPath, true, ch.oldContent); if (ch.op.kind === "update" && ch.absDest !== ch.absPath) take(ch.absDest, ch.destExisted, ch.destOldContent); }
  }
  const undo: Array<() => void> = [], added: string[] = [], modified: string[] = [], deleted: string[] = [];
  try {
    for (const ch of changes) {
      if (ch.op.kind === "add") {
        atomicWrite(ch.absDest, ch.newContent, ch.oldMode || 0o644);
        undo.push(() => ch.destExisted ? atomicWrite(ch.absDest, ch.destOldContent, ch.destOldMode) : rmSync(ch.absDest, { force: true }));
        added.push(ch.op.path);
      } else if (ch.op.kind === "delete") {
        rmSync(ch.absPath);
        undo.push(() => atomicWrite(ch.absPath, ch.oldContent, ch.oldMode)); deleted.push(ch.op.path);
      } else {
        atomicWrite(ch.absDest, ch.newContent, ch.oldMode);
        if (ch.absDest !== ch.absPath) {
          undo.push(() => ch.destExisted ? atomicWrite(ch.absDest, ch.destOldContent, ch.destOldMode) : rmSync(ch.absDest, { force: true }));
          rmSync(ch.absPath); undo.push(() => atomicWrite(ch.absPath, ch.oldContent, ch.oldMode)); modified.push(ch.op.moveTo);
        } else { undo.push(() => atomicWrite(ch.absPath, ch.oldContent, ch.oldMode)); modified.push(ch.op.path); }
      }
    }
  } catch (cause) {
    const failures: string[] = [];
    for (const fn of undo.reverse()) try { fn(); } catch (e) { failures.push(String(e)); }
    if (failures.length) throw new Error(`apply failed (${String(cause)}); rollback failures: ${failures.join("; ")} — inspect these paths`);
    throw cause;
  }
  return "Success. Updated the following files:\n" +
    added.map(p => `A ${p}\n`).join("") + modified.map(p => `M ${p}\n`).join("") + deleted.map(p => `D ${p}\n`).join("");
}

/** Consume the latest forge-compatible snapshot for a path. */
export async function undoFile(path: string, workspacePath = process.cwd()): Promise<string> {
  const abs = resolve(path), dir = join(resolve(workspacePath), ".swarm", "snapshots");
  if (!existsSync(dir)) throw new Error(`no snapshot found for ${abs} (no edits have been tracked yet)`);
  const prefix = `${basename(abs)}-`;
  const entries = (await import("node:fs")).readdirSync(dir).filter(n => n.startsWith(prefix) && n.endsWith(".bak"))
    .map(name => ({ name, ts: BigInt(name.slice(prefix.length, -4)) })).sort((a, b) => a.ts > b.ts ? -1 : a.ts < b.ts ? 1 : 0);
  if (!entries.length) throw new Error(`no snapshot found for ${abs}`);
  const bak = join(dir, entries[0].name), ctxPath = bak.slice(0, -4) + ".ctx.json";
  let ctx: any;
  try { ctx = JSON.parse(readFileSync(ctxPath, "utf8")); } catch {}
  let message: string;
  if (ctx?.new_file) {
    rmSync(abs, { force: true });
    message = `Successfully deleted ${abs} (was a newly created file — no prior state to restore)`;
  } else {
    writeFileSync(abs, readFileSync(bak), { mode: 0o644 });
    message = `Successfully reverted ${abs}`;
  }
  const parts: string[] = [];
  if (ctx?.tool_name) parts.push(`tool: ${ctx.tool_name}`);
  if (ctx?.conversation_id) parts.push(`conversation: ${ctx.conversation_id}`);
  if (ctx?.user_message) parts.push(`because: ${JSON.stringify(ctx.user_message)}`);
  if (ctx?.timestamp) parts.push(`at: ${ctx.timestamp}`);
  rmSync(bak, { force: true }); rmSync(ctxPath, { force: true });
  return message + (parts.length ? `\n${parts.join(" | ")}` : "");
}
