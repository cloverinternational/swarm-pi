package settings

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/launch"
	sdkprovider "github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider/anthropic"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider/gemini"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider/openai"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider/xai"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/commands"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
	"github.com/atotto/clipboard"
)

// ── multi-account store ───────────────────────────────────────────────────────

type authAccount struct {
	ID        string          `json:"id"`
	Provider  string          `json:"provider"`
	Email     string          `json:"email,omitempty"`
	IsActive  bool            `json:"is_active"`
	AddedAt   int64           `json:"added_at"`
	TokenData json.RawMessage `json:"token_data,omitempty"`
}

func (a authAccount) displayLabel() string {
	if a.Email != "" {
		return a.Email
	}
	if len(a.ID) > 4 {
		return i18n.T("settings.auth.account.anonymous_suffix", a.ID[len(a.ID)-4:])
	}
	return i18n.T("settings.auth.account.anonymous")
}

type authAccountFile struct {
	Version  string        `json:"version"`
	Accounts []authAccount `json:"accounts"`
}

var openAIAccountRefreshGate = make(chan struct{}, 1)

func authAccountsFilePath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".swarmos", "tui_accounts.json")
}

func loadAuthAccountFile() authAccountFile {
	data, err := os.ReadFile(authAccountsFilePath())
	if err != nil {
		return authAccountFile{Version: "1"}
	}
	var f authAccountFile
	if err := json.Unmarshal(data, &f); err != nil {
		return authAccountFile{Version: "1"}
	}
	return f
}

func saveAuthAccountFile(f authAccountFile) error {
	path := authAccountsFilePath()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tui-accounts-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0600); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func accountsForProvider(provider string) []authAccount {
	f := loadAuthAccountFile()
	var out []authAccount
	for _, a := range f.Accounts {
		// Match by wire family, not exact label: OAuth accounts are stored under
		// historical labels ("OpenAI", "Gemini") that must surface on the catalog
		// rows that own their family's OAuth login ("codex", "Google"). Each wire
		// family has exactly one OAuth row on the auth screen, so family matching
		// cannot double-list an account.
		if strings.EqualFold(a.Provider, provider) || sdkprovider.ProvidersMatch(provider, a.Provider) {
			out = append(out, a)
		}
	}
	return out
}

// activeAccountFor returns a copy of the active account for the given provider,
// or nil if none is marked active. Used during refresh to restore the active
// account's token into the SDK store after refreshing a non-active account
// (whose SDK refresh would otherwise clobber the active account's SDK token).
func activeAccountFor(provider string) *authAccount {
	for _, a := range accountsForProvider(provider) {
		if a.IsActive {
			acct := a
			return &acct
		}
	}
	return nil
}

// readGeminiSDKTokens reads the Gemini OAuth tokens the SDK persists to disk
// (~/.config/gemini-cli/oauth_credentials.json). The gemini OAuthManager has no
// exported token accessor, so after a refresh we read the persisted file back to
// mirror it into tui_accounts.json. Returns nil on any error. BUG #2 helper.
func readGeminiSDKTokens() *gemini.OAuthTokens {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	path := filepath.Join(home, ".config", "gemini-cli", "oauth_credentials.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var tok gemini.OAuthTokens
	if json.Unmarshal(data, &tok) != nil || tok.AccessToken == "" {
		return nil
	}
	return &tok
}

// PublicAuthAccount is an exported view of an auth account for cross-package use.
type PublicAuthAccount struct {
	ID        string
	Provider  string
	Email     string
	IsActive  bool
	TokenData json.RawMessage
}

// AccountsForProvider returns accounts for the given provider. Exported for use
// by the usage screen and other non-settings code.
func AccountsForProvider(provider string) []PublicAuthAccount {
	accts := accountsForProvider(provider)
	out := make([]PublicAuthAccount, len(accts))
	for i, a := range accts {
		out[i] = PublicAuthAccount{
			ID:        a.ID,
			Provider:  a.Provider,
			Email:     a.Email,
			IsActive:  a.IsActive,
			TokenData: a.TokenData,
		}
	}
	return out
}

// SaveAccountEmail updates the email field for an account by ID.
// Exported so the usage screen can persist discovered emails without a full
// re-authentication flow.
func SaveAccountEmail(accountID, email string) {
	f := loadAuthAccountFile()
	for i, a := range f.Accounts {
		if a.ID == accountID {
			f.Accounts[i].Email = email
			_ = saveAuthAccountFile(f)
			return
		}
	}
}

// UpdateAccountTokenData updates the stored token data for a specific account.
// Exported so the usage screen can persist refreshed Gemini tokens.
func UpdateAccountTokenData(accountID string, tokenData json.RawMessage) {
	f := loadAuthAccountFile()
	for i, a := range f.Accounts {
		if a.ID == accountID {
			f.Accounts[i].TokenData = tokenData
			_ = saveAuthAccountFile(f)
			return
		}
	}
}

func upsertAccount(acct authAccount) error {
	f := loadAuthAccountFile()
	if f.Version == "" {
		f.Version = "1"
	}

	// First, try to find by ID (exact match)
	found := false
	for i, a := range f.Accounts {
		if a.ID == acct.ID {
			f.Accounts[i] = acct
			found = true
			break
		}
	}

	// If not found by ID, try dedup by email+provider to prevent duplicates
	// when re-authenticating the same account (which generates a new ID).
	if !found && acct.Email != "" {
		for i, a := range f.Accounts {
			if strings.EqualFold(a.Provider, acct.Provider) && strings.EqualFold(a.Email, acct.Email) {
				// Same email+provider: update the existing entry in place,
				// preserving the original ID so references stay consistent.
				acct.ID = a.ID
				f.Accounts[i] = acct
				found = true
				break
			}
		}
	}

	// If still not found, also try dedup by refresh_token to catch cases
	// where email hasn't been fetched yet but it's the same underlying account.
	if !found && len(acct.TokenData) > 0 {
		var newTok struct {
			RefreshToken string `json:"refresh_token"`
		}
		if json.Unmarshal(acct.TokenData, &newTok) == nil && newTok.RefreshToken != "" {
			for i, a := range f.Accounts {
				if !strings.EqualFold(a.Provider, acct.Provider) {
					continue
				}
				var existTok struct {
					RefreshToken string `json:"refresh_token"`
				}
				if json.Unmarshal(a.TokenData, &existTok) == nil && existTok.RefreshToken == newTok.RefreshToken {
					acct.ID = a.ID
					f.Accounts[i] = acct
					found = true
					break
				}
			}
		}
	}

	if !found {
		f.Accounts = append(f.Accounts, acct)
	}
	return saveAuthAccountFile(f)
}

// removeAccount removes an account from tui_accounts.json by ID.
// If the account was active, the next account for the same provider (if any)
// is automatically promoted to active.
func removeAccount(provider, id string) error {
	f := loadAuthAccountFile()
	newAccounts := f.Accounts[:0]
	wasActive := false
	for _, a := range f.Accounts {
		if a.ID == id {
			wasActive = a.IsActive
			continue // skip — this is the one we're deleting
		}
		newAccounts = append(newAccounts, a)
	}
	f.Accounts = newAccounts
	// If the deleted account was active, promote the first remaining account
	// for that provider so the user has a working credential again.
	if wasActive {
		for i, a := range f.Accounts {
			if strings.EqualFold(a.Provider, provider) {
				f.Accounts[i].IsActive = true
				_ = activateAccountToken(f.Accounts[i]) // best-effort
				break
			}
		}
	}
	return saveAuthAccountFile(f)
}

// setActiveAccount marks one account active (others inactive for that provider)
// and copies its token to the SDK's standard storage path.
func setActiveAccount(provider, id string) error {
	f := loadAuthAccountFile()
	for i, a := range f.Accounts {
		if !strings.EqualFold(a.Provider, provider) {
			continue
		}
		f.Accounts[i].IsActive = a.ID == id
	}
	if err := saveAuthAccountFile(f); err != nil {
		return err
	}
	for _, a := range f.Accounts {
		if strings.EqualFold(a.Provider, provider) && a.ID == id {
			return activateAccountToken(a)
		}
	}
	return nil
}

func activateAccountToken(a authAccount) error {
	prov := strings.ToLower(a.Provider)
	switch prov {
	case "claudecode", "anthropic", "claude":
		var tok anthropic.OAuthToken
		if err := json.Unmarshal(a.TokenData, &tok); err != nil {
			return err
		}
		return anthropic.StoreOAuthToken(&tok)
	case "openai", "codex":
		var tok openai.OAuthToken
		if err := json.Unmarshal(a.TokenData, &tok); err != nil {
			return err
		}
		return openai.StoreOAuthToken(&tok)
	case "gemini":
		var tok gemini.OAuthTokens
		if err := json.Unmarshal(a.TokenData, &tok); err != nil {
			return err
		}
		cfg := gemini.Config{AuthMode: gemini.AuthModeOAuth}
		mgr := gemini.NewOAuthManager(&cfg)
		return mgr.SaveTokens(&tok)
	}
	return nil
}

func genAccountID(provider string) string {
	return fmt.Sprintf("%s_%d", strings.ToLower(provider), time.Now().UnixNano())
}

// ── email fetching (best-effort, async) ───────────────────────────────────────

// extractEmailFromJWT tries to pull the "email" claim out of any JWT payload.
func extractEmailFromJWT(token string) string {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		// Try standard padding
		payload, err = base64.StdEncoding.DecodeString(parts[1])
		if err != nil {
			return ""
		}
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		return ""
	}
	if email, ok := claims["email"].(string); ok && email != "" {
		return email
	}
	// Also check "sub" which is often an email for Google
	if sub, ok := claims["sub"].(string); ok && strings.Contains(sub, "@") {
		return sub
	}
	return ""
}

