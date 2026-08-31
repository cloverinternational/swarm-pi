package commands

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/launch"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider/anthropic"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider/gemini"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider/openai"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider/xai"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/safego"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
	"github.com/atotto/clipboard"
	"github.com/charmbracelet/x/ansi"
)

// AuthCommand implements the /auth command for OAuth management
type AuthCommand struct {
	interactive    bool
	deviceFlow     *anthropic.DeviceFlow
	openaiFlow     *openai.DeviceFlow
	geminiManager  *gemini.OAuthManager
	xaiLoopback    *xai.LoopbackFlow // xAI PKCE loopback flow
	openaiUserCode string
	openaiCopyOK   bool
	openaiCopyAt   time.Time
	state          string // "menu", "login_waiting_url", "login_waiting_code", "login_exchanging", "login_success", "login_error", "view_token", "xai_browser", "xai_importing"
	authURL        string
	errorMessage   string
	accessToken    string
	loginProvider  string
	width          int
	height         int
	inputValue     string
	cursorPos      int
	selectedMenu   int
	onComplete     func(provider string) error // Callback rebuilds the live provider after credentials change
}

type geminiFlowMsg struct {
	authURL string
	resCh   <-chan *gemini.OAuthTokens
	errCh   <-chan error
	cleanup func()
	err     error
}

type geminiResultMsg struct {
	token *gemini.OAuthTokens
	err   error
}

type openaiFlowMsg struct {
	flow *openai.DeviceFlow
	err  error
}

type openaiResultMsg struct {
	token *openai.OAuthToken
	err   error
}

type openaiCopyNoticeMsg struct {
	at time.Time
}

// xAI / SuperGrok OAuth flow messages
type xaiFlowMsg struct {
	flow    *xai.LoopbackFlow // nil when Grok CLI import succeeded
	authURL string
	err     error
}

type xaiResultMsg struct {
	token *xai.OAuthToken
	err   error
}

const openaiCopyNoticeDuration time.Duration = 2 * time.Second

// NewAuthCommand creates a new /auth command
func NewAuthCommand() *AuthCommand {
	return &AuthCommand{
		interactive:   false,
		state:         "menu",
		selectedMenu:  0,
		inputValue:    "",
		cursorPos:     0,
		loginProvider: "claude",
		width:         80,
		height:        24,
	}
}

// Name returns the command name
func (a *AuthCommand) Name() string {
	return "auth"
}

// Description returns command description
func (a *AuthCommand) Description() string {
	return i18n.T("commands.auth.description")
}

// Aliases returns command aliases
func (a *AuthCommand) Aliases() []string {
	return []string{"login", "oauth"}
}

// Execute shows the auth menu
func (a *AuthCommand) Execute(args []string) tea.Cmd {
	a.interactive = true
	a.resetMenuState()
	return nil
}

// ExecuteForProvider starts the auth flow for a specific provider directly,
// skipping the menu. providerName should match the provider's Name field
// (e.g. "ClaudeCode", "OpenAI", "Gemini").
func (a *AuthCommand) ExecuteForProvider(providerName string) tea.Cmd {
	a.interactive = true
	a.resetMenuState()
	switch strings.ToLower(strings.TrimSpace(providerName)) {
	case "claudecode", "anthropic", "claude":
		a.state = "login_waiting_url"
		a.deviceFlow = anthropic.NewDeviceFlow()
		a.loginProvider = "claude"
		safego.Go("commands.auth.initiateDeviceFlow", func() {
			deviceResp, err := a.deviceFlow.InitiateDeviceFlow()
			if err != nil {
				a.state = "login_error"
				a.errorMessage = err.Error()
				return
			}
			a.authURL = deviceResp.VerificationURIComplete
			a.state = "login_waiting_code"
			_ = openBrowser(a.authURL)
		})
		return nil
	case "openai", "codex":
		// codex (ChatGPT-subscription OAuth) shares the OpenAI device flow.
		a.state = "openai_waiting_code"
		a.loginProvider = "openai"
		a.openaiCopyOK = false
		a.openaiCopyAt = time.Time{}
		return a.startOpenAICmd()
	case "gemini":
		a.state = "gemini_polling"
		a.loginProvider = "gemini"
		return a.startGeminiCmd()
	case "xai", "grok", "supergrok", "xai-oauth", "grok-oauth":
		a.state = "xai_importing"
		a.loginProvider = "xai"
		return a.startXAICmd()
	default:
		// Unknown provider — fall back to the generic menu
		a.state = "menu"
		return nil
	}
}

// openBrowser opens the given URL in the default browser
func openBrowser(url string) error {
	return launch.Open(url)
}

// IsInteractive returns true (this command has interactive UI)
func (a *AuthCommand) IsInteractive() bool {
	return a.interactive
}

// SetOnComplete sets the callback for when login completes
func (a *AuthCommand) SetOnComplete(fn func(provider string) error) {
	a.onComplete = fn
}

// NotifyCredentialChanged routes non-command OAuth completions (for example,
// Settings) through the same application lifecycle callback as /auth.
func (a *AuthCommand) NotifyCredentialChanged(provider string) error {
	if a.onComplete == nil {
		return nil
	}
	return a.onComplete(provider)
}

