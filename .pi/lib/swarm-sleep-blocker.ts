/**
 * Port of Swarm's builtin sleep blocker so `pi -p` blocks the same bash
 * commands `swarm -p` does and returns the same model-visible text.
 *
 * Source of truth: swarm-sdk internal/hooks/builtin/sleep_blocker_hook.go
 * (sleepCommandPattern, stripHeredocs, isAllowedSleepCommand, isPollingLoop,
 * allSleepInvocationsBackgrounded, deriveUntilRewrite, buildBlockMessage)
 * and internal/agent/agent_tools.go ("Tool '%s' blocked by hook: %s").
 */

// (?:^|[\n;&|(`{])\s*(?:(?:do|then|else|!)\s+)*sleep\s
const sleepCommandPattern = /(?:^|[\n;&|(`{])\s*(?:(?:do|then|else|!)\s+)*sleep\s/g;
const loopKeywordPattern = /\b(until|while|for)\b/;
const loopBodyPattern = /\bdo\b[\s\S]*\bdone\b/;
const leadingSleepChainPattern = /^\s*sleep\s+(\S+)\s*(?:&&|;)\s*(.+)$/;
const filePathPattern = /(?:^|\s)((?:\.{0,2}\/|~\/)?[\w./@-]*[\w]\/[\w./@-]+|\/[\w./@-]+|[\w.@-]+\.[\w]{1,8})/;

const allowedSleepPatterns = ["tlmgr", "pdflatex", "xelatex", "lualatex", "npm install", "pip install", "go mod tidy", "cargo build", "apt-get", "dnf install", "brew install"];

interface HeredocTag { word: string; dash: boolean }
const isHeredocWordChar = (c: string) => /[A-Za-z0-9_]/.test(c);

function heredocOpeners(line: string): HeredocTag[] {
  const tags: HeredocTag[] = [];
  for (let i = 0; i + 1 < line.length; i++) {
    if (line[i] !== "<" || line[i + 1] !== "<") continue;
    if (i + 2 < line.length && line[i + 2] === "<") { i += 2; continue; }
    if (i > 0 && line[i - 1] === "<") continue;
    let j = i + 2;
    const tag: HeredocTag = { word: "", dash: false };
    if (j < line.length && line[j] === "-") { tag.dash = true; j++; }
    while (j < line.length && (line[j] === " " || line[j] === "\t")) j++;
    if (j < line.length && line[j] === "\\") j++;
    if (j < line.length && (line[j] === "'" || line[j] === "\"")) {
      const quote = line[j]; j++;
      const start = j;
      while (j < line.length && line[j] !== quote) j++;
      if (j >= line.length) break;
      tag.word = line.slice(start, j); j++;
    } else {
      const start = j;
      while (j < line.length && isHeredocWordChar(line[j])) j++;
      tag.word = line.slice(start, j);
    }
    if (tag.word === "") { i = j - 1; continue; }
    tags.push(tag);
    i = j - 1;
  }
  return tags;
}

export function stripHeredocs(command: string): string {
  if (!command.includes("<<")) return command;
  const lines = command.split("\n");
  const kept: string[] = [];
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i];
    kept.push(line);
    for (const tag of heredocOpeners(line)) {
      while (i + 1 < lines.length) {
        i++;
        let candidate = lines[i];
        if (tag.dash) candidate = candidate.replace(/^\t+/, "");
        if (candidate.replace(/[ \t\r]+$/, "") === tag.word) break;
      }
    }
  }
  return kept.join("\n");
}

export function isAllowedSleepCommand(command: string, env: NodeJS.ProcessEnv = process.env): boolean {
  const cmd = command.toLowerCase();
  for (const pattern of allowedSleepPatterns) if (cmd.includes(pattern.toLowerCase())) return true;
  return env.SWARM_ALLOW_SLEEP === "1";
}

export const isPollingLoop = (command: string) => loopKeywordPattern.test(command) && loopBodyPattern.test(command);

function isBackgroundedSleepClause(rest: string): boolean {
  for (let i = 0; i < rest.length; i++) {
    switch (rest[i]) {
      case ";": case "\n": return false;
      case "&":
        if (i + 1 < rest.length && rest[i + 1] === "&") return false;
        if (i > 0 && rest[i - 1] === ">") continue;
        return true;
    }
  }
  return false;
}

function allSleepInvocationsBackgrounded(scanned: string): boolean {
  const matches = [...scanned.matchAll(sleepCommandPattern)];
  if (matches.length === 0) return false;
  for (const m of matches) if (!isBackgroundedSleepClause(scanned.slice((m.index ?? 0) + m[0].length))) return false;
  return true;
}

function firstFilePath(cmd: string): string {
  const m = filePathPattern.exec(cmd);
  return m ? m[1].trim() : "";
}

export function deriveUntilRewrite(command: string): string {
  const m = leadingSleepChainPattern.exec(command);
  if (!m) return "";
  const chained = m[2].trim();
  if (chained === "") return "";
  const file = firstFilePath(chained);
  if (file !== "") return `until grep -q "SUCCESS\\|TIMEOUT\\|done" ${file}; do sleep 5; done && ${chained}`;
  return `until <check>; do sleep 5; done && ${chained}`;
}

export function buildBlockMessage(command: string): string {
  const rewrite = deriveUntilRewrite(command);
  if (rewrite !== "") {
    return `Blocked: a bare sleep can't wait for a condition. To wait until something is ready, poll with an until-loop instead. Replace this command with:\n\n  ${rewrite}\n\n(Adjust the marker/condition and the trailing command for your case. To wait for a command you started, use run_in_background: true. Do not chain shorter sleeps to work around this block — set timeout_seconds on the Bash call if you only need a hard cap.)`;
  }
  return "Blocked: a bare sleep can't wait for a condition. Use one of these instead:\n\n  - Poll until a condition holds:  until <check>; do sleep 5; done && <next-command>\n  - Wait for a command you started: run_in_background: true, then read its output when notified.\n  - Just need a hard cap on a single command? Set timeout_seconds on the Bash call.\n\nDo not chain shorter sleeps to work around this block.";
}

/** SleepBlockerHook.OnEvent: returns the block message, or undefined to continue. */
export function sleepBlockReason(command: string | undefined, env: NodeJS.ProcessEnv = process.env): string | undefined {
  if (!command) return undefined;
  const scanned = stripHeredocs(command);
  sleepCommandPattern.lastIndex = 0;
  if (!new RegExp(sleepCommandPattern.source).test(scanned)) return undefined;
  if (isAllowedSleepCommand(scanned, env)) return undefined;
  if (isPollingLoop(scanned)) return undefined;
  if (allSleepInvocationsBackgrounded(scanned)) return undefined;
  return buildBlockMessage(command);
}

/** agent_tools.go: the model-visible text for a hook block. */
export const blockedByHookText = (toolName: string, reason: string) => `Tool '${toolName}' blocked by hook: ${reason}`;
