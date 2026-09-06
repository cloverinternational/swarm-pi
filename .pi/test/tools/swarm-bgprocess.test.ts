import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";
import { resolve } from "node:path";
import { registerSwarmBackgroundBash } from "../../extensions/30-tools/swarm-background-bash.ts";
import { PERMISSIVE_PARAMETERS, overlaySwarmToolSchemas } from "../../lib/runtime/swarm-tool-surface.ts";
import { bgOutputPreview, formatBackgroundDone } from "../../lib/tools/swarm-bgprocess.ts";
import { boundToolOutput, elideOversizedToolOutput } from "../../lib/runtime/swarm-toolout.ts";
import { afterTurnFlushListeners } from "../../lib/runtime/swarm-builtin-hooks-runtime.ts";

const root = resolve(import.meta.dirname, "../../..");
const harness = () => {
  const tools: any[] = [];
  const sent: any[] = [];
  const handlers: Record<string, any[]> = {};
  const pi = {
    getCwd: () => root, registerTool: (t: any) => tools.push(t),
    on: (event: string, handler: any) => { (handlers[event] ??= []).push(handler); },
    sendMessage: (message: any, options: any) => sent.push({ kind: "custom", message, options }),
    sendUserMessage: (content: string, options: any) => sent.push({ kind: "user", content, options }),
  };
  registerSwarmBackgroundBash(pi);
  const emit = (event: string) => { for (const h of handlers[event] ?? []) h({}, {}); };
  return { bash: tools.find(t => t.name === "Bash"), read: tools.find(t => t.name === "ReadBackgroundCommand"), tools, sent, emit };
};
const invoke = (tool: any, params: any) => tool.execute("call", params, undefined, undefined, { cwd: root });