type authEmailFetchedMsg struct {
	provider  string
	accountID string
	email     string
}

// FetchAnthropicEmailFromToken queries the Anthropic OAuth roles endpoint to
// resolve the email address associated with an access token, then parses the
// organization_name field (e.g. "user@example.com's Organization"). Exported
// so the usage screen can call it when populating account labels.
func FetchAnthropicEmailFromToken(accessToken string) string {
	// Try JWT claims first (works for some token types).
	if email := extractEmailFromJWT(accessToken); email != "" {
		return email
	}
	// Use the Claude CLI roles endpoint — returns organization_name which
	// encodes the user's email as "<email>'s Organization".
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet,
		"https://api.anthropic.com/api/oauth/claude_cli/roles", nil)
	if err != nil {
		return ""
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("anthropic-beta", "oauth-2025-04-20")
	req.Header.Set("x-app", "cli")
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		return ""
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var result struct {
		OrganizationName string `json:"organization_name"`
	}
	if json.Unmarshal(body, &result) == nil {
		// Pattern: "user@example.com's Organization"
		if idx := strings.Index(result.OrganizationName, "'s "); idx > 0 {
			candidate := result.OrganizationName[:idx]
			if strings.Contains(candidate, "@") {
				return candidate
			}
		}
	}
	return ""
}

func fetchAnthropicEmail(tok *anthropic.OAuthToken, provider, accountID string) tea.Cmd {
	return func() tea.Msg {
		email := FetchAnthropicEmailFromToken(tok.AccessToken)
		return authEmailFetchedMsg{provider: provider, accountID: accountID, email: email}
	}
}

func fetchOpenAIEmail(tok *openai.OAuthToken, provider, accountID string) tea.Cmd {
	return func() tea.Msg {
		email := extractEmailFromJWT(tok.IDToken)
		if email == "" {
			email = extractEmailFromJWT(tok.AccessToken)
		}
		return authEmailFetchedMsg{provider: provider, accountID: accountID, email: email}
	}
}

func fetchGeminiEmail(tok *gemini.OAuthTokens, provider, accountID string) tea.Cmd {
	return func() tea.Msg {
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet,
			"https://www.googleapis.com/oauth2/v3/userinfo", nil)
		if err != nil {
			return authEmailFetchedMsg{provider: provider, accountID: accountID}
		}
		req.Header.Set("Authorization", "Bearer "+tok.AccessToken)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return authEmailFetchedMsg{provider: provider, accountID: accountID}
		}
		defer resp.Body.Close()
		var result map[string]any
		body, _ := io.ReadAll(resp.Body)
		if json.Unmarshal(body, &result) == nil {
			if email, ok := result["email"].(string); ok && email != "" {
				return authEmailFetchedMsg{provider: provider, accountID: accountID, email: email}
			}
		}
		return authEmailFetchedMsg{provider: provider, accountID: accountID}
	}
}

// ── async message types ───────────────────────────────────────────────────────

type authURLReadyMsg struct{ url string }

type authCodeSuccessMsg struct {
	provider  string
	accountID string
}

type authErrorMsg struct{ err error }

type authOpenAIFlowMsg struct {
	flow *openai.DeviceFlow
	err  error
}

type authOpenAIResultMsg struct {
	token *openai.OAuthToken
	err   error
}

type authGeminiFlowMsg struct {
	authURL string
	resCh   <-chan *gemini.OAuthTokens
	errCh   <-chan error
	cleanup func()
	err     error
}

type authGeminiResultMsg struct {
	token *gemini.OAuthTokens
	err   error
}

type authXAIFlowMsg struct {
	flow    *xai.LoopbackFlow // nil if Grok CLI import succeeded immediately
	authURL string
	token   *xai.OAuthToken // non-nil when Grok CLI import succeeded
	err     error
}

type authXAIResultMsg struct {
	token *xai.OAuthToken
	err   error
}
type authCursorFlowMsg struct {
	flow    *cursorPKCEFlow
	authURL string
	err     error
}
type authCursorResultMsg struct {
	token *cursorToken
	err   error
}

type authCopyNoticeMsg struct{ at time.Time }

type authTokenRefreshedMsg struct {
	provider  string
	accountID string
	newToken  json.RawMessage
	email     string
	err       error
}

type authClaudeCodeImportMsg struct {
	token     *anthropic.OAuthToken
	email     string
	accountID string
	err       error
}

// ── provider entry ────────────────────────────────────────────────────────────

type authProviderEntry struct {
	Name        string
	DisplayName string
	Color       string
	IsAuthed    bool
	Accounts    []authAccount
}

// ── AuthSettings ──────────────────────────────────────────────────────────────

type AuthSettings struct {
	// Provider list
	providers   []authProviderEntry
	selectedIdx int

	// Account picker sub-view (viewState == "account_list")
	viewState      string // "list" | "account_list" | "usage"
	pickerProvider string // provider being viewed in account picker
	pickerIdx      int    // 0 = "Add account", 1..N = existing accounts

	// Usage statistics (set from app)

	// Flow state machine
	flowState    string // ""|"waiting_url"|"waiting_code"|"openai_code"|"openai_poll"|"gemini_wait"|"exchanging"|"success"|"error"
	flowProvider string
	flowAccount  string // account ID being created
	flowURL      string
	flowInput    string
	flowCursor   int

	// OpenAI device flow refs
	openaiFlow     *openai.DeviceFlow
	openaiUserCode string
	openaiCopyOK   bool
	openaiCopyAt   time.Time

	// Gemini
	geminiManager *gemini.OAuthManager

	// Anthropic device flow
	anthropicFlow *anthropic.DeviceFlow

	// xAI / Grok loopback flow
	xaiLoopback *xai.LoopbackFlow
	// Cursor PKCE flow
	cursorFlow *cursorPKCEFlow

	// Result / feedback
	flowError string
	message   string
	messageOK bool
	// credentialChanged rebuilds the running provider after credentials are
	// durably stored. Errors prevent a misleading success screen.
	credentialChanged func(provider string) error
}

func NewAuthSettings(provider string, authenticated bool) *AuthSettings {
	a := &AuthSettings{viewState: "list"}
	a.loadProviders()
	return a
}

// SetCredentialChangedCallback installs the shared live-provider rebuild path.
func (a *AuthSettings) SetCredentialChangedCallback(callback func(provider string) error) {
	a.credentialChanged = callback
}

func (a *AuthSettings) loadProviders() {
	cm, err := commands.NewConfigManager()
	if err != nil {
		return
	}
	configs, err := cm.LoadProviders()
	if err != nil {
		return
	}
	a.providers = make([]authProviderEntry, 0, len(configs))
	for _, p := range configs {
		if p.Type != "oauth" {
			continue
		}
		accts := accountsForProvider(p.Name)
		a.providers = append(a.providers, authProviderEntry{
			Name:        p.Name,
			DisplayName: p.DisplayName,
			Color:       p.Color,
			IsAuthed:    p.Available || len(accts) > 0,
			Accounts:    accts,
		})
	}
}

// Refresh reloads provider/account data from disk (synchronous) and returns an
// async tea.Cmd that refreshes any expired ACTIVE tokens via their provider SDK,
// reconciling tui_accounts.json with the SDK store. The caller should dispatch
// the returned cmd so the UI does not block; on completion the resulting
// authTokenRefreshedMsg(s) are handled by HandleMsg which reloads the list.
// BUG #1 fix.
func (a *AuthSettings) Refresh() tea.Cmd {
	// Import SDK-stored OAuth tokens that predate the account registry so
	// they appear (and can be stacked/rotated) like any other account.
	_ = ReconcileSDKTokensIntoAccounts()
	a.loadProviders()
	return a.refreshExpiredActiveTokensCmd()
}

// refreshExpiredActiveTokensCmd inspects the active account of every provider
// and, for any whose token is expired (per isAccountTokenExpired), triggers a
// provider refresh. This is what keeps tui_accounts.json from diverging from the
// SDK store when the SDK refreshed the token out-of-band. Returns nil when
// nothing needs refreshing. BUG #1 fix.
func (a *AuthSettings) refreshExpiredActiveTokensCmd() tea.Cmd {
	f := loadAuthAccountFile()
	var cmds []tea.Cmd
	for _, acct := range f.Accounts {
		if !acct.IsActive {
			continue
		}
		if !isAccountTokenExpired(acct) {
			continue
		}
		// refreshAccountToken handles writing back BOTH stores and returns an
		// authTokenRefreshedMsg consumed by HandleMsg (which reloads the list).
		cmds = append(cmds, a.refreshAccountToken(acct))
	}
	if len(cmds) == 0 {
		return nil
	}
	return tea.Batch(cmds...)
}

// IsInFlow returns true when an OAuth flow is currently active.
func (a *AuthSettings) IsInFlow() bool {
	return a.flowState != "" && a.flowState != "success" && a.flowState != "error"
}

// IsShowingFlowResult returns true when a success or error screen is being
// shown after a flow completes — the next key press dismisses it.
func (a *AuthSettings) IsShowingFlowResult() bool {
	return a.flowState == "success" || a.flowState == "error"
}

// IsInAccountPicker returns true when the account sub-view is open.
func (a *AuthSettings) IsInAccountPicker() bool {
	return a.viewState == "account_list"
}

// IsViewingUsage returns true when the usage statistics sub-view is open.
func (a *AuthSettings) IsViewingUsage() bool {
	return a.viewState == "usage"
}

// IsEditingCode returns true when the user is typing an authorization code.
func (a *AuthSettings) IsEditingCode() bool {
	return a.flowState == "waiting_code"
}

