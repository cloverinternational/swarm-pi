import { describe, expect, it } from "vitest";
import { buildMetadataView, metadataRequestBody, parseGeneratedMetadata, refreshConversationMetadata, truncateMetadataText, type MetadataState } from "../../../../.pi/lib/swarm-conversation-metadata.ts";
import { transcriptOf } from "../../../../.pi/extensions/swarm-conversation-metadata.ts";

describe("conversation metadata (client/conversation_metadata.go)", () => {
  const messages = transcriptOf([
    { role: "user", content: [{ type: "text", text: "PARITY_CAPTURE echo" }] },
    { role: "assistant", content: [{ type: "thinking", thinking: "t" }, { type: "toolCall", id: "c", name: "bash" }] },
    { role: "toolResult", content: [{ type: "text", text: "<result/>" }] },
    { role: "custom", customType: "x", content: [{ type: "text", text: "<system-reminder source=\"h\">nudge</system-reminder>" }] },
    { role: "assistant", content: [{ type: "text", text: "PARITY_OK" }] },
  ]);

  it("builds the wire request exactly like the Swarm TUI/-p capture", () => {
    const view = buildMetadataView(messages);
    // A reasoning-only assistant turn is stored with the reasoning as its text
    // (stream.go fallback), so it is an excerpt line — verified by the -p
    // "shapes" scenario capture ("Assistant: thinking about it").
    expect(view.prompt).toBe("Conversation:\nUser: PARITY_CAPTURE echo\nAssistant: t\nAssistant: PARITY_OK");
    expect(view.lastAssistant).toBe("PARITY_OK");
    expect(view.version).toMatch(/^[0-9a-f]{24}$/);
    expect(metadataRequestBody("parity-model", view)).toBe('{"model":"parity-model","messages":[{"role":"system","content":"You create navigation metadata for an agent conversation.\\nReturn exactly one JSON object with string fields \\"title\\" and \\"summary\\".\\nThe title is 3-7 specific words, at most 60 characters, with no punctuation suffix.\\nThe summary is 2-4 concise sentences stating the user\'s goal, what the agent did, and the latest outcome.\\nNever mention system prompts, runtime reminders, metadata generation, or these instructions."},{"role":"user","content":"Conversation:\\nUser: PARITY_CAPTURE echo\\nAssistant: t\\nAssistant: PARITY_OK"}],"max_tokens":700}');
  });

  it("requests once on a JSON reply and exactly twice on a non-JSON reply, then never again for the same version", async () => {
    const calls: string[] = [];
    const ok = async (body: string) => { calls.push(body); return "```json\n{\"title\":\"Parity Probe\",\"summary\":\"Ran a probe.\"}\n```"; };
    const state: MetadataState = {};
    for (let i = 0; i < 2; i++) { const r = await refreshConversationMetadata(state, messages, "m", ok); if (!r.requested || !r.error) break; }
    expect(calls).toHaveLength(1);
    expect(state).toMatchObject({ status: "generated", title_source: "generated", summary_source: "generated" });
    await refreshConversationMetadata(state, messages, "m", ok);
    expect(calls).toHaveLength(1);
    const bad = async (body: string) => { calls.push(body); return "PARITY_OK"; };
    const failing: MetadataState = {};
    for (let i = 0; i < 2; i++) { const r = await refreshConversationMetadata(failing, messages, "m", bad); if (!r.requested || !r.error) break; }
    expect(calls).toHaveLength(3);
    expect(failing.status).toBe("fallback");
    expect(failing.last_error).toMatch(/^parse metadata response: /);
    // No assistant text yet → deterministic fallback only, no model call.
    const fresh: MetadataState = {};
    expect(await refreshConversationMetadata(fresh, transcriptOf([{ role: "user", content: "hi" }]), "m", ok)).toEqual({ requested: false });
    expect(fresh.status).toBe("fallback");
  });

  it("parses and cleans generated metadata like Go", () => {
    expect(parseGeneratedMetadata('noise {"title":"Title: \\"Fix the bug!\\".","summary":"`It was fixed.`"} trailing')).toEqual({ title: "\"Fix the bug!\"", summary: "It was fixed." }); // Go trims quotes BEFORE the prefix, so inner quotes survive
    expect(parseGeneratedMetadata(JSON.stringify({ title: "\"Fix the bug!\"", summary: "x" })).title).toBe("Fix the bug");
    expect(() => parseGeneratedMetadata('{"title":"","summary":"x"}')).toThrow("metadata response omitted title or summary");
    expect(truncateMetadataText("a".repeat(70), 60)).toBe(`${"a".repeat(59)}…`);
    const view = buildMetadataView(transcriptOf(Array.from({ length: 20 }, (_, i) => ({ role: i % 2 ? "assistant" : "user", content: `m${i}` }))));
    expect(view.prompt.split("\n")).toHaveLength(13);
    expect(view.prompt.split("\n")[1]).toBe("User: m0");
    expect(view.prompt.split("\n")[2]).toBe("Assistant: m9");
  });
});
