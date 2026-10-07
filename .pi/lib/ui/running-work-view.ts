/** Focused, read-only work browser. Pi's non-overlay custom UI replaces the prompt editor. */
import { closeSync, openSync, readSync, statSync } from "node:fs";
import { matchesKey, truncateToWidth, visibleWidth, wrapTextWithAnsi } from "@earendil-works/pi-tui";
import { formatRunningWorkDuration, onRunningWorkChange, runningWorkListLabel, runningWorkSelection, safeInspectionText, selectedRunningWork, visibleRunningWork, type RunningWorkItem } from "./running-work.ts";

export interface ConversationStep { id: string; kind: "user" | "assistant" | "tool"; title: string; body: string }
const clean = (value: unknown) => safeInspectionText(String(value ?? ""), 100).replace(/[\x00-\x08\x0b-\x1f\x7f-\x9f]/g, "");
const short = (text: string, size = 72) => [...text.replace(/\s+/g, " ").trim()].slice(0, size).join("");
const bounded = (text: string) => [...text].length > 3000 ? `${[...text].slice(0, 3000).join("")}… (step truncated)` : text;

/** Project Pi's session entries into a small human-readable timeline, not a raw JSONL dump. */
export function parseSubagentConversation(lines: readonly string[]): ConversationStep[] {
  const steps: ConversationStep[] = [];
  for (const line of lines.slice(-200)) {
    let record: any;
    try { record = JSON.parse(line); } catch { continue; }
    if (record?.type !== "message") continue;
    const message = record.message;
    if (!message || !["user", "assistant", "toolResult"].includes(message.role)) continue;
    const parts = Array.isArray(message.content) ? message.content : [];
    const texts = parts.filter((part: any) => part?.type === "text" && typeof part.text === "string").map((part: any) => clean(part.text));
    const calls = parts.filter((part: any) => part?.type === "toolCall").map((part: any) => String(part.name ?? "tool"));
    // A step preview, not an unbounded tool transcript. Inspection stays navigable.
    const rawBody = texts.join("\n").trim();
    const body = bounded(rawBody);
    if (message.role === "assistant" && !body && !calls.length) continue;
    const kind = message.role === "toolResult" ? "tool" : message.role;
    const title = kind === "tool" ? `Tool result · ${String(message.toolName ?? "tool")}`
      : kind === "user" ? "User request" : calls.length ? `Assistant · ${calls.join(", ")}` : "Assistant";
    steps.push({ id: String(record.id ?? steps.length), kind, title: `${title}${body && kind !== "tool" ? ` · ${short(body)}` : ""}`, body: body || (calls.length ? `Called ${calls.join(", ")}` : "(no text)") });
  }
  return steps.slice(-80);
}

/** Read only a bounded tail; children may disappear or append an incomplete final line. */
export function readSubagentConversation(path: string): ConversationStep[] | undefined {
  let fd: number | undefined;
  try {
    fd = openSync(path, "r");
    const size = statSync(path).size;
    const length = Math.min(size, 512 * 1024);
    const bytes = Buffer.alloc(length);
    const start = size - length;
    const n = readSync(fd, bytes, 0, length, start);
    const raw = bytes.subarray(0, n).toString("utf8");
    const lines = raw.split("\n");
    if (start > 0) lines.shift();
    return parseSubagentConversation(lines);
  } catch { return undefined; }
  finally { if (fd !== undefined) try { closeSync(fd); } catch { /* removed child */ } }
}

