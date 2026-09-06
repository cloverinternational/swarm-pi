const MAX_DISPLAY_CHARS = 20_000;
/** Deliberately dependency-free: this helper is also used by headless tests and adapters. */
class ToolOutputComponent {
    value;
    constructor(value) { this.value = value; }
    render(width) {
        if (!this.value)
            return [""];
        const lines = [];
        for (const source of this.value.split("\n")) {
            if (width <= 0 || source.length <= width) {
                lines.push(source);
                continue;
            }
            for (let offset = 0; offset < source.length; offset += width)
                lines.push(source.slice(offset, offset + width));
        }
        return lines;
    }
    invalidate() { }
}
function stringify(value) {
    if (typeof value === "string")
        return value;
    try {
        return JSON.stringify(value, null, 2) ?? String(value);
    }
    catch {
        return String(value);
    }
}
function output(result, expanded) {
    const parts = Array.isArray(result?.content) ? result.content : [];
    const text = parts.filter((part) => part?.type === "text" && typeof part.text === "string").map((part) => part.text).join("\n");
    const images = parts.filter((part) => part?.type === "image").map((part) => `[image: ${part.mimeType ?? "unknown"}]`);
    const body = [text, ...images].filter(Boolean).join("\n");
    const details = expanded && result?.details !== undefined ? stringify(result.details) : "";
    const value = [body, details ? `details:\n${details}` : ""].filter(Boolean).join("\n");
    if (value.length <= MAX_DISPLAY_CHARS)
        return value;
    return `${value.slice(0, MAX_DISPLAY_CHARS)}\n… [display truncated]`;
}
/** Adds only the missing renderer; tool-specific renderers remain authoritative. */
export function withDefaultToolRenderer(tool) {
    if (typeof tool.renderResult === "function")
        return tool;
    return {
        ...tool,
        renderResult(result, options, theme) {
            const value = output(result, Boolean(options?.expanded));
            const label = options?.isPartial ? "…" : result?.isError || options?.isError ? "✗ " : "";
            const failed = result?.isError || options?.isError;
            const raw = `${label}${value || (options?.isPartial ? "working" : failed ? "failed" : "done")}`;
            const styled = failed && typeof theme?.fg === "function" ? theme.fg("error", raw) : raw;
            return new ToolOutputComponent(styled);
        },
    };
}