// Update handles user input
func (a *AuthCommand) Update(msg tea.Msg) (Command, tea.Cmd) {
	if !a.interactive {
		return a, nil
	}

	switch msg := msg.(type) {
	case tea.KeyMsg:
		key := msg.String()

		// Menu state
		if a.state == "menu" {
			switch key {
			case "esc":
				a.interactive = false
				a.resetMenuState()
				return a, nil

			case "up", "k":
				if a.selectedMenu > 0 {
					a.selectedMenu--
				}

			case "down", "j":
				if a.selectedMenu < 5 { // 6 menu items (0–5)
					a.selectedMenu++
				}

			case "tab":
				if a.selectedMenu == 0 {
					a.selectedMenu = 1
				} else {
					a.selectedMenu = 0
				}

			case "1", "2", "3", "4", "5", "6":
				idx := int(key[0] - '1')
				if idx >= 0 && idx <= 5 {
					a.selectedMenu = idx
				}

			case "enter":
				// Execute selected action
				switch a.selectedMenu {
				case 0: // New Claude login
					a.state = "login_waiting_url"
					a.deviceFlow = anthropic.NewDeviceFlow()
					a.inputValue = ""
					a.cursorPos = 0
					a.loginProvider = "claude"

					safego.Go("commands.auth.initiateDeviceFlow.menu", func() {
						deviceResp, err := a.deviceFlow.InitiateDeviceFlow()
						if err != nil {
							a.state = "login_error"
							a.errorMessage = err.Error()
							return
						}

						a.authURL = deviceResp.VerificationURIComplete
						a.state = "login_waiting_code"

						_ = openBrowser(a.authURL)
					})

				case 1: // New OpenAI/Codex login
					a.state = "openai_waiting_code"
					a.loginProvider = "openai"
					a.openaiCopyOK = false
					a.openaiCopyAt = time.Time{}
					return a, a.startOpenAICmd()

				case 2: // New Gemini login
					a.state = "gemini_polling"
					a.loginProvider = "gemini"
					return a, a.startGeminiCmd()

				case 3: // New xAI / Grok login (SuperGrok OAuth)
					a.state = "xai_importing"
					a.loginProvider = "xai"
					return a, a.startXAICmd()

				case 4: // View current token
					a.state = "view_token"

				case 5: // Refresh tokens
					go a.refreshTokens()
				}
			}

			// Success/Error/View states
		} else if a.state == "login_success" || a.state == "login_error" || a.state == "view_token" {
			if key == "esc" || key == "enter" {
				a.resetMenuState()
			}
		} else if a.state == "login_waiting_code" {
			// Handle Esc in code input state
			if key == "esc" {
				a.resetMenuState()
				return a, nil
			}
			// Handle Enter to submit
			if key == "enter" {
				code := strings.TrimSpace(a.inputValue)
				if code != "" {
					a.state = "login_exchanging"

					safego.Go("commands.auth.exchangeCode", func() {
						token, err := a.deviceFlow.ExchangeCodeForToken(code)
						if err != nil {
							a.state = "login_error"
							a.errorMessage = i18n.T("commands.auth.error.exchange_code", err)
							return
						}

						if err := anthropic.StoreOAuthToken(token); err != nil {
							a.state = "login_error"
							a.errorMessage = i18n.T("commands.auth.error.store_token", err)
							return
						}

						a.accessToken = token.AccessToken
						a.state = "login_success"

						if a.onComplete != nil {
							if err := a.onComplete("ClaudeCode"); err != nil {
								a.state = "login_error"
								a.errorMessage = err.Error()
							}
						}
					})
				}
				return a, nil
			}

			// Handle Ctrl+V paste from clipboard
			if key == "ctrl+v" {
				content, err := clipboard.ReadAll()
				if err == nil && content != "" {
					a.inputValue = strings.TrimSpace(content)
					a.cursorPos = len(a.inputValue)
				}
				return a, nil
			}

			// Handle backspace
			if key == "backspace" {
				if a.cursorPos > 0 {
					a.inputValue = a.inputValue[:a.cursorPos-1] + a.inputValue[a.cursorPos:]
					a.cursorPos--
				}
				return a, nil
			}

			// Handle delete
			if key == "delete" {
				if a.cursorPos < len(a.inputValue) {
					a.inputValue = a.inputValue[:a.cursorPos] + a.inputValue[a.cursorPos+1:]
				}
				return a, nil
			}

			// Handle left arrow
			if key == "left" {
				if a.cursorPos > 0 {
					a.cursorPos--
				}
				return a, nil
			}

			// Handle right arrow
			if key == "right" {
				if a.cursorPos < len(a.inputValue) {
					a.cursorPos++
				}
				return a, nil
			}

			// Handle home
			if key == "home" {
				a.cursorPos = 0
				return a, nil
			}

			// Handle end
			if key == "end" {
				a.cursorPos = len(a.inputValue)
				return a, nil
			}

			// Handle regular text input
			text := msg.Key().Text
			if text != "" && len(text) < 50 { // Reasonable limit per keystroke
				a.inputValue = a.inputValue[:a.cursorPos] + text + a.inputValue[a.cursorPos:]
				a.cursorPos += len(text)
			}
		} else if a.state == "openai_waiting_code" || a.state == "openai_polling" {
			if key == "esc" {
				a.resetMenuState()
			}
			if (key == "ctrl+y" || key == "cmd+y") && a.openaiUserCode != "" {
				var cmd tea.Cmd = a.copyOpenAICode()
				return a, cmd
			}
		}

	case tea.PasteMsg:
		// Handle paste events (bracketed paste mode)
		if a.state == "login_waiting_code" {
			pasteContent := strings.TrimSpace(msg.Content)
			if pasteContent != "" {
				a.inputValue = pasteContent
				a.cursorPos = len(a.inputValue)
			}
		}

	case tea.WindowSizeMsg:
		a.width = msg.Width
		a.height = msg.Height

	case tea.MouseClickMsg:
		var mouse tea.Mouse = msg.Mouse()
		if mouse.Button != tea.MouseLeft {
			return a, nil
		}
		if a.openAICodeHit(mouse.X, mouse.Y) {
			var cmd tea.Cmd = a.copyOpenAICode()
			return a, cmd
		}
		return a, nil

	case tea.MouseMsg:
		// Explicitly ignore mouse up/down/drag to avoid unintended state changes while selecting text.
		return a, nil

	// Async OpenAI device flow started
	case openaiFlowMsg:
		if msg.err != nil {
			a.state = "login_error"
			a.errorMessage = formatOpenAIError(i18n.T("commands.auth.error.openai_start"), msg.err)
			debugAuthLog("openai oauth: start device flow error: %v", msg.err)
			return a, nil
		}

		a.openaiFlow = msg.flow
		a.openaiUserCode = msg.flow.UserCode
		a.openaiCopyOK = false
		a.openaiCopyAt = time.Time{}
		a.authURL = msg.flow.Verification
		a.state = "openai_polling"
		debugAuthLog("openai oauth: device flow ready interval=%d", msg.flow.Interval)
		if err := openBrowser(a.authURL); err != nil {
			debugAuthLog("openai oauth: failed to open browser: %v", err)
		}
		// Do NOT auto-copy; only copy on explicit user action to avoid stale codes on accidental focus.
		return a, a.pollOpenAICmd(msg.flow)

	case openaiResultMsg:
		if msg.err != nil {
			a.state = "login_error"
			a.errorMessage = formatOpenAIError(i18n.T("commands.auth.error.openai_failed"), msg.err)
			debugAuthLog("openai oauth: flow error: %v", msg.err)
			a.accessToken = ""
			return a, nil
		}

		token := msg.token
		if token.APIKey == "" && openai.HasModelRequestScope(token.AccessToken) {
			token.APIKey = token.AccessToken
		}

		if err := openai.StoreOAuthToken(token); err != nil {
			a.state = "login_error"
			a.errorMessage = i18n.T("commands.auth.error.openai_store", err)
			debugAuthLog("openai oauth: store token error: %v", err)
			return a, nil
		}

		var accessToken string = token.APIKey
		if accessToken == "" {
			accessToken = token.AccessToken
		}
		a.accessToken = accessToken
		a.state = "openai_success"
		debugAuthLog("openai oauth: completed")

		if a.onComplete != nil {
			if err := a.onComplete("codex"); err != nil {
				a.state = "login_error"
				a.errorMessage = err.Error()
				return a, nil
			}
		}

		// Auto-close the dialog after success to avoid stuck UI
		a.interactive = false
		a.state = "menu"

	case geminiFlowMsg:
		if msg.err != nil {
			a.state = "login_error"
			a.errorMessage = formatGeminiError(i18n.T("commands.auth.error.gemini_start"), msg.err)
			debugAuthLog("gemini oauth: start flow error: %v", msg.err)
			return a, nil
		}

		a.authURL = msg.authURL
		// We are already in polling state (set in Enter handler), but just to be sure
		a.state = "gemini_polling"

		return a, a.pollGeminiCmd(msg.resCh, msg.errCh)

	case geminiResultMsg:
		if msg.err != nil {
			a.state = "login_error"
			a.errorMessage = formatGeminiError(i18n.T("commands.auth.error.gemini_failed"), msg.err)
			debugAuthLog("gemini oauth: flow error: %v", msg.err)
			a.accessToken = ""
			return a, nil
		}

		// Save tokens using the manager (we updated oauth.go to export SaveTokens)
		if a.geminiManager != nil {
			if err := a.geminiManager.SaveTokens(msg.token); err != nil {
				a.state = "login_error"
				a.errorMessage = i18n.T("commands.auth.error.gemini_store", err)
				debugAuthLog("gemini oauth: store token error: %v", err)
				return a, nil
			}
		}

		a.accessToken = msg.token.AccessToken
		a.state = "login_success"
		debugAuthLog("gemini oauth: completed")

		if a.onComplete != nil {
			if err := a.onComplete("Gemini"); err != nil {
				a.state = "login_error"
				a.errorMessage = err.Error()
				return a, nil
			}
		}

		// Auto-close the dialog after success to avoid stuck UI
		a.interactive = false
		a.state = "menu"

	case openaiCopyNoticeMsg:
		if a.openaiCopyOK && a.openaiCopyAt.Equal(msg.at) {
			a.openaiCopyOK = false
			a.openaiCopyAt = time.Time{}
		}

	case xaiFlowMsg:
		if msg.err != nil {
			a.state = "login_error"
			a.errorMessage = i18n.T("commands.auth.error.xai_start", msg.err)
			debugAuthLog("xai oauth: start flow error: %v", msg.err)
			return a, nil
		}
		// Loopback flow started — show the auth URL and wait for the browser callback.
		a.xaiLoopback = msg.flow
		a.authURL = msg.authURL
		a.state = "xai_browser"
		debugAuthLog("xai oauth: browser URL ready: %s", msg.authURL)
		_ = openBrowser(msg.authURL)
		return a, a.waitXAICallbackCmd()

	case xaiResultMsg:
		if msg.err != nil {
			a.state = "login_error"
			a.errorMessage = i18n.T("commands.auth.error.xai_failed", msg.err)
			debugAuthLog("xai oauth: flow error: %v", msg.err)
			return a, nil
		}
		a.accessToken = msg.token.AccessToken
		a.state = "login_success"
		debugAuthLog("xai oauth: completed")
		if a.onComplete != nil {
			if err := a.onComplete("xai"); err != nil {
				a.state = "login_error"
				a.errorMessage = err.Error()
				return a, nil
			}
		}
		a.interactive = false
		a.state = "menu"
	}

	return a, nil
}

