import { describe, expect, it } from "vitest";
import { decodePiOutput, extractIds, invokePi, runCase, validateIds } from "../tools/experiments/bootstrap-agent/runner.mjs";
import { writeFile } from "node:fs/promises";

const envelope = (text = "ok") => [
  { type: "message_start", message: { role: "assistant", usage: { input: 2, output: 1, totalTokens: 3 } } },
  { type: "message_update", assistantMessageEvent: { type: "text_delta", delta: text } },
  { type: "message_end", message: { role: "assistant", content: [{ type: "text", text }] } }, { type: "agent_end" },
].map(JSON.stringify).join("\n");

describe("bootstrap benchmark validation", () => {
  it("decodes wrapped JSONL and does not duplicate deltas", () => { expect(decodePiOutput(envelope("hello")).text).toBe("hello"); });
  it("strictly parses selector JSON and rejects unknown or out-of-scope IDs", () => {
    expect(extractIds('{"ids":["repo:queue-api"]}')).toEqual(["repo:queue-api"]);
    expect(() => extractIds('noise {"ids":["repo:queue-api"]}')).toThrow(/strict JSON/);
    expect(() => validateIds(["nope"])).toThrow(/unknown evidence IDs/);
    expect(() => validateIds(["repo:queue-api"], undefined, ["skill"])).toThrow(/unknown evidence IDs/);
  });
  it("requires terminal completion and rejects error events", () => { expect(() => decodePiOutput(JSON.stringify({ type: "error", error: "bad" }))).toThrow(/Pi error/); expect(() => decodePiOutput(JSON.stringify({ type: "message_end", message: { role: "assistant", content: [{ type: "text", text: "x" }] } }))).toThrow(/terminal/); });
});

describe("bootstrap benchmark subprocess safety", () => {
  it("fails on overflow rather than silently truncating", async () => { const r = await invokePi("console.log('x'.repeat(100))", { pi: process.execPath, outputLimit: 20 }); expect(r.ok).toBe(false); expect(r.overflow).toBe(true); });
  it("cancels a truly hung injectable executable", async () => { const r = await invokePi("ignored", { command: { file: process.execPath, args: ["-e", "setInterval(() => {}, 1000)"] }, timeout: 100 }); expect(r.ok).toBe(false); expect(r.killed).toBe(true); });
  it("fails errored intermediate calls without downstream fabrication", async () => { const script = "/tmp/bootstrap-error-fixture.mjs"; await writeFile(script, `console.log(${JSON.stringify(JSON.stringify({ type: "error", error: "intermediate" }))})`); const r = await runCase("combined-selector", { id: "failure", request: "x" }, { command: { file: process.execPath, args: [script] }, timeout: 1000 }); expect(r.ok).toBe(false); expect(r.calls).toHaveLength(1); });
});
