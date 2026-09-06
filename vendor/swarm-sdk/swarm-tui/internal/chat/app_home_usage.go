package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation/usageindex"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider/anthropic"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider/codex"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider/openai"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/safego"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/settings"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/metrics"
)

// ─── Usage data types ────────────────────────────────────────────────────────

type anthropicUsageQuota struct {
	Utilization float64 `json:"utilization"` // already a percentage: 70.0 = 70%
	ResetsAt    string  `json:"resets_at"`
}

type anthropicExtraUsage struct {
	IsEnabled    bool     `json:"is_enabled"`
	UsedCredits  *float64 `json:"used_credits"`  // pointer — can be null
	MonthlyLimit *float64 `json:"monthly_limit"` // pointer — can be null
	Utilization  *float64 `json:"utilization"`   // pointer — can be null
}

// anthropicUsageData is the full response from GET /api/oauth/usage.
type anthropicUsageData struct {
	FiveHour       anthropicUsageQuota  `json:"five_hour"`
	SevenDay       anthropicUsageQuota  `json:"seven_day"`
	SevenDaySonnet anthropicUsageQuota  `json:"seven_day_sonnet"`
	ExtraUsage     *anthropicExtraUsage `json:"extra_usage,omitempty"`
}

// geminiQuotaBucket represents a single quota bucket for a Gemini model
type geminiQuotaBucket struct {
	ModelID           string  `json:"modelId"`
	RemainingAmount   string  `json:"remainingAmount"`   // Can be a large number, hence string
	RemainingFraction float64 `json:"remainingFraction"` // 0.0-1.0
	ResetTime         string  `json:"resetTime"`         // RFC3339 format
}

// geminiUsageQuota represents Gemini's per-model quota
type geminiUsageQuota struct {
	Remaining   int64   // Calculated from RemainingAmount / RemainingFraction
	Limit       int64   // Calculated as RemainingAmount / RemainingFraction
	Utilization float64 // Percentage 0-100
	ResetsAt    string  // RFC3339 timestamp
	ModelID     string  // Which model this quota is for
}

// geminiUsageData holds multiple model quotas from Gemini API
type geminiUsageData struct {
	Models             map[string]*geminiUsageQuota // key is model ID
	HighestUtilization *geminiUsageQuota            // The quota bucket with highest utilization
}

// cursorUsageData holds Cursor's current-period plan usage from DashboardService.
type cursorUsageData struct {
	PlanLimit       float64 // planUsage.limit
	PlanRemaining   float64 // planUsage.remaining
	PlanPercentUsed float64 // planUsage.totalPercentUsed (0-100)
	BillingStart    string  // billingCycleStart (epoch-ms string or RFC3339)
	BillingEnd      string  // billingCycleEnd
	DisplayMessage  string  // human-facing status from the API
	HardLimitCents  float64 // GetHardLimit.hardLimit (cents), 0 if unknown
}

// xaiUsageData holds xAI prepaid/postpaid spend pulled from the management API.
// Only available when a management key + team are configured (the stored
// SuperGrok OAuth token cannot read billing).
type xaiUsageData struct {
	PrepaidCreditsUSD float64 // remaining prepaid credit balance (USD)
	SoftLimitUSD      float64 // postpaid soft spending limit (USD), 0 if unset
	HasData           bool
}

// codexUsageData wraps the latest live or cached ChatGPT rate-limit snapshot
// for one account (see internal/provider/codex/ratelimits.go).
type codexUsageData struct {
	Snapshot   codex.RateLimitSnapshot
	Live       bool
	RefreshErr error
}

type providerUsageEntry struct {
	Provider      string
	Email         string
	AccountID     string
	AnthropicData *anthropicUsageData
	GeminiData    *geminiUsageData
	CursorData    *cursorUsageData
	XAIData       *xaiUsageData
	CodexData     *codexUsageData
	Err           error
}

// codexAccountEmails maps ChatGPT account ids (from stored token payloads) to
// the emails recorded in the account store, for display next to usage rows.
func codexAccountEmails() map[string]string {
	out := map[string]string{}
	for _, acct := range settings.AccountsForProvider("codex") {
		var tok openai.OAuthToken
		if err := json.Unmarshal(acct.TokenData, &tok); err != nil {
			continue
		}
		id := tok.AccountID
		if id == "" && tok.IDToken != "" {
			id = openai.ExtractAccountIDFromIDToken(tok.IDToken)
		}
		if id != "" && acct.Email != "" {
			out[id] = acct.Email
		}
	}
	return out
}

// usageCacheTTL is how long a previously fetched usage snapshot is considered
// fresh. Within this window, screens (home + debug Usage tab) render the cached
// a.usageResult immediately instead of blocking on a fresh network fetch. Older
// snapshots trigger a background refresh that leaves the stale content visible.
const usageCacheTTL = 30 * time.Second

type usageDataResult struct {
	Entries         []providerUsageEntry
	Conversation    *metrics.ConversationUsageSummary
	ConversationErr error
	Daily           []usageindex.DayBucket
	Spikes          []usageindex.Finding
	Breaks          []usageindex.Finding
	Backend         string
	Degraded        bool
	DegradedReason  string
	FilesParsed     int64
	FilesScanned    int64
	SyncDuration    time.Duration
	FetchedAt       time.Time
}

type usageDataFetchedMsg struct {
	generation uint64
	result     *usageDataResult
	err        error
}

type usageEntryFetchedMsg struct {
	generation uint64
	entry      providerUsageEntry
}

type conversationUsageFetchedMsg struct {
	generation uint64
	snapshot   metrics.ConversationUsageSnapshot
	err        error
}

// ─── Fetching ────────────────────────────────────────────────────────────────

func fetchAnthropicUsage(accessToken string) (*anthropicUsageData, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://api.anthropic.com/api/oauth/usage", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("anthropic-beta", "oauth-2025-04-20")
	req.Header.Set("x-app", "cli")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var usage anthropicUsageData
	if err := json.Unmarshal(body, &usage); err != nil {
		return nil, fmt.Errorf("parse error: %w", err)
	}
	return &usage, nil
}