func (a *AuthCommand) startOpenAICmd() tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		debugAuthLog("openai oauth: start device flow (cmd)")
		flow, err := openai.StartDeviceFlow(ctx)
		if err != nil {
			debugAuthLog("openai oauth: device flow error: %v", err)
			return openaiFlowMsg{flow: nil, err: err}
		}
		// Capture user code immediately so UI can render without waiting for another update.
		a.openaiUserCode = flow.UserCode
		debugAuthLog("openai oauth: device flow received interval=%d", flow.Interval)
		return openaiFlowMsg{flow: flow, err: nil}
	}
}

// formatOpenAIError shortens noisy error payloads for the auth dialog.
func formatOpenAIError(prefix string, err error) string {
	if err == nil {
		return prefix
	}
	msg := err.Error()
	if len(msg) > 240 {
		msg = msg[:240] + "..."
	}
	lower := strings.ToLower(msg)
	if strings.Contains(lower, "<html") || strings.Contains(lower, "<!doctype") {
		msg = i18n.T("commands.auth.error.temporary_html")
	}
	return fmt.Sprintf("%s: %s", prefix, msg)
}

func (a *AuthCommand) pollOpenAICmd(flow *openai.DeviceFlow) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		debugAuthLog("openai oauth: start polling device flow")
		exchange, err := openai.PollForAuthorizationCodeWithCallback(ctx, flow, func(status int, body string) {
			debugAuthLog("openai oauth: poll status=%d", status)

			// If device authorization is unknown, just log and continue polling;
			// don't forcibly flip the UI into an error state on incidental 403s.
			if status == 403 && strings.Contains(body, "deviceauth_authorization_unknown") {
				debugAuthLog("openai oauth: deviceauth_authorization_unknown (continuing to poll)")
			}
		})
		if err != nil {
			debugAuthLog("openai oauth: polling error: %v", err)
			return openaiResultMsg{err: err}
		}
		debugAuthLog("openai oauth: authorization_code received")

		token, err := openai.ExchangeCodeForToken(ctx, exchange)
		if err != nil {
			debugAuthLog("openai oauth: exchange error: %v", err)
			return openaiResultMsg{err: err}
		}
		debugAuthLog("openai oauth: token exchange success (exp=%d)", token.ExpiresAt)
		return openaiResultMsg{token: token, err: nil}
	}
}

