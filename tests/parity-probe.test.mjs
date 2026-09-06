import assert from "node:assert/strict";
import test from "node:test";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import {
  EXPECTED_PRIMARY_REQUESTS,
  canonicalizeRequest,
  captureParity,
  diffJson,
  primarySystemPrompt,
  promptSliceEvidence,
  sharedPromptPrefix,
  summarizeMismatches,
} from "../tools/parity/probe.mjs";

test("canonicalizer replaces generated tool call IDs consistently", () => {
  const request = canonicalizeRequest({
    messages: [
      { role: "assistant", tool_calls: [{ id: "generated-123", function: { name: "bash" } }] },
      { role: "tool", tool_call_id: "generated-123", content: "ok" },
    ],
  });
  assert.equal(request.messages[0].tool_calls[0].id, "<tool-call-1>");
  assert.equal(request.messages[1].tool_call_id, "<tool-call-1>");
});

test("canonicalizer preserves prompt, tool schema, order, and semantic request fields", () => {
  const request = {
    model: "same-model",
    messages: [{ role: "developer", content: "exact prompt\n" }],
    tools: [
      { type: "function", function: { name: "b", parameters: { type: "object" } } },
      { type: "function", function: { name: "a", parameters: { type: "object" } } },
    ],
    reasoning_effort: "high",
  };
  assert.deepEqual(canonicalizeRequest(request), {
    messages: [{ content: "exact prompt\n", role: "developer" }],
    model: "same-model",
    reasoning_effort: "high",
    tools: [
      { function: { name: "b", parameters: { type: "object" } }, type: "function" },
      { function: { name: "a", parameters: { type: "object" } }, type: "function" },
    ],
  });
});

test("JSON diff reports exact pointers and does not hide semantic differences", () => {
  const differences = diffJson(
    { messages: [{ role: "system", content: "one" }], tools: [{ name: "read" }] },
    { messages: [{ role: "developer", content: "two" }], tools: [{ name: "bash" }], temperature: 0 },
  );
  assert.deepEqual(differences, [
    { path: "/messages/0/content", kind: "changed", left: "one", right: "two" },
    { path: "/messages/0/role", kind: "changed", left: "system", right: "developer" },
    { path: "/temperature", kind: "missing-left", right: 0 },
    { path: "/tools/0/name", kind: "changed", left: "read", right: "bash" },
  ]);
  assert.deepEqual(summarizeMismatches(differences), { messages: 2, temperature: 1, tools: 1 });
});

test("mismatch summary skips the request-array index", () => {
  assert.deepEqual(summarizeMismatches([
    { path: "/0/tools/3/function/name", kind: "changed" },
    { path: "/1/messages/4/content", kind: "changed" },
    { path: "/1/tools/0/function/parameters", kind: "changed" },
  ]), { tools: 2, messages: 1 });
});

test("project prompt alignment excludes host-owned context and skill suffixes", () => {
  const prompt = "shared\n\n<context_file path=\"AGENTS.md\">\nlocal\n</context_file>\n\n<available_skills>x</available_skills>";
  const requests = [{
    messages: [
      { role: "developer", content: prompt },
      { role: "user", content: "PARITY_CAPTURE" },
    ],
  }];
  assert.equal(primarySystemPrompt(requests), prompt);
  assert.equal(sharedPromptPrefix(prompt), "shared");
  assert.deepEqual(promptSliceEvidence(`before\n${prompt}\nafter`, prompt), {
    offset: 7,
    present: true,
    exact: true,
  });
  assert.deepEqual(promptSliceEvidence("different", prompt), {
    offset: -1,
    present: false,
    exact: false,
  });
  assert.equal(
    sharedPromptPrefix(`<available_skills>\n  <skill>x</skill>\n</available_skills>\nWhen a user request matches an available skill, invoke the Skill tool before responding.\n\n${prompt}`),
    "shared",
  );
});

test("clean profile captures matched isolated Pi and Swarm request sequences", async () => {
  const output = await mkdtemp(join(tmpdir(), "pi-swarm-parity-test-"));
  try {
    const result = await captureParity({
      profile: "clean",
      workspace: resolve("."),
      output,
    });
    assert.equal(result.profile, "clean");
    assert.equal(result.pi.requests.length, EXPECTED_PRIMARY_REQUESTS);
    assert.equal(result.swarm.requests.length, EXPECTED_PRIMARY_REQUESTS);
    assert.deepEqual(
      result.pi.tools.map(tool => tool.function?.name ?? tool.name),
      ["bash"],
    );
    assert.deepEqual(
      result.swarm.tools.map(tool => tool.function?.name ?? tool.name),
      ["bash"],
    );
    // The goal condition: identical JSON at the model boundary. Timing fields
    // (duration_ms) and random error ids are normalised by the canonicaliser;
    // everything else — system prompt, user turn + capability manifest, tool
    // schema, request options, tool call/result — must match byte for byte.
    assert.equal(result.pi.prompt.sha256, result.swarm.prompt.sha256);
    assert.deepEqual(result.mismatches, [], `pi -p and swarm -p diverged: ${JSON.stringify(result.mismatchCategories)}`);
    // Stronger than the structural diff: the raw request bodies must agree
    // byte for byte (key order included) once run-random ids/timings and Go's
    // \u003c HTML escaping are folded.
    assert.equal(result.wire.identical, true, `wire bytes diverged: ${JSON.stringify(result.wire.requests)}`);
  } finally {
    await rm(output, { recursive: true, force: true });
  }
}, { timeout: 30_000 });

test("project profile is wire-identical with full skills, context, and the 28-tool surface", async () => {
  const output = await mkdtemp(join(tmpdir(), "pi-swarm-parity-test-"));
  try {
    const result = await captureParity({ profile: "project", workspace: resolve("."), output });
    assert.equal(result.pi.requests.length, EXPECTED_PRIMARY_REQUESTS);
    assert.equal(result.swarm.requests.length, EXPECTED_PRIMARY_REQUESTS);
    assert.equal(result.pi.tools.length, 28);
    assert.equal(result.swarm.tools.length, 28);
    assert.equal(result.pi.skills.length, 1);
    assert.equal(result.swarm.skills.length, 1);
    assert.equal(result.pi.prompt.sha256, result.swarm.prompt.sha256);
    assert.deepEqual(result.mismatches, [], `pi -p and swarm -p diverged: ${JSON.stringify(result.mismatchCategories)}`);
    assert.equal(result.wire.identical, true, `wire bytes diverged: ${JSON.stringify(result.wire.requests)}`);
  } finally {
    await rm(output, { recursive: true, force: true });
  }
}, { timeout: 120_000 });

test("unknown capture profiles are rejected before launching either runtime", async () => {
  await assert.rejects(
    captureParity({ profile: "invalid", workspace: resolve(".") }),
    /unknown parity profile: invalid/,
  );
});
