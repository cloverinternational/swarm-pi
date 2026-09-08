/**
 * Port of swarm-sdk internal/hooks/toolclass.go — the shared tool/command
 * classifiers every builtin hook gates on (task-enforcement,
 * task-maintenance-reminder, skill-budget-enforcement, autogenskills).
 * Names, cases and the pragmatic shell tokenizer are mirrored 1:1 so Pi
 * exempts exactly the calls Swarm exempts.
 */

export const normalizeToolName = (name: string) => name.replace(/_/g, "").toLowerCase();

export const isTaskManagementTool = (name: string) =>
  ["taskcreate", "tasklist", "taskget", "taskupdate", "taskmanage", "todowrite", "todoread"].includes(normalizeToolName(name));
export const isTaskManageTool = (name: string) => normalizeToolName(name) === "taskmanage";
/** CodeMode is an orchestration wrapper; nested calls use the normal hook bridge. */
export const isCodeModeTool = (name: string) => normalizeToolName(name) === "codemode";
export const isSkillTool = (name: string) =>
  ["skillmanage", "skillinvoke", "skillcall", "useskill", "skillexec", "skill"].includes(normalizeToolName(name));
export const isPlanModeTool = (name: string) => ["enterplanmode", "exitplanmode"].includes(normalizeToolName(name));
export const isUserInteractionTool = (name: string) => ["askuserquestion", "pushagentupdate", "annoyed"].includes(normalizeToolName(name));
export const isReadOnlyExplorationTool = (name: string) => [
  "read", "readfile",
  "grep", "glob", "search", "websearch", "webfetch", "codesearch",
  "gitlog", "gitstatus", "gitdiff", "gitshow", "gitblame",
  "lsp", "lspdefinition", "lspreferences", "lsphover",
  "ls", "list", "listdir",
].includes(normalizeToolName(name));
export const isReadOnlyRecallTool = (name: string) => [
  "historysearch", "historyget", "findingsquery",
  "websearch", "webfetch", "xaiwebsearch", "xsearch",
  "subagentoutput", "taskoutput", "delegateoutput", "vaultlist",
].includes(normalizeToolName(name));
export const isReadOnlyResearchTool = (name: string) => isReadOnlyExplorationTool(name) || isReadOnlyRecallTool(name);
export const isBashTool = (name: string) => ["bash", "shell", "execute"].includes(normalizeToolName(name));

interface ShellHeredoc { word: string; stripTabs: boolean }

function scanDoubleQuoted(command: string, start: number): [number, string, boolean] {
  let value = "";
  for (let i = start + 1; i < command.length; i++) {
    const c = command[i];
    if (c === "\"") return [i, value, true];
    if (c === "\\") { if (i + 1 >= command.length) return [0, "", false]; i++; value += command[i]; continue; }
    if (c === "`") { const [end, ok] = skipBackticks(command, i); if (!ok) return [0, "", false]; value += "x"; i = end; continue; }
    if (c === "$" && i + 1 < command.length && command[i + 1] === "(") { const [end, ok] = skipDollarSubstitution(command, i); if (!ok) return [0, "", false]; value += "x"; i = end; continue; }
    value += c;
  }
  return [0, "", false];
}

function skipBackticks(command: string, start: number): [number, boolean] {
  for (let i = start + 1; i < command.length; i++) {
    if (command[i] === "\\") { i++; continue; }
    if (command[i] === "`") return [i, true];
  }
  return [0, false];
}

function skipDollarSubstitution(command: string, start: number): [number, boolean] {
  let depth = 1;
  for (let i = start + 2; i < command.length; i++) {
    const c = command[i];
    if (c === "\\") { i++; continue; }
    if (c === "'") { const end = command.indexOf("'", i + 1); if (end < 0) return [0, false]; i = end; continue; }
    if (c === "\"") { const [end, , ok] = scanDoubleQuoted(command, i); if (!ok) return [0, false]; i = end; continue; }
    if (c === "`") { const [end, ok] = skipBackticks(command, i); if (!ok) return [0, false]; i = end; continue; }
    if (c === "(") depth++;
    else if (c === ")") { depth--; if (depth === 0) return [i, true]; }
  }
  return [0, false];
}

function parseShellHeredoc(command: string, start: number): [ShellHeredoc, number, boolean] {
  let i = start + 2;
  const heredoc: ShellHeredoc = { word: "", stripTabs: false };
  if (i < command.length && command[i] === "-") { heredoc.stripTabs = true; i++; }
  while (i < command.length && (command[i] === " " || command[i] === "\t")) i++;
  if (i < command.length && command[i] === "\\") i++;
  if (i >= command.length) return [{ word: "", stripTabs: false }, 0, false];
  if (command[i] === "'" || command[i] === "\"") {
    const quote = command[i];
    i++;
    const startWord = i;
    while (i < command.length && command[i] !== quote) i++;
    if (i >= command.length || i === startWord) return [{ word: "", stripTabs: false }, 0, false];
    heredoc.word = command.slice(startWord, i);
    return [heredoc, i + 1, true];
  }
  const startWord = i;
  while (i < command.length && !" \t\r\n;|&<>".includes(command[i])) i++;
  if (i === startWord) return [{ word: "", stripTabs: false }, 0, false];
  heredoc.word = command.slice(startWord, i);
  return [heredoc, i, true];
}

