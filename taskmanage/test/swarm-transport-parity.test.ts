import { describe, expect, it } from "vitest";
import { mkdtempSync, writeFileSync, mkdirSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import {
  CapabilityManifestState,
  alignProviderPayload,
  applySwarmModelCompat,
  buildCapabilityManifest,
  collapseUserText,
  firstSentence,
  swarmPayloadKeyOrder,
  swarmToolOrder,
} from "../../.pi/lib/swarm-transport-parity.ts";
import { gateActiveTools, swarmSurfaceFor, xaiHasCredentials } from "../../.pi/lib/swarm-tool-gating.ts";
import { buildContextBlock, discoverAgentsMdPaths, injectContextBlocks, renderSources, trimSourceContent } from "../../.pi/lib/swarm-context.ts";
import { buildResultXML, bashTruncateOutput, estimateTokens } from "../../.pi/lib/swarm-bash.ts";
import { loadSwarmToolSurface } from "../../.pi/lib/swarm-tool-surface.ts";

describe("transport parity with swarm -p", () => {
  it("orders tools bytewise like Go sort.Strings", () => {
    expect(swarmToolOrder(["bash", "Read", "annoyed", "CronList"])).toEqual(["CronList", "Read", "annoyed", "bash"]);
    expect(swarmToolOrder(["A", "B"])).toBeUndefined();
  });
  it("re-keys the provider payload in ChatCompletionRequest field order", () => {
    // Pi's openai-completions literal order, as captured on the wire.
    const pi = { model: "m", messages: [], stream: true, stream_options: { include_usage: true }, max_tokens: 4096, tools: [], temperature: 0, reasoning_effort: "high", zzz_unknown: 1, aaa_unknown: 2 };
    const ordered = swarmPayloadKeyOrder(pi)!;
    expect(Object.keys(ordered)).toEqual(["model", "messages", "max_tokens", "temperature", "stream", "stream_options", "tools", "reasoning_effort", "zzz_unknown", "aaa_unknown"]);
    expect(JSON.parse(JSON.stringify(ordered))).toEqual(pi);
    expect(swarmPayloadKeyOrder(ordered)).toBeUndefined();
  });
  it("sorts tool schema keys recursively like Go map encoding", () => {
    const payload = { model: "m", messages: [], stream: true, tools: [{ type: "function", function: { name: "t", description: "d", parameters: { type: "object", required: ["b"], properties: { b: { type: "string", description: "x" }, a: { items: { type: "string" }, type: "array" } } } } }] };
    const aligned = alignProviderPayload(payload)!;
    expect(JSON.stringify(aligned.tools[0].function.parameters)).toBe('{"properties":{"a":{"items":{"type":"string"},"type":"array"},"b":{"description":"x","type":"string"}},"required":["b"],"type":"object"}');
    expect(Object.keys(aligned.tools[0].function)).toEqual(["name", "description", "parameters"]);
    expect(alignProviderPayload(aligned)).toBeUndefined();
  });
  it("applies Swarm request-option defaults onto the model compat", () => {
    const model: any = { compat: { supportsStrictMode: true } };
    expect(applySwarmModelCompat(model)).toBe(true);
    expect(model.compat).toMatchObject({ supportsStrictMode: false, supportsStore: false, maxTokensField: "max_tokens" });
    expect(model.samplingParams).toEqual({ temperature: 0, reasoning_effort: "high" });
    expect(applySwarmModelCompat(model)).toBe(false);
  });
  it("collapses text-only user content to a string and leaves images alone", () => {
    expect(collapseUserText([{ role: "user", content: [{ type: "text", text: "hi" }] }])?.[0].content).toBe("hi");
    expect(collapseUserText([{ role: "user", content: [{ type: "image", data: "x" }] }])).toBeUndefined();
    expect(collapseUserText([{ role: "assistant", content: [{ type: "text", text: "a" }] }])).toBeUndefined();
  });
  it("renders the capability manifest like capability_manifest.go", () => {
    expect(firstSentence("Hello world. Second sentence.")).toBe("Hello world.");
    expect(firstSentence("e.g. this")).toBe("e.g."); // Go stops at ". " even mid-abbreviation
    expect(firstSentence("v1.2 works")).toBe("v1.2 works");
    expect(firstSentence("")).toBe("Available for this turn.");
    const manifest = buildCapabilityManifest("act", [{ name: "bash", description: "Run it. More." }, { name: "Read", description: "" }]);
    expect(manifest).toBe("<effective_capabilities>\nThis is a request-scoped summary of tools actually exposed to the model (mode: act). Tool schemas remain authoritative; this summary grants no permissions.\n- `bash`: Run it.\n- `Read`: Available for this turn.\n</effective_capabilities>");
    const many = Array.from({ length: 45 }, (_, i) => ({ name: `t${String(i).padStart(2, "0")}`, description: "d." }));
    expect(buildCapabilityManifest("act", many)).toContain("- 5 additional tool omitted; use the tool search/discovery capability when available.\n");
  });
  it("emits the manifest once per signature on the current user turn only", () => {
    const state = new CapabilityManifestState();
    const tools = [{ name: "bash", description: "d." }];
    const turn1 = [{ role: "user", content: "a", timestamp: 1 }];
    const first = state.apply(turn1, "act", tools)!;
    expect(first[0].content).toMatch(/^a\n\n<effective_capabilities>/);
    // second provider call inside the same turn keeps it
    expect(state.apply(turn1, "act", tools)![0].content).toMatch(/<effective_capabilities>/);
    // next turn, same signature: nothing
    const turn2 = [...turn1, { role: "assistant", content: "ok" }, { role: "user", content: "b", timestamp: 2 }];
    expect(state.apply(turn2, "act", tools)).toBeUndefined();
    // signature change: emitted on the new turn
    const turn3 = [...turn2, { role: "assistant", content: "ok" }, { role: "user", content: "c", timestamp: 3 }];
    const third = state.apply(turn3, "plan", tools)!;
    expect(third[4].content).toMatch(/mode: plan/);
    expect(third[0].content).toBe("a");
  });
});

describe("tool surface gating", () => {
  it("hides Pi-only tools and mirrors Swarm's environment gates", () => {
    const fixture = new Set(loadSwarmToolSurface().keys());
    expect(fixture.size).toBe(28);
    const headless = swarmSurfaceFor({ interactive: false, home: "/nonexistent", env: {} });
    expect(headless.has("ask_user_question")).toBe(false);
    expect(headless.has("x_search")).toBe(false);
    const interactive = swarmSurfaceFor({ interactive: true, home: "/nonexistent", env: { XAI_API_KEY: "k" } });
    expect(interactive.has("enter_plan_mode")).toBe(true);
    expect(interactive.has("xai_web_search")).toBe(true);
    expect(gateActiveTools(["bash", "read", "edit", "annoyed"], { interactive: false, home: "/nonexistent", env: {} }, {})).toEqual(["bash", "annoyed"]);
    expect(gateActiveTools(["bash"], { interactive: false, home: "/nonexistent", env: {} }, {})).toBeUndefined();
    expect(gateActiveTools(["bash", "read"], { interactive: false }, { PI_SWARM_TOOL_SURFACE: "all" })).toBeUndefined();
  });
  it("detects xAI credentials like xaitools.HasCredentials", () => {
    const home = mkdtempSync(join(tmpdir(), "pi-xai-"));
    expect(xaiHasCredentials(home, {})).toBe(false);
    mkdirSync(join(home, ".swarm", "config", "oauth"), { recursive: true });
    writeFileSync(join(home, ".swarm", "config", "oauth", "xai.json"), JSON.stringify({ token: { access_token: "a", expires_at: 100 } }));
    expect(xaiHasCredentials(home, {}, () => 1_000_000)).toBe(false);
    writeFileSync(join(home, ".swarm", "config", "oauth", "xai.json"), JSON.stringify({ token: { access_token: "a", expires_at: 100, refresh_token: "r" } }));
    expect(xaiHasCredentials(home, {}, () => 1_000_000)).toBe(true);
  });
});

describe("swarm context blocks", () => {
  it("renders and budgets sources like orchestrator.go", () => {
    expect(renderSources([{ name: "a", content: "x\n\n" }], 4000, 200)).toBe("As you answer the user's questions, you can use the following context:\n<context name=\"a\">\nx\n</context>");
    expect(trimSourceContent("1\n2\n3", 4000, 2)).toEqual({ text: "1\n2", truncated: true });
    expect(injectContextBlocks("base\n\n<swarmos_context>\nold\n</swarmos_context>", "", "new")).toBe("base\n\n<swarmos_context>\nnew\n</swarmos_context>");
  });
  it("builds cached + ephemeral blocks for a workspace", () => {
    const dir = mkdtempSync(join(tmpdir(), "pi-ctx-"));
    writeFileSync(join(dir, "AGENTS.md"), "rules\n");
    const { block } = buildContextBlock({ workDir: dir, home: "/nonexistent", now: () => new Date(2026, 8, 5) });
    expect(block).toBe(`<swarmos_cached_context>\nAs you answer the user's questions, you can use the following context:\n<context name="agentsMd">\nrules\n</context>\n<context name="projectName">\n${dir.split("/").pop()}\n</context>\n</swarmos_cached_context>\n\n<swarmos_context>\nAs you answer the user's questions, you can use the following context:\n<context name="currentDate">\n2026-09-05\n</context>\n</swarmos_context>`);
  });
  it("discovers hierarchical AGENTS.md files from repository root to cwd", () => {
    const root = mkdtempSync(join(tmpdir(), "pi-agents-"));
    mkdirSync(join(root, ".git"));
    const nested = join(root, "packages", "demo");
    mkdirSync(nested, { recursive: true });
    writeFileSync(join(root, "AGENTS.md"), "root rules");
    writeFileSync(join(root, "packages", "AGENTS.md"), "package rules");
    writeFileSync(join(nested, "AGENTS.md"), "demo rules");
    expect(discoverAgentsMdPaths(nested)).toEqual([join(root, "AGENTS.md"), join(root, "packages", "AGENTS.md"), join(nested, "AGENTS.md")]);
    expect(buildContextBlock({ workDir: nested, home: "/nonexistent" }).block).toContain("root rules\n\npackage rules\n\ndemo rules");
  });
});

describe("swarm bash envelope", () => {
  it("matches tools.NewXML('result') output", () => {
    const xml = buildResultXML({ exitCode: 0, durationMs: 3, stdout: "hi\n", stderr: "", timedOut: false, requestedSecs: 0, effectiveSecs: 60 });
    expect(xml).toBe("<result exit_code=\"0\" duration_ms=\"3\" timed_out=\"false\">\n  <stdout><![CDATA[hi\n]]></stdout>\n  <stderr><![CDATA[]]></stderr>\n</result>");
    const clamped = buildResultXML({ exitCode: 0, durationMs: 3, stdout: "", stderr: "", timedOut: false, requestedSecs: 5, effectiveSecs: 60, description: "say \"hi\"" });
    expect(clamped).toContain("description=\"say \\\"hi\\\"\">\n  <timeout_clamped requested_seconds=\"5\" effective_seconds=\"60\"/>\n");
    expect(estimateTokens("abc")).toBe(1);
    expect(bashTruncateOutput("x".repeat(60_000)).truncated).toBe(true);
  });
});
