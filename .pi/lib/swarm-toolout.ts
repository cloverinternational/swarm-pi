/**
 * Port of swarm-sdk internal/toolout/bound.go and the oversized-tool-result
 * guard in internal/agent/agent_tools.go: every tool result (any tool, headless
 * and interactive alike) larger than MaxToolOutputChars bytes or
 * MaxToolOutputLines lines is middle-elided to a head + marker + tail before
 * after-hooks run and before it enters the conversation. Byte-based like Go.
 */
export const CHARS_PER_TOKEN = 4;
export const MAX_TOOL_OUTPUT_TOKENS = 25_000;
export const MAX_TOOL_OUTPUT_CHARS = MAX_TOOL_OUTPUT_TOKENS * CHARS_PER_TOKEN; // 100,000
export const MAX_TOOL_OUTPUT_LINES = 1000;
const MARKER_RESERVE_CHARS = 256;
const MARKER_RESERVE_LINES = 4;

export interface BoundResult { output: string; head: string; tail: string; elidedChars: number; elidedLines: number; truncated: boolean }

const NL = 0x0a;
const isRuneStart = (b: number) => (b & 0xc0) !== 0x80;
const countLines = (buf: Buffer) => buf.length === 0 ? 0 : buf.filter(b => b === NL).length + 1;
function utf8SafePrefixEnd(buf: Buffer, budget: number): number {
  if (budget >= buf.length) return buf.length;
  if (budget <= 0) return 0;
  let end = budget;
  while (end > 0 && !isRuneStart(buf[end])) end--;
  return end;
}
function utf8SafeSuffixStart(buf: Buffer, budget: number): number {
  if (budget >= buf.length) return 0;
  if (budget <= 0) return buf.length;
  let start = buf.length - budget;
  while (start < buf.length && !isRuneStart(buf[start])) start++;
  return start;
}
function prefixEndForLines(buf: Buffer, lines: number): number {
  if (lines <= 0) return 0;
  let end = 0;
  for (let i = 0; i < lines; i++) {
    const index = buf.indexOf(NL, end);
    if (index < 0) return buf.length;
    end = index + 1;
  }
  return end;
}
function suffixStartForLines(buf: Buffer, lines: number): number {
  if (lines <= 0) return buf.length;
  let start = buf.length;
  for (let i = 0; i < lines; i++) {
    const index = buf.lastIndexOf(NL, start - 1);
    if (index < 0 || start === 0) return 0;
    start = index;
  }
  return start + 1;
}

/** toolout.Bound. */
export function boundToolOutput(input: string, maxChars: number, maxLines: number): BoundResult {
  const buf = Buffer.from(input, "utf8");
  const originalLines = countLines(buf);
  const overChars = maxChars > 0 && buf.length > maxChars;
  const overLines = maxLines > 0 && originalLines > maxLines;
  if (!overChars && !overLines) return { output: input, head: input, tail: "", elidedChars: 0, elidedLines: 0, truncated: false };
  let headBudget = buf.length, tailBudget = buf.length;
  if (maxChars > 0) { headBudget = Math.floor((maxChars + 1) / 2); tailBudget = Math.floor(maxChars / 2); }
  let headEnd = utf8SafePrefixEnd(buf, headBudget);
  let tailStart = utf8SafeSuffixStart(buf, tailBudget);
  if (maxLines > 0) {
    if (maxLines === 1 && originalLines > 1) {
      headEnd = Math.min(headEnd, buf.indexOf(NL));
      tailStart = Math.max(tailStart, buf.lastIndexOf(NL) + 1);
    } else {
      headEnd = Math.min(headEnd, prefixEndForLines(buf, Math.floor((maxLines + 1) / 2)));
      tailStart = Math.max(tailStart, suffixStartForLines(buf, Math.floor(maxLines / 2)));
    }
  }
  if (headEnd > tailStart) headEnd = tailStart;
  const head = buf.subarray(0, headEnd).toString("utf8");
  const tail = buf.subarray(tailStart).toString("utf8");
  const output = head + tail;
  const elidedLines = Math.max(0, originalLines - countLines(Buffer.from(output, "utf8")));
  return { output, head, tail, elidedChars: tailStart - headEnd, elidedLines, truncated: tailStart - headEnd > 0 };
}

/** agent_tools.go: the model-visible text for an oversized result, or undefined when unchanged. */
export function elideOversizedToolOutput(output: string): string | undefined {
  const chars = Buffer.byteLength(output, "utf8");
  const lines = output.split("\n").length;
  if (chars <= MAX_TOOL_OUTPUT_CHARS && lines <= MAX_TOOL_OUTPUT_LINES) return undefined;
  const bounded = boundToolOutput(output, MAX_TOOL_OUTPUT_CHARS - MARKER_RESERVE_CHARS, MAX_TOOL_OUTPUT_LINES - MARKER_RESERVE_LINES);
  return `${bounded.head}\n\n... [${bounded.elidedChars} chars elided from oversized tool result; use a narrower query to show more] ...\n\n${bounded.tail}`;
}
