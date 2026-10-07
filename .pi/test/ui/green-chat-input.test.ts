import { describe, expect, it } from "vitest";
import greenChatInput from "../../../extensions/green-chat-input/extension.ts";

describe("green chat editor integration", () => {
  it("preserves the previously installed input handler while styling its border", () => {
    let handler: ((event: unknown, ctx: any) => void) | undefined;
    greenChatInput({ on: (name: string, callback: typeof handler) => {
      if (name === "session_start") handler = callback;
    } });
    const input = () => "work browser opened";
    const existing = () => ({ handleInput: input, borderColor: undefined as unknown });
    let installed: typeof existing = existing;
    const ui = {
      getEditorComponent: () => installed,
      setEditorComponent: (factory: typeof existing) => { installed = factory; },
    };
    handler?.({}, { ui });
    const editor = installed({} as any, { fg: (_: string, text: string) => `green:${text}` } as any, {} as any) as ReturnType<typeof existing> & { borderColor: (text: string) => string };
    expect(editor.handleInput).toBe(input);
    expect(editor.borderColor("prompt")).toBe("green:prompt");
  });

  it("does not overwrite a missing editor factory", () => {
    let handler: ((event: unknown, ctx: any) => void) | undefined;
    greenChatInput({ on: (_: string, callback: typeof handler) => { handler = callback; } });
    let replaced = false;
    handler?.({}, { ui: { getEditorComponent: () => undefined, setEditorComponent: () => { replaced = true; } } });
    expect(replaced).toBe(false);
  });
});
