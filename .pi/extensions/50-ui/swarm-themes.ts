import { CustomEditor } from "@earendil-works/pi-coding-agent";
import { handleRunningWorkInput } from "../../lib/ui/running-work.ts";

/**
 * Pi presentation adapter for the Swarm TUI palette and prompt input.
 *
 * The editor already owns ordinary up/down history navigation. This small
 * adapter adds readline-style reverse search without changing submitted text:
 * Ctrl-R searches the prompts captured by addToHistory(), and repeated Ctrl-R
 * moves to the next older match.
 */
class SwarmPromptEditor extends CustomEditor {
  constructor(tui: any, theme: any, keybindings: any, private readonly runningWorkContext: any) { super(tui, theme, keybindings); }
  private promptHistory: string[] = [];
  private reverseSearchQuery: string | undefined;
  private reverseSearchIndex = -1;
  private reverseSearchDraft = "";

  addToHistory(text: string): void {
    const prompt = text.trim();
    if (prompt && this.promptHistory[0] !== prompt) {
      this.promptHistory.unshift(prompt);
      if (this.promptHistory.length > 100) this.promptHistory.pop();
    }
    super.addToHistory(text);
  }

  private score(prompt: string, query: string): number | undefined {
    const candidate = prompt.toLocaleLowerCase();
    const needle = query.trim().toLocaleLowerCase();
    if (!needle) return 0;
    if (candidate === needle) return 10000;
    const exact = candidate.indexOf(needle);
    if (exact >= 0) {
      // Prefer word-boundary and earlier matches, while retaining recency as
      // the final tie-breaker in the caller. This is more useful than a plain
      // substring scan for prompts such as "fix tests" and "fix tests again".
      const boundary = exact === 0 || /\s/.test(candidate[exact - 1] ?? "") ? 1000 : 0;
      return 5000 + boundary - exact;
    }
    // Fuzzy subsequence matching: tolerate omitted spaces/words, but penalize
    // gaps so a nearly typed command beats an accidental broad match.
    let cursor = 0;
    let gaps = 0;
    for (const char of needle) {
      const found = candidate.indexOf(char, cursor);
      if (found < 0) return undefined;
      gaps += found - cursor;
      cursor = found + 1;
    }
    return 1000 - gaps - (candidate.length - needle.length) / 100;
  }

  private findNextReverseMatch(): void {
    const query = this.reverseSearchQuery ?? "";
    const matches = this.promptHistory
      .map((prompt, index) => ({ prompt, index, score: this.score(prompt, query) }))
      .filter((match): match is { prompt: string; index: number; score: number } => match.score !== undefined)
      .sort((a, b) => b.score - a.score || a.index - b.index);
    if (matches.length === 0) return;
    const current = matches.findIndex((match) => match.index === this.reverseSearchIndex);
    const next = matches[(current + 1) % matches.length];
    this.reverseSearchIndex = next.index;
    this.setText(next.prompt);
  }

  handleInput(data: string): void {
    if (handleRunningWorkInput(data, { ...this.runningWorkContext, editor: this })) return;
    // Ctrl-R is sent as DC2 by ordinary terminal input. Kitty's disambiguated
    // mode uses CSI-u, so accept that representation too.
    const reverseSearch = data === "\x12" || data === "\x1b[114;5u";
    if (reverseSearch) {
      if (this.reverseSearchQuery === undefined) {
        this.reverseSearchDraft = this.getText();
        this.reverseSearchQuery = this.reverseSearchDraft;
        this.reverseSearchIndex = -1;
      }
      this.findNextReverseMatch();
      return;
    }

    if (this.reverseSearchQuery !== undefined) {
      // Escape accepts the selected result, while Ctrl-C cancels and restores
      // the exact draft that existed when reverse search began. Enter executes
      // the selected prompt, matching Claude Code's search-mode contract.
      if (data === "\x1b" || data === "\t") {
        this.reverseSearchQuery = undefined;
        this.reverseSearchIndex = -1;
        return;
      }
      if (data === "\x03" || data === "\x07") {
        this.setText(this.reverseSearchDraft);
        this.reverseSearchQuery = undefined;
        this.reverseSearchIndex = -1;
        return;
      }
      if (data === "\r" || data === "\n") {
        this.reverseSearchQuery = undefined;
        this.reverseSearchIndex = -1;
        super.handleInput(data);
        return;
      }
      // Any edit starts a fresh normal input, just as leaving a search field
      // and modifying its result should not keep cycling the old query.
      this.reverseSearchQuery = undefined;
      this.reverseSearchIndex = -1;
    }
    super.handleInput(data);
  }
}

export default function swarmThemes(pi: any) {
  pi.on("session_start", (_event: any, ctx: any) => {
    if (ctx.ui?.getAllThemes?.().some((t: any) => t.name === "swarm-swarmcode") && !process.env.PI_THEME) {
      ctx.ui.setTheme?.("swarm-swarmcode");
    }
    ctx.ui?.setEditorComponent?.((tui: any, theme: any, keybindings: any) =>
      new SwarmPromptEditor(tui, theme, keybindings, { ui: ctx.ui }),
    );
  });
}
