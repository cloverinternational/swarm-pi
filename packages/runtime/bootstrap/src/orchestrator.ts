import type { BootstrapDraft, BootstrapMode, BootstrapResult, BootstrapSelection, BootstrapSelector, ParallelSelectors } from "./types.js";

function boundSelection(value: unknown): BootstrapSelection {
  const input = value as Partial<BootstrapSelection> | null | undefined;
  return {
    memories: Array.isArray(input?.memories) ? input.memories.slice(0, 8).map((memory) => ({
      ...memory,
      text: String(memory?.text ?? "").slice(0, 1200),
    })) : [],
    skills: Array.isArray(input?.skills) ? input.skills.slice(0, 8).map((skill) => ({
      ...skill,
      body: String(skill?.body ?? "").slice(0, 4000),
    })) : [],
    evidence: Array.isArray(input?.evidence) ? input.evidence.slice(0, 20).map(String) : [],
  };
}

export async function runBootstrap(
  mode: BootstrapMode,
  task: string,
  selector: BootstrapSelector | ParallelSelectors,
  draft?: BootstrapDraft,
  signal: AbortSignal = new AbortController().signal,
  model = "session",
): Promise<BootstrapResult> {
  const resultBase: Pick<BootstrapResult, "mode" | "model" | "usage"> = {
    mode,
    model,
    usage: { selectorCalls: 0, draftCalls: 0, inputChars: task.length, outputChars: 0 },
  };
  if (mode === "off") return { ...resultBase, status: "disabled" };
  if (signal.aborted) return { ...resultBase, status: "cancelled" };

  try {
    let selection: BootstrapSelection;
    if (mode === "parallel") {
      const selectors = typeof selector === "function" ? { memory: selector, skills: selector } : selector;
      const [memory, skills] = await Promise.all([selectors.memory(task, signal), selectors.skills(task, signal)]);
      selection = { memories: [...memory.memories], skills: [...skills.skills], evidence: [...memory.evidence, ...skills.evidence] };
      resultBase.usage.selectorCalls = 2;
    } else {
      selection = await (selector as BootstrapSelector)(task, signal);
      resultBase.usage.selectorCalls = 1;
    }
    if (signal.aborted) return { ...resultBase, status: "cancelled" };
    const bounded = boundSelection(selection);
    const result: BootstrapResult = { ...resultBase, status: "ready", selection: bounded };
    if (draft) {
      result.tasks = (await draft(task, bounded, signal)).slice(0, 50);
      result.usage.draftCalls = 1;
    }
    result.usage.outputChars = JSON.stringify(result).length;
    return result;
  } catch (error) {
    return { ...resultBase, status: "degraded", error: error instanceof Error ? error.message : String(error) };
  }
}