// HandlePaste inserts pasted text into the authorization code input field.
func (a *AuthSettings) HandlePaste(text string) {
	if a.flowState != "waiting_code" {
		return
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	if a.flowCursor > len(a.flowInput) {
		a.flowCursor = len(a.flowInput)
	}
	a.flowInput = a.flowInput[:a.flowCursor] + text + a.flowInput[a.flowCursor:]
	a.flowCursor += len(text)
}

// ── key handling ─────────────────────────────────────────────────────────────

func (a *AuthSettings) HandleKey(key string) tea.Cmd {
	// Account picker
	if a.viewState == "account_list" {
		return a.handleAccountListKey(key)
	}
	// Usage view
	if a.viewState == "usage" {
		if key == "esc" || key == "q" {
			a.viewState = "list"
			return nil
		}
		return nil
	}

	// Post-flow: any key dismisses
	if a.flowState == "success" || a.flowState == "error" {
		a.flowState = ""
		a.flowProvider = ""
		a.flowURL = ""
		a.flowInput = ""
		a.flowError = ""
		a.loadProviders()
		return nil
	}

	// Claude code entry
	if a.flowState == "waiting_code" {
		return a.handleClaudeCodeKey(key)
	}

	// Other flow states: only esc (and ctrl+y for openai)
	if a.flowState == "waiting_url" || a.flowState == "exchanging" ||
		a.flowState == "openai_code" || a.flowState == "openai_poll" ||
		a.flowState == "gemini_wait" || a.flowState == "xai_wait" ||
		a.flowState == "cursor_wait" {
		if key == "esc" {
			a.cancelFlow()
		}
		if key == "ctrl+y" && a.flowState == "openai_code" {
			return a.copyOpenAICode()
		}
		return nil
	}

	// Provider list navigation
	switch key {
	case "up", "k":
		if a.selectedIdx > 0 {
			a.selectedIdx--
			a.message = ""
		}
	case "down", "j":
		// Allow selecting up to len(a.providers) (the Usage button)
		if a.selectedIdx < len(a.providers) {
			a.selectedIdx++
			a.message = ""
		}
	case "enter", " ":
		// Check if Usage button is selected
		if a.selectedIdx == len(a.providers) {
			a.viewState = "usage"
			return nil
		}
		if a.selectedIdx >= 0 && a.selectedIdx < len(a.providers) {
			p := a.providers[a.selectedIdx]
			if len(p.Accounts) > 0 {
				// Has accounts → open picker
				a.viewState = "account_list"
				a.pickerProvider = p.Name
				a.pickerIdx = 0
				return nil
			}
			return a.startFlow(p)
		}
	}
	return nil
}

func (a *AuthSettings) handleAccountListKey(key string) tea.Cmd {
	accts := accountsForProvider(a.pickerProvider)
	total := len(accts) + 1 // 0 = add, 1..N = existing

	switch key {
	case "esc", "backspace":
		a.viewState = "list"
		a.pickerProvider = ""
		a.pickerIdx = 0
		return nil
	case "up", "k":
		if a.pickerIdx > 0 {
			a.pickerIdx--
		}
	case "down", "j":
		if a.pickerIdx < total-1 {
			a.pickerIdx++
		}
	case "enter", " ":
		if a.pickerIdx == 0 {
			// Add new account
			a.viewState = "list"
			for _, p := range a.providers {
				if strings.EqualFold(p.Name, a.pickerProvider) {
					return a.startFlow(p)
				}
			}
		} else {
			idx := a.pickerIdx - 1
			if idx < len(accts) {
				acct := accts[idx]
				if err := setActiveAccount(a.pickerProvider, acct.ID); err != nil {
					a.message = i18n.T("settings.auth.message.switch_failed")
					a.messageOK = false
				} else {
					label := acct.displayLabel()
					a.message = i18n.T("settings.auth.message.switched", label)
					a.messageOK = true
				}
				a.viewState = "list"
				a.loadProviders()
			}
		}
	case "d", "ctrl+d":
		// Delete the selected account (not the "+ Add" row at index 0)
		if a.pickerIdx > 0 {
			idx := a.pickerIdx - 1
			if idx < len(accts) {
				acct := accts[idx]
				if err := removeAccount(a.pickerProvider, acct.ID); err != nil {
					a.message = i18n.T("settings.auth.message.remove_failed")
					a.messageOK = false
				} else {
					a.message = i18n.T("settings.auth.message.removed", acct.displayLabel())
					a.messageOK = true
					// Move cursor up if we just removed the last item
					newTotal := len(accts) // old len; after delete it's len-1
					if a.pickerIdx >= newTotal {
						a.pickerIdx = newTotal - 1
						if a.pickerIdx < 0 {
							a.pickerIdx = 0
						}
					}
				}
				a.loadProviders()
			}
		}
	case "r":
		// Refresh the selected account's token
		if a.pickerIdx > 0 {
			idx := a.pickerIdx - 1
			if idx < len(accts) {
				acct := accts[idx]
				return a.refreshAccountToken(acct)
			}
		}
	case "i":
		// Import token from Claude Code (~/.claude/.credentials.json)
		provNorm := strings.ToLower(a.pickerProvider)
		if provNorm == "claudecode" || provNorm == "anthropic" || provNorm == "claude" {
			return a.importClaudeCodeToken()
		}
	}
	return nil
}

func (a *AuthSettings) handleClaudeCodeKey(key string) tea.Cmd {
	switch key {
	case "esc":
		a.cancelFlow()
		return nil
	case "enter":
		code := strings.TrimSpace(a.flowInput)
		if code == "" {
			return nil
		}
		return a.exchangeClaudeCode(code)
	case "ctrl+v":
		if content, err := clipboard.ReadAll(); err == nil && content != "" {
			a.flowInput = strings.TrimSpace(content)
			a.flowCursor = len(a.flowInput)
		}
		return nil
	case "backspace":
		if a.flowCursor > 0 {
			a.flowInput = a.flowInput[:a.flowCursor-1] + a.flowInput[a.flowCursor:]
			a.flowCursor--
		}
	case "delete":
		if a.flowCursor < len(a.flowInput) {
			a.flowInput = a.flowInput[:a.flowCursor] + a.flowInput[a.flowCursor+1:]
		}
	case "left":
		if a.flowCursor > 0 {
			a.flowCursor--
		}
	case "right":
		if a.flowCursor < len(a.flowInput) {
			a.flowCursor++
		}
	case "home", "ctrl+a":
		a.flowCursor = 0
	case "end", "ctrl+e":
		a.flowCursor = len(a.flowInput)
	default:
		if len(key) == 1 && key[0] >= 0x20 {
			a.flowInput = a.flowInput[:a.flowCursor] + key + a.flowInput[a.flowCursor:]
			a.flowCursor++
		}
	}
	return nil
}

func (a *AuthSettings) copyOpenAICode() tea.Cmd {
	code := a.openaiUserCode
	if a.openaiFlow != nil && a.openaiFlow.UserCode != "" {
		code = a.openaiFlow.UserCode
	}
	if code != "" {
		_ = clipboard.WriteAll(code)
		a.openaiCopyOK = true
		a.openaiCopyAt = time.Now()
		at := a.openaiCopyAt
		return tea.Tick(2*time.Second, func(_ time.Time) tea.Msg {
			return authCopyNoticeMsg{at: at}
		})
	}
	return nil
}

func (a *AuthSettings) cancelFlow() {
	a.flowState = ""
	a.flowProvider = ""
	a.flowAccount = ""
	a.flowURL = ""
	a.flowInput = ""
	a.flowCursor = 0
	a.flowError = ""
	a.openaiFlow = nil
	a.openaiUserCode = ""
	a.geminiManager = nil
	a.anthropicFlow = nil
	a.cursorFlow = nil
}

// ── HandleMsg ─────────────────────────────────────────────────────────────────

func (a *AuthSettings) HandleMsg(msg tea.Msg) tea.Cmd {
	switch m := msg.(type) {
	case authURLReadyMsg:
		a.flowURL = m.url
		a.flowState = "waiting_code"
		return nil

	case authCodeSuccessMsg:
		a.flowState = "success"
		a.message = i18n.T("settings.auth.message.logged_in", m.provider)
		a.messageOK = true
		a.loadProviders()
		// Fetch email from stored token (best-effort)
		if tok, err := anthropic.GetStoredOAuthToken(); err == nil && tok != nil {
			return fetchAnthropicEmail(tok, m.provider, m.accountID)
		}
		return nil

	case authErrorMsg:
		a.flowState = "error"
		a.flowError = m.err.Error()
		return nil

	case authOpenAIFlowMsg:
		if m.err != nil {
			a.flowState = "error"
			a.flowError = m.err.Error()
			return nil
		}
		a.openaiFlow = m.flow
		a.openaiUserCode = m.flow.UserCode
		a.flowState = "openai_poll"
		if urlStr := openai.OAuthDeviceVerificationURL(); urlStr != "" {
			a.flowURL = urlStr
			_ = launch.Open(urlStr)
		}
		return a.pollOpenAICmd(m.flow)

	case authOpenAIResultMsg:
		if m.err != nil {
			a.flowState = "error"
			a.flowError = m.err.Error()
			return nil
		}
		token := m.token
		if token.APIKey == "" && openai.HasModelRequestScope(token.AccessToken) {
			token.APIKey = token.AccessToken
		}
		if err := openai.StoreOAuthToken(token); err != nil {
			a.flowState = "error"
			a.flowError = err.Error()
			return nil
		}
		// Store in multi-account file
		id := a.flowAccount
		if id == "" {
			id = genAccountID("openai")
		}
		td, _ := json.Marshal(token)
		acct := authAccount{
			ID:        id,
			Provider:  "OpenAI",
			IsActive:  true,
			AddedAt:   time.Now().Unix(),
			TokenData: td,
		}
		_ = upsertAccount(acct)
		if a.credentialChanged != nil {
			if err := a.credentialChanged("codex"); err != nil {
				a.flowState = "error"
				a.flowError = err.Error()
				return nil
			}
		}
		a.flowState = "success"
		a.message = i18n.T("settings.auth.message.logged_in", "OpenAI")
		a.messageOK = true
		a.loadProviders()
		return fetchOpenAIEmail(token, "OpenAI", id)

	case authGeminiFlowMsg:
		if m.err != nil {
			a.flowState = "error"
			a.flowError = m.err.Error()
			return nil
		}
		a.flowURL = m.authURL
		a.flowState = "gemini_wait"
		return a.pollGeminiCmd(m.resCh, m.errCh)

	case authGeminiResultMsg:
		if m.err != nil {
			a.flowState = "error"
			a.flowError = m.err.Error()
			return nil
		}
		if a.geminiManager != nil {
			if err := a.geminiManager.SaveTokens(m.token); err != nil {
				a.flowState = "error"
				a.flowError = err.Error()
				return nil
			}
		}
		// Store in multi-account file
		id := a.flowAccount
		if id == "" {
			id = genAccountID("gemini")
		}
		td, _ := json.Marshal(m.token)
		acct := authAccount{
			ID:        id,
			Provider:  "Gemini",
			IsActive:  true,
			AddedAt:   time.Now().Unix(),
			TokenData: td,
		}
		_ = upsertAccount(acct)
		a.flowState = "success"
		a.message = i18n.T("settings.auth.message.logged_in", "Gemini")
		a.messageOK = true
		a.loadProviders()
		return fetchGeminiEmail(m.token, "Gemini", id)

	case authXAIFlowMsg:
		if m.err != nil {
			a.flowState = "error"
			a.flowError = m.err.Error()
			return nil
		}
		// Grok CLI import succeeded immediately — no browser wait needed.
		if m.token != nil {
			id := a.flowAccount
			if id == "" {
				id = genAccountID("xai")
			}
			td, _ := json.Marshal(m.token)
			_ = upsertAccount(authAccount{
				ID: id, Provider: "xai", IsActive: true,
				AddedAt: time.Now().Unix(), TokenData: td,
			})
			a.flowState = "success"
			a.message = i18n.T("settings.auth.message.grok_imported")
			a.messageOK = true
			a.loadProviders()
			return nil
		}
		// Browser flow: show URL and wait for callback.
		a.xaiLoopback = m.flow
		a.flowURL = m.authURL
		a.flowState = "xai_wait"
		return a.waitXAICallbackCmd()

	case authXAIResultMsg:
		if m.err != nil {
			a.flowState = "error"
			a.flowError = m.err.Error()
			return nil
		}
		id := a.flowAccount
		if id == "" {
			id = genAccountID("xai")
		}
		td, _ := json.Marshal(m.token)
		_ = upsertAccount(authAccount{
			ID: id, Provider: "xai", IsActive: true,
			AddedAt: time.Now().Unix(), TokenData: td,
		})
		a.flowState = "success"
		a.message = i18n.T("settings.auth.message.logged_in", "xAI / Grok")
		a.messageOK = true
		a.loadProviders()
		return nil

	case authCursorFlowMsg:
		if m.err != nil {
			a.flowState = "error"
			a.flowError = m.err.Error()
			return nil
		}
		a.cursorFlow = m.flow
		a.flowURL = m.authURL
		a.flowState = "cursor_wait"
		return a.waitCursorCallbackCmd()
	case authCursorResultMsg:
		if m.err != nil {
			a.flowState = "error"
			a.flowError = m.err.Error()
			return nil
		}
		id := a.flowAccount
		if id == "" {
			id = genAccountID("cursor")
		}
		td, _ := json.Marshal(m.token)
		_ = upsertAccount(authAccount{
			ID: id, Provider: "Cursor", IsActive: true,
			AddedAt: time.Now().Unix(), TokenData: td,
		})
		a.flowState = "success"
		a.message = i18n.T("settings.auth.message.logged_in", "Cursor")
		a.messageOK = true
		a.loadProviders()
		return nil
	case authCopyNoticeMsg:
		if a.openaiCopyOK && a.openaiCopyAt.Equal(m.at) {
			a.openaiCopyOK = false
			a.openaiCopyAt = time.Time{}
		}
		return nil

	case authTokenRefreshedMsg:
		if m.err != nil {
			a.message = i18n.T("settings.auth.message.refresh_failed", m.err)
			a.messageOK = false
		} else {
			label := m.email
			if label == "" {
				label = m.accountID
			}
			a.message = i18n.T("settings.auth.message.refreshed", label)
			a.messageOK = true
		}
		a.loadProviders()
		return nil

	case authClaudeCodeImportMsg:
		if m.err != nil {
			a.message = i18n.T("settings.auth.message.import_failed", m.err)
			a.messageOK = false
		} else {
			label := m.email
			if label == "" {
				label = i18n.T("settings.auth.account.claude_code")
			}
			a.message = i18n.T("settings.auth.message.imported", label)
			a.messageOK = true
		}
		a.loadProviders()
		return nil

	case authEmailFetchedMsg:
		// Update email in stored account and refresh provider list
		f := loadAuthAccountFile()
		for i, acc := range f.Accounts {
			if acc.ID == m.accountID {
				f.Accounts[i].Email = m.email
				break
			}
		}
		_ = saveAuthAccountFile(f)
		a.loadProviders()
		return nil
	}
	return nil
}

// ── token refresh & import ──────────────────────────────────────────────────

func (a *AuthSettings) refreshAccountToken(acct authAccount) tea.Cmd {
	provider := acct.Provider
	accountID := acct.ID
	email := acct.Email
	tokenData := acct.TokenData
	isActive := acct.IsActive

	return func() tea.Msg {
		provNorm := strings.ToLower(provider)
		switch provNorm {
		case "claudecode", "anthropic", "claude":
			var tok anthropic.OAuthToken
			if err := json.Unmarshal(tokenData, &tok); err != nil {
				return authTokenRefreshedMsg{provider: provider, accountID: accountID, err: fmt.Errorf("parse token: %w", err)}
			}
			if tok.RefreshToken == "" {
				return authTokenRefreshedMsg{provider: provider, accountID: accountID, err: fmt.Errorf("no refresh token available")}
			}
			newTok, err := anthropic.RefreshAccessToken(tok.RefreshToken)
			if err != nil {
				return authTokenRefreshedMsg{provider: provider, accountID: accountID, err: err}
			}
			// Store to SDK path only if this is the active account
			if isActive {
				_ = anthropic.StoreOAuthToken(newTok)
			}
			// Update in multi-account file
			td, _ := json.Marshal(newTok)
			updatedAcct := authAccount{
				ID:        accountID,
				Provider:  provider,
				Email:     email,
				IsActive:  isActive,
				AddedAt:   acct.AddedAt,
				TokenData: td,
			}
			_ = upsertAccount(updatedAcct)

			// Try to fetch email if we don't have one yet
			resolvedEmail := email
			if resolvedEmail == "" {
				resolvedEmail = FetchAnthropicEmailFromToken(newTok.AccessToken)
				if resolvedEmail != "" {
					SaveAccountEmail(accountID, resolvedEmail)
				}
			}
			return authTokenRefreshedMsg{provider: provider, accountID: accountID, newToken: td, email: resolvedEmail}

		case "openai":
			// OpenAI refreshes via the SDK, which reads/writes the SDK store
			// (~/.swarmos/oauth_openai.json). RefreshAndStoreToken keeps the SDK
			// store fresh; we then mirror the result into tui_accounts.json so the
			// two stores stay in sync. BUG #2 fix.
			newTok, err := openai.RefreshAndStoreToken(context.Background())
			if err != nil {
				return authTokenRefreshedMsg{provider: provider, accountID: accountID, err: err}
			}
			if newTok.APIKey == "" && openai.HasModelRequestScope(newTok.AccessToken) {
				newTok.APIKey = newTok.AccessToken
			}
			// RefreshAndStoreToken already persisted to the SDK store. Only keep it
			// authoritative when this account is the active one; if it isn't, the
			// active account's token must remain in the SDK store, so re-store it.
			if !isActive {
				if active := activeAccountFor(provider); active != nil && active.ID != accountID {
					_ = activateAccountToken(*active)
				}
			}
			td, _ := json.Marshal(newTok)
			_ = upsertAccount(authAccount{
				ID: accountID, Provider: provider, Email: email,
				IsActive: isActive, AddedAt: acct.AddedAt, TokenData: td,
			})
			return authTokenRefreshedMsg{provider: provider, accountID: accountID, newToken: td, email: email}

		case "xai", "grok", "x-ai", "supergrok", "xai-oauth", "grok-oauth":
			var tok xai.OAuthToken
			if err := json.Unmarshal(tokenData, &tok); err != nil {
				return authTokenRefreshedMsg{provider: provider, accountID: accountID, err: fmt.Errorf("parse token: %w", err)}
			}
			if tok.RefreshToken == "" {
				return authTokenRefreshedMsg{provider: provider, accountID: accountID, err: fmt.Errorf("no refresh token available")}
			}
			newTok, err := xai.RefreshToken(context.Background(), tok.RefreshToken)
			if err != nil {
				return authTokenRefreshedMsg{provider: provider, accountID: accountID, err: err}
			}
			// Write back to SDK storage only for the active account, but always
			// mirror into tui_accounts.json. BUG #2 fix.
			if isActive {
				_ = xai.StoreOAuthToken(newTok)
			}
			td, _ := json.Marshal(newTok)
			_ = upsertAccount(authAccount{
				ID: accountID, Provider: provider, Email: email,
				IsActive: isActive, AddedAt: acct.AddedAt, TokenData: td,
			})
			return authTokenRefreshedMsg{provider: provider, accountID: accountID, newToken: td, email: email}

		case "gemini", "google", "gemini-code-assist":
			// Gemini's OAuthManager refreshes + persists to its SDK store
			// internally. We seed it from the account's stored tokens, call
			// AccessToken (which refreshes if needed), then read the refreshed
			// tokens back out to mirror them into tui_accounts.json. BUG #2 fix.
			var tok gemini.OAuthTokens
			if err := json.Unmarshal(tokenData, &tok); err != nil {
				return authTokenRefreshedMsg{provider: provider, accountID: accountID, err: fmt.Errorf("parse token: %w", err)}
			}
			cfg := gemini.Config{
				AuthMode:     gemini.AuthModeOAuth,
				ClientSecret: os.Getenv("GEMINI_OAUTH_CLIENT_SECRET"),
			}
			mgr := gemini.NewOAuthManager(&cfg)
			// Seed the manager with the account's tokens so it refreshes THIS
			// account rather than whatever happens to be in the SDK store.
			if err := mgr.SaveTokens(&tok); err != nil {
				return authTokenRefreshedMsg{provider: provider, accountID: accountID, err: fmt.Errorf("seed gemini tokens: %w", err)}
			}
			if _, err := mgr.AccessToken(context.Background()); err != nil {
				if gemini.IsReauthRequired(err) {
					return authTokenRefreshedMsg{provider: provider, accountID: accountID, err: fmt.Errorf("Gemini session expired — please re-authenticate (Add account)")}
				}
				return authTokenRefreshedMsg{provider: provider, accountID: accountID, err: err}
			}
			// Read the refreshed tokens back out of the SDK store on disk
			// (AccessToken persisted them there via SaveTokens).
			refreshed := readGeminiSDKTokens()
			if refreshed == nil {
				refreshed = &tok
			}
			// AccessToken already saved to the SDK store. If this account isn't
			// active, restore the active account's tokens to the SDK store.
			if !isActive {
				if active := activeAccountFor(provider); active != nil && active.ID != accountID {
					_ = activateAccountToken(*active)
				}
			}
			td, _ := json.Marshal(refreshed)
			_ = upsertAccount(authAccount{
				ID: accountID, Provider: provider, Email: email,
				IsActive: isActive, AddedAt: acct.AddedAt, TokenData: td,
			})
			return authTokenRefreshedMsg{provider: provider, accountID: accountID, newToken: td, email: email}

		default:
			return authTokenRefreshedMsg{provider: provider, accountID: accountID, err: fmt.Errorf("refresh not supported for %s", provider)}
		}
	}
}

// importClaudeCodeToken reads the token from ~/.claude/.credentials.json
// (the file Claude Code CLI uses) and imports it into the TUI account store.
func (a *AuthSettings) importClaudeCodeToken() tea.Cmd {
	provider := a.pickerProvider
	if provider == "" {
		provider = "ClaudeCode"
	}

	return func() tea.Msg {
		home, err := os.UserHomeDir()
		if err != nil {
			return authClaudeCodeImportMsg{err: fmt.Errorf("cannot find home dir: %w", err)}
		}

		credPath := filepath.Join(home, ".claude", ".credentials.json")
		data, err := os.ReadFile(credPath)
		if err != nil {
			return authClaudeCodeImportMsg{err: fmt.Errorf("cannot read %s: %w", credPath, err)}
		}

		// Parse Claude Code's credential format:
		// {"claudeAiOauth":{"accessToken":"...","refreshToken":"...","expiresAt":...,...}}
		var credFile struct {
			ClaudeAiOauth struct {
				AccessToken      string   `json:"accessToken"`
				RefreshToken     string   `json:"refreshToken"`
				ExpiresAt        float64  `json:"expiresAt"` // milliseconds epoch
				Scopes           []string `json:"scopes"`
				SubscriptionType string   `json:"subscriptionType"`
			} `json:"claudeAiOauth"`
		}
		if err := json.Unmarshal(data, &credFile); err != nil {
			return authClaudeCodeImportMsg{err: fmt.Errorf("parse credentials: %w", err)}
		}

		cc := credFile.ClaudeAiOauth
		if cc.AccessToken == "" {
			return authClaudeCodeImportMsg{err: fmt.Errorf("no Claude Code token found in %s", credPath)}
		}

		// Convert to OAuthToken format
		// expiresAt is milliseconds since epoch; convert to seconds
		expiry := int64(cc.ExpiresAt / 1000)
		if cc.ExpiresAt > 1e12 { // already ms
			expiry = int64(cc.ExpiresAt / 1000)
		} else if cc.ExpiresAt > 0 { // already seconds
			expiry = int64(cc.ExpiresAt)
		}

		token := &anthropic.OAuthToken{
			AccessToken:  cc.AccessToken,
			TokenType:    "Bearer",
			RefreshToken: cc.RefreshToken,
			Scope:        strings.Join(cc.Scopes, " "),
			Expiry:       expiry,
		}

		// If token is expired but we have a refresh token, refresh it
		if anthropic.IsTokenExpired(token) && token.RefreshToken != "" {
			refreshed, err := anthropic.RefreshAccessToken(token.RefreshToken)
			if err == nil {
				token = refreshed
			}
			// If refresh fails, still import the token — the user can refresh later
		}

		// Store to SDK path
		_ = anthropic.StoreOAuthToken(token)

		// Fetch email for the token
		email := FetchAnthropicEmailFromToken(token.AccessToken)

		// Generate account ID and upsert
		accountID := genAccountID(provider)
		td, _ := json.Marshal(token)
		acct := authAccount{
			ID:        accountID,
			Provider:  provider,
			Email:     email,
			IsActive:  false,
			AddedAt:   time.Now().Unix(),
			TokenData: td,
		}
		_ = upsertAccount(acct)
		// Set this account as active (deactivates others for this provider)
		setActiveAccount(provider, accountID)

		return authClaudeCodeImportMsg{token: token, email: email, accountID: accountID}
	}
}

// RefreshAccountTokenByID refreshes the token for a specific account and updates
// both the tui_accounts.json and SDK storage. Exported for use by the usage screen.
//
// The return type is *anthropic.OAuthToken for backwards compatibility with the
// usage screen, which only reads AccessToken/Expiry. For non-anthropic providers
// we refresh via that provider's SDK (mirroring into tui_accounts.json) and
// synthesize a minimal anthropic.OAuthToken carrying the new access token +
// expiry so the caller can still read a fresh token. BUG #2 fix.
func RefreshAccountTokenByID(accountID string) (*anthropic.OAuthToken, error) {
	return RefreshAccountTokenByIDContext(context.Background(), accountID)
}

// RefreshAccountTokenByIDContext is the context-aware form used by background
// UI refreshes, which must not let a stalled OAuth endpoint block forever.
func RefreshAccountTokenByIDContext(ctx context.Context, accountID string) (*anthropic.OAuthToken, error) {
	f := loadAuthAccountFile()
	for _, a := range f.Accounts {
		if a.ID != accountID {
			continue
		}
		provNorm := strings.ToLower(a.Provider)
		switch provNorm {
		case "claudecode", "anthropic", "claude":
			var tok anthropic.OAuthToken
			if err := json.Unmarshal(a.TokenData, &tok); err != nil {
				return nil, fmt.Errorf("parse token: %w", err)
			}
			if tok.RefreshToken == "" {
				return nil, fmt.Errorf("no refresh token available")
			}
			newTok, err := anthropic.RefreshAccessToken(tok.RefreshToken)
			if err != nil {
				return nil, err
			}
			// Only store to SDK path if this is the active account
			if a.IsActive {
				_ = anthropic.StoreOAuthToken(newTok)
			}
			td, _ := json.Marshal(newTok)
			a.TokenData = td
			_ = upsertAccount(a)
			return newTok, nil

		case "openai":
			select {
			case openAIAccountRefreshGate <- struct{}{}:
				defer func() { <-openAIAccountRefreshGate }()
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			var tok openai.OAuthToken
			if err := json.Unmarshal(a.TokenData, &tok); err != nil {
				return nil, fmt.Errorf("parse token: %w", err)
			}
			if tok.RefreshToken == "" {
				return nil, fmt.Errorf("no refresh token available")
			}
			newTok, err := openai.RefreshAccessToken(ctx, &tok)
			if err != nil {
				return nil, err
			}
			if newTok.APIKey == "" && openai.HasModelRequestScope(newTok.AccessToken) {
				newTok.APIKey = newTok.AccessToken
			}
			if a.IsActive {
				_ = openai.StoreOAuthToken(newTok)
			}
			td, _ := json.Marshal(newTok)
			a.TokenData = td
			_ = upsertAccount(a)
			return &anthropic.OAuthToken{AccessToken: newTok.AccessToken, RefreshToken: newTok.RefreshToken, Expiry: newTok.ExpiresAt}, nil

		case "xai", "grok", "x-ai", "supergrok", "xai-oauth", "grok-oauth":
			var tok xai.OAuthToken
			if err := json.Unmarshal(a.TokenData, &tok); err != nil {
				return nil, fmt.Errorf("parse token: %w", err)
			}
			if tok.RefreshToken == "" {
				return nil, fmt.Errorf("no refresh token available")
			}
			newTok, err := xai.RefreshToken(context.Background(), tok.RefreshToken)
			if err != nil {
				return nil, err
			}
			if a.IsActive {
				_ = xai.StoreOAuthToken(newTok)
			}
			td, _ := json.Marshal(newTok)
			a.TokenData = td
			_ = upsertAccount(a)
			return &anthropic.OAuthToken{AccessToken: newTok.AccessToken, RefreshToken: newTok.RefreshToken, Expiry: newTok.ExpiresAt}, nil

		case "gemini", "google", "gemini-code-assist":
			var tok gemini.OAuthTokens
			if err := json.Unmarshal(a.TokenData, &tok); err != nil {
				return nil, fmt.Errorf("parse token: %w", err)
			}
			cfg := gemini.Config{
				AuthMode:     gemini.AuthModeOAuth,
				ClientSecret: os.Getenv("GEMINI_OAUTH_CLIENT_SECRET"),
			}
			mgr := gemini.NewOAuthManager(&cfg)
			if err := mgr.SaveTokens(&tok); err != nil {
				return nil, fmt.Errorf("seed gemini tokens: %w", err)
			}
			if _, err := mgr.AccessToken(context.Background()); err != nil {
				if gemini.IsReauthRequired(err) {
					return nil, fmt.Errorf("Gemini session expired — please re-authenticate (Add account)")
				}
				return nil, err
			}
			refreshed := readGeminiSDKTokens()
			if refreshed == nil {
				refreshed = &tok
			}
			if !a.IsActive {
				if active := activeAccountFor(a.Provider); active != nil && active.ID != accountID {
					_ = activateAccountToken(*active)
				}
			}
			td, _ := json.Marshal(refreshed)
			a.TokenData = td
			_ = upsertAccount(a)
			return &anthropic.OAuthToken{AccessToken: refreshed.AccessToken, RefreshToken: refreshed.RefreshToken, Expiry: refreshed.ExpiresAt}, nil

		default:
			return nil, fmt.Errorf("refresh not supported for %s", a.Provider)
		}
	}
	return nil, fmt.Errorf("account %s not found", accountID)
}

// ── flow starters ─────────────────────────────────────────────────────────────

func (a *AuthSettings) startFlow(p authProviderEntry) tea.Cmd {
	a.cancelFlow()
	a.flowProvider = p.Name
	a.flowAccount = genAccountID(p.Name)
	name := strings.ToLower(strings.TrimSpace(p.Name))

	switch name {
	case "claudecode", "anthropic", "claude":
		return a.startClaudeFlow()
	case "openai", "codex":
		// codex is the catalog identity for the ChatGPT-subscription OAuth row;
		// it shares the OpenAI device flow and token store.
		return a.startOpenAIFlow()
	case "gemini":
		return a.startGeminiFlow()
	case "xai", "xai-api":
		return a.startXAIFlow()
	case "cursor":
		return a.startCursorFlow()
	default:
		a.flowState = "error"
		a.flowError = i18n.T("settings.auth.error.unknown_provider", p.Name)
		return nil
	}
}

func (a *AuthSettings) startClaudeFlow() tea.Cmd {
	a.flowState = "waiting_url"
	a.anthropicFlow = anthropic.NewDeviceFlow()
	flow := a.anthropicFlow
	return func() tea.Msg {
		resp, err := flow.InitiateDeviceFlow()
		if err != nil {
			return authErrorMsg{err: err}
		}
		_ = launch.Open(resp.VerificationURIComplete)
		return authURLReadyMsg{url: resp.VerificationURIComplete}
	}
}

func (a *AuthSettings) exchangeClaudeCode(code string) tea.Cmd {
	a.flowState = "exchanging"
	flow := a.anthropicFlow
	provider := a.flowProvider
	accountID := a.flowAccount
	return func() tea.Msg {
		token, err := flow.ExchangeCodeForToken(code)
		if err != nil {
			return authErrorMsg{err: fmt.Errorf("code exchange failed: %w", err)}
		}
		if err := anthropic.StoreOAuthToken(token); err != nil {
			return authErrorMsg{err: fmt.Errorf("failed to store token: %w", err)}
		}
		// Store in multi-account file
		td, _ := json.Marshal(token)
		acct := authAccount{
			ID:        accountID,
			Provider:  provider,
			IsActive:  true,
			AddedAt:   time.Now().Unix(),
			TokenData: td,
		}
		_ = upsertAccount(acct)
		// Kick off email fetch as a side-channel cmd (returned later via authEmailFetchedMsg)
		go func() {
			// We can't return a cmd from here; the fetchAnthropicEmail cmd
			// is batched in HandleMsg when authCodeSuccessMsg is processed.
		}()
		return authCodeSuccessMsg{provider: "Claude Code", accountID: accountID}
	}
}

func (a *AuthSettings) startOpenAIFlow() tea.Cmd {
	a.flowState = "openai_code"
	return func() tea.Msg {
		ctx := context.Background()
		flow, err := openai.StartDeviceFlow(ctx)
		if err != nil {
			return authOpenAIFlowMsg{err: err}
		}
		return authOpenAIFlowMsg{flow: flow}
	}
}

func (a *AuthSettings) pollOpenAICmd(flow *openai.DeviceFlow) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		exchange, err := openai.PollForAuthorizationCodeWithCallback(ctx, flow, nil)
		if err != nil {
			return authOpenAIResultMsg{err: err}
		}
		token, err := openai.ExchangeCodeForToken(ctx, exchange)
		if err != nil {
			return authOpenAIResultMsg{err: err}
		}
		return authOpenAIResultMsg{token: token}
	}
}