function skipShellHeredocBodies(command: string, start: number, heredocs: ShellHeredoc[]): [number, boolean] {
  let pos = start;
  for (const heredoc of heredocs) {
    let found = false;
    while (pos <= command.length) {
      let end = command.indexOf("\n", pos);
      if (end < 0) end = command.length;
      let line = command.slice(pos, end).replace(/[ \t\r]+$/, "");
      if (heredoc.stripTabs) line = line.replace(/^\t+/, "");
      if (line === heredoc.word) { found = true; pos = end < command.length ? end + 1 : end; break; }
      if (end === command.length) break;
      pos = end + 1;
    }
    if (!found) return [0, false];
  }
  return [pos, true];
}

/** toolclass.go parseShellCommands: top-level simple commands with their argv. */
export function parseShellCommands(command: string): [string[][], boolean] {
  const commands: string[][] = [];
  let fields: string[] = [];
  let word = "";
  let pendingHeredocs: ShellHeredoc[] = [];
  const flushWord = () => { if (word.length === 0) return; fields.push(word); word = ""; };
  const flushCommand = () => { flushWord(); if (fields.length > 0) { commands.push(fields); fields = []; } };
  for (let i = 0; i < command.length; i++) {
    const c = command[i];
    switch (c) {
      case "\\":
        if (i + 1 >= command.length) return [[], false];
        i++;
        if (command[i] !== "\n") word += command[i];
        break;
      case "'": {
        const end = command.indexOf("'", i + 1);
        if (end < 0) return [[], false];
        word += command.slice(i + 1, end);
        i = end;
        break;
      }
      case "\"": {
        const [end, value, ok] = scanDoubleQuoted(command, i);
        if (!ok) return [[], false];
        word += value;
        i = end;
        break;
      }
      case "`": {
        const [end, ok] = skipBackticks(command, i);
        if (!ok) return [[], false];
        word += "x";
        i = end;
        break;
      }
      case "$":
        if (i + 1 < command.length && command[i + 1] === "(") {
          const [end, ok] = skipDollarSubstitution(command, i);
          if (!ok) return [[], false];
          word += "x";
          i = end;
          continue;
        }
        word += c;
        break;
      case "<":
        if (i + 1 < command.length && command[i + 1] === "<") {
          if (i + 2 < command.length && command[i + 2] === "<") { word += "<<<"; i += 2; continue; }
          flushWord();
          const [heredoc, next, ok] = parseShellHeredoc(command, i);
          if (!ok) return [[], false];
          pendingHeredocs.push(heredoc);
          i = next - 1;
          continue;
        }
        word += c;
        break;
      case " ": case "\t": case "\r":
        flushWord();
        break;
      case "\n":
        flushCommand();
        if (pendingHeredocs.length > 0) {
          const [next, ok] = skipShellHeredocBodies(command, i + 1, pendingHeredocs);
          if (!ok) return [[], false];
          pendingHeredocs = [];
          i = next - 1;
        }
        break;
      case ";": case "|": case "&":
        flushCommand();
        if (i + 1 < command.length && command[i + 1] === c) i++;
        break;
      default:
        word += c;
    }
  }
  flushCommand();
  return [commands, true];
}

function isShellAssignment(field: string): boolean {
  const eq = field.indexOf("=");
  if (eq <= 0) return false;
  for (let i = 0; i < eq; i++) {
    const c = field[i];
    const alpha = (c >= "a" && c <= "z") || (c >= "A" && c <= "Z") || c === "_";
    const digit = c >= "0" && c <= "9";
    if (!alpha && (i === 0 || !digit)) return false;
  }
  return true;
}

export function shellExecutableIndex(fields: string[]): number {
  for (let i = 0; i < fields.length; i++) if (!isShellAssignment(fields[i])) return i;
  return -1;
}

export const shellBase = (word: string) => { const slash = word.lastIndexOf("/"); return slash >= 0 ? word.slice(slash + 1) : word; };

/** toolclass.go ExtractShellCommandWords. */
export function extractShellCommandWords(command: string): string[] {
  const [commands, ok] = parseShellCommands(command);
  if (!ok) return [];
  const words: string[] = [];
  for (const fields of commands) { const i = shellExecutableIndex(fields); if (i >= 0) words.push(shellBase(fields[i])); }
  return words;
}

const readOnlyBinaries = new Set([
  "ls", "cat", "head", "tail", "find", "grep", "rg", "ag", "wc", "file", "stat", "which",
  "tree", "du", "df", "pwd", "echo", "printf", "date", "cd", "pushd", "popd",
]);

/** toolclass.go IsBashReadOnly — allowlist semantics, including its quirks. */
export function isBashReadOnly(cmd: string): boolean {
  const [commands, ok] = parseShellCommands(cmd);
  if (!ok || commands.length === 0) return false;
  for (const fields of commands) {
    const executable = shellExecutableIndex(fields);
    if (executable < 0) return false;
    const binary = shellBase(fields[executable]);
    if (readOnlyBinaries.has(binary)) continue;
    const args = fields.slice(executable + 1);
    if (binary === "git") {
      if (args.length === 0) return false;
      switch (args[0]) {
        case "status": case "log": case "diff": case "show": case "blame": case "branch": case "tag":
        case "remote": case "ls-files": case "ls-tree": case "rev-parse": case "describe": case "shortlog":
          continue;
        case "stash":
          if (args.length > 1 && args[1] === "list") continue;
      }
      return false;
    }
    if (binary === "go") {
      if (args.length > 0) switch (args[0]) { case "doc": case "list": case "version": case "env": case "vet": continue; }
      return false;
    }
    return false;
  }
  return true;
}
