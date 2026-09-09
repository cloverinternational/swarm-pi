import { contentText } from "@earendil-works/pi-ai";
import {
	buildSessionContext,
	convertToLlm,
	createAgentSession,
	DefaultResourceLoader,
	getAgentDir,
	getMarkdownTheme,
	SessionManager,
	SettingsManager,
	type AgentSession,
	type ExtensionAPI,
	type ExtensionCommandContext,
} from "@earendil-works/pi-coding-agent";
import { Input, Key, Markdown, matchesKey, type Component, type TUI, type Theme } from "@earendil-works/pi-tui";

// Pi-Swarm-owned /btw side channel. It is deliberately in-tree and has no
// runtime dependency on an upstream repository or package.
// This implementation is derived from the Apache-2.0 pi-btw design; see
// LICENSE.pi-btw for the required notice.
const SIDE_PROMPT = "You are a temporary, read-only side agent. Answer directly using the current session as background context. You may inspect files, but never edit them or claim to have changed anything.";
type Entry = { question: string; answer: string; error?: string };

export default function swarmBtw(pi: ExtensionAPI) {
	let side: AgentSession | undefined;
	let active: { question: string; answer: string } | undefined;
	let entries: Entry[] = [];
	let view: BtwView | undefined;
	let closeView: (() => void) | undefined;

	async function ensureSide(ctx: ExtensionCommandContext): Promise<AgentSession | undefined> {
		if (side) return side;
		if (!ctx.model) {
			ctx.ui.notify("No active model is available for /btw.", "error");
			return undefined;
		}
		const prompt = ctx.getSystemPromptOptions();
		const loader = new DefaultResourceLoader({
			cwd: ctx.cwd,
			agentDir: getAgentDir(),
			noExtensions: true,
			noPromptTemplates: true,
			noThemes: true,
			systemPrompt: prompt.customPrompt,
			appendSystemPrompt: [prompt.appendSystemPrompt, SIDE_PROMPT].filter(Boolean).join("\n\n"),
		});
		await loader.reload();
		const manager = SessionManager.inMemory(ctx.cwd);
		const seed = convertToLlm(buildSessionContext(ctx.sessionManager.getEntries(), ctx.sessionManager.getLeafId()).messages);
		for (const message of seed) manager.appendMessage(message);
		const result = await createAgentSession({
			model: ctx.model,
			thinkingLevel: pi.getThinkingLevel(),
			tools: ["read", "grep", "find", "ls"],
			sessionManager: manager,
			resourceLoader: loader,
			settingsManager: SettingsManager.inMemory(),
		});
		side = result.session;
		side.subscribe((event) => {
			if (active && event.type === "message_update" && event.message.role === "assistant") {
				active.answer = contentText(event.message.content).trim();
				view?.invalidate();
			}
		});
		return side;
	}

	async function ask(ctx: ExtensionCommandContext, question: string): Promise<void> {
		if (active) {
			ctx.ui.notify("A /btw question is already running; press Escape to abort it.", "info");
			return;
		}
		const session = await ensureSide(ctx);
		if (!session) return;
		active = { question, answer: "" };
		view?.invalidate();
		try {
			await session.prompt(question, { source: "extension" });
			const response = [...session.messages].reverse().find((message) => message.role === "assistant");
			entries = [...entries, { question, answer: response ? contentText(response.content).trim() : active.answer, error: response?.stopReason === "error" ? response.errorMessage : undefined }].slice(-20);
		} catch (error) {
			entries = [...entries, { question, answer: active.answer, error: error instanceof Error ? error.message : String(error) }].slice(-20);
		} finally {
			active = undefined;
			view?.invalidate();
		}
	}

	function open(ctx: ExtensionCommandContext): void {
		if (view) return view.invalidate();
		let finish!: () => void;
		void ctx.ui.custom<void>((tui, theme, _keys, done) => {
			finish = done;
			closeView = () => { done(); view = undefined; closeView = undefined; };
			view = new BtwView(tui, theme, () => entries, () => active, (question) => void ask(ctx, question), () => { void side?.abort(); }, () => closeView?.());
			return view;
		}, {
			overlay: true,
			overlayOptions: { width: "78%", maxHeight: "78%", anchor: "top-center", margin: { top: 1, left: 2, right: 2 }, nonCapturing: true },
			onHandle: () => undefined,
		}).catch((error) => { view = undefined; ctx.ui.notify(error instanceof Error ? error.message : String(error), "error"); });
		void finish;
	}

	pi.registerCommand("btw", {
		description: "Ask a read-only side question without interrupting the main agent",
		handler: async (args, ctx) => {
			if (ctx.mode !== "tui") return ctx.ui.notify("/btw requires interactive TUI mode.", "error");
			open(ctx);
			if (args.trim()) await ask(ctx, args.trim());
		},
	});
}

class BtwView implements Component {
	private readonly input = new Input();
	constructor(private readonly tui: TUI, private readonly theme: Theme, private readonly readEntries: () => Entry[], private readonly readActive: () => { question: string; answer: string } | undefined, private readonly submit: (value: string) => void, private readonly abort: () => void, private readonly close: () => void) {
		this.input.onSubmit = (value) => { if (value.trim()) { this.input.setValue(""); this.submit(value.trim()); } };
	}
	handleInput(data: string): void {
		if (matchesKey(data, Key.escape)) return this.readActive() ? this.abort() : this.close();
		this.input.handleInput(data);
	}
	render(width: number): string[] {
		const inner = Math.max(30, width - 4);
		const active = this.readActive();
		const latest = this.readEntries().at(-1);
		const body = active ? [`You: ${active.question}`, "", active.answer || "…"] : latest ? [`You: ${latest.question}`, "", ...new Markdown(latest.error || latest.answer || "(empty)", 0, 0, getMarkdownTheme()).render(inner)] : ["No side questions yet."];
		const edge = this.theme.fg("border", "│");
		const line = (text: string) => `${edge} ${text.slice(0, inner).padEnd(inner, " ")} ${edge}`;
		return [this.theme.fg("border", `┌${"─".repeat(inner + 2)}┐`), line(this.theme.fg("accent", "btw · Pi-Swarm side question")), this.theme.fg("border", `├${"─".repeat(inner + 2)}┤`), ...body.map(line), this.theme.fg("border", `├${"─".repeat(inner + 2)}┤`), line(this.input.render(inner)[0] ?? ""), line(this.theme.fg("dim", "Enter ask · Escape abort/close")), this.theme.fg("border", `└${"─".repeat(inner + 2)}┘`)];
	}
	invalidate(): void { this.tui.requestRender(); }
}