func (a *AuthCommand) openAICodeBounds() (int, int, int, bool) {
	if a.openaiUserCode == "" {
		return 0, 0, 0, false
	}
	if a.state != "openai_waiting_code" && a.state != "openai_polling" {
		return 0, 0, 0, false
	}
	if a.width <= 0 || a.height <= 0 {
		return 0, 0, 0, false
	}
	if !a.interactive {
		return 0, 0, 0, false
	}

	var view string = a.View()
	if view == "" {
		return 0, 0, 0, false
	}

	var lines []string = strings.Split(view, "\n")
	for lineIndex, line := range lines {
		var stripped string = ansi.Strip(line)
		var startIndex int = strings.Index(stripped, a.openaiUserCode)
		if startIndex < 0 {
			continue
		}
		var startX int = ansi.StringWidth(stripped[:startIndex])
		var codeWidth int = ansi.StringWidth(a.openaiUserCode)
		return startX, lineIndex, codeWidth, true
	}

	return 0, 0, 0, false
}

func (a *AuthCommand) openAICodeHit(x int, y int) bool {
	var startX int
	var startY int
	var width int
	var ok bool
	startX, startY, width, ok = a.openAICodeBounds()
	if !ok {
		return false
	}
	if y != startY {
		return false
	}
	return x >= startX && x < startX+width
}

func (a *AuthCommand) copyOpenAICode() tea.Cmd {
	if a.openaiUserCode == "" {
		return nil
	}

	var err error = clipboard.WriteAll(a.openaiUserCode)
	if err != nil {
		debugAuthLog("openai oauth: user requested code copy failed: %v", err)
		return nil
	}

	debugAuthLog("openai oauth: user requested code copy")
	var now time.Time = time.Now()
	a.openaiCopyOK = true
	a.openaiCopyAt = now

	return tea.Tick(openaiCopyNoticeDuration, func(t time.Time) tea.Msg {
		return openaiCopyNoticeMsg{at: now}
	})
}

