package builtin

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"slices"
	"strings"
	"time"

	sdkerr "github.com/Swarm-Code/mono/swarm-sdk/internal/sdkerr"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/browser"
)

// AgentBrowserParams are the typed parameters for the agent_browser tool.
type AgentBrowserParams struct {
	// Args is the full argument string passed verbatim to the agent-browser CLI.
	// e.g. "open https://example.com", "snapshot -i -c", "click @e2", "get text @e1"
	Args string `json:"args" description:"Full argument string passed to agent-browser. E.g. 'open https://example.com', 'snapshot -i -c', 'click @e2', 'get text @e1'" required:"true"`

	// Session is an optional isolated browser session name. Defaults to \"default\".
	// Use different session names to run multiple independent browser contexts simultaneously.
	Session string `json:"session,omitempty" description:"Isolated session name (default: 'default'). Set AGENT_BROWSER_SESSION env var or use --session flag. Use distinct names for parallel browser contexts."`

	// TimeoutSeconds is how long to wait for the command before timing out.
	TimeoutSeconds int `json:"timeout_seconds,omitempty" description:"Max seconds to wait for the command (default: 30)"`
}

// AgentBrowserTool wraps the agent-browser CLI, giving agents a fast, reliable
// way to automate browsers without ever reaching for Playwright or Puppeteer.
type AgentBrowserTool struct {
	tools.BaseTool
}

// NewAgentBrowserTool creates the agent_browser tool.
func NewAgentBrowserTool() tools.Tool {
	t := &AgentBrowserTool{}
	return tools.Typed[AgentBrowserParams](t)
}

// Name returns the tool name.
func (t *AgentBrowserTool) Name() string { return "agent_browser" }

