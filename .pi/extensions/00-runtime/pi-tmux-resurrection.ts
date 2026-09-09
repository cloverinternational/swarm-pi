import { execFileSync } from "node:child_process";
import { mkdirSync, readFileSync, renameSync, writeFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { homedir } from "node:os";

const STATE_FILE = join(process.env.XDG_STATE_HOME || join(homedir(), ".local", "state"), "pi-tmux", "sessions.json");

function tmuxPane(): Record<string, string> | undefined {
  const pane = process.env.TMUX_PANE;
  if (!pane) return undefined;
  try {
    const format = "#{session_name}\t#{window_index}\t#{pane_index}\t#{pane_current_path}";
    const output = execFileSync("tmux", ["display-message", "-p", "-t", pane, format], { encoding: "utf8" }).trim();
    const [sessionName, windowIndex, paneIndex, cwd] = output.split("\t");
    if (!sessionName || !windowIndex || !paneIndex || !cwd) return undefined;
    return { pane, sessionName, windowIndex, paneIndex, cwd: resolve(cwd) };
  } catch { return undefined; }
}

function writeState(entry: Record<string, string>): void {
  mkdirSync(dirname(STATE_FILE), { recursive: true, mode: 0o700 });
  let state: Record<string, Record<string, string>> = {};
  try { state = JSON.parse(readFileSync(STATE_FILE, "utf8")); } catch { /* first run or interrupted write */ }
  state[`${entry.sessionName}:${entry.windowIndex}.${entry.paneIndex}`] = { ...entry, sessionFile: entry.sessionFile };
  const temporary = `${STATE_FILE}.${process.pid}.tmp`;
  writeFileSync(temporary, `${JSON.stringify(state, null, 2)}\n`, { mode: 0o600 });
  renameSync(temporary, STATE_FILE);
}

export default function piTmuxResurrection(pi: any) {
  pi.on?.("session_start", (_event: unknown, ctx: any) => {
    const location = tmuxPane();
    const sessionFile = ctx?.sessionManager?.getSessionFile?.();
    if (!location || !sessionFile) return;
    writeState({ ...location, sessionFile: resolve(sessionFile) });
  });
}