func (a *AuthCommand) startGeminiCmd() tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		debugAuthLog("gemini oauth: start browser auth (cmd)")

		// Create a new manager with default config (OAuth mode)
		cfg := gemini.Config{
			AuthMode: gemini.AuthModeOAuth,
			// ClientSecret must be in env
			ClientSecret: os.Getenv("GEMINI_OAUTH_CLIENT_SECRET"),
		}

		// Validate config to apply defaults (like DefaultClientID)
		if err := cfg.Validate(); err != nil {
			return geminiFlowMsg{err: err}
		}

		a.geminiManager = gemini.NewOAuthManager(&cfg)

		authURL, resCh, errCh, cleanup, err := a.geminiManager.StartBrowserAuth(ctx)
		if err != nil {
			debugAuthLog("gemini oauth: start error: %v", err)
			return geminiFlowMsg{err: err}
		}

		debugAuthLog("gemini oauth: started, url=%s", authURL)
		if err := openBrowser(authURL); err != nil {
			debugAuthLog("gemini oauth: failed to open browser: %v", err)
		}

		return geminiFlowMsg{
			authURL: authURL,
			resCh:   resCh,
			errCh:   errCh,
			cleanup: cleanup,
		}
	}
}

func (a *AuthCommand) pollGeminiCmd(resCh <-chan *gemini.OAuthTokens, errCh <-chan error) tea.Cmd {
	return func() tea.Msg {
		select {
		case token := <-resCh:
			return geminiResultMsg{token: token}
		case err := <-errCh:
			return geminiResultMsg{err: err}
		}
	}
}

func formatGeminiError(prefix string, err error) string {
	if err == nil {
		return prefix
	}
	return fmt.Sprintf("%s: %s", prefix, err.Error())
}

// startXAICmd initiates the xAI / Grok authentication flow.
//
// Priority:
//  1. Grok CLI credentials (~/.grok/auth.json) — zero-friction import if the
//     user has already run "grok login".
//  2. PKCE loopback browser flow against accounts.x.ai as fallback.
func (a *AuthCommand) startXAICmd() tea.Cmd {
	return func() tea.Msg {
		// 1. Try Grok CLI import.
		token, err := xai.ImportFromGrokCLI("")
		if err == nil && token != nil {
			if storeErr := xai.StoreOAuthToken(token); storeErr == nil {
				return xaiResultMsg{token: token}
			}
		}

		// 2. Start PKCE loopback flow.
		flow, flowErr := xai.NewLoopbackFlow(context.Background())
		if flowErr != nil {
			return xaiFlowMsg{err: flowErr}
		}
		return xaiFlowMsg{flow: flow, authURL: flow.AuthorizeURL()}
	}
}

// waitXAICallbackCmd blocks until the xAI loopback server receives the
// browser callback, exchanges the code for tokens, and stores them.
func (a *AuthCommand) waitXAICallbackCmd() tea.Cmd {
	flow := a.xaiLoopback
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		token, err := flow.WaitForCallback(ctx)
		if err != nil {
			return xaiResultMsg{err: err}
		}
		if storeErr := xai.StoreOAuthToken(token); storeErr != nil {
			return xaiResultMsg{err: fmt.Errorf("xAI OAuth: failed to store token: %w", storeErr)}
		}
		return xaiResultMsg{token: token}
	}
}

func (a *AuthCommand) refreshTokens() {
	refreshed := false

	if _, err := anthropic.RefreshAndStoreToken(); err == nil {
		refreshed = true
	}

	if _, err := openai.RefreshAndStoreToken(context.Background()); err == nil {
		refreshed = true
	}

	if _, err := xai.RefreshAndStoreToken(context.Background()); err == nil {
		refreshed = true
	}

	if refreshed {
		a.state = "view_token"
		a.loginProvider = "refresh"
		debugAuthLog("oauth: refresh succeeded")
	} else {
		a.state = "login_error"
		a.errorMessage = i18n.T("commands.auth.error.no_tokens_refresh")
		debugAuthLog("oauth: refresh failed, no tokens found")
	}
}

// View renders the auth UI
func (a *AuthCommand) View() string {
	if !a.interactive {
		return ""
	}

	var content string

	switch a.state {
	case "menu":
		content = a.renderMenu()
	case "login_waiting_url":
		content = a.renderWaitingURL()
	case "login_waiting_code":
		content = a.renderWaitingCode()
	case "login_exchanging":
		content = a.renderExchanging()
	case "login_success":
		content = a.renderSuccess()
	case "login_error":
		content = a.renderError()
	case "view_token":
		content = a.renderViewToken()
	case "openai_waiting_code", "openai_polling":
		content = a.renderOpenAIDevice()
	case "gemini_polling":
		content = a.renderGeminiPolling()
	case "xai_importing":
		content = a.renderXAIImporting()
	case "xai_browser":
		content = a.renderXAIBrowser()
	case "openai_success":
		content = a.renderSuccess()
	default:
		content = i18n.T("commands.common.loading")
	}

	maxWidth := 70
	if a.width > 0 {
		maxWidth = a.width - 6
		if maxWidth > 70 {
			maxWidth = 70
		}
		if maxWidth < 40 {
			maxWidth = a.width - 2
		}
	}

	modal := lipgloss.NewStyle().
		Width(maxWidth).
		Padding(2, 3).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(ColorAccent)).
		Render(content)

	// Center the modal within the available window size if known
	if a.width > 0 && a.height > 0 {
		return lipgloss.Place(a.width, a.height,
			lipgloss.Center, lipgloss.Center,
			modal,
			lipgloss.WithWhitespaceChars(" "),
		)
	}

	return modal
}

