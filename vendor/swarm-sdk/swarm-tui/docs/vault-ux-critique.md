# Vault UX Critique and Improvement Recommendations

## Executive Summary

The current vault implementation has significant UX issues that hinder adoption and daily use. This document provides a thorough critique of the current user experience and offers detailed improvement recommendations.

---

## Part 1: Current UX Analysis

### 1.1 Command Line Interface (CLI)

#### Issues Identified

**PROBLEM 1: Discovery is Non-Existent**
- Running `swarmos --help` only shows `init` as a subcommand, but doesn't explain what it does
- The `vault` subcommand is listed but with minimal documentation
- No examples provided in help text
- No `--help` flag on individual vault subcommands

**Current Output:**
```
Subcommands:
  init                     Initialize vault with passphrase
  vault init               Initialize vault with passphrase
  vault unlock             Unlock vault for current session
  ...
```

**User Impact:** Users cannot discover features without reading source code or external documentation. This violates the principle of self-documenting interfaces.

---

**PROBLEM 2: Inconsistent Command Naming**
- `swarmos init` is a shortcut for `swarmos vault init` - confusing!
- What if we later add project initialization? Name collision imminent.
- Commands like `vault add` require secret input via stdin, which is awkward

**User Impact:** Confusion about whether `init` initializes the project or the vault.

---

**PROBLEM 3: Password Handling is Awkward**
- `--password` flag exposes secrets in shell history
- No keyring integration for secure password storage
- Session management uses plain file storage (~/.swarm/vault/session)
- No warning when using `--password` flag about shell history

**User Impact:** Secrets leak via shell history and process listings. This is a SECURITY ISSUE.

---

**PROBLEM 4: No Feedback During Operations**
- `vault init` with wrong password fails silently with no helpful message
- `vault unlock` shows no progress indicator
- Long operations have no timeout or cancel mechanism

**User Impact:** Users don't know if operations are in progress or frozen.

---

**PROBLEM 5: Error Messages Are Cryptic**
```
Failed to unlock vault: failed to decrypt: age: no identity matched any of the file's recipients
```

**User Impact:** Non-technical users cannot understand or recover from errors.

---

### 1.2 TUI Integration

#### Issues Identified

**PROBLEM 6: Vault Panel is Hidden in Settings**
- Users must navigate: Settings → Vault to access credentials
- No keyboard shortcut to quickly unlock vault
- No indication in main UI that vault is locked/unlocked

**User Impact:** Vault feels like an afterthought, not a core feature.

---

**PROBLEM 7: No Visual Status Indicator**
- The vault state (locked/unlocked) is not visible in the main chat interface
- Users don't know if their credentials are available
- No indication when vault auto-locks due to timeout

**User Impact:** Users forget to unlock vault, then wonder why their API keys don't work.

---

**PROBLEM 8: Adding Credentials Requires CLI**
- The TUI shows credentials but cannot add them
- User must exit TUI, run CLI command, then re-enter TUI
- This breaks workflow continuity

**User Impact:** Friction in credential management reduces adoption.

---

**PROBLEM 9: No Credential Search/Filter**
- Credential list shows all credentials without filtering
- No search by name, kind, or tag
- No sorting options

**User Impact:** With many credentials, finding the right one is tedious.

---

**PROBLEM 10: Scope Switching is Confusing**
- Global vs Project scope switch requires Tab key
- No explanation of what each scope means
- No indication of which credentials are available in each scope

**User Impact:** Users accidentally store credentials in wrong scope.

---

### 1.3 Architecture Issues

**PROBLEM 11: Separate Binary Still Exists**
- `swarm-vault` binary in swarm-sdk/cmd still exists
- Creates confusion about which binary to use
- Maintenance burden of two codebases

**User Impact:** Documentation and examples may reference wrong binary.

---

**PROBLEM 12: No Keyring Integration**
- Session passphrase stored in plain file
- No OS keyring integration (gnome-keyring, macOS Keychain, Windows Credential Manager)
- Auto-unlock not possible securely

**User Impact:** Users must re-enter password every session, reducing convenience.

---

## Part 2: Improvement Recommendations

### 2.1 CLI Improvements

#### Recommendation 1: Better Help and Discovery

```go
// Add detailed help for each subcommand
func printVaultUsage() {
    fmt.Println(`swarmos vault - Secure credential storage and execution