// fetchGeminiUsage queries the Gemini API for current quota information.
// Note: This uses the internal cloudcode API endpoint that the Gemini CLI uses.
// For production use with public API, you would use the Google Cloud API.
func fetchGeminiUsage(accessToken string, projectID string) (*geminiUsageData, error) {
	if projectID == "" {
		return nil, fmt.Errorf("project ID required for Gemini quota fetch")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Use the Gemini internal API endpoint (cloudcode-pa.googleapis.com)
	url := fmt.Sprintf("https://cloudcode-pa.googleapis.com/v1internal:retrieveUserQuota?project=%s", projectID)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	// Parse the Gemini quota response
	var quotaResp struct {
		Buckets []geminiQuotaBucket `json:"buckets"`
	}
	if err := json.Unmarshal(body, &quotaResp); err != nil {
		return nil, fmt.Errorf("parse error: %w", err)
	}

	// Convert to our usage structure
	geminiData := &geminiUsageData{
		Models: make(map[string]*geminiUsageQuota),
	}

	for _, bucket := range quotaResp.Buckets {
		if bucket.ModelID == "" || bucket.RemainingAmount == "" {
			continue
		}

		remaining := parseQuotaAmount(bucket.RemainingAmount)
		if remaining <= 0 || bucket.RemainingFraction <= 0 {
			continue
		}

		limit := int64(float64(remaining) / bucket.RemainingFraction)
		utilization := 100.0 * (1.0 - bucket.RemainingFraction)

		quota := &geminiUsageQuota{
			Remaining:   remaining,
			Limit:       limit,
			Utilization: utilization,
			ResetsAt:    bucket.ResetTime,
			ModelID:     bucket.ModelID,
		}

		geminiData.Models[bucket.ModelID] = quota

		// Track the highest utilization quota
		if geminiData.HighestUtilization == nil || quota.Utilization > geminiData.HighestUtilization.Utilization {
			geminiData.HighestUtilization = quota
		}
	}

	if len(geminiData.Models) == 0 {
		return nil, fmt.Errorf("no quota buckets found in response")
	}

	return geminiData, nil
}

// parseQuotaAmount converts a quota amount string to int64
func parseQuotaAmount(s string) int64 {
	s = strings.TrimSpace(s)
	var amount int64
	_, err := fmt.Sscanf(s, "%d", &amount)
	if err != nil {
		return 0
	}
	return amount
}

func (a *App) loadUsageAsync() tea.Cmd {
	if a.usageLoadCancel != nil {
		a.usageLoadCancel()
	}
	loadCtx, cancelLoad := context.WithCancel(context.Background())
	a.usageLoadCancel = cancelLoad
	a.usageLoading = true
	a.usageLoadGeneration++
	generation := a.usageLoadGeneration
	animCmd := a.animationClock.Subscribe()
	queue := a.updateQueue

	safego.Go("chat.loadUsageAsync", func() {
		result := &usageDataResult{FetchedAt: time.Now()}
		var resultMu sync.Mutex
		publishEntry := func(entry providerUsageEntry) {
			resultMu.Lock()
			result.Entries = append(result.Entries, entry)
			resultMu.Unlock()
			queue <- usageEntryFetchedMsg{generation: generation, entry: entry}
			a.wakeRuntimeAsync()
		}
		safego.Go("chat.scanConversationUsage", func() {
			conversationDir, err := metrics.DefaultConversationStorageDir()
			if err != nil {
				queue <- conversationUsageFetchedMsg{generation: generation, err: err}
				a.wakeRuntimeAsync()
				return
			}
			historical, err := metrics.LoadConversationUsage(loadCtx, conversationDir)
			queue <- conversationUsageFetchedMsg{generation: generation, snapshot: historical, err: err}
			a.wakeRuntimeAsync()
		})
		seen := make(map[string]bool)

		// Codex (ChatGPT OAuth) — actively query the read-only usage endpoint so
		// opening this tab does not require a model request first. Publish each
		// account immediately, before slower provider requests finish.
		settings.ReconcileSDKTokensIntoAccounts()
		cachedCodex, _ := codex.LoadRateLimitSnapshots()
		codexSeen := make(map[string]bool)
		var codexWG sync.WaitGroup
		codexSlots := make(chan struct{}, 4)
		for _, acct := range settings.AccountsForProvider("codex") {
			var tok openai.OAuthToken
			if err := json.Unmarshal(acct.TokenData, &tok); err != nil {
				publishEntry(providerUsageEntry{
					Provider: "Codex", Email: acct.Email, AccountID: acct.ID,
					Err: fmt.Errorf("invalid stored token: %w", err),
				})
				continue
			}
			var refreshErr error
			if openai.IsTokenExpired(&tok) && tok.RefreshToken != "" {
				refreshCtx, cancel := context.WithTimeout(loadCtx, 10*time.Second)
				refreshed, err := settings.RefreshAccountTokenByIDContext(refreshCtx, acct.ID)
				cancel()
				if err != nil {
					refreshErr = err
				} else {
					tok.AccessToken = refreshed.AccessToken
					tok.ExpiresAt = refreshed.Expiry
				}
			}

			accountID := tok.AccountID
			if accountID == "" && tok.IDToken != "" {
				accountID = openai.ExtractAccountIDFromIDToken(tok.IDToken)
			}
			entry := providerUsageEntry{Provider: "Codex", Email: acct.Email, AccountID: accountID}
			if accountID == "" || tok.AccessToken == "" {
				entry.Err = fmt.Errorf("stored login is missing token or account id")
				publishEntry(entry)
				continue
			}
			if codexSeen[accountID] {
				continue
			}
			codexSeen[accountID] = true

			account := acct
			token := tok
			id := accountID
			tokenRefreshErr := refreshErr
			codexWG.Add(1)
			safego.Go("chat.fetchCodexUsage", func() {
				defer codexWG.Done()
				select {
				case codexSlots <- struct{}{}:
					defer func() { <-codexSlots }()
				case <-loadCtx.Done():
					return
				}
				entry := providerUsageEntry{Provider: "Codex", Email: account.Email, AccountID: id}
				ctx, cancel := context.WithTimeout(loadCtx, 10*time.Second)
				defer cancel()

				snapshot, fetchErr := codex.FetchRateLimitSnapshot(ctx, token.AccessToken, id)
				if fetchErr == nil {
					entry.CodexData = &codexUsageData{Snapshot: *snapshot, Live: true}
					_ = codex.SaveRateLimitSnapshot(*snapshot)
				} else if cached, ok := cachedCodex[id]; ok {
					if tokenRefreshErr != nil {
						fetchErr = fmt.Errorf("token refresh failed: %v; live usage failed: %w", tokenRefreshErr, fetchErr)
					}
					entry.CodexData = &codexUsageData{Snapshot: cached, RefreshErr: fetchErr}
				} else {
					if tokenRefreshErr != nil {
						fetchErr = fmt.Errorf("token refresh failed: %v; live usage failed: %w", tokenRefreshErr, fetchErr)
					}
					entry.Err = fetchErr
				}
				publishEntry(entry)
			})
		}
		codexWG.Wait()
		// Preserve snapshots for accounts no longer present in the account store;
		// they remain useful as explicitly stale/offline usage.
		emailByAccountID := codexAccountEmails()
		cachedIDs := make([]string, 0, len(cachedCodex))
		for id := range cachedCodex {
			if !codexSeen[id] {
				cachedIDs = append(cachedIDs, id)
			}
		}
		sort.Strings(cachedIDs)
		for _, id := range cachedIDs {
			publishEntry(providerUsageEntry{
				Provider: "Codex", Email: emailByAccountID[id], AccountID: id,
				CodexData: &codexUsageData{Snapshot: cachedCodex[id]},
			})
		}

		// tui_accounts.json (multi-account store)
		for _, providerName := range []string{"claudecode", "anthropic"} {
			for _, acct := range settings.AccountsForProvider(providerName) {
				var tok anthropic.OAuthToken
				if err := json.Unmarshal(acct.TokenData, &tok); err != nil || tok.AccessToken == "" {
					continue
				}

				// Auto-refresh expired tokens before fetching usage
				if anthropic.IsTokenExpired(&tok) && tok.RefreshToken != "" {
					if refreshed, err := settings.RefreshAccountTokenByID(acct.ID); err == nil {
						tok = *refreshed
					}
				}

				if seen[tok.AccessToken] {
					continue
				}
				seen[tok.AccessToken] = true

				email := acct.Email
				if email == "" {
					email = settings.FetchAnthropicEmailFromToken(tok.AccessToken)
					if email != "" {
						settings.SaveAccountEmail(acct.ID, email)
					}
				}

				entry := providerUsageEntry{Provider: "Anthropic", Email: email, AccountID: acct.ID}
				if d, err := fetchAnthropicUsage(tok.AccessToken); err != nil {
					entry.Err = err
				} else {
					entry.AnthropicData = d
				}
				publishEntry(entry)
			}
		}

		// ~/.swarmos/oauth.json (single-account / legacy)
		if tok, err := anthropic.GetStoredOAuthToken(); err == nil && tok != nil && tok.AccessToken != "" {
			if !seen[tok.AccessToken] {
				seen[tok.AccessToken] = true
				email := settings.FetchAnthropicEmailFromToken(tok.AccessToken)
				entry := providerUsageEntry{Provider: "Anthropic", Email: email}
				if d, err := fetchAnthropicUsage(tok.AccessToken); err != nil {
					entry.Err = err
				} else {
					entry.AnthropicData = d
				}
				publishEntry(entry)
			}
		}

		// Gemini accounts from tui_accounts.json
		geminiSeen := make(map[string]bool)
		for _, acct := range settings.AccountsForProvider("gemini") {
			var tok struct {
				AccessToken  string `json:"access_token"`
				RefreshToken string `json:"refresh_token,omitempty"`
			}
			if err := json.Unmarshal(acct.TokenData, &tok); err != nil || tok.AccessToken == "" {
				continue
			}

			if geminiSeen[tok.AccessToken] {
				continue
			}
			geminiSeen[tok.AccessToken] = true

			email := acct.Email
			if email == "" {
				// Try to fetch email from ID token if available
				// For now, just use empty email
			}

			entry := providerUsageEntry{Provider: "Gemini", Email: email, AccountID: acct.ID}

			// Resolve the Code Assist project ID (required by retrieveUserQuota),
			// then fetch quota.
			projectID, perr := fetchGeminiProjectID(tok.AccessToken)
			if perr != nil {
				entry.Err = perr
			} else if d, err := fetchGeminiUsage(tok.AccessToken, projectID); err != nil {
				entry.Err = err
			} else {
				entry.GeminiData = d
			}
			publishEntry(entry)
		}

		// Cursor — usage via DashboardService using the captured CLI token.
		if token, err := getCursorAccessToken(); err == nil && token != "" {
			entry := providerUsageEntry{Provider: "Cursor"}
			if d, ferr := fetchCursorUsage(token); ferr != nil {
				entry.Err = ferr
			} else {
				entry.CursorData = d
			}
			publishEntry(entry)
		}

		// xAI / Grok — prepaid balance via management API (only when configured).
		if d, err := fetchXAIUsage(); err != nil {
			publishEntry(providerUsageEntry{Provider: "xAI / Grok", Err: err})
		} else if d != nil {
			publishEntry(providerUsageEntry{Provider: "xAI / Grok", XAIData: d})
		}

		queue <- usageDataFetchedMsg{generation: generation, result: result}
		a.wakeRuntimeAsync()
	})

	return animCmd
}

func (a *App) applyUsageEntryFetched(msg usageEntryFetchedMsg) {
	if msg.generation != a.usageLoadGeneration {
		return
	}
	if a.usageResult == nil {
		a.usageResult = &usageDataResult{FetchedAt: time.Now()}
	}
	for i := range a.usageResult.Entries {
		current := &a.usageResult.Entries[i]
		sameAccount := msg.entry.AccountID != "" && current.AccountID == msg.entry.AccountID
		sameUnscopedProvider := msg.entry.AccountID == "" && current.AccountID == "" &&
			current.Provider == msg.entry.Provider && current.Email == msg.entry.Email
		if current.Provider == msg.entry.Provider && (sameAccount || sameUnscopedProvider) {
			*current = msg.entry
			a.viewNeedsRefresh = true
			return
		}
	}
	a.usageResult.Entries = append(a.usageResult.Entries, msg.entry)
	a.viewNeedsRefresh = true
}

func (a *App) applyUsageDataFetched(msg usageDataFetchedMsg) {
	if msg.generation != a.usageLoadGeneration {
		return
	}
	// The historical conversation scan is independent and can take a long time
	// for multi-gigabyte archives. It must not keep the entire Usage screen in
	// its loading state after provider subscription data has finished loading.
	a.usageLoading = false
	if msg.err != nil {
		logDebug("[USAGE] Failed to fetch usage data: %v", msg.err)
	}
	if msg.result != nil && a.usageResult != nil {
		msg.result.Conversation = a.usageResult.Conversation
		msg.result.ConversationErr = a.usageResult.ConversationErr
		msg.result.Daily = a.usageResult.Daily
		msg.result.Spikes = a.usageResult.Spikes
		msg.result.Breaks = a.usageResult.Breaks
		msg.result.Backend = a.usageResult.Backend
		msg.result.Degraded = a.usageResult.Degraded
		msg.result.DegradedReason = a.usageResult.DegradedReason
		msg.result.FilesParsed = a.usageResult.FilesParsed
		msg.result.FilesScanned = a.usageResult.FilesScanned
		msg.result.SyncDuration = a.usageResult.SyncDuration
	}
	a.usageResult = msg.result
	a.viewNeedsRefresh = true
}

func (a *App) applyConversationUsageFetched(msg conversationUsageFetchedMsg) {
	if msg.generation != a.usageLoadGeneration {
		return
	}
	if a.usageResult == nil {
		a.usageResult = &usageDataResult{FetchedAt: time.Now()}
	}
	if msg.err != nil {
		a.usageResult.Conversation = nil
		a.usageResult.ConversationErr = msg.err
		logDebug("[USAGE] Failed to scan conversation usage: %v", msg.err)
	} else {
		summary := msg.snapshot.Summary
		a.usageResult.Conversation = &summary
		a.usageResult.ConversationErr = nil
		a.usageResult.Daily = msg.snapshot.Daily
		a.usageResult.Spikes = msg.snapshot.Spikes
		a.usageResult.Breaks = msg.snapshot.Breaks
		a.usageResult.Backend = msg.snapshot.Backend
		a.usageResult.Degraded = msg.snapshot.Degraded
		a.usageResult.DegradedReason = msg.snapshot.DegradedReason
		a.usageResult.FilesParsed = msg.snapshot.FilesParsed
		a.usageResult.FilesScanned = msg.snapshot.FilesScanned
		a.usageResult.SyncDuration = msg.snapshot.SyncDuration
	}
	a.viewNeedsRefresh = true
}

// ─── Rendering ───────────────────────────────────────────────────────────────

func (a *App) renderUsageTab(width, height int) string {
	th := a.theme
	bg := th.BG
	innerW := width - 6
	if innerW < 40 {
		innerW = 40
	}

	// Styles
	st := func(fg string) lipgloss.Style {
		return lipgloss.NewStyle().Foreground(lipgloss.Color(fg)).Background(lipgloss.Color(bg))
	}
	muted := st(th.TextMuted)
	acc := st(th.Primary).Bold(true)
	rule := muted.Render(strings.Repeat("─", innerW))

	var lines []string

	// ── Header with sub-tabs ───────────────────────────────────────────────────
	subTabs := []string{tr("classic.usage.subscription"), tr("classic.usage.consumption"), tr("classic.usage.diagnostics")}
	var tabLine strings.Builder
	for i, tab := range subTabs {
		if i == a.usageSubTab {
			tabLine.WriteString(acc.Render("["+tab+"]") + "  ")
		} else {
			tabLine.WriteString(muted.Render(" "+tab+" ") + "  ")
		}
	}
	hint := muted.Render(tr("classic.usage.hint"))
	pad := innerW - lipgloss.Width(tabLine.String()) - lipgloss.Width(hint)
	if pad < 1 {
		pad = 1
	}
	lines = append(lines, tabLine.String()+strings.Repeat(" ", pad)+hint)
	lines = append(lines, rule)

	// ── Loading state ─────────────────────────────────────────────────────────
	if a.usageLoading {
		lines = append(lines, "")
		lines = append(lines, muted.Render(tr("classic.usage.refreshing")))
		if a.usageResult == nil {
			return renderUsageFrame(lines, width, height, bg)
		}
	}

	// Render content based on sub-tab
	var contentLines []string
	switch a.usageSubTab {
	case UsageSubTabSubscription:
		contentLines = a.renderUsageSubscriptionTab(innerW, &th, st)
	case UsageSubTabConsumption:
		contentLines = a.renderUsageConsumptionTab(innerW, &th, st)
	case UsageSubTabDiagnostics:
		contentLines = a.renderUsageDiagnosticsTab(innerW, &th, st)
	}
	lines = append(lines, contentLines...)

	lines = append(lines, "")
	return renderUsageFrame(lines, width, height, bg)
}

// renderUsageSubscriptionTab renders the subscription view (rate limits, pay-as-you-go)
func (a *App) renderUsageSubscriptionTab(innerW int, th *Theme, st func(fg string) lipgloss.Style) []string {
	bg := th.BG
	bold := st(th.Text).Bold(true)
	muted := st(th.TextMuted)
	warn := st("#e8a838")
	crit := st("#e85538")
	ok := st("#38c47a")

	var lines []string
	rule := muted.Render(strings.Repeat("─", innerW))

	// ── Provider quota section ────────────────────────────────────────────────
	if a.usageResult == nil {
		lines = append(lines, "")
		lines = append(lines, muted.Render("  No usage data available. Press [r] to refresh."))
		return lines
	}

	renderErrors := func() []string {
		var errorLines []string
		for _, entry := range a.usageResult.Entries {
			if entry.Err == nil {
				continue
			}
			label := entry.Provider
			if entry.Email != "" {
				label += " · " + entry.Email
			}
			detail := entry.Err.Error()
			if len(detail) > 120 {
				detail = detail[:117] + "…"
			}
			errorLines = append(errorLines, muted.Render("  "+label+": unavailable · "+detail))
		}
		return errorLines
	}

	// Count successful providers
	successCount := 0
	for _, entry := range a.usageResult.Entries {
		if entry.AnthropicData != nil || entry.GeminiData != nil ||
			entry.CursorData != nil || entry.XAIData != nil || entry.CodexData != nil {
			successCount++
		}
	}

	if successCount == 0 {
		lines = append(lines, "")
		lines = append(lines, muted.Render(tr("classic.usage.no_subscription")))
		lines = append(lines, renderErrors()...)
		return lines
	}

	// Render each successful provider (hide errors silently)
	for _, entry := range a.usageResult.Entries {
		// Skip failed providers silently
		if entry.Err != nil || entry.AnthropicData == nil {
			continue
		}

		d := entry.AnthropicData
		barW := innerW - 34
		if barW < 16 {
			barW = 16
		}
		if barW > 36 {
			barW = 36
		}

		// Provider header
		providerName := "Anthropic"
		if entry.Provider != "" {
			providerName = entry.Provider
		}
		lines = append(lines, "")
		lines = append(lines, bold.Render("  "+providerName))
		lines = append(lines, muted.Render("  "+strings.Repeat("─", innerW-4)))

		// Rate limits
		lines = append(lines, bold.Render(tr("classic.usage.rate_limits")))
		lines = append(lines, renderQuotaLine(bg, th, tr("classic.usage.five_hour"), d.FiveHour, barW, warn, crit, ok))
		lines = append(lines, renderQuotaLine(bg, th, tr("classic.usage.seven_day_all"), d.SevenDay, barW, warn, crit, ok))
		lines = append(lines, renderQuotaLine(bg, th, tr("classic.usage.seven_day_sonnet"), d.SevenDaySonnet, barW, warn, crit, ok))

		// Pay-as-you-go credits
		if ex := d.ExtraUsage; ex != nil {
			lines = append(lines, "")
			lines = append(lines, bold.Render(tr("classic.usage.paygo")))

			usedStr := "—"
			if ex.UsedCredits != nil {
				usedStr = fmt.Sprintf("$%.2f", *ex.UsedCredits)
			}
			limitStr := tr("classic.usage.no_limit")
			if ex.MonthlyLimit != nil {
				limitStr = tr("classic.usage.monthly_limit", *ex.MonthlyLimit)
			}
			utilStr := ""
			if ex.Utilization != nil {
				utilStr = fmt.Sprintf("  (%.1f%%)", *ex.Utilization)
			}
			lines = append(lines, muted.Render(tr("classic.usage.paygo_line", usedStr, limitStr, utilStr)))
		}
	}

	// ── Gemini / Cursor / xAI provider blocks ─────────────────────────────────
	pbarW := innerW - 34
	if pbarW < 16 {
		pbarW = 16
	}
	if pbarW > 36 {
		pbarW = 36
	}
	for _, entry := range a.usageResult.Entries {
		if entry.Err != nil {
			continue
		}
		switch {
		case entry.GeminiData != nil:
			lines = append(lines, "")
			lines = append(lines, bold.Render("  Gemini"))
			lines = append(lines, muted.Render("  "+strings.Repeat("─", innerW-4)))
			lines = append(lines, bold.Render(tr("classic.usage.model_quotas")))
			models := entry.GeminiData.Models
			names := make([]string, 0, len(models))
			for id := range models {
				names = append(names, id)
			}
			sort.Strings(names)
			for _, id := range names {
				q := models[id]
				label := "  " + truncateLabel(id, 14)
				gq := anthropicUsageQuota{Utilization: q.Utilization, ResetsAt: q.ResetsAt}
				lines = append(lines, renderQuotaLine(bg, th, label, gq, pbarW, warn, crit, ok))
			}
		case entry.CursorData != nil:
			d := entry.CursorData
			lines = append(lines, "")
			lines = append(lines, bold.Render("  Cursor"))
			lines = append(lines, muted.Render("  "+strings.Repeat("─", innerW-4)))
			lines = append(lines, bold.Render(tr("classic.usage.plan_usage")))
			cq := anthropicUsageQuota{Utilization: d.PlanPercentUsed, ResetsAt: epochMillisToRFC3339(d.BillingEnd)}
			lines = append(lines, renderQuotaLine(bg, th, tr("classic.usage.plan_usage_label"), cq, pbarW, warn, crit, ok))
			lines = append(lines, muted.Render(tr("classic.usage.used_limit_remaining", d.PlanLimit-d.PlanRemaining, d.PlanLimit, d.PlanRemaining)))
			if d.DisplayMessage != "" {
				lines = append(lines, muted.Render("  "+d.DisplayMessage))
			}
		case entry.XAIData != nil && entry.XAIData.HasData:
			d := entry.XAIData
			lines = append(lines, "")
			lines = append(lines, bold.Render("  xAI / Grok"))
			lines = append(lines, muted.Render("  "+strings.Repeat("─", innerW-4)))
			lines = append(lines, bold.Render(tr("classic.usage.prepaid_credits")))
			limitStr := tr("classic.usage.no_soft_limit")
			if d.SoftLimitUSD > 0 {
				limitStr = tr("classic.usage.soft_limit", d.SoftLimitUSD)
			}
			lines = append(lines, muted.Render(tr("classic.usage.credits_remaining", d.PrepaidCreditsUSD, limitStr)))
		case entry.CodexData != nil:
			d := entry.CodexData
			snap := d.Snapshot
			header := "  Codex (ChatGPT)"
			if entry.Email != "" {
				header += "  ·  " + entry.Email
			}
			lines = append(lines, "")
			lines = append(lines, bold.Render(header))
			lines = append(lines, muted.Render("  "+strings.Repeat("─", innerW-4)))
			planBits := tr("classic.usage.subscription_limits")
			if snap.PlanType != "" {
				planBits += "  (" + strings.ToUpper(snap.PlanType) + " plan)"
			}
			lines = append(lines, bold.Render(planBits))
			renderWindow := func(w *codex.RateLimitWindow) {
				if w == nil {
					return
				}
				label := w.Label()
				if label == "" {
					label = tr("classic.usage.window")
				}
				resets := ""
				if rt := w.ResetTime(); !rt.IsZero() {
					resets = rt.Format(time.RFC3339)
				}
				q := anthropicUsageQuota{Utilization: w.UsedPercent, ResetsAt: resets}
				lines = append(lines, renderQuotaLine(bg, th, "  "+truncateLabel(label, 13), q, pbarW, warn, crit, ok))
			}
			renderWindow(snap.Primary)
			renderWindow(snap.Secondary)
			age := time.Since(snap.CapturedAt).Round(time.Minute)
			ageStr := tr("classic.time.just_now")
			if age >= time.Minute {
				ageStr = tr("classic.time.duration_ago", age.String())
			}
			source := tr("classic.usage.live_updated", ageStr)
			if !d.Live {
				source = tr("classic.usage.cached_captured", ageStr)
			}
			lines = append(lines, muted.Render("  "+source))
			if d.RefreshErr != nil {
				detail := d.RefreshErr.Error()
				if len(detail) > 100 {
					detail = detail[:97] + "…"
				}
				lines = append(lines, muted.Render(tr("classic.usage.live_unavailable", detail)))
			}
		}
	}
	if errorLines := renderErrors(); len(errorLines) > 0 {
		lines = append(lines, "")
		lines = append(lines, bold.Render(tr("classic.usage.unavailable")))
		lines = append(lines, errorLines...)
	}

	// If xAI was attempted but billing isn't configured, show a one-line hint.
	for _, entry := range a.usageResult.Entries {
		if entry.Provider == "xAI / Grok" && entry.XAIData == nil && entry.Err == nil {
			lines = append(lines, "")
			lines = append(lines, muted.Render("  xAI / Grok: set XAI_MANAGEMENT_API_KEY + XAI_TEAM_ID to show prepaid credits."))
		}
	}

	// ── Session stats (compact) ───────────────────────────────────────────────
	if a.metrics != nil {
		modelStats := a.metrics.GetAllModelMetrics()
		if len(modelStats) > 0 {
			lines = append(lines, "")
			lines = append(lines, rule)
			lines = append(lines, bold.Render(tr("classic.usage.session_stats")))
			lines = append(lines, "")

			// Compact table
			colHdr := fmt.Sprintf("  %-24s  %8s  %7s", tr("classic.usage.model"), tr("classic.usage.tokens"), tr("classic.usage.sessions_short"))
			lines = append(lines, st(th.TextMuted).Render(colHdr))

			// Sort models by name for consistent ordering (prevents flickering)
			type modelEntry struct {
				id   string
				name string
				stat metrics.ModelMetrics
			}
			var sortedModels []modelEntry
			for id, m := range modelStats {
				name := m.ModelName
				if name == "" {
					name = id
				}
				sortedModels = append(sortedModels, modelEntry{id: id, name: name, stat: m})
			}
			sort.Slice(sortedModels, func(i, j int) bool {
				return sortedModels[i].name < sortedModels[j].name
			})

			var totalTokens int64
			var totalSessions int
			for _, entry := range sortedModels {
				name := entry.name
				if len(name) > 24 {
					name = name[:24]
				}
				line := fmt.Sprintf("  %-24s  %8s  %7d",
					name,
					formatUsageTokenCount(entry.stat.TotalTokensGenerated),
					entry.stat.SessionCount)
				lines = append(lines, muted.Render(line))
				totalTokens += entry.stat.TotalTokensGenerated
				totalSessions += entry.stat.SessionCount
			}

			// Total line
			totLine := fmt.Sprintf("  %-24s  %8s  %7d", tr("classic.usage.total"),
				formatUsageTokenCount(totalTokens), totalSessions)
			lines = append(lines, bold.Render(totLine))
		}
	}

	return lines
}

// renderUsageConsumptionTab renders the consumption view: what was actually
// billed, split into the dimensions providers charge differently for. The
// conversation store is the historical source of truth — every persisted
// assistant response carries provider-reported usage, including repeated
// full-context inputs across tool-loop turns.
func (a *App) renderUsageConsumptionTab(innerW int, th *Theme, st func(fg string) lipgloss.Style) []string {
	bold := st(th.Text).Bold(true)
	muted := st(th.TextMuted)
	dim := st(th.TextMuted)

	var lines []string

	if a.usageResult != nil && a.usageResult.Conversation != nil {
		history := a.usageResult.Conversation
		lines = append(lines, "")
		lines = append(lines, bold.Render(tr("classic.usage.recorded_consumption")))
		lines = append(lines, "")
		lines = append(lines, bold.Render(fmt.Sprintf("  %-24s  %12s", tr("classic.usage.total_processed"), formatUsageTokenCount(history.TotalTokens))))
		lines = append(lines, muted.Render(fmt.Sprintf("  %-24s  %12s", tr("classic.usage.fresh_input"), formatUsageTokenCount(history.FreshInput))))
		lines = append(lines, muted.Render(fmt.Sprintf("  %-24s  %12s", tr("classic.usage.cache_write"), formatUsageTokenCount(history.CacheWrite))))
		lines = append(lines, muted.Render(fmt.Sprintf("  %-24s  %12s", tr("classic.usage.cache_read"), formatUsageTokenCount(history.CacheRead))))
		lines = append(lines, muted.Render(fmt.Sprintf("  %-24s  %12s", tr("classic.usage.model_output"), formatUsageTokenCount(history.OutputTokens))))
		if history.OtherTokens > 0 {
			lines = append(lines, dim.Render(fmt.Sprintf("  %-24s  %12s", tr("classic.usage.other_tokens"), formatUsageTokenCount(history.OtherTokens))))
		}
		lines = append(lines, "")
		// The hit ratio covers only cache-capable providers, so show how much of
		// the traffic it actually speaks for. A low share means the headline
		// number describes a minority of the work rather than a regression.
		lines = append(lines, muted.Render(fmt.Sprintf("  %-24s  %12.1f%%", tr("classic.usage.cache_hit"), history.CacheHitRatio()*100)))
		lines = append(lines, muted.Render(fmt.Sprintf("  %-24s  %12.1f%%", tr("classic.usage.cache_capable"), history.CacheCapableShare()*100)))
		lines = append(lines, muted.Render(fmt.Sprintf("  %-24s  %12d", tr("classic.usage.provider_responses"), history.ResponseCount)))
		lines = append(lines, muted.Render(fmt.Sprintf("  %-24s  %12d", tr("classic.usage.conversations"), history.ConversationCount)))
		lines = append(lines, "")
		lines = append(lines, dim.Render(tr("classic.usage.deduplicated")))
		lines = append(lines, dim.Render(tr("classic.usage.repeated_context")))
		if history.DuplicateFiles > 0 || history.DuplicateResponses > 0 || history.UnreadableFiles > 0 {
			lines = append(lines, dim.Render(tr("classic.usage.scan_stats", history.DuplicateFiles, history.DuplicateResponses, history.UnreadableFiles)))
		}
		if a.usageResult.Degraded {
			lines = append(lines, dim.Render(tr("classic.usage.index_unavailable", a.usageResult.DegradedReason)))
		} else if a.usageResult.Backend != "" {
			refreshNote := tr("classic.usage.index_stats", a.usageResult.Backend, a.usageResult.FilesParsed,
				a.usageResult.FilesScanned, a.usageResult.SyncDuration.Round(time.Millisecond))
			lines = append(lines, dim.Render(refreshNote))
		}

		if trend := renderUsageDailyTrend(a.usageResult.Daily, innerW, bold, muted, dim); len(trend) > 0 {
			lines = append(lines, trend...)
		}
	} else if a.usageResult != nil && a.usageResult.ConversationErr != nil {
		lines = append(lines, "")
		lines = append(lines, muted.Render(tr("classic.usage.history_unavailable", a.usageResult.ConversationErr.Error())))
	} else if a.usageResult != nil {
		lines = append(lines, "")
		lines = append(lines, muted.Render(tr("classic.usage.scanning")))
	} else {
		lines = append(lines, "")
		lines = append(lines, muted.Render(tr("classic.usage.no_data_yet")))
	}

	return lines
}

// usageSparkChars renders relative magnitudes as a compact braille-free bar
// sequence readable in any terminal font.
var usageSparkChars = []rune("▁▂▃▄▅▆▇█")

// renderUsageDailyTrend renders the last N days of deduplicated input context
// as a sparkline plus a short table, so a spike is visible before diving into
// the Diagnostics tab.
func renderUsageDailyTrend(daily []usageindex.DayBucket, innerW int, bold, muted, dim lipgloss.Style) []string {
	if len(daily) == 0 {
		return nil
	}
	var lines []string
	lines = append(lines, "")
	lines = append(lines, bold.Render(tr("classic.usage.last_days", len(daily))))
	lines = append(lines, "")

	var peak int64
	for _, day := range daily {
		if input := day.InputContext(); input > peak {
			peak = input
		}
	}
	var spark strings.Builder
	for _, day := range daily {
		if peak <= 0 {
			spark.WriteRune(usageSparkChars[0])
			continue
		}
		level := int(float64(day.InputContext()) / float64(peak) * float64(len(usageSparkChars)-1))
		if level < 0 {
			level = 0
		}
		if level >= len(usageSparkChars) {
			level = len(usageSparkChars) - 1
		}
		spark.WriteRune(usageSparkChars[level])
	}
	lines = append(lines, muted.Render("  "+spark.String()))

	last := daily[len(daily)-1]
	lines = append(lines, dim.Render(tr("classic.usage.daily_line", last.Day.Format("2006-01-02"),
		formatUsageTokenCount(last.InputContext()), formatUsageTokenCount(last.Output), last.Responses)))
	return lines
}

// renderUsageDiagnosticsTab surfaces the anomalies that make the totals look
// the way they do: turns whose input context spiked well above the
// conversation's own baseline, and prompt-cache breaks where a prefix that
// should have been read from cache was paid for again.
func (a *App) renderUsageDiagnosticsTab(innerW int, th *Theme, st func(fg string) lipgloss.Style) []string {
	bold := st(th.Text).Bold(true)
	muted := st(th.TextMuted)
	dim := st(th.TextMuted)
	warn := st("#e8a838")

	var lines []string
	if a.usageResult == nil {
		lines = append(lines, "")
		lines = append(lines, muted.Render(tr("classic.usage.no_data")))
		return lines
	}

	lines = append(lines, "")
	lines = append(lines, bold.Render(tr("classic.usage.top_spikes")))
	lines = append(lines, dim.Render(tr("classic.usage.spikes_help")))
	lines = append(lines, "")
	if len(a.usageResult.Spikes) == 0 {
		lines = append(lines, muted.Render("  None detected."))
	} else {
		for _, spike := range a.usageResult.Spikes {
			row := fmt.Sprintf(tr("classic.usage.spike_row"),
				formatUsageFindingTime(spike.Timestamp), truncateUsageID(spike.ConversationID, 24),
				formatUsageTokenCount(spike.Tokens), spike.Ratio)
			lines = append(lines, warn.Render(row))
		}
	}

	lines = append(lines, "")
	lines = append(lines, bold.Render(tr("classic.usage.cache_breaks")))
	lines = append(lines, dim.Render(tr("classic.usage.breaks_help")))
	lines = append(lines, "")
	if len(a.usageResult.Breaks) == 0 {
		lines = append(lines, muted.Render("  None detected."))
	} else {
		var repaid int64
		for _, brk := range a.usageResult.Breaks {
			repaid += brk.Tokens
			cause := tr("classic.common.unknown")
			causeStyle := muted
			switch brk.Cause {
			case usageindex.CauseTTLExpiry:
				cause = tr("classic.usage.ttl_expiry")
			case usageindex.CausePrefixMutation:
				cause = tr("classic.usage.prefix_mutation")
				causeStyle = warn
			}
			row := fmt.Sprintf(tr("classic.usage.break_row"),
				formatUsageFindingTime(brk.Timestamp), truncateUsageID(brk.ConversationID, 24),
				formatUsageTokenCount(brk.Tokens), usageFormatGap(brk.GapSeconds), cause)
			lines = append(lines, causeStyle.Render(row))
		}
		lines = append(lines, "")
		lines = append(lines, dim.Render(tr("classic.usage.repaid_total", len(a.usageResult.Breaks), formatUsageTokenCount(repaid))))
	}

	lines = append(lines, "")
	lines = append(lines, dim.Render(tr("classic.usage.prefix_help_1")))
	lines = append(lines, dim.Render(tr("classic.usage.prefix_help_2")))
	return lines
}

func formatUsageFindingTime(ts time.Time) string {
	if ts.IsZero() {
		return "-"
	}
	return ts.Format("2006-01-02 15:04")
}

func truncateUsageID(id string, max int) string {
	if len(id) <= max {
		return id
	}
	if max < 3 {
		return id[:max]
	}
	return id[:max-3] + "..."
}

func usageFormatGap(seconds int64) string {
	switch {
	case seconds <= 0:
		return "-"
	case seconds < 60:
		return fmt.Sprintf("%ds", seconds)
	case seconds < 3600:
		return fmt.Sprintf("%dm", seconds/60)
	default:
		return fmt.Sprintf("%dh%dm", seconds/3600, (seconds%3600)/60)
	}
}

// renderQuotaLine renders a labeled progress bar with % and reset time.
func renderQuotaLine(bg string, th *Theme, label string, q anthropicUsageQuota, barW int, warn, crit, ok lipgloss.Style) string {
	muted := lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).Background(lipgloss.Color(bg))

	pct := q.Utilization
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}

	var barStyle lipgloss.Style
	switch {
	case pct >= 80:
		barStyle = crit
	case pct >= 50:
		barStyle = warn
	default:
		barStyle = ok
	}

	filled := int(float64(barW) * pct / 100)
	empty := barW - filled
	bar := barStyle.Render(strings.Repeat("█", filled)) +
		muted.Render(strings.Repeat("░", empty))

	pctStr := barStyle.Render(fmt.Sprintf("%5.1f%%", pct))

	resetStr := ""
	if q.ResetsAt != "" {
		if t, err := time.Parse(time.RFC3339, q.ResetsAt); err == nil {
			dur := time.Until(t).Round(time.Minute)
			if dur > 0 {
				resetStr = "  " + usageFormatReset(dur)
			}
		}
	}

	return muted.Render(label+"  ") + bar + "  " + pctStr + muted.Render(resetStr)
}