func (a *AuthCommand) renderMenu() string {
	title := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorAccent)).
		Bold(true).
		Render(i18n.T("commands.auth.title"))

	dividerWidth := 60
	if a.width > 0 && a.width-12 < dividerWidth {
		dividerWidth = a.width - 12
	}
	if dividerWidth < 20 {
		dividerWidth = 20
	}
	divider := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorBorder)).
		Render(strings.Repeat("─", dividerWidth))

	menuItems := []string{
		i18n.T("commands.auth.menu.claude"),
		i18n.T("commands.auth.menu.openai"),
		i18n.T("commands.auth.menu.gemini"),
		i18n.T("commands.auth.menu.xai"),
		i18n.T("commands.auth.menu.view_tokens"),
		i18n.T("commands.auth.menu.refresh_tokens"),
	}

	var items []string
	for i, item := range menuItems {
		isSelected := i == a.selectedMenu

		prefix := "  "
		color := ColorMuted
		if isSelected {
			prefix = "▶ "
			color = ColorAccent
		}

		itemStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(color)).
			Bold(isSelected)

		items = append(items, itemStyle.Render(prefix+item))
	}

	list := lipgloss.JoinVertical(lipgloss.Left, items...)

	hint := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorGray)).
		Render(i18n.T("commands.auth.hint.menu"))

	return lipgloss.JoinVertical(
		lipgloss.Left,
		title,
		"",
		divider,
		"",
		list,
		"",
		hint,
	)
}

func (a *AuthCommand) renderWaitingURL() string {
	title := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorAccent)).
		Bold(true).
		Render(i18n.T("commands.auth.claude.authenticating"))

	message := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorMuted)).
		Render(i18n.T("commands.auth.initializing"))

	return lipgloss.JoinVertical(
		lipgloss.Left,
		title,
		"",
		message,
	)
}

func (a *AuthCommand) renderWaitingCode() string {
	title := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorAccent)).
		Bold(true).
		Render(i18n.T("commands.auth.claude.authenticate"))

	dividerWidth := 60
	if a.width > 0 && a.width-12 < dividerWidth {
		dividerWidth = a.width - 12
	}
	if dividerWidth < 20 {
		dividerWidth = 20
	}
	divider := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorBorder)).
		Render(strings.Repeat("─", dividerWidth))

	step1 := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorWhite)).
		Render(i18n.T("commands.auth.visit_url"))

	url := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorCyan)).
		Underline(true).
		Render(a.authURL)

	step2 := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorWhite)).
		Render(i18n.T("commands.auth.sign_in_authorize"))

	step3 := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorWhite)).
		Render(i18n.T("commands.auth.paste_code"))

	// Render custom input field with cursor
	inputStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorWhite)).
		Background(lipgloss.Color(ColorPanel))

	var inputDisplay string
	if a.inputValue == "" {
		inputDisplay = inputStyle.Render("█") // Show cursor when empty
	} else {
		// Show text with cursor
		before := a.inputValue[:a.cursorPos]
		after := ""
		if a.cursorPos < len(a.inputValue) {
			after = a.inputValue[a.cursorPos+1:]
		}
		cursor := "█"
		if a.cursorPos < len(a.inputValue) {
			cursor = string(a.inputValue[a.cursorPos])
		}
		cursorStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(ColorPanel)).
			Background(lipgloss.Color(ColorWhite))
		inputDisplay = inputStyle.Render(before) + cursorStyle.Render(cursor) + inputStyle.Render(after)
	}

	input := lipgloss.NewStyle().
		Width(60).
		Padding(0, 1).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(ColorAccent)).
		Render(inputDisplay)

	hint := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorGray)).
		Render(i18n.T("commands.auth.hint.code"))

	return lipgloss.JoinVertical(
		lipgloss.Left,
		title,
		"",
		divider,
		"",
		step1,
		url,
		"",
		step2,
		"",
		step3,
		input,
		"",
		hint,
	)
}

func (a *AuthCommand) renderExchanging() string {
	title := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorAccent)).
		Bold(true).
		Render(i18n.T("commands.auth.claude.authenticating"))

	message := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorMuted)).
		Render(i18n.T("commands.auth.exchanging"))

	return lipgloss.JoinVertical(
		lipgloss.Left,
		title,
		"",
		message,
	)
}

