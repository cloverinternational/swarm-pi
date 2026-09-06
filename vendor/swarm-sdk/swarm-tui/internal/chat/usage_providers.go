package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Per-provider usage fetchers for the Usage Statistics screen.
//
//   - Gemini: reuses fetchGeminiUsage but first resolves the Code Assist
//     projectID (loadCodeAssist), which fetchGeminiUsage requires.
//   - Cursor: aiserver.v1.DashboardService/GetCurrentPeriodUsage + GetHardLimit
//     (Connect-RPC JSON, same auth as model loading).
//   - xAI:   prepaid credit balance via management-api.x.ai. This needs a
//     MANAGEMENT key + team id (the SuperGrok OAuth token cannot read billing),
//     so it is gated on XAI_MANAGEMENT_API_KEY + XAI_TEAM_ID.

// ─── Gemini projectID resolution ─────────────────────────────────────────────

// fetchGeminiProjectID calls loadCodeAssist to obtain the Code Assist project
// ID required by the retrieveUserQuota endpoint. Falls back to env project IDs.
func fetchGeminiProjectID(accessToken string) (string, error) {
	for _, env := range []string{"GOOGLE_CLOUD_PROJECT", "GOOGLE_CLOUD_PROJECT_ID"} {
		if v := strings.TrimSpace(os.Getenv(env)); v != "" {
			return v, nil
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	body := []byte(`{"metadata":{"ideType":"GEMINI_CLI","ideName":"IDE_UNSPECIFIED","pluginType":"GEMINI"}}`)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://cloudcode-pa.googleapis.com/v1internal:loadCodeAssist", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("loadCodeAssist HTTP %d", resp.StatusCode)
	}
	var out struct {
		CloudAICompanionProject string `json:"cloudaicompanionProject"`
	}
	if err := json.Unmarshal(respBody, &out); err != nil {
		return "", err
	}
	if out.CloudAICompanionProject == "" {
		return "", fmt.Errorf("no project ID available for Gemini quota")
	}
	return out.CloudAICompanionProject, nil
}

// ─── Cursor usage (DashboardService) ─────────────────────────────────────────

// cursorDashboardCall POSTs an empty body to a DashboardService method and
// decodes the JSON response into out.
func cursorDashboardCall(token, method string, out any) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	url := cursorAPIBaseURL() + "/aiserver.v1.DashboardService/" + method
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader([]byte("{}")))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Connect-Protocol-Version", "1")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-cursor-client-version", cursorClientVersion)
	req.Header.Set("x-cursor-client-type", cursorClientType)
	req.Header.Set("x-request-id", uuid.NewString())
	req.Header.Set("x-ghost-mode", "false")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("cursor not logged in (401)")
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("cursor %s HTTP %d: %s", method, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return json.Unmarshal(body, out)
}

// fetchCursorUsage queries Cursor's current-period plan usage and hard limit.
func fetchCursorUsage(token string) (*cursorUsageData, error) {
	var period struct {
		BillingCycleStart string `json:"billingCycleStart"`
		BillingCycleEnd   string `json:"billingCycleEnd"`
		DisplayMessage    string `json:"displayMessage"`
		PlanUsage         struct {
			Limit            float64 `json:"limit"`
			Remaining        float64 `json:"remaining"`
			TotalPercentUsed float64 `json:"totalPercentUsed"`
		} `json:"planUsage"`
	}
	if err := cursorDashboardCall(token, "GetCurrentPeriodUsage", &period); err != nil {
		return nil, err
	}

	data := &cursorUsageData{
		PlanLimit:       period.PlanUsage.Limit,
		PlanRemaining:   period.PlanUsage.Remaining,
		PlanPercentUsed: period.PlanUsage.TotalPercentUsed,
		BillingStart:    period.BillingCycleStart,
		BillingEnd:      period.BillingCycleEnd,
		DisplayMessage:  period.DisplayMessage,
	}

	// Hard limit is best-effort; ignore failures.
	var hard struct {
		HardLimit float64 `json:"hardLimit"`
	}
	if err := cursorDashboardCall(token, "GetHardLimit", &hard); err == nil {
		data.HardLimitCents = hard.HardLimit
	}
	return data, nil
}

// ─── xAI usage (management API) ──────────────────────────────────────────────

// fetchXAIUsage reads prepaid credit balance + soft spending limit from
// management-api.x.ai. Requires XAI_MANAGEMENT_API_KEY and XAI_TEAM_ID — the
// stored SuperGrok OAuth token cannot access billing. Returns (nil, nil) when
// not configured so the caller can show an informative note instead of an error.
func fetchXAIUsage() (*xaiUsageData, error) {
	key := strings.TrimSpace(os.Getenv("XAI_MANAGEMENT_API_KEY"))
	team := strings.TrimSpace(os.Getenv("XAI_TEAM_ID"))
	if key == "" || team == "" {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	base := "https://management-api.x.ai"
	data := &xaiUsageData{}

	// Prepaid credit balance.
	prepaidURL := fmt.Sprintf("%s/v1/billing/teams/%s/prepaid", base, team)
	if req, err := http.NewRequestWithContext(ctx, http.MethodGet, prepaidURL, nil); err == nil {
		req.Header.Set("Authorization", "Bearer "+key)
		if resp, err := http.DefaultClient.Do(req); err == nil {
			defer resp.Body.Close()
			body, _ := io.ReadAll(resp.Body)
			if resp.StatusCode == http.StatusOK {
				var out struct {
					Credits struct {
						Val string `json:"val"`
					} `json:"credits"`
				}
				if json.Unmarshal(body, &out) == nil {
					data.PrepaidCreditsUSD = parseUSDCents(out.Credits.Val)
					data.HasData = true
				}
			} else {
				return nil, fmt.Errorf("xAI billing HTTP %d", resp.StatusCode)
			}
		}
	}
	if !data.HasData {
		return nil, fmt.Errorf("xAI billing returned no data")
	}
	return data, nil
}

// parseUSDCents converts an xAI "USD cents" string value into whole USD.
func parseUSDCents(s string) float64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	var cents float64
	if _, err := fmt.Sscanf(s, "%f", &cents); err != nil {
		return 0
	}
	return cents / 100.0
}

// truncateLabel trims a label to n runes, padding to a fixed width for aligned
// quota rows.
func truncateLabel(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n])
	}
	return s + strings.Repeat(" ", n-len(r))
}

// epochMillisToRFC3339 converts an epoch-millis string (Cursor's billing cycle
// timestamps) into an RFC3339 string so renderQuotaLine can compute a reset.
// Returns "" for empty/unparseable input or values already in RFC3339.
func epochMillisToRFC3339(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	// Already a date string — pass through.
	if strings.Contains(s, "T") || strings.Contains(s, "-") {
		return s
	}
	var ms int64
	if _, err := fmt.Sscanf(s, "%d", &ms); err != nil || ms <= 0 {
		return ""
	}
	return time.UnixMilli(ms).UTC().Format(time.RFC3339)
}