DESCRIPTION:
    The vault securely stores API keys, tokens, and secrets that can be
    used by Swarm agents and external tools. Credentials are encrypted
    at rest using age encryption.

USAGE:
    swarmos vault <command> [arguments...]

COMMANDS:
    init        Create a new vault
        --global          Create in ~/.swarm/vault/ (default)
        --project         Create in ./.swarm/vault/
        --password PASS   Set passphrase (WARNING: visible in shell history)
        
    unlock      Unlock vault for current session
        --password PASS   Unlock with passphrase
        
    lock        Lock vault and clear session
    
    status      Show vault status and credential count
    
    add         Add a new credential
        --kind KIND       Type: api_key, bearer_token, ssh_key, aws_access_key
        --host PATTERN    Restrict to specific hosts (e.g., github.com)
        --command PATTERN Restrict to specific commands
        --tag TAG         Add tag for organization
        --expire DURATION Set expiration (e.g., 24h, 7d)
        
    list        List credentials (secrets not shown)
    
    show ID     Show credential details (secret redacted)
    
    exec ID -- CMD   Execute command with credential injected
    
    remove ID   Remove a credential

EXAMPLES:
    # Initialize vault (interactive)
    swarmos vault init
    
    # Add GitHub token
    swarmos vault add github-token --kind bearer_token --host github.com --tag ci
    
    # List credentials
    swarmos vault list
    
    # Use AWS credentials
    swarmos vault exec aws-prod -- aws s3 ls
    
ENVIRONMENT VARIABLES:
    SWARM_VAULT_PASSWORD    Vault passphrase (for automation)

FILES:
    ~/.swarm/vault/global.vault    Global credential vault
    ./.swarm/vault/project.vault   Project-specific vault
    ~/.swarm/vault/session         Session cache (auto-generated)

SEE ALSO:
    swarmos(1), age(1)
`)
}
```

---

#### Recommendation 2: Keyring Integration

Add OS keyring support using `github.com/zalando/go-keyring`:

```go
// saveSession stores passphrase in OS keyring
func saveSession(global bool, passphrase string) error {
    keyring.Set("swarmos", "vault_passphrase", passphrase)
    // Also store in file as fallback
    sessionPath := getSessionPath(global)
    // ...
}

// loadSessionPassphrase tries keyring first, then file
func loadSessionPassphrase(global bool) string {
    // Try keyring first
    if pass, err := keyring.Get("swarmos", "vault_passphrase"); err == nil {
        return pass
    }
    // Fall back to file
    // ...
}
```

**Benefits:**
- Single unlock per user session
- Better security than plain file
- Matches user expectations from other tools

---

#### Recommendation 3: Environment Variable Support

```go
// Allow passphrase from environment variable
func getPassphrase(confirm bool, passwordArg string) string {
    // Priority: CLI arg > env var > interactive
    if passwordArg != "" {
        return passwordArg
    }
    if envPass := os.Getenv("SWARM_VAULT_PASSWORD"); envPass != "" {
        return envPass
    }
    // Interactive prompt...
}
```

**Benefits:**
- Safer than --password flag for automation
- CI/CD friendly
- Follows 12-factor app principles

---

#### Recommendation 4: Better Error Messages

```go
func unlockVault(args []string) {
    // ...
    if err != nil {
        if strings.Contains(err.Error(), "no identity matched") {
            fmt.Fprintln(os.Stderr, "Wrong passphrase. Please try again.")
            fmt.Fprintln(os.Stderr, "If you've forgotten your passphrase, you'll need to reinitialize the vault.")
            fmt.Fprintln(os.Stderr, "WARNING: Reinitializing will delete all stored credentials!")
        } else if os.IsNotExist(err) {
            fmt.Fprintln(os.Stderr, "Vault not found. Run 'swarmos vault init' to create one.")
        } else {
            fmt.Fprintf(os.Stderr, "Failed to unlock vault: %v\n", err)
        }
        os.Exit(1)
    }
}
```

---

#### Recommendation 5: Security Warning for --password Flag

```go
if passwordArg != "" {
    fmt.Fprintln(os.Stderr, "⚠ WARNING: Passphrase provided via --password flag")
    fmt.Fprintln(os.Stderr, "  This will appear in shell history and process listings.")
    fmt.Fprintln(os.Stderr, "  Consider using SWARM_VAULT_PASSWORD environment variable instead.")
    fmt.Fprintln(os.Stderr)
}
```

---

### 2.2 TUI Improvements