// Description returns the full usage guide shown to every agent.
func (t *AgentBrowserTool) Description() string {
	return `Fast, headless browser automation CLI for AI agents.

CRITICAL RULE — NEVER USE PLAYWRIGHT:
  Do NOT use Playwright, Puppeteer, Selenium, or any other browser automation
  library directly. ALWAYS use this tool (agent_browser) instead. It is faster,
  requires no Node/Python setup, and is purpose-built for AI agents.

────────────────────────────────────────
USAGE
  Pass the full argument string in the 'args' parameter.
  Example: { "args": "open https://example.com" }

CORE COMMANDS
  open <url>                Navigate to a URL (adds https:// automatically)
  snapshot                  Get the accessibility tree with element refs — USE THIS to find elements before clicking/typing
  snapshot -i               Only interactive elements (buttons, inputs, links)
  snapshot -c               Compact mode — removes empty structural elements
  snapshot -d <n>           Limit tree depth to n levels
  snapshot -s <css>         Scope snapshot to a CSS selector
  click <sel>               Click an element (CSS selector or @ref from snapshot)
  dblclick <sel>            Double-click an element
  type <sel> <text>         Type text into an element (appends to existing value)
  fill <sel> <text>         Clear and fill an element with text
  press <key>               Press a key: Enter, Tab, Escape, Control+a, ArrowDown, etc.
  hover <sel>               Hover over an element
  focus <sel>               Focus an element
  check <sel>               Check a checkbox
  uncheck <sel>             Uncheck a checkbox
  select <sel> <val...>     Select one or more dropdown options
  drag <src> <dst>          Drag element <src> and drop onto <dst>
  upload <sel> <files...>   Upload one or more files via an input element
  download <sel> <path>     Click element and save the downloaded file to <path>
  scroll <dir> [px]         Scroll: up | down | left | right, optional pixel amount
  scrollintoview <sel>      Scroll an element into view
  wait <sel|ms>             Wait for an element to appear, or wait N milliseconds
  screenshot [path]         Take a screenshot (prints base64 if no path given)
  screenshot --full [path]  Full-page screenshot
  pdf <path>                Save the current page as a PDF
  eval <js>                 Run arbitrary JavaScript and return the result
  connect <port|url>        Connect to an existing browser via CDP
  close                     Close the current browser session

NAVIGATION
  back                      Go back in history
  forward                   Go forward in history
  reload                    Reload the current page

GET INFO  (agent_browser get <what> [selector])
  get text <sel>            Visible text content of an element
  get html <sel>            Outer HTML of an element
  get value <sel>           Current value of an input/textarea
  get attr <name> <sel>     Value of a named attribute
  get title                 Page title
  get url                   Current URL
  get count <sel>           Number of matching elements
  get box <sel>             Bounding box: x, y, width, height
  get styles <sel>          Computed CSS styles

CHECK STATE  (agent_browser is <what> <selector>)
  is visible <sel>          Returns "true" / "false"
  is enabled <sel>          Returns "true" / "false"
  is checked <sel>          Returns "true" / "false"

FIND ELEMENTS  (agent_browser find <locator> <value> <action> [text])
  find role <role> click    Find by ARIA role and perform action
  find text <text> click    Find by visible text
  find label <lbl> fill <v> Find by label text and fill
  find placeholder <ph> type <v>
  find alt <alt> click      Find by image alt text
  find title <t> click      Find by title attribute
  find testid <id> click    Find by data-testid attribute
  find first <sel> click    First matching element
  find last <sel> click     Last matching element
  find nth <n> <sel> click  Nth matching element (0-indexed)

MOUSE  (agent_browser mouse <action> [args])
  mouse move <x> <y>        Move mouse to absolute coordinates
  mouse down [btn]          Press mouse button (left/right/middle, default: left)
  mouse up [btn]            Release mouse button
  mouse wheel <dy> [dx]     Scroll wheel delta-y (and optional delta-x)

BROWSER SETTINGS  (agent_browser set <setting> [value])
  set viewport <w> <h>      Set viewport size in pixels
  set device <name>         Emulate a device (e.g. "iPhone 15", "Pixel 7")
  set geo <lat> <lng>       Spoof geolocation
  set offline [on|off]      Toggle offline mode
  set headers <json>        Set HTTP headers as a JSON object
  set credentials <u> <p>   Set HTTP Basic Auth credentials
  set media [dark|light]    Emulate prefers-color-scheme or prefers-reduced-motion

NETWORK
  network route <url> [--abort|--body <json>]   Intercept and mock a URL
  network unroute [url]     Remove a route (all routes if no URL given)
  network requests [--clear] [--filter <pat>]   List captured network requests

STORAGE
  cookies                   List all cookies
  cookies get <name>        Get a specific cookie
  cookies set <json>        Set cookies from JSON
  cookies clear             Clear all cookies
  storage local             Dump localStorage
  storage session           Dump sessionStorage

TABS
  tab new                   Open a new tab
  tab list                  List open tabs
  tab close                 Close the current tab
  tab <n>                   Switch to tab number n (0-indexed)

SESSIONS
  session                   Show current session name
  session list              List all active sessions

DEBUGGING
  console [--clear]         View browser console logs
  errors [--clear]          View page JS errors
  highlight <sel>           Visually highlight an element
  trace start [path]        Start recording a Playwright trace
  trace stop [path]         Stop recording and save the trace
  record start <path> [url] Record video to a WebM file
  record stop               Stop video recording

GLOBAL FLAGS (append to any command)
  --session <name>          Isolated session (or set AGENT_BROWSER_SESSION env var)
  --profile <path>          Persistent browser profile directory
  --headed                  Show the browser window (not headless)
  --cdp <port>              Attach to an existing Chrome via Chrome DevTools Protocol
  --proxy <url>             HTTP/SOCKS proxy: "http://user:pass@host:port"
  --proxy-bypass <hosts>    Comma-separated hosts to bypass the proxy
  --user-agent <ua>         Override the User-Agent header
  --headers <json>          Per-origin HTTP headers for authentication
  --executable-path <path>  Use a custom browser binary
  --extension <path>        Load a browser extension (repeatable)
  --args <args>             Extra browser launch args (comma or newline separated)
  --json                    Return output as JSON
  --full / -f               Full-page screenshot
  --debug                   Verbose debug output

ENVIRONMENT VARIABLES
  AGENT_BROWSER_SESSION          Session name (default: "default")
  AGENT_BROWSER_EXECUTABLE_PATH  Custom browser binary path
  AGENT_BROWSER_PROVIDER         Cloud browser provider
  AGENT_BROWSER_STREAM_PORT      Enable WebSocket streaming (e.g. 9223)
  AGENT_BROWSER_USER_AGENT       Custom User-Agent string
  AGENT_BROWSER_PROXY            Proxy server URL
  AGENT_BROWSER_PROXY_BYPASS     Proxy bypass list
  AGENT_BROWSER_ARGS             Extra browser launch arguments

TYPICAL AGENT WORKFLOW
  1. agent_browser { "args": "open https://example.com" }
  2. agent_browser { "args": "snapshot -i -c" }          // discover refs
  3. agent_browser { "args": "click @e3" }               // use ref from snapshot
  4. agent_browser { "args": "fill @e5 \"my value\"" }
  5. agent_browser { "args": "press Enter" }
  6. agent_browser { "args": "snapshot -i -c" }          // verify state

ELEMENT SELECTORS
  @ref      — Reference from snapshot (e.g. @e2, @e15) — PREFERRED, most reliable
  CSS       — Standard CSS selector (e.g. "#submit", ".btn-primary", "input[type=email]")
  Text      — Use 'find text' subcommand for text-based selection`
}

