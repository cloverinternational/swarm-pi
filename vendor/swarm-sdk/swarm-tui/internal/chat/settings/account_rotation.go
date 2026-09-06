package settings

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider/openai"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

// Account stacking & rotation.
//
// Users may add multiple OAuth accounts per wire family (the auth screen's
// "+ Add account"). tui_accounts.json stores them all; exactly one per family
// is active, mirrored into the SDK token store that providers read at build
// time. RotateToNextAccount advances the active account — the chat layer
// calls it when the active account hits its subscription rate limit, so a
// stacked family keeps working while the limited account's window resets.

// RotateToNextAccount switches the wire family of the given provider row
// (e.g. "codex" → OpenAI-family accounts) to the next stacked account in
// stable order, wrapping. It returns the previously-active and newly-active
// accounts and the family's account count. Fails when fewer than two
// accounts are stacked.
func RotateToNextAccount(provider string) (from, to *authAccount, total int, err error) {
	accts := accountsForProvider(provider)
	// Stable order: AddedAt, then ID (accountsForProvider preserves file
	// order, but AddedAt survives file rewrites that reorder entries).
	sort.SliceStable(accts, func(i, j int) bool {
		if accts[i].AddedAt != accts[j].AddedAt {
			return accts[i].AddedAt < accts[j].AddedAt
		}
		return accts[i].ID < accts[j].ID
	})
	total = len(accts)
	if total < 2 {
		return nil, nil, total, fmt.Errorf("%s", i18n.T("settings.account_rotation.error.insufficient_accounts", provider, total))
	}

	activeIdx := 0
	for i, a := range accts {
		if a.IsActive {
			activeIdx = i
			break
		}
	}
	fromAcct := accts[activeIdx]
	toAcct := accts[(activeIdx+1)%total]

	// setActiveAccount matches on the account's own stored label (accounts
	// created by the OAuth flows use historical labels like "OpenAI"), and
	// pushes the token into the SDK store providers read at build time.
	if err := setActiveAccount(toAcct.Provider, toAcct.ID); err != nil {
		return nil, nil, total, fmt.Errorf("%s: %w", i18n.T("settings.account_rotation.error.activate_account", toAcct.ID), err)
	}
	return &fromAcct, &toAcct, total, nil
}

// AccountDisplayLabel names an account for user-facing messages.
func AccountDisplayLabel(a *authAccount) string {
	if a == nil {
		return i18n.T("settings.account_rotation.unknown_account")
	}
	if a.Email != "" {
		return a.Email
	}
	return a.ID
}

// ReconcileSDKTokensIntoAccounts imports SDK-stored OAuth tokens that have no
// corresponding account into tui_accounts.json, so single-login users start
// with account #1 already stacked (login paths outside the auth screen — the
// codex CLI, /auth in older builds — store the token without registering an
// account). Returns the number of accounts imported. Currently covers the
// OpenAI/codex family; other families' logins already register accounts.
func ReconcileSDKTokensIntoAccounts() int {
	imported := 0
	if importOpenAITokenAccount() {
		imported++
	}
	return imported
}

func importOpenAITokenAccount() bool {
	if len(accountsForProvider("codex")) > 0 {
		return false
	}
	tok, err := openai.GetStoredOAuthToken()
	if err != nil || tok == nil || (tok.AccessToken == "" && tok.APIKey == "") {
		return false
	}
	td, err := json.Marshal(tok)
	if err != nil {
		return false
	}
	email := ""
	if tok.IDToken != "" {
		email = extractEmailFromJWT(tok.IDToken)
	}
	if email == "" && tok.AccessToken != "" {
		email = extractEmailFromJWT(tok.AccessToken)
	}
	acct := authAccount{
		ID:        genAccountID("openai"),
		Provider:  "OpenAI",
		Email:     strings.TrimSpace(email),
		IsActive:  true,
		AddedAt:   time.Now().Unix(),
		TokenData: td,
	}
	return upsertAccount(acct) == nil
}