func (a *AuthSettings) startGeminiFlow() tea.Cmd {
	a.flowState = "gemini_wait"
	return func() tea.Msg {
		ctx := context.Background()
		cfg := gemini.Config{
			AuthMode:     gemini.AuthModeOAuth,
			ClientSecret: os.Getenv("GEMINI_OAUTH_CLIENT_SECRET"),
		}
		if err := cfg.Validate(); err != nil {
			return authGeminiFlowMsg{err: err}
		}
		mgr := gemini.NewOAuthManager(&cfg)
		authURL, resCh, errCh, cleanup, err := mgr.StartBrowserAuth(ctx)
		if err != nil {
			return authGeminiFlowMsg{err: err}
		}
		return authGeminiFlowMsg{
			authURL: authURL,
			resCh:   resCh,
			errCh:   errCh,
			cleanup: cleanup,
		}
	}
}

func (a *AuthSettings) pollGeminiCmd(resCh <-chan *gemini.OAuthTokens, errCh <-chan error) tea.Cmd {
	return func() tea.Msg {
		select {
		case token := <-resCh:
			return authGeminiResultMsg{token: token}
		case err := <-errCh:
			return authGeminiResultMsg{err: err}
		}
	}
}

// startXAIFlow initiates xAI / SuperGrok OAuth.
// Tries Grok CLI import (~/.grok/auth.json) first, then falls back to the
// PKCE loopback browser flow on fixed port 56121.
func (a *AuthSettings) startXAIFlow() tea.Cmd {
	a.flowState = "xai_wait"
	return func() tea.Msg {
		// 1. Try Grok CLI import (zero-friction path).
		if token, err := xai.ImportFromGrokCLI(""); err == nil && token != nil {
			_ = xai.StoreOAuthToken(token)
			return authXAIFlowMsg{token: token}
		}

		// 2. PKCE loopback browser flow (OIDC discovery + port 56121).
		ctx := context.Background()
		flow, err := xai.NewLoopbackFlow(ctx)
		if err != nil {
			return authXAIFlowMsg{err: err}
		}
		authURL := flow.AuthorizeURL()
		_ = launch.Open(authURL)
		return authXAIFlowMsg{flow: flow, authURL: authURL}
	}
}