func (a *AuthCommand) renderOpenAIDevice() string {
	title := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorAccent)).
		Bold(true).
		Render(i18n.T("commands.auth.openai.authenticate"))

	dividerWidth := 60
	if a.width > 0 && a.width-12 < dividerWidth {
		dividerWidth = a.width - 12
	}
	if dividerWidth < 20 {
		dividerWidth = 20
	}
	divider := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorBorder)).
		Render(strings.Repeat("─", dividerWidth))

	codeLabel := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorWhite)).
		Render(i18n.T("commands.auth.openai.enter_code"))

	userCode := i18n.T("commands.common.loading")
	if a.openaiUserCode != "" {
		userCode = a.openaiUserCode
	} else if a.openaiFlow != nil {
		userCode = a.openaiFlow.UserCode
	}

	code := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorCyan)).
		Bold(true).
		Render(userCode)

	codeCopied := ""
	if a.openaiUserCode != "" || a.openaiFlow != nil {
		if a.openaiCopyOK {
			codeCopied = lipgloss.NewStyle().
				Foreground(lipgloss.Color(ColorSuccess)).
				Render(i18n.T("commands.auth.copied"))
		} else {
			codeCopied = lipgloss.NewStyle().
				Foreground(lipgloss.Color(ColorMuted)).
				Render(i18n.T("commands.auth.copy_hint"))
		}
	}

	urlLabel := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorWhite)).
		Render(i18n.T("commands.auth.visit_url_continue"))

	urlVal := a.authURL
	if urlVal == "" {
		urlVal = openai.OAuthDeviceVerificationURL()
	}

	url := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorCyan)).
		Underline(true).
		Render(urlVal)

	statusMsg := i18n.T("commands.auth.waiting_approval")
	if a.state == "openai_waiting_code" {
		statusMsg = i18n.T("commands.auth.generating_code")
	}

	status := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorMuted)).
		Render(statusMsg)

	hint := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorGray)).
		Render(i18n.T("commands.auth.hint.cancel"))

	return lipgloss.JoinVertical(
		lipgloss.Left,
		title,
		"",
		divider,
		"",
		codeLabel,
		code,
		"",
		urlLabel,
		url,
		"",
		codeCopied,
		"",
		status,
		"",
		hint,
	)
}

func (a *AuthCommand) renderGeminiPolling() string {
	title := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorAccent)).
		Bold(true).
		Render(i18n.T("commands.auth.gemini.authenticate"))

	dividerWidth := 60
	if a.width > 0 && a.width-12 < dividerWidth {
		dividerWidth = a.width - 12
	}
	if dividerWidth < 20 {
		dividerWidth = 20
	}
	divider := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorBorder)).
		Render(strings.Repeat("─", dividerWidth))

	urlLabel := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorWhite)).
		Render(i18n.T("commands.auth.visit_url_browser"))

	url := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorCyan)).
		Underline(true).
		Render(a.authURL)

	status := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorMuted)).
		Render(i18n.T("commands.auth.waiting_browser"))

	hint := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorGray)).
		Render(i18n.T("commands.auth.hint.cancel"))

	return lipgloss.JoinVertical(
		lipgloss.Left,
		title,
		"",
		divider,
		"",
		urlLabel,
		url,
		"",
		status,
		"",
		hint,
	)
}

func (a *AuthCommand) renderXAIImporting() string {
	title := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorAccent)).
		Bold(true).
		Render(i18n.T("commands.auth.xai.oauth_title"))

	status := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorMuted)).
		Render(i18n.T("commands.auth.xai.checking_credentials"))

	hint := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorGray)).
		Render(i18n.T("commands.auth.xai.browser_fallback"))

	return lipgloss.JoinVertical(lipgloss.Left,
		title, "", status, "", hint,
	)
}

func (a *AuthCommand) renderXAIBrowser() string {
	title := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorAccent)).
		Bold(true).
		Render(i18n.T("commands.auth.xai.browser_title"))

	status := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorMuted)).
		Render(i18n.T("commands.auth.xai.browser_opened"))

	urlLabel := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorGray)).
		Render(i18n.T("commands.auth.xai.manual_url"))

	urlStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorAccent))

	urlText := a.authURL
	if urlText == "" {
		urlText = i18n.T("commands.auth.generating")
	}
	url := urlStyle.Render(urlText)

	hint := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorGray)).
		Render(i18n.T("commands.auth.xai.instructions"))

	return lipgloss.JoinVertical(lipgloss.Left,
		title, "", status, "", urlLabel, url, "", hint,
	)
}

func (a *AuthCommand) renderSuccess() string {
	providerLabel := "Claude"
	if a.loginProvider == "openai" {
		providerLabel = "OpenAI (Codex)"
	} else if a.loginProvider == "gemini" {
		providerLabel = "Gemini"
	} else if a.loginProvider == "xai" {
		providerLabel = "xAI / Grok"
	} else if a.loginProvider == "refresh" {
		providerLabel = i18n.T("commands.auth.tokens")
	}

	title := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorSuccess)).
		Bold(true).
		Render(i18n.T("commands.auth.success.title", providerLabel))

	divider := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorBorder)).
		Render(strings.Repeat("─", dividerWidth(a.width)))

	message := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorWhite)).
		Render(i18n.T("commands.auth.success.saved"))

	modelsTitle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorMuted)).
		Render(i18n.T("commands.auth.success.models"))

	var modelLines []string
	if a.loginProvider == "openai" {
		modelLines = []string{
			"  GPT-4 Turbo (OAuth)",
			"  GPT-4 (OAuth)",
			"  GPT-3.5 Turbo (OAuth)",
		}
	} else if a.loginProvider == "gemini" {
		modelLines = []string{
			"  Gemini 3 Pro Preview (OAuth)",
			"  Gemini 2.5 Pro (OAuth)",
			"  Gemini 2.0 Flash (OAuth)",
		}
	} else {
		modelLines = []string{
			"  Claude Opus 4.1 (OAuth)",
			"  Claude Sonnet 4.5 (OAuth)",
			"  Claude Haiku 4.5 (OAuth)",
		}
	}

	modelList := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorAccent)).
		Render(strings.Join(modelLines, "\n"))

	displayToken := a.accessToken
	if len(displayToken) > 20 {
		displayToken = displayToken[:12] + "..." + displayToken[len(displayToken)-4:]
	}

	tokenInfo := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorGray)).
		Render(i18n.T("commands.auth.token", displayToken))

	hint := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorGray)).
		Render(i18n.T("commands.auth.hint.continue"))

	return lipgloss.JoinVertical(
		lipgloss.Left,
		title,
		"",
		divider,
		"",
		message,
		"",
		modelsTitle,
		modelList,
		"",
		tokenInfo,
		"",
		hint,
	)
}