#### Recommendation 6: Status Bar Indicator

Add vault status to the TUI status bar:

```
┌─────────────────────────────────────────────────────────┐
│ SwarmOS Chat                                    🔒 Vault │
├─────────────────────────────────────────────────────────┤
│ ...                                                     │
└─────────────────────────────────────────────────────────┘
```

When unlocked:
```
│ SwarmOS Chat                                    🔓 3 Creds│
```

---

#### Recommendation 7: Quick Unlock Modal

Add `Ctrl+V` shortcut to quickly unlock vault from main chat:

```
┌─────────────────────────────────────────────────────────┐
│                    Unlock Vault                         │
│                                                         │
│  Passphrase: [********                              ]   │
│                                                         │
│  [Enter] Unlock    [Esc] Cancel                        │
└─────────────────────────────────────────────────────────┘
```

---

#### Recommendation 8: In-TUI Credential Addition

Add ability to create credentials directly in the TUI:

```
┌─────────────────────────────────────────────────────────┐
│                    Add Credential                       │
│                                                         │
│  ID:          [github-token                        ]    │
│  Name:        [GitHub Personal Access Token        ]    │
│  Kind:        [bearer_token ▼]                          │
│  Secret:      [********************************    ]    │
│                                                         │
│  Hosts:       [github.com, api.github.com         ]    │
│  Tags:        [ci, github                            ]    │
│                                                         │
│  [Tab] Next field    [Enter] Save    [Esc] Cancel      │
└─────────────────────────────────────────────────────────┘
```

---

#### Recommendation 9: Credential Search/Filter

Add `/` to search credentials in vault panel:

```
┌─────────────────────────────────────────────────────────┐
│ Credential Vault                              [github]  │
│                                                         │
│ ID                   KIND            SCOPE              │
│ ─────────────────────────────────────────────────────── │
│ > github-token       bearer_token    global             │
│   github-ssh         ssh_key         global             │
│                                                         │
│ 2 matching credentials                                  │
│                                                         │
│ [j/k] Navigate  [Enter] View  [/] Search  [l] Lock     │
└─────────────────────────────────────────────────────────┘
```

---

#### Recommendation 10: Scope Explanation

Add help text when switching scopes:

```
┌─────────────────────────────────────────────────────────┐
│ Credential Vault                                        │
│                                                         │
│ ┌─────────────────────────────────────────────────────┐ │
│ │ [Global]    Project                                 │ │
│ │                                                     │ │
│ │ Global credentials are available in all projects.   │ │
│ │ Use for personal API keys, tokens, etc.             │ │
│ └─────────────────────────────────────────────────────┘ │
│                                                         │
│ Path: /home/user/.swarm/vault/global.vault             │
│                                                         │
│ Vault is locked.                                        │
│                                                         │
│ [Enter] Unlock  [Tab] Switch scope                      │
└─────────────────────────────────────────────────────────┘
```

---

### 2.3 User Stories

#### Story 1: First-Time Setup

**As a new user**, I want to quickly set up my vault so I can start storing credentials.

**Current Experience:**
1. Run `swarmos vault init`
2. Type passphrase
3. Confirm passphrase
4. See success message
5. Vault unlocked

**Problems:**
- No guidance on passphrase strength
- No explanation of what happens next
- No suggestion to add first credential

**Improved Experience:**
1. Run `swarmos vault init`
2. See welcome message explaining vault purpose
3. Type passphrase (with strength indicator)
4. Confirm passphrase
5. See success message with next steps
6. Prompt: "Would you like to add your first credential? [Y/n]"
7. If yes, enter credential wizard

---

#### Story 2: Daily Unlock

**As a daily user**, I want to unlock my vault quickly so I can use my credentials.

**Current Experience:**
1. Start TUI
2. Navigate to Settings
3. Navigate to Vault
4. Press Enter to unlock
5. Type passphrase
6. Press Enter

**Problems:**
- Too many steps
- Vault state not visible in main UI
- No keyboard shortcut

**Improved Experience:**
1. Start TUI
2. See 🔒 in status bar
3. Press Ctrl+V
4. Type passphrase
5. Press Enter
6. See 🔓 in status bar

---

#### Story 3: Adding API Key

**As a developer**, I want to add a new API key so my agent can use it.

**Current Experience:**
1. Exit TUI (or open new terminal)
2. Run `swarmos vault add my-key --kind api_key`
3. Type secret value
4. No confirmation of what was added