// waitXAICallbackCmd blocks until the loopback server receives the callback,
// exchanges the code, and returns the token.
func (a *AuthSettings) waitXAICallbackCmd() tea.Cmd {
	flow := a.xaiLoopback
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		token, err := flow.WaitForCallback(ctx)
		if err != nil {
			return authXAIResultMsg{err: err}
		}
		if storeErr := xai.StoreOAuthToken(token); storeErr != nil {
			return authXAIResultMsg{err: fmt.Errorf("xAI: failed to store token: %w", storeErr)}
		}
		return authXAIResultMsg{token: token}
	}
}

// startCursorFlow initiates the Cursor (Anysphere) OAuth PKCE login: build a
// challenge, open the loginDeepControl deeplink, then wait for /auth/poll.
func (a *AuthSettings) startCursorFlow() tea.Cmd {
	a.flowState = "cursor_wait"
	return func() tea.Msg {
		flow, err := newCursorPKCEFlow()
		if err != nil {
			return authCursorFlowMsg{err: err}
		}
		_ = launch.Open(flow.authURL)
		return authCursorFlowMsg{flow: flow, authURL: flow.authURL}
	}
}

// waitCursorCallbackCmd polls Cursor's /auth/poll until the user approves the
// login in the browser, then persists the token to cli-config.json.
func (a *AuthSettings) waitCursorCallbackCmd() tea.Cmd {
	flow := a.cursorFlow
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		tok, err := flow.poll(ctx)
		if err != nil {
			return authCursorResultMsg{err: err}
		}
		if storeErr := storeCursorToken(tok); storeErr != nil {
			return authCursorResultMsg{err: storeErr}
		}
		return authCursorResultMsg{token: tok}
	}
}