// usageFormatReset formats a duration as "Xd Xh" or "Xh Xm" (compact).
func usageFormatReset(d time.Duration) string {
	d = d.Round(time.Minute)
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	mins := int(d.Minutes()) % 60
	switch {
	case days > 0:
		return fmt.Sprintf("%dd %dh", days, hours)
	case hours > 0:
		return fmt.Sprintf("%dh %dm", hours, mins)
	default:
		return fmt.Sprintf("%dm", mins)
	}
}

// formatUsageTokenCount formats large token counts without collapsing billions
// into an unreadable number of millions.
func formatUsageTokenCount(n int64) string {
	switch {
	case n >= 1_000_000_000_000:
		return fmt.Sprintf("%.2fT", float64(n)/1_000_000_000_000)
	case n >= 1_000_000_000:
		return fmt.Sprintf("%.2fB", float64(n)/1_000_000_000)
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}

// usageTabLines renders the usage view as a slice of lines (without the outer
// frame), so it can be embedded inside the debug screen's scrolling container.
// It mirrors renderUsageTab but returns composable lines instead of a framed string.
func (a *App) usageTabLines(width, height int) []string {
	th := a.theme
	bg := th.BG
	innerW := width - 6
	if innerW < 40 {
		innerW = 40
	}

	st := func(fg string) lipgloss.Style {
		return lipgloss.NewStyle().Foreground(lipgloss.Color(fg)).Background(lipgloss.Color(bg))
	}
	muted := st(th.TextMuted)
	acc := st(th.Primary).Bold(true)
	rule := muted.Render(strings.Repeat("─", innerW))

	var lines []string

	// ── Header with sub-tabs ───────────────────────────────────────────────────
	subTabs := []string{tr("classic.usage.subscription"), tr("classic.usage.consumption"), tr("classic.usage.diagnostics")}
	var tabLine strings.Builder
	for i, tab := range subTabs {
		if i == a.usageSubTab {
			tabLine.WriteString(acc.Render("["+tab+"]") + "  ")
		} else {
			tabLine.WriteString(muted.Render(" "+tab+" ") + "  ")
		}
	}
	lines = append(lines, tabLine.String())
	lines = append(lines, rule)

	// ── Loading state ─────────────────────────────────────────────────────────
	if a.usageLoading {
		lines = append(lines, "")
		lines = append(lines, muted.Render(tr("classic.usage.refreshing")))
		if a.usageResult == nil {
			return lines
		}
	}

	// Render content based on sub-tab
	switch a.usageSubTab {
	case UsageSubTabSubscription:
		lines = append(lines, a.renderUsageSubscriptionTab(innerW, &th, st)...)
	case UsageSubTabConsumption:
		lines = append(lines, a.renderUsageConsumptionTab(innerW, &th, st)...)
	case UsageSubTabDiagnostics:
		lines = append(lines, a.renderUsageDiagnosticsTab(innerW, &th, st)...)
	}

	return lines
}

func renderUsageFrame(lines []string, width, height int, bg string) string {
	return lipgloss.NewStyle().
		Width(width).Height(height).
		Background(lipgloss.Color(bg)).
		Padding(1, 3).
		Render(strings.Join(lines, "\n"))
}
