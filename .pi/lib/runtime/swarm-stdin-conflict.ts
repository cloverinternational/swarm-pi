/**
 * Port of Swarm's builtin stdin-conflict hook (pipe + heredoc feeding the
 * same simple command). Source of truth:
 * swarm-sdk internal/hooks/builtin/stdin_conflict_hook.go
 *   hasPipeHeredocStdinConflict, parseHeredocDelim, consumeHeredocBodies,
 *   skip{Single,Double,Back}Quoted, stdinConflict{Advisory,Block}Message,
 *   maxStdinConflictAdvisories, SWARM_STDIN_CONFLICT_MODE.
 * Hook name "stdin-conflict-advisory"; advisory is a pre-tool
 * ContinueWithMessage, which agent_tools.go embeds INTO the tool result as
 * `<system-reminder …>` + "\n\n---\n\n" + output.
 */

export const STDIN_CONFLICT_HOOK_NAME = "stdin-conflict-advisory";
export const MAX_STDIN_CONFLICT_ADVISORIES = 3;

interface HeredocDelim { word: string; stripTabs: boolean }

const isShellSpace = (c: string) => c === " " || c === "\t" || c === "\r" || c === "\n";
const isDelimTerminator = (c: string) => [" ", "\t", "\n", ";", "|", "&", "<", ">", "(", ")"].includes(c);

function skipSingleQuoted(s: string[], i: number): number { for (let j = i + 1; j < s.length; j++) if (s[j] === "'") return j; return -1; }
function skipDoubleQuoted(s: string[], i: number): number { for (let j = i + 1; j < s.length; j++) { if (s[j] === "\\") { j++; continue; } if (s[j] === "\"") return j; } return -1; }
function skipBackquoted(s: string[], i: number): number { for (let j = i + 1; j < s.length; j++) { if (s[j] === "\\") { j++; continue; } if (s[j] === "`") return j; } return -1; }

function parseHeredocDelim(s: string[], i: number): [HeredocDelim, number, boolean] {
  const n = s.length;
  let j = i + 2;
  const d: HeredocDelim = { word: "", stripTabs: false };
  if (j < n && s[j] === "-") { d.stripTabs = true; j++; }
  while (j < n && (s[j] === " " || s[j] === "\t")) j++;
  if (j >= n) return [d, j, false];
  let word = "";
  while (j < n) {
    const c = s[j];
    if (c === "'") { const k = skipSingleQuoted(s, j); if (k < 0) return [d, j, false]; word += s.slice(j + 1, k).join(""); j = k + 1; }
    else if (c === "\"") { const k = skipDoubleQuoted(s, j); if (k < 0) return [d, j, false]; word += s.slice(j + 1, k).join(""); j = k + 1; }
    else if (c === "\\") { if (j + 1 >= n) return [d, j, false]; word += s[j + 1]; j += 2; }
    else if (isDelimTerminator(c)) { d.word = word; return [d, j, d.word !== ""]; }
    else { word += c; j++; }
  }
  d.word = word;
  return [d, j, d.word !== ""];
}

function consumeHeredocBodies(s: string[], start: number, delims: HeredocDelim[]): number {
  const n = s.length;
  let pos = start;
  for (const d of delims) {
    while (pos <= n) {
      let lineEnd = pos;
      while (lineEnd < n && s[lineEnd] !== "\n") lineEnd++;
      let line = s.slice(pos, lineEnd).join("");
      if (d.stripTabs) line = line.replace(/^\t+/, "");
      if (line.replace(/\r+$/, "") === d.word) { pos = lineEnd; if (pos < n) pos++; break; }
      if (lineEnd >= n) return n;
      pos = lineEnd + 1;
    }
  }
  return pos;
}