export class RunningWorkView {
  private selectedId?: string;
  private stepIndex = 0;
  private scroll = 0;
  private detail = false;
  private closed = false;
  private unsubscribe: () => void;
  constructor(private tui: any, private theme: any, private done: () => void) {
    this.selectedId = selectedRunningWork()?.id ?? visibleRunningWork()[0]?.id;
    this.unsubscribe = onRunningWorkChange(() => { if (!this.closed) this.tui.requestRender(); });
  }
  dispose() { this.closed = true; this.unsubscribe(); }
  private items() {
    const items = visibleRunningWork();
    if (!items.some(item => item.id === this.selectedId)) this.selectedId = items[Math.min(runningWorkSelection(), items.length - 1)]?.id;
    return items;
  }
  handleInput(data: string) {
    if (matchesKey(data, "escape")) {
      if (this.detail) { this.detail = false; this.scroll = 0; }
      else { this.closed = true; this.done(); }
      this.tui.requestRender(); return;
    }
    const items = this.items();
    const delta = matchesKey(data, "down") ? 1 : matchesKey(data, "up") ? -1 : 0;
    if (delta) {
      if (this.detail) { this.stepIndex = Math.max(0, Math.min(this.steps(items.find(item => item.id === this.selectedId)).length - 1, this.stepIndex + delta)); this.scroll = 0; }
      else { const index = items.findIndex(item => item.id === this.selectedId); this.selectedId = items[Math.max(0, Math.min(items.length - 1, index + delta))]?.id; }
    } else if (matchesKey(data, "enter") && !this.detail && items.length) { this.detail = true; this.stepIndex = 0; this.scroll = 0; }
    else if (matchesKey(data, "pageDown")) this.scroll += 10;
    else if (matchesKey(data, "pageUp")) this.scroll = Math.max(0, this.scroll - 10);
    this.tui.requestRender();
  }
  private steps(item?: RunningWorkItem): ConversationStep[] {
    if (!item) return [];
    if (item.kind === "subagent") {
      const steps = item.transcriptPath ? readSubagentConversation(item.transcriptPath) : undefined;
      if (steps?.length) return steps;
      return [{ id: "fallback", kind: "assistant", title: "Conversation unavailable", body: bounded(clean(item.output ?? "No conversation yet. The child may still be starting or its session was removed.")) }];
    }
    let output: string | undefined;
    try { output = item.readOutput?.() ?? item.output; } catch { output = item.output; }
    return [{ id: "command", kind: "user", title: "Command", body: bounded(clean(item.detail)) }, { id: "output", kind: "tool", title: "Output", body: bounded(clean(output || "(no output yet)")) }];
  }
  render(width: number): string[] {
    width = Math.max(1, width);
    const items = this.items();
    const item = items.find(row => row.id === this.selectedId);
    const steps = this.detail ? this.steps(item) : [];
    this.stepIndex = Math.max(0, Math.min(this.stepIndex, steps.length - 1));
    const height = Math.max(6, Math.floor((this.tui.terminal?.rows ?? 24) * 0.6));
    const line = (text: string) => truncateToWidth(text, width);
    const header = [line(this.theme.fg("accent", "─".repeat(width))), line(this.theme.bold(` Running work · ${items.length} recent`)), ""];
    const body: string[] = [];
    if (!this.detail) {
      for (const row of items) {
        const selected = row.id === this.selectedId;
        const text = ` ${selected ? "❯" : " "} ${row.status === "running" ? "●" : row.status === "completed" ? "✓" : "■"} ${runningWorkListLabel(row)}  ·  ${row.status}  ·  ${formatRunningWorkDuration(row)}`;
        body.push(line(selected ? this.theme.bg("selectedBg", this.theme.fg("text", text)) : this.theme.fg("muted", text)));
      }
      if (!items.length) body.push(" No running work");
    } else {
      header[1] = line(` ${item?.label ?? "Work removed"} · ${item?.status ?? "unavailable"}`);
      // Fixed-height step strip leaves room for the selected step's text.
      const from = Math.max(0, Math.min(this.stepIndex - 1, steps.length - 3));
      if (from > 0) body.push(line(`  ↑ ${from} earlier steps`));
      for (let i = from; i < Math.min(steps.length, from + 3); i++) {
        const step = steps[i];
        body.push(line(`${i === this.stepIndex ? " ❯" : "  "} ${i + 1}. ${step.title}`));
      }
      if (from + 3 < steps.length) body.push(line(`  ↓ ${steps.length - from - 3} later steps`));
      const current = steps[this.stepIndex];
      body.push("", line(this.theme.fg("accent", ` ${current?.title ?? "No conversation yet"}`)));
      const paragraphs = (current?.body ?? "").split("\n").flatMap(text => wrapTextWithAnsi(` ${text}`, width).map(line));
      const available = Math.max(1, height - header.length - body.length - 3);
      this.scroll = Math.min(this.scroll, Math.max(0, paragraphs.length - available));
      body.push(...paragraphs.slice(this.scroll, this.scroll + available));
      if (paragraphs.length > available) body.push(line(` ${this.scroll + 1}–${Math.min(paragraphs.length, this.scroll + available)}/${paragraphs.length} lines`));
    }
    const hint = this.detail ? " ↑/↓ steps · PgUp/PgDn scroll · Esc back" : " ↑/↓ select · Enter inspect · Esc close";
    return [...header, ...body, "", line(this.theme.fg("dim", hint)), line(this.theme.fg("accent", "─".repeat(width)))].map(row => {
      const fitted = line(row);
      return fitted + " ".repeat(Math.max(0, width - visibleWidth(fitted)));
    });
  }
  invalidate() { this.tui.requestRender(); }
}

let opening = false;
export function openRunningWorkView(ctx: any): boolean {
  if (opening || ctx?.mode !== "tui" || typeof ctx?.ui?.custom !== "function" || !visibleRunningWork().length) return false;
  opening = true;
  try {
    Promise.resolve(ctx.ui.custom((_tui: any, theme: any, _keys: any, done: () => void) => new RunningWorkView(_tui, theme, done)))
      .catch(() => { try { ctx.ui.notify?.("Unable to open work browser", "warning"); } catch {} })
      .finally(() => { opening = false; });
    return true;
  } catch { opening = false; return false; }
}
