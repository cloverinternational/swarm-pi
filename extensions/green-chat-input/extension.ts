/** Give only the chat input border a green accent. */
export default function greenChatInput(pi: any) {
	pi.on("session_start", (_event: any, ctx: any) => {
		// Compose with the installed editor rather than replacing it: swarm-themes
		// owns prompt shortcuts (including the running-work browser's Down key).
		const previous = ctx.ui?.getEditorComponent?.();
		if (!previous) return;
		ctx.ui.setEditorComponent((tui: any, theme: any, keybindings: any) => {
			const editor = previous(tui, theme, keybindings);
			editor.borderColor = (text: string) => theme.fg("success", text);
			return editor;
		});
	});
}