// ── HandleMsg: Claude Code success needs to batch email fetch ─────────────────
// (overrides authCodeSuccessMsg to also return email fetch cmd)

// ── Render ────────────────────────────────────────────────────────────────────

// parseExpiryValue normalizes a heterogeneous JSON expiry value into unix
// seconds. It accepts:
//   - int64 / float64 unix seconds (e.g. 1717000000 or 1717000000.0)
//   - float64 unix milliseconds (e.g. 1717000000000) — auto-detected by magnitude
//   - RFC3339 / RFC3339Nano date strings (e.g. "2026-06-26T12:00:00Z")
//
// It returns (seconds, true) when a usable expiry was parsed, or (0, false)
// when the value is empty/unparseable. Different providers persist expiry in
// different shapes, so this keeps isAccountTokenExpired robust across all of
// them. BUG #3 fix.
func parseExpiryValue(v any) (int64, bool) {
	switch t := v.(type) {
	case float64:
		if t <= 0 {
			return 0, false
		}
		// Heuristic: values above ~year 33658 in seconds are actually ms.
		if t > 1e12 {
			return int64(t / 1000), true
		}
		return int64(t), true
	case json.Number:
		if f, err := t.Float64(); err == nil {
			return parseExpiryValue(f)
		}
		return 0, false
	case string:
		if t == "" {
			return 0, false
		}
		// Numeric string (e.g. "1717000000").
		if n, err := json.Number(t).Int64(); err == nil {
			return parseExpiryValue(float64(n))
		}
		// RFC3339 / RFC3339Nano date string.
		for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
			if ts, err := time.Parse(layout, t); err == nil {
				return ts.Unix(), true
			}
		}
		return 0, false
	default:
		return 0, false
	}
}