func (a *AuthCommand) renderError() string {
	title := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorRed)).
		Bold(true).
		Render(i18n.T("commands.auth.failed.title"))

	divider := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorBorder)).
		Render(strings.Repeat("─", dividerWidth(a.width)))

	message := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorWhite)).
		Render(i18n.T("commands.auth.error_line", a.errorMessage))

	hint := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorGray)).
		Render(i18n.T("commands.auth.hint.retry"))

	return lipgloss.JoinVertical(
		lipgloss.Left,
		title,
		"",
		divider,
		"",
		message,
		"",
		hint,
	)
}

func (a *AuthCommand) renderViewToken() string {
	title := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorAccent)).
		Bold(true).
		Render(i18n.T("commands.auth.current_tokens"))

	divider := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorBorder)).
		Render(strings.Repeat("─", dividerWidth(a.width)))

	anthCfg, _ := anthropic.LoadOAuthConfig()
	var anthToken string
	var anthExpiry int64
	if anthCfg != nil && anthCfg.Token != nil {
		anthToken = anthCfg.Token.AccessToken
		anthExpiry = anthCfg.Token.Expiry
	}

	openAIToken, _ := openai.GetStoredOAuthToken()
	var oaToken string
	var oaExpiry int64
	if openAIToken != nil {
		if openAIToken.APIKey != "" {
			oaToken = openAIToken.APIKey
		} else {
			oaToken = openAIToken.AccessToken
		}
		oaExpiry = openAIToken.ExpiresAt
	}

	anthSection := renderTokenSection("Claude Code", anthToken, anthExpiry)
	openaiSection := renderTokenSection("OpenAI (Codex)", oaToken, oaExpiry)

	allSections := lipgloss.JoinVertical(lipgloss.Left, anthSection, "", openaiSection)

	hint := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorGray)).
		Render(i18n.T("commands.auth.hint.back"))

	return lipgloss.JoinVertical(
		lipgloss.Left,
		title,
		"",
		divider,
		"",
		allSections,
		"",
		hint,
	)
}

func renderTokenSection(label, token string, expiry int64) string {
	header := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorMuted)).
		Bold(true).
		Render(label)

	if token == "" {
		missing := lipgloss.NewStyle().
			Foreground(lipgloss.Color(ColorRed)).
			Render(i18n.T("commands.auth.no_token"))
		return lipgloss.JoinVertical(lipgloss.Left, header, missing)
	}

	displayToken := token
	if len(displayToken) > 20 {
		displayToken = displayToken[:12] + "..." + displayToken[len(displayToken)-4:]
	}

	tokenInfo := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorWhite)).
		Render(i18n.T("commands.auth.token", displayToken))

	expiryInfo := i18n.T("commands.auth.expiry.unknown")
	expiryStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(ColorMuted))
	if expiry > 0 {
		expiryTime := time.Unix(expiry, 0)
		timeRemaining := time.Until(expiryTime)
		if timeRemaining > 0 {
			days := int(timeRemaining.Hours() / 24)
			hours := int(timeRemaining.Hours()) % 24
			expiryInfo = i18n.T("commands.auth.expiry.valid", days, hours)
		} else {
			expiryInfo = i18n.T("commands.auth.expiry.expired")
			expiryStyle = expiryStyle.Foreground(lipgloss.Color(ColorRed))
		}
	}

	expiryLine := expiryStyle.Render(expiryInfo)

	return lipgloss.JoinVertical(
		lipgloss.Left,
		header,
		tokenInfo,
		expiryLine,
	)
}

// dividerWidth adjusts divider length based on available width.
func dividerWidth(totalWidth int) int {
	w := 60
	if totalWidth > 0 && totalWidth-12 < w {
		w = totalWidth - 12
	}
	if w < 20 {
		w = 20
	}
	return w
}

// debugAuthLog writes auth debug info to swarmos_debug.log for troubleshooting.
func debugAuthLog(format string, args ...any) {
	if os.Getenv("SWARMOS_AUTH_DEBUG") == "" {
		return
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	logDir := filepath.Join(home, ".swarmos")
	if err := os.MkdirAll(logDir, 0700); err != nil {
		return
	}
	logPath := filepath.Join(logDir, "swarmos_debug.log")

	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return
	}
	logger := log.New(f, "AUTH ", log.LstdFlags|log.Lmicroseconds)
	logger.Printf(format, args...)
	_ = f.Close()
}

// resetMenuState brings the auth command back to the menu with defaults.
func (a *AuthCommand) resetMenuState() {
	a.state = "menu"
	a.selectedMenu = 0
	a.inputValue = ""
	a.cursorPos = 0
	a.loginProvider = ""
	a.openaiCopyOK = false
	a.openaiCopyAt = time.Time{}
}