export function hasPipeHeredocStdinConflict(command: string): boolean {
  if (command === "") return false;
  if (!command.includes("|") || !command.includes("<<")) return false;
  const s = [...command];
  const n = s.length;
  let depth = 0;
  let pipedSegment = false;
  let segHasContent = false;
  let pendingHeredocs: HeredocDelim[] = [];
  for (let i = 0; i < n; i++) {
    const c = s[i];
    if (c === "\\") { i++; segHasContent = true; }
    else if (c === "'") { const j = skipSingleQuoted(s, i); if (j < 0) return false; i = j; segHasContent = true; }
    else if (c === "\"") { const j = skipDoubleQuoted(s, i); if (j < 0) return false; i = j; segHasContent = true; }
    else if (c === "`") { const j = skipBackquoted(s, i); if (j < 0) return false; i = j; segHasContent = true; }
    else if (c === "$" && i + 1 < n && s[i + 1] === "(") { depth++; i++; segHasContent = true; }
    else if (c === "(" && depth > 0) { depth++; segHasContent = true; }
    else if (c === ")" && depth > 0) { depth--; segHasContent = true; }
    else if (depth > 0) { if (!isShellSpace(c)) segHasContent = true; }
    else if (c === "\n") {
      if (pendingHeredocs.length > 0) { i = consumeHeredocBodies(s, i + 1, pendingHeredocs) - 1; pendingHeredocs = []; }
      if (segHasContent) { pipedSegment = false; segHasContent = false; }
    }
    else if (c === "<") {
      if (i + 2 < n && s[i + 1] === "<" && s[i + 2] === "<") { i += 2; segHasContent = true; continue; }
      if (i + 1 < n && s[i + 1] === "<") {
        const [delim, next, ok] = parseHeredocDelim(s, i);
        if (!ok) return false;
        if (pipedSegment) return true;
        pendingHeredocs.push(delim);
        i = next - 1;
        segHasContent = true;
        continue;
      }
      segHasContent = true;
    }
    else if (c === "|") {
      if (i + 1 < n && s[i + 1] === "|") { i++; pipedSegment = false; segHasContent = false; continue; }
      pipedSegment = true; segHasContent = false;
    }
    else if (c === "&") { if (i + 1 < n && s[i + 1] === "&") i++; pipedSegment = false; segHasContent = false; }
    else if (c === ";") { pipedSegment = false; segHasContent = false; }
    else if (!isShellSpace(c)) segHasContent = true;
  }
  return false;
}

export const stdinConflictAdvisoryMessage = () =>
  "[stdin conflict] This command gives the same command two stdin sources: a pipe on the left and a heredoc on the right. " +
  "Bash applies the last redirection and silently discards the other input, which usually fails in a confusing way.\n\n" +
  "If you meant to send the piped data, drop the heredoc. If you meant to send the heredoc, drop the pipe " +
  "(e.g. inline the data, or use `ssh host 'cmd' < file`). Running as-is — this is advice, not a block.";

export const stdinConflictBlockMessage = () =>
  "[stdin conflict] Refusing to run: this command gives the same command two stdin sources, a pipe on the left and a heredoc on the right. " +
  "Bash applies the last redirection and silently discards the other input, so this would return plausible output that is missing half its input.\n\n" +
  "Fix one of the two: if you meant to send the piped data, drop the heredoc; if you meant to send the heredoc, drop the pipe " +
  "(e.g. inline the data, or use `ssh host 'cmd' < file`). Then run the command again.";

export type StdinConflictMode = "advise" | "block";
export const stdinConflictModeFromEnv = (env: NodeJS.ProcessEnv = process.env): StdinConflictMode =>
  (env.SWARM_STDIN_CONFLICT_MODE ?? "").trim().toLowerCase() === "block" ? "block" : "advise";

/** One instance per session, like Swarm's hook (advisory budget is per instance). */
export class StdinConflictHook {
  private advisoryCount = 0;
  constructor(private readonly mode: StdinConflictMode = stdinConflictModeFromEnv()) {}
  /** Returns { block } or { advise } or undefined to continue silently. */
  onBashCommand(command: string | undefined): { block: string } | { advise: string } | undefined {
    if (!command || !hasPipeHeredocStdinConflict(command)) return undefined;
    if (this.mode === "block") return { block: stdinConflictBlockMessage() };
    if (++this.advisoryCount > MAX_STDIN_CONFLICT_ADVISORIES) return undefined;
    return { advise: stdinConflictAdvisoryMessage() };
  }
}