**Problems:**
- Must exit TUI
- Secret typing is awkward
- No verification that key works

**Improved Experience:**
1. In TUI, press Ctrl+V to open vault
2. Press `a` to add credential
3. Fill form:
   - ID: my-api-key
   - Kind: api_key (dropdown)
   - Secret: (masked input)
4. Press Enter to save
5. See credential in list
6. Press `t` to test credential works

---

#### Story 4: Using Credentials

**As a user**, I want to run a command with credentials so I can access protected resources.

**Current Experience:**
1. Run `swarmos vault exec my-aws-key -- aws s3 ls`

**Problems:**
- Doesn't work well in TUI
- Output redaction can hide important info
- No way to use in agent sessions

**Improved Experience:**
1. In TUI chat, type: "List my S3 buckets"
2. Agent requests vault access
3. TUI shows: "Agent wants to use 'my-aws-key' for AWS access. Allow? [Y/n]"
4. User approves
5. Agent executes command with credentials
6. Output shows redacted summary

---

#### Story 5: Team Vault

**As a team lead**, I want to share credentials with my team securely.

**Current Experience:**
1. Run `swarm-vault init --project`
2. Create recipients.txt with team public keys
3. Each team member needs identity file
4. Very manual process

**Problems:**
- No documentation for team setup
- No easy way to add/remove team members
- No audit trail of who accessed what

**Improved Experience:**
1. In project root, run `swarmos vault init --project --team`
2. See team setup wizard:
   - "Enter team member emails (comma-separated):"
   - System sends invite emails with public key instructions
3. Team members run `swarmos vault identity` to create identity
4. Team members share public key
5. Lead adds to recipients via `swarmos vault allow user@example.com`
6. All team members can now unlock project vault

---

### 2.4 Security Improvements

#### Recommendation 11: Passphrase Strength Indicator

```go
func assessPassphraseStrength(passphrase string) string {
    score := 0
    if len(passphrase) >= 12 {
        score++
    }
    if len(passphrase) >= 16 {
        score++
    }
    if regexp.MustCompile(`[A-Z]`).MatchString(passphrase) {
        score++
    }
    if regexp.MustCompile(`[a-z]`).MatchString(passphrase) {
        score++
    }
    if regexp.MustCompile(`[0-9]`).MatchString(passphrase) {
        score++
    }
    if regexp.MustCompile(`[^A-Za-z0-9]`).MatchString(passphrase) {
        score++
    }
    
    switch {
    case score >= 6:
        return "Strong ✓"
    case score >= 4:
        return "Moderate"
    default:
        return "Weak - Consider a longer passphrase"
    }
}
```

---

#### Recommendation 12: Auto-Lock Timeout

```go
// In TUI vault settings
type VaultSettings struct {
    // ...
    lastActivity time.Time
    autoLockTimeout time.Duration // Default: 30 minutes
}

func (s *VaultSettings) CheckAutoLock() {
    if s.storage == nil && s.projectStore == nil {
        return // Already locked
    }
    if time.Since(s.lastActivity) > s.autoLockTimeout {
        s.lockVault()
        s.message = "Vault auto-locked due to inactivity"
    }
}
```

---

## Part 3: Implementation Priority

### Phase 1: Critical Fixes (Do Immediately)
1. ✅ Integrate vault into main swarmos binary (COMPLETED)
2. Add security warning for --password flag
3. Improve error messages
4. Add environment variable support

### Phase 2: UX Polish (Next Sprint)
5. Add status bar indicator
6. Add quick unlock shortcut (Ctrl+V)
7. Add better help text
8. Add passphrase strength indicator

### Phase 3: Feature Completion (Next Quarter)
9. Add keyring integration
10. Add in-TUI credential creation
11. Add credential search/filter
12. Add auto-lock timeout

### Phase 4: Team Features (Future)
13. Team vault wizard
14. Audit logging
15. Credential rotation reminders

---

## Conclusion

The current vault implementation is functionally complete but has significant UX debt that prevents adoption. By implementing the recommendations in this document, we can transform the vault from a hidden power-user feature into a core capability that all users leverage daily.

Key takeaways:
- **Discovery is key**: Users can't use what they can't find
- **Reduce friction**: Every extra step reduces adoption
- **Be secure by default**: Don't let users shoot themselves in the foot
- **Provide feedback**: Users need to know what's happening
- **Integrate tightly**: The vault should feel like part of the app, not a separate tool