// Parameters returns the JSON schema for this tool.
func (t *AgentBrowserTool) Parameters() any {
	return tools.SchemaFor[AgentBrowserParams]()
}

// IsIdempotent returns false — browser commands mutate state.
func (t *AgentBrowserTool) IsIdempotent() bool { return false }

// RequiresPermission returns the permissions required by this tool.
func (t *AgentBrowserTool) RequiresPermission() []tools.Permission {
	return []tools.Permission{tools.PermissionBashExecute, tools.PermissionNetworkAccess}
}

// SupportedContentTypes returns the content types this tool can produce.
func (t *AgentBrowserTool) SupportedContentTypes() []tools.ContentType {
	return []tools.ContentType{tools.ContentTypeText}
}

// OptimizationHints provides guidance for efficient tool use.
func (t *AgentBrowserTool) OptimizationHints() *tools.OptimizationHints { return nil }

// Run executes an agent-browser command.
func (t *AgentBrowserTool) Run(ctx context.Context, params AgentBrowserParams) (*tools.ToolResult, error) {
	// Early context check.
	select {
	case <-ctx.Done():
		return nil, sdkerr.Wrap(ctx.Err(), "agent_browser.context_cancelled")
	default:
	}

	if strings.TrimSpace(params.Args) == "" {
		return nil, sdkerr.Permanent("agent_browser.empty_args",
			"args parameter is required — pass the agent-browser subcommand and flags (e.g. \"open https://example.com\")")
	}

	// Verify the binary exists.
	binPath, err := exec.LookPath("agent-browser")
	if err != nil {
		return nil, sdkerr.Permanent("agent_browser.not_found",
			"agent-browser binary not found in PATH — run 'agent-browser install' to install browser binaries")
	}

	// Apply timeout (default 30 s).
	timeout := 30
	if params.TimeoutSeconds > 0 {
		timeout = params.TimeoutSeconds
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()

	// Build the argument list: optionally inject --session before user args.
	var cmdArgs []string
	if params.Session != "" {
		cmdArgs = append(cmdArgs, "--session", params.Session)
	}

	// Split user args by shell-like tokenisation (handle quoted strings).
	userArgs, splitErr := shellSplitArgs(params.Args)
	if splitErr != nil {
		return nil, sdkerr.Permanent("agent_browser.invalid_args",
			fmt.Sprintf("failed to parse args: %v", splitErr))
	}

	// Check for --headed override, otherwise default to --headless
	hasHeaded := slices.Contains(userArgs, "--headed")
	if !hasHeaded {
		cmdArgs = append(cmdArgs, "--headless")
	}

	cmdArgs = append(cmdArgs, userArgs...)

	cmd := exec.CommandContext(ctx, binPath, cmdArgs...)

	// Register browser process for lifecycle management
	sessionID := params.Session
	if sessionID == "" {
		sessionID = "default"
	}
	registry := browser.Global()
	procInfo := &browser.ProcessInfo{
		SessionID:  sessionID,
		ToolType:   "agent-browser",
		StartTime:  time.Now(),
		CancelFunc: cancel,
	}

	// Capture stdout+stderr into a single buffer while still using Start()+Wait()
	// so we can grab the PID for the process registry before the command finishes.
	// NOTE: CombinedOutput() must NOT be called after Start() — it calls Start()
	// internally and returns "exec: already started".
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf

	start := time.Now()
	if err := cmd.Start(); err != nil {
		return nil, sdkerr.Wrap(err, "agent_browser.start_failed")
	}
	procInfo.PID = cmd.Process.Pid
	registry.Register(procInfo)
	defer registry.Unregister(sessionID)

	runErr := cmd.Wait()
	out := buf.Bytes()
	elapsed := time.Since(start)

	output := strings.TrimSpace(string(out))
	if output == "" {
		output = "(no output)"
	}

	exitCode := 0
	if cmd.ProcessState != nil {
		exitCode = cmd.ProcessState.ExitCode()
	}

	result := tools.NewXMLResult(tools.NewXML("result").
		AttrInt("exit_code", int64(exitCode)).
		AttrInt("duration_ms", elapsed.Milliseconds()).
		Field("output", output)).WithDuration(elapsed.Milliseconds())
	result.Metadata["exit_code"] = exitCode
	result.Metadata["duration_ms"] = elapsed.Milliseconds()
	result.Metadata["args"] = params.Args
	if params.Session != "" {
		result.Metadata["session"] = params.Session
	}
	result.Metadata["render_type"] = "ansi"
	result.Metadata["preserve_colors"] = true

	if runErr != nil && ctx.Err() == context.DeadlineExceeded {
		return result, sdkerr.Wrap(
			fmt.Errorf("agent-browser timed out after %ds", timeout),
			"agent_browser.timeout",
		)
	}

	return result, nil
}

// shellSplitArgs splits a string into tokens the same way a POSIX shell would,
// respecting single and double quoted spans. This avoids exec.Command
// re-quoting issues when the agent passes a multi-word args string.
func shellSplitArgs(s string) ([]string, error) {
	var tokens []string
	var current strings.Builder
	inSingle := false
	inDouble := false

	for i := 0; i < len(s); i++ {
		ch := s[i]
		switch {
		case inSingle:
			if ch == '\'' {
				inSingle = false
			} else {
				current.WriteByte(ch)
			}
		case inDouble:
			if ch == '"' {
				inDouble = false
			} else if ch == '\\' && i+1 < len(s) {
				i++
				current.WriteByte(s[i])
			} else {
				current.WriteByte(ch)
			}
		case ch == '\'':
			inSingle = true
		case ch == '"':
			inDouble = true
		case ch == ' ' || ch == '\t' || ch == '\n':
			if current.Len() > 0 {
				tokens = append(tokens, current.String())
				current.Reset()
			}
		default:
			current.WriteByte(ch)
		}
	}

	if inSingle {
		return nil, fmt.Errorf("unterminated single quote")
	}
	if inDouble {
		return nil, fmt.Errorf("unterminated double quote")
	}
	if current.Len() > 0 {
		tokens = append(tokens, current.String())
	}
	return tokens, nil
}