// isAccountTokenExpired checks whether an account's stored token has expired.
// It tolerates the different expiry encodings used across providers: int64 or
// float unix seconds, float unix milliseconds, and RFC3339 strings. BUG #3 fix.
func isAccountTokenExpired(acct authAccount) bool {
	if len(acct.TokenData) == 0 {
		return false
	}
	// Decode the two candidate fields as `any` so we accept numbers and strings.
	var tok struct {
		Expiry    any `json:"expiry"`
		ExpiresAt any `json:"expires_at"`
	}
	if json.Unmarshal(acct.TokenData, &tok) != nil {
		return false
	}
	// Use whichever expiry field is set (treat unparseable/0 as unset), and
	// prefer the earlier of the two when both are present.
	var effectiveExpiry int64
	if v, ok := parseExpiryValue(tok.Expiry); ok {
		effectiveExpiry = v
	}
	if v, ok := parseExpiryValue(tok.ExpiresAt); ok && (effectiveExpiry == 0 || v < effectiveExpiry) {
		effectiveExpiry = v
	}
	if effectiveExpiry <= 0 {
		return false // no expiry set, treat as valid
	}
	return time.Now().Unix() >= (effectiveExpiry - 300) // 5-min buffer like SDK
}

func (a *AuthSettings) Render(width, height int, state *State, theme any) string {
	th := theme.(Theme)
	innerWidth := maxInt(20, width-8)
	if width < 50 {
		innerWidth = maxInt(20, width-4)
	}

	var content string
	switch {
	case a.flowState == "success":
		content = a.renderSuccess(innerWidth, th)
	case a.flowState == "error":
		content = a.renderError(innerWidth, th)
	case a.flowState == "waiting_url":
		content = a.renderWaitingURL(innerWidth, th)
	case a.flowState == "waiting_code":
		content = a.renderClaudeCode(innerWidth, th)
	case a.flowState == "exchanging":
		content = a.renderExchanging(innerWidth, th)
	case a.flowState == "openai_code" || a.flowState == "openai_poll":
		content = a.renderOpenAIDevice(innerWidth, th)
	case a.flowState == "gemini_wait":
		content = a.renderGeminiWait(innerWidth, th)
	case a.flowState == "xai_wait":
		content = a.renderXAIWait(innerWidth, th)
	case a.flowState == "cursor_wait":
		content = a.renderCursorWait(innerWidth, th)
	case a.viewState == "account_list":
		content = a.renderAccountList(innerWidth, th)
	case a.viewState == "usage":
		content = a.renderUsage(innerWidth, th)
	default:
		content = a.renderList(innerWidth, th)
	}

	containerPad := 2
	if width < 50 {
		containerPad = 1
	}
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(th.Border)).
		Width(maxInt(20, width-2)).
		Padding(1, containerPad).
		Render(content)
}

// ── sub-renders ───────────────────────────────────────────────────────────────

func (a *AuthSettings) renderList(innerWidth int, th Theme) string {
	var lines []string

	lines = append(lines,
		lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(th.Primary)).Render(i18n.T("settings.auth.title")),
		lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).Render(i18n.T("settings.auth.subtitle")),
		"",
	)

	if len(a.providers) == 0 {
		lines = append(lines,
			lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).Italic(true).
				Render(i18n.T("settings.auth.empty")),
		)
	}

	for i, p := range a.providers {
		isSelected := i == a.selectedIdx

		statusIcon := "○"
		statusColor := th.TextMuted
		statusLabel := i18n.T("settings.auth.status.not_logged_in")
		if len(p.Accounts) > 0 {
			statusIcon = "●"
			statusColor = th.Success
			n := len(p.Accounts)
			if n == 1 {
				statusLabel = i18n.T("settings.auth.status.one_account")
			} else {
				statusLabel = i18n.T("settings.auth.status.accounts", n)
			}
		} else if p.IsAuthed {
			statusIcon = "●"
			statusColor = th.Success
			statusLabel = i18n.T("settings.auth.status.logged_in")
		}

		rowBg := th.BGLight
		textFg := th.Text
		if isSelected {
			rowBg = th.Primary
			textFg = th.BG
		}

		colorDot := lipgloss.NewStyle().Foreground(lipgloss.Color(p.Color)).Render("◆")
		name := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(textFg)).Render(p.DisplayName)
		status := lipgloss.NewStyle().Foreground(lipgloss.Color(statusColor)).Render(statusIcon + " " + statusLabel)

		left := colorDot + "  " + name
		right := status
		pad := innerWidth - lipgloss.Width(left) - lipgloss.Width(right) - 2
		if pad < 1 {
			pad = 1
		}

		row := lipgloss.NewStyle().
			Background(lipgloss.Color(rowBg)).
			Width(innerWidth).
			Padding(0, 1).
			Render(left + strings.Repeat(" ", pad) + right)
		lines = append(lines, row)

		// Show email of active account under the row
		if len(p.Accounts) > 0 {
			for _, acct := range p.Accounts {
				if acct.IsActive && acct.Email != "" {
					emailLine := lipgloss.NewStyle().
						Foreground(lipgloss.Color(th.TextMuted)).
						Italic(true).
						Render("    " + acct.Email)
					lines = append(lines, emailLine)
					break
				}
			}
		}

		if isSelected {
			var hint string
			if len(p.Accounts) > 0 {
				hint = i18n.T("settings.auth.hint.manage", p.DisplayName)
			} else {
				hint = i18n.T("settings.auth.hint.login", p.DisplayName)
			}
			lines = append(lines,
				lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).Italic(true).Render(hint),
			)
		}
		lines = append(lines, "")
	}

	if a.message != "" {
		c := th.Success
		if !a.messageOK {
			c = th.Error
		}
		lines = append(lines,
			lipgloss.NewStyle().Foreground(lipgloss.Color(c)).Bold(true).Render("  "+a.message),
			"",
		)
	}

	// Usage button
	usageSelected := a.selectedIdx == len(a.providers)
	usageBg := th.BGLight
	usageFg := th.Text
	if usageSelected {
		usageBg = th.Primary
		usageFg = th.BG
	}
	usageRow := lipgloss.NewStyle().
		Background(lipgloss.Color(usageBg)).
		Foreground(lipgloss.Color(usageFg)).
		Width(innerWidth).
		Padding(0, 1).
		Render(i18n.T("settings.auth.usage.row"))
	lines = append(lines, usageRow)
	if usageSelected {
		lines = append(lines,
			lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).Italic(true).
				Render(i18n.T("settings.auth.usage.open")),
		)
	}
	lines = append(lines, "")
	lines = append(lines,
		lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).
			Render(i18n.T("settings.auth.hints.main")),
	)

	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}

func (a *AuthSettings) renderUsage(width int, th Theme) string {
	innerWidth := width - 4
	if innerWidth < 20 {
		innerWidth = 20
	}

	// Header
	headerStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Primary)).
		Bold(true).
		Padding(0, 1).
		Width(innerWidth).
		Align(lipgloss.Left)

	header := headerStyle.Render(i18n.T("settings.auth.usage.row"))

	// Content
	var lines []string
	lines = append(lines, header)
	lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color(th.Border)).Render(strings.Repeat("─", innerWidth)))

	// Usage info - simplified placeholder for now
	// In the future, this can be connected to the app's usage tracking
	lines = append(lines, "")
	noData := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		Italic(true).
		Render(i18n.T("settings.auth.usage.available"))
	lines = append(lines, noData)
	lines = append(lines, "")
	hint := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		Render(i18n.T("settings.auth.usage.start"))
	lines = append(lines, hint)

	lines = append(lines, "")
	lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).
		Render(i18n.T("settings.auth.usage.back")))

	containerStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(th.Border)).
		Background(lipgloss.Color(th.BG)).
		Padding(1, 1)

	innerContent := lipgloss.JoinVertical(lipgloss.Left, lines...)
	return containerStyle.Render(innerContent)
}