describe("Swarm interactive background bash", () => {
  it("registers both canonical interactive tools", () => {
    const { tools } = harness();
    const fixture = new Map(JSON.parse(readFileSync(resolve(root, "tools/parity/fixtures/swarm-interactive-tools.json"), "utf8"))
      .map((x: any) => [x.function.name, x.function]));
    expect(tools.map(x => x.name)).toEqual(["Bash", "ReadBackgroundCommand"]);
    for (const tool of tools) {
      expect(tool.parameters).toEqual(PERMISSIVE_PARAMETERS);
      expect(tool.description).toBe((fixture.get(tool.name) as any).description);
      const overlaid: any = overlaySwarmToolSchemas({ tools: [{ type: "function", function: tool }] });
      expect(overlaid.tools[0].function.parameters).toEqual((fixture.get(tool.name) as any).parameters);
    }
  });

  it("returns completedProcessResult text: plain output, Exit code prefix on failure, no-output sentinels", async () => {
    const { bash } = harness();
    // executor.go Wait appends "\n" after every tailed line, even a partial one.
    expect((await invoke(bash, { command: "printf hello", timeout_seconds: 2 })).content[0].text).toBe("hello\n");
    await expect(invoke(bash, { command: "printf bad >&2; exit 7", timeout_seconds: 2 })).rejects.toThrow("Exit code: 7\nbad\n");
    expect((await invoke(bash, { command: "true", timeout_seconds: 2 })).content[0].text).toBe("Command completed successfully (no output)");
    await expect(invoke(bash, { command: "exit 2", timeout_seconds: 2 })).rejects.toThrow("Exit code: 2\nCommand failed with no output");
    // ExecuteStreaming: a missing timeout falls back to DefaultTimeout (5m).
    expect((await invoke(bash, { command: "echo hi" })).content[0].text).toBe("hi\n");
  });

  it("reports spawn failures like manager.Spawn and queues the failed-status notification", async () => {
    const { bash, sent, emit } = harness();
    emit("agent_start");
    await expect(invoke(bash, { command: "pwd", cwd: "/nonexistent-dir", timeout_seconds: 5 }))
      .rejects.toThrow(/^failed to spawn process: execute \[bg-\d+-\d+\]: failed to start command: fork\/exec \/usr\/bin\/stdbuf: no such file or directory$/);
    // Queued while the agent runs; contributed to the post-tool hook slot at turn_end.
    expect(sent).toHaveLength(0);
    const parts = afterTurnFlushListeners().flatMap((listener) => listener({ runContinues: true }) ?? []);
    expect(parts).toHaveLength(1);
    expect(parts[0].role).toBe("system");
    expect(parts[0].text).toMatch(/^\[BACKGROUND\] task_id=bg-\d+-\d+ command=pwd status=failed\n\noutput:\nexecute \[bg-\d+-\d+\]: failed to start command: fork\/exec \/usr\/bin\/stdbuf: no such file or directory\n\nBackground command failed \(exit code -1\)\.$/);
    expect(((globalThis as any)[Symbol.for("pi-swarm-background-system-texts")] as Set<string>).has(parts[0].text)).toBe(true);
  });

  it("wakes an idle agent with the joined completion notifications", async () => {
    const { bash, sent } = harness();
    const launched = await invoke(bash, { command: "for i in 1 2; do sleep 1; done; echo bg-done >&2; exit 7", timeout_seconds: 1 });
    const id = JSON.parse(launched.content[0].text).task_id;
    await new Promise(r => setTimeout(r, 2500));
    // onError receives result.Error (the exec error), not the process output.
    expect(sent).toEqual([{ kind: "user", content: `[BACKGROUND] task_id=${id} command=for i in 1 2; do sleep 1; done; echo bg-done >&2; exit 7 status=failed\n\noutput:\nexit status 7\n\nBackground command failed (exit code 7).`, options: { deliverAs: "followUp" } }]);
  }, 6000);

  it("formats notifications and previews like app_update.go", () => {
    expect(formatBackgroundDone({ id: "bg-1-2", command: "echo x", state: "completed", exitCode: 0, outputPreview: "x\n", outputTruncated: false })).toBe("[BACKGROUND] task_id=bg-1-2 command=echo x status=completed\n\noutput:\nx\n\n\nBackground command completed successfully (exit code 0).");
    expect(formatBackgroundDone({ id: "bg-1-2", command: "x".repeat(130), state: "cancelled", exitCode: -1, outputPreview: "", outputTruncated: true })).toBe(`[BACKGROUND] task_id=bg-1-2 command=${"x".repeat(117)}... status=cancelled\n\nBackground command cancelled. Output truncated above — call ReadBackgroundCommand with task_id "bg-1-2" for the full text.`);
    const big = bgOutputPreview("a".repeat(5000));
    expect(big.truncated).toBe(true);
    expect(big.preview).toBe(`${"a".repeat(2048)}\n\n... [truncated 904 bytes] ...\n\n${"a".repeat(2048)}`);
  });

  it("middle-elides oversized tool results like agent_tools.go / toolout.Bound", () => {
    expect(elideOversizedToolOutput("small")).toBeUndefined();
    const lines = Array.from({ length: 1500 }, (_, i) => String(i + 1)).join("\n") + "\n";
    const elided = elideOversizedToolOutput(lines)!;
    // 996 lines kept: 498 head + 498 tail lines.
    expect(elided.startsWith("1\n2\n")).toBe(true);
    expect(elided).toContain("\n498\n\n\n... [");
    expect(elided).toMatch(/chars elided from oversized tool result; use a narrower query to show more\] \.\.\.\n\n1004\n1005\n/);
    expect(elided.endsWith("\n1500\n")).toBe(true);
    const bounded = boundToolOutput("αβγδε", 4, 0);
    expect(bounded.head).toBe("α"); expect(bounded.tail).toBe("ε"); expect(bounded.elidedChars).toBe(6);
    expect(boundToolOutput("a\nb\nc", 0, 1)).toMatchObject({ head: "a", tail: "c", elidedChars: 3 });
  });

  it("auto-backgrounds, reads/filter/tails/lists, and cancels", async () => {
    const { bash, read } = harness();
    const launched = await invoke(bash, { command: "sleep 3; printf 'alpha\\nbeta\\ngamma\\n'; : >&2", timeout_seconds: 1 });
    const masked = launched.content[0].text.replace(/bg-\d+-\d+/, "TASK");
    expect(masked).toBe('{"backgrounded":true,"message":"Command idle for 1s (no output) and was auto-backgrounded. Use ReadBackgroundCommand to check status — look at metadata.seconds_since_last_output to decide if it is still healthy.","status":"running","task_id":"TASK","timeout_seconds":1}');
    const id = JSON.parse(launched.content[0].text).task_id;
    const running = JSON.parse((await invoke(read, { task_id: id, action: "status" })).content[0].text);
    expect(running).toMatchObject({ task_id: id, status: "running", metadata: { total_lines: 0, bytes_written: 0 } });
    expect(running).not.toHaveProperty("output");
    await new Promise(r => setTimeout(r, 2100));
    const filtered = JSON.parse((await invoke(read, { task_id: id, pattern: "beta" })).content[0].text);
    expect(filtered.output.map((x: any) => x.content)).toEqual(["beta"]);
    const tail = JSON.parse((await invoke(read, { task_id: id, max_lines: 2 })).content[0].text);
    expect(tail.output.map((x: any) => x.content)).toEqual(["beta", "gamma"]);
    expect(tail.metadata).toMatchObject({ total_lines: 3, output_truncated: true });
    expect(tail.metadata.output_file).toMatch(new RegExp(`bgprocess-output-${id}\\.txt$`));
    expect(readFileSync(tail.metadata.output_file, "utf8")).toBe("alpha\nbeta\ngamma\n");
    const listedText = (await invoke(read, { action: "list" })).content[0].text;
    const listed = JSON.parse(listedText);
    expect(listed.count).toBe(1);
    expect(Object.keys(listed.processes[0])).toEqual(["task_id", "command", "status", "started_at", "duration", "exit_code"]);
    // encoding/json HTML-escapes <>& and time.Now() formats in LOCAL time.
    expect(listedText).toContain("\\u003e\\u00262");
    expect(listedText).not.toContain(">&2");
    const local = new Date(); const pad = (n: number) => String(n).padStart(2, "0");
    expect(listed.processes[0].started_at.slice(0, 13)).toBe(`${local.getFullYear()}-${pad(local.getMonth() + 1)}-${pad(local.getDate())} ${pad(local.getHours())}`);
    const offset = -local.getTimezoneOffset();
    const zone = offset === 0 ? "Z" : `${offset > 0 ? "+" : "-"}${pad(Math.floor(Math.abs(offset) / 60))}:${pad(Math.abs(offset) % 60)}`;
    expect(tail.started_at.endsWith(zone)).toBe(true);
    expect(tail.output[0].timestamp.endsWith(zone)).toBe(true);

    const second = await invoke(bash, { command: "sleep 9", timeout_seconds: 1 });
    const secondId = JSON.parse(second.content[0].text).task_id;
    expect((await invoke(read, { task_id: secondId, action: "cancel" })).content[0].text).toBe(`Process ${secondId} has been cancelled`);
    const cancelled = JSON.parse((await invoke(read, { task_id: secondId, action: "status" })).content[0].text);
    expect(cancelled.status).toBe("cancelled");
  }, 9000);

  it("matches validation, unknown id, and invalid regex errors", async () => {
    const { bash, read } = harness();
    await expect(invoke(bash, { timeout_seconds: 1 })).rejects.toThrow("command is required");
    await expect(invoke(bash, {})).rejects.toThrow("command is required");
    // registry_impl.go Validate errors are sdkerr-wrapped; Execute errors are plain NewErrorResult text.
    await expect(invoke(read, {})).rejects.toThrow(/^Error executing ReadBackgroundCommand: validation failed for ReadBackgroundCommand: task_id is required for action "output" \(error_id=err_[0-9a-f]{20}\)$/);
    await expect(invoke(read, { task_id: "missing" })).rejects.toThrow("format_output [missing]: process not found: process not found");
    await expect(invoke(read, { task_id: "x", action: "wat" })).rejects.toThrow(/^Error executing ReadBackgroundCommand: validation failed for ReadBackgroundCommand: invalid action: wat \(must be status, output, cancel, or list\) \(error_id=/);
    const launched = await invoke(bash, { command: "sleep 3", timeout_seconds: 1 });
    const id = JSON.parse(launched.content[0].text).task_id;
    await expect(invoke(read, { task_id: id, pattern: "[" }))
      .rejects.toThrow(`output [${id}]: failed to get output lines: invalid pattern: error parsing regexp: missing closing ]: \`[\``);
    await invoke(read, { task_id: id, action: "cancel" });
  });
});