func (a *AuthSettings) renderAccountList(innerWidth int, th Theme) string {
	accts := accountsForProvider(a.pickerProvider)

	// Find display name
	displayName := a.pickerProvider
	for _, p := range a.providers {
		if strings.EqualFold(p.Name, a.pickerProvider) {
			displayName = p.DisplayName
			break
		}
	}

	var lines []string
	lines = append(lines,
		lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(th.Primary)).Render(i18n.T("settings.auth.accounts.title", displayName)),
		lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).Render(i18n.T("settings.auth.accounts.subtitle")),
		"",
	)

	// Row 0: Add another account
	addSelected := a.pickerIdx == 0
	addBg := th.BGLight
	addFg := th.Text
	if addSelected {
		addBg = th.Primary
		addFg = th.BG
	}
	addRow := lipgloss.NewStyle().
		Background(lipgloss.Color(addBg)).
		Foreground(lipgloss.Color(addFg)).
		Width(innerWidth).
		Padding(0, 1).
		Render(i18n.T("settings.auth.accounts.add"))
	lines = append(lines, addRow, "")

	// Import from Claude Code hint (only for Anthropic/ClaudeCode providers)
	provNorm := strings.ToLower(a.pickerProvider)
	if provNorm == "claudecode" || provNorm == "anthropic" || provNorm == "claude" {
		importHint := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Italic(true).
			Render(i18n.T("settings.auth.accounts.import_claude"))
		lines = append(lines, importHint, "")
	}

	// Existing accounts
	for i, acct := range accts {
		isSelected := a.pickerIdx == i+1

		rowBg := th.BGLight
		textFg := th.Text
		if isSelected {
			rowBg = th.Primary
			textFg = th.BG
		}

		label := acct.displayLabel()

		// Determine status tag based on active state AND token expiry
		var statusTag string
		if acct.IsActive {
			expired := isAccountTokenExpired(acct)
			if expired {
				statusTag = lipgloss.NewStyle().
					Foreground(lipgloss.Color(th.Error)).
					Bold(true).
					Render(i18n.T("settings.auth.accounts.expired_warning"))
			} else {
				statusTag = lipgloss.NewStyle().
					Foreground(lipgloss.Color(th.Success)).
					Render(i18n.T("settings.auth.accounts.active"))
			}
		} else {
			expired := isAccountTokenExpired(acct)
			if expired {
				statusTag = lipgloss.NewStyle().
					Foreground(lipgloss.Color(th.Warning)).
					Render(i18n.T("settings.auth.accounts.expired"))
			}
		}

		left := "  " + label
		right := statusTag
		pad := innerWidth - lipgloss.Width(left) - lipgloss.Width(right) - 2
		if pad < 1 {
			pad = 1
		}

		row := lipgloss.NewStyle().
			Background(lipgloss.Color(rowBg)).
			Foreground(lipgloss.Color(textFg)).
			Width(innerWidth).
			Padding(0, 1).
			Bold(acct.IsActive).
			Render(left + strings.Repeat(" ", pad) + right)
		lines = append(lines, row, "")
	}

	if a.message != "" {
		c := th.Success
		if !a.messageOK {
			c = th.Error
		}
		lines = append(lines,
			lipgloss.NewStyle().Foreground(lipgloss.Color(c)).Bold(true).Render("  "+a.message),
			"",
		)
	}

	lines = append(lines,
		lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).
			Render(i18n.T("settings.auth.hints.accounts")),
	)

	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}

func (a *AuthSettings) renderWaitingURL(w int, th Theme) string {
	return lipgloss.JoinVertical(lipgloss.Left,
		lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(th.Primary)).Render(i18n.T("settings.auth.connecting.title", a.flowProvider)),
		"",
		lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).Render(i18n.T("settings.auth.connecting.browser")),
		"",
		lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).Italic(true).Render(i18n.T("settings.auth.cancel")),
	)
}

func (a *AuthSettings) renderClaudeCode(w int, th Theme) string {
	urlStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(th.Primary)).Underline(true)
	inputStyle := lipgloss.NewStyle().
		Border(lipgloss.NormalBorder()).
		BorderForeground(lipgloss.Color(th.Primary)).
		Padding(0, 1).
		Width(maxInt(20, w-4))

	var inputDisplay string
	if a.flowInput == "" {
		inputDisplay = "█"
	} else {
		cur := a.flowCursor
		if cur > len(a.flowInput) {
			cur = len(a.flowInput)
		}
		before := a.flowInput[:cur]
		cursor := "█"
		after := ""
		if cur < len(a.flowInput) {
			after = a.flowInput[cur:]
		}
		inputDisplay = before + cursor + after
	}

	return lipgloss.JoinVertical(lipgloss.Left,
		lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(th.Primary)).Render(i18n.T("settings.auth.authenticate", a.flowProvider)),
		"",
		lipgloss.NewStyle().Foreground(lipgloss.Color(th.Text)).Render(i18n.T("settings.auth.code.step_url")),
		urlStyle.Render(a.flowURL),
		"",
		lipgloss.NewStyle().Foreground(lipgloss.Color(th.Text)).Render(i18n.T("settings.auth.code.step_authorize")),
		"",
		lipgloss.NewStyle().Foreground(lipgloss.Color(th.Text)).Render(i18n.T("settings.auth.code.step_paste")),
		"  "+inputStyle.Render(inputDisplay),
		"",
		lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).Italic(true).
			Render(i18n.T("settings.auth.code.hints")),
	)
}

func (a *AuthSettings) renderExchanging(w int, th Theme) string {
	return lipgloss.JoinVertical(lipgloss.Left,
		lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(th.Primary)).Render(i18n.T("settings.auth.verifying.title")),
		"",
		lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).Render(i18n.T("settings.auth.verifying.exchange")),
	)
}

func (a *AuthSettings) renderOpenAIDevice(w int, th Theme) string {
	userCode := a.openaiUserCode
	if a.openaiFlow != nil && a.openaiFlow.UserCode != "" {
		userCode = a.openaiFlow.UserCode
	}
	if userCode == "" {
		userCode = i18n.T("settings.auth.loading")
	}

	urlVal := a.flowURL
	if urlVal == "" {
		urlVal = openai.OAuthDeviceVerificationURL()
	}

	statusMsg := i18n.T("settings.auth.device.waiting")
	if a.flowState == "openai_code" {
		statusMsg = i18n.T("settings.auth.device.generating")
	}

	copyHint := i18n.T("settings.auth.device.copy")
	if a.openaiCopyOK {
		copyHint = i18n.T("settings.auth.device.copied")
	}

	return lipgloss.JoinVertical(lipgloss.Left,
		lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(th.Primary)).Render(i18n.T("settings.auth.authenticate", "OpenAI")),
		"",
		lipgloss.NewStyle().Foreground(lipgloss.Color(th.Text)).Render(i18n.T("settings.auth.device.step_code")),
		lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(th.Primary)).Render("   "+userCode),
		lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).Italic(true).Render("   "+copyHint),
		"",
		lipgloss.NewStyle().Foreground(lipgloss.Color(th.Text)).Render(i18n.T("settings.auth.device.step_url")),
		lipgloss.NewStyle().Foreground(lipgloss.Color(th.Primary)).Underline(true).Render(urlVal),
		"",
		lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).Render(statusMsg),
		"",
		lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).Italic(true).Render(i18n.T("settings.auth.cancel")),
	)
}

func (a *AuthSettings) renderGeminiWait(w int, th Theme) string {
	url := a.flowURL
	if url == "" {
		url = i18n.T("settings.auth.opening_browser")
	}
	return lipgloss.JoinVertical(lipgloss.Left,
		lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(th.Primary)).Render(i18n.T("settings.auth.authenticate", "Gemini")),
		"",
		lipgloss.NewStyle().Foreground(lipgloss.Color(th.Text)).Render(i18n.T("settings.auth.browser.complete")),
		"",
		lipgloss.NewStyle().Foreground(lipgloss.Color(th.Text)).Render(i18n.T("settings.auth.url")),
		lipgloss.NewStyle().Foreground(lipgloss.Color(th.Primary)).Underline(true).Render(url),
		"",
		lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).Render(i18n.T("settings.auth.browser.waiting")),
		"",
		lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).Italic(true).Render(i18n.T("settings.auth.cancel")),
	)
}

func (a *AuthSettings) renderXAIWait(w int, th Theme) string {
	url := a.flowURL
	if url == "" {
		url = i18n.T("settings.auth.grok.checking")
	}
	return lipgloss.JoinVertical(lipgloss.Left,
		lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(th.Primary)).Render(i18n.T("settings.auth.authenticate", "xAI / Grok")),
		"",
		lipgloss.NewStyle().Foreground(lipgloss.Color(th.Text)).Render(i18n.T("settings.auth.grok.browser")),
		lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).Render(i18n.T("settings.auth.grok.callback")),
		"",
		lipgloss.NewStyle().Foreground(lipgloss.Color(th.Text)).Render(i18n.T("settings.auth.auth_url")),
		lipgloss.NewStyle().Foreground(lipgloss.Color(th.Primary)).Underline(true).Render(url),
		"",
		lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).Italic(true).
			Render(i18n.T("settings.auth.grok.no_browser")),
		"",
		lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).Italic(true).Render(i18n.T("settings.auth.cancel")),
	)
}

func (a *AuthSettings) renderCursorWait(w int, th Theme) string {
	url := a.flowURL
	if url == "" {
		url = i18n.T("settings.auth.cursor.preparing")
	}
	return lipgloss.JoinVertical(lipgloss.Left,
		lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(th.Primary)).Render(i18n.T("settings.auth.authenticate", "Cursor")),
		"",
		lipgloss.NewStyle().Foreground(lipgloss.Color(th.Text)).Render(i18n.T("settings.auth.cursor.browser")),
		lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).Render(i18n.T("settings.auth.cursor.waiting")),
		"",
		lipgloss.NewStyle().Foreground(lipgloss.Color(th.Text)).Render(i18n.T("settings.auth.auth_url")),
		lipgloss.NewStyle().Foreground(lipgloss.Color(th.Primary)).Underline(true).Render(url),
		"",
		lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).Italic(true).
			Render(i18n.T("settings.auth.cursor.no_browser")),
		"",
		lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).Italic(true).Render(i18n.T("settings.auth.cancel")),
	)
}

func (a *AuthSettings) renderSuccess(w int, th Theme) string {
	return lipgloss.JoinVertical(lipgloss.Left,
		lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(th.Success)).Render("✓  "+a.message),
		"",
		lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).Italic(true).Render(i18n.T("settings.auth.return")),
	)
}

func (a *AuthSettings) renderError(w int, th Theme) string {
	return lipgloss.JoinVertical(lipgloss.Left,
		lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(th.Error)).Render(i18n.T("settings.auth.failed")),
		"",
		lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).Render(a.flowError),
		"",
		lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).Italic(true).Render(i18n.T("settings.auth.return")),
	)
}
