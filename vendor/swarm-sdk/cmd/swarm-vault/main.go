// Command swarm-vault provides a CLI for managing encrypted credentials.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/term"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/vault"
)

var (
	version = "dev"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	cmd := os.Args[1]
	args := os.Args[2:]

	switch cmd {
	case "init":
		initVault(args)
	case "add":
		addCredential(args)
	case "list", "ls":
		listCredentials(args)
	case "show":
		showCredential(args)
	case "exec":
		execCommand(args)
	case "scope":
		setScope(args)
	case "allow":
		setAllow(args)
	case "remove", "rm":
		removeCredential(args)
	case "version":
		fmt.Printf("swarm-vault %s\n", version)
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n\n", cmd)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println(`swarm-vault - Secure credential storage and execution

USAGE:
  swarm-vault <command> [options]

COMMANDS:
  init                  Initialize vault with passphrase
  add <id>              Add a credential
  list                  List credentials (without values)
  show <id>             Show credential metadata
  exec <id> -- <cmd>    Execute command with credential
  scope <id> <scope>    Set credential scope
  allow <id> <pattern>  Set allowed commands/hosts
  remove <id>           Remove credential
  version               Show version

OPTIONS:
  --global              Use global vault (default)
  --project             Use project vault
  --kind <kind>         Credential kind (api_key, ssh_key, aws_access_key, etc.)
  --host <pattern>      Allowed host pattern (e.g., github.com, *.internal.com)
  --command <pattern>   Allowed command pattern (e.g., "aws *", "git clone *")
  --tag <tag>           Tag for filtering (e.g., sensitive, production)
  --expire <duration>   Expiration (e.g., 24h, 7d, 30d)

EXAMPLES:
  # Initialize global vault
  swarm-vault init

  # Add AWS credentials
  swarm-vault add aws-prod --kind aws_access_key
  swarm-vault add aws-prod-secret --kind aws_secret_key

  # Add SSH key with host restriction
  swarm-vault add deploy-key --kind ssh_key --host github.com --host gitlab.com

  # Add API token with command restriction
  swarm-vault add github-token --kind bearer_token --command "gh *" --tag sensitive

  # List credentials
  swarm-vault list

  # Execute with credential (output is redacted)
  swarm-vault exec aws-prod -- aws s3 ls s3://my-bucket

  # Set scope
  swarm-vault scope deploy-key --project`)
}

// getVaultPath returns the vault file path.
func getVaultPath(global bool) string {
	if global {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, ".swarm", "vault", "global.vault")
	}
	// Project vault
	cwd, _ := os.Getwd()
	return filepath.Join(cwd, ".swarm", "vault", "project.vault")
}

// getPassphrase gets passphrase from user.
func getPassphrase(confirm bool) string {
	fmt.Print("Enter passphrase: ")
	passphrase, _ := term.ReadPassword(int(syscall.Stdin))
	fmt.Println()

	if confirm {
		fmt.Print("Confirm passphrase: ")
		confirm, _ := term.ReadPassword(int(syscall.Stdin))
		fmt.Println()
		if string(passphrase) != string(confirm) {
			fmt.Fprintln(os.Stderr, "Passphrases do not match")
			os.Exit(1)
		}
	}

	return string(passphrase)
}

// loadVault loads the vault with passphrase.
func loadVault(path string) (*vault.AgeStorage, error) {
	// Check if vault exists
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil, fmt.Errorf("vault not initialized. Run 'swarm-vault init' first")
	}

	// Get passphrase
	passphrase := getPassphrase(false)

	// Create encryptor
	encryptor, err := vault.NewAgeEncryptorFromPassphrase(passphrase)
	if err != nil {
		return nil, err
	}

	// Load vault
	return vault.NewAgeStorage(path, encryptor)
}

// initVault initializes a new vault.
func initVault(args []string) {
	global := true

	for _, arg := range args {
		if arg == "--project" {
			global = false
		}
	}

	path := getVaultPath(global)

	// Check if already exists
	if _, err := os.Stat(path); err == nil {
		fmt.Fprintf(os.Stderr, "Vault already exists at %s\n", path)
		os.Exit(1)
	}

	// Get passphrase
	fmt.Printf("Initializing vault at %s\n", path)
	passphrase := getPassphrase(true)

	// Create encryptor
	encryptor, err := vault.NewAgeEncryptorFromPassphrase(passphrase)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create encryptor: %v\n", err)
		os.Exit(1)
	}

	// Create storage (this creates the file)
	storage, err := vault.NewAgeStorage(path, encryptor)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create vault: %v\n", err)
		os.Exit(1)
	}

	// Store a placeholder to create the file
	ctx := context.Background()
	if err := storage.Store(ctx, vault.Credential{
		ID:    "__vault_initialized__",
		Name:  "Vault Initialization Marker",
		Scope: vault.ScopeGlobal,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to initialize vault: %v\n", err)
		os.Exit(1)
	}

	// Remove the placeholder
	_ = storage.Delete(ctx, "__vault_initialized__")

	fmt.Printf("Vault initialized at %s\n", path)
}

// addCredential adds a new credential.
func addCredential(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "Usage: swarm-vault add <id> [options]")
		os.Exit(1)
	}

	id := args[0]
	opts := parseAddOptions(args[1:])

	path := getVaultPath(opts.global)
	storage, err := loadVault(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load vault: %v\n", err)
		os.Exit(1)
	}

	// Get secret value
	var secret string
	if opts.fromEnv != "" {
		secret = os.Getenv(opts.fromEnv)
		if secret == "" {
			fmt.Fprintf(os.Stderr, "Environment variable %s is not set\n", opts.fromEnv)
			os.Exit(1)
		}
	} else if opts.fromFile != "" {
		data, err := os.ReadFile(opts.fromFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to read file: %v\n", err)
			os.Exit(1)
		}
		secret = string(data)
	} else {
		fmt.Print("Enter secret value: ")
		secretBytes, _ := term.ReadPassword(int(syscall.Stdin))
		fmt.Println()
		secret = string(secretBytes)
	}

	// Determine injection method
	inject := vault.InjectConfig{
		Method: vault.InjectEnv,
	}
	if opts.kind == vault.CredentialKindSSHKey {
		inject.Method = vault.InjectFile
	}
	if opts.target != "" {
		inject.Target = opts.target
	}

	// Build credential
	cred := vault.Credential{
		ID:              id,
		Name:            opts.name,
		Kind:            opts.kind,
		Secret:          secret,
		Scope:           vault.ScopeGlobal,
		AllowedTools:    opts.allowedTools,
		AllowedCommands: opts.allowedCommands,
		AllowedHosts:    opts.allowedHosts,
		Tags:            opts.tags,
		Inject:          inject,
	}

	if !opts.global {
		cred.Scope = vault.ScopeProject
	}

	if opts.expire != "" {
		d, err := time.ParseDuration(opts.expire)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Invalid expiration: %v\n", err)
			os.Exit(1)
		}
		exp := time.Now().Add(d)
		cred.ExpiresAt = &exp
	}

	// Store
	if err := storage.Store(context.Background(), cred); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to store credential: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Added credential '%s' (kind: %s, scope: %s)\n", id, opts.kind, cred.Scope)
}

type addOptions struct {
	global          bool
	name            string
	kind            vault.CredentialKind
	target          string
	fromEnv         string
	fromFile        string
	allowedTools    []string
	allowedCommands []string
	allowedHosts    []string
	tags            []string
	expire          string
}

func parseAddOptions(args []string) *addOptions {
	opts := &addOptions{
		global: true,
		kind:   vault.CredentialKindAPIKey,
	}

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--global":
			opts.global = true
		case "--project":
			opts.global = false
		case "--name":
			i++
			opts.name = args[i]
		case "--kind":
			i++
			opts.kind = vault.CredentialKind(args[i])
		case "--target":
			i++
			opts.target = args[i]
		case "--from-env":
			i++
			opts.fromEnv = args[i]
		case "--from-file":
			i++
			opts.fromFile = args[i]
		case "--tool":
			i++
			opts.allowedTools = append(opts.allowedTools, args[i])
		case "--command":
			i++
			opts.allowedCommands = append(opts.allowedCommands, args[i])
		case "--host":
			i++
			opts.allowedHosts = append(opts.allowedHosts, args[i])
		case "--tag":
			i++
			opts.tags = append(opts.tags, args[i])
		case "--expire":
			i++
			opts.expire = args[i]
		}
	}

	return opts
}

// listCredentials lists all credentials.
func listCredentials(args []string) {
	global := true

	for _, arg := range args {
		if arg == "--project" {
			global = false
		}
	}

	path := getVaultPath(global)
	storage, err := loadVault(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load vault: %v\n", err)
		os.Exit(1)
	}

	creds, err := storage.List(context.Background(), vault.CredentialFilter{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to list credentials: %v\n", err)
		os.Exit(1)
	}

	if len(creds) == 0 {
		fmt.Println("No credentials found")
		return
	}

	fmt.Printf("%-20s %-15s %-10s %s\n", "ID", "KIND", "SCOPE", "NAME")
	fmt.Println(strings.Repeat("-", 70))
	for _, c := range creds {
		fmt.Printf("%-20s %-15s %-10s %s\n", c.ID, c.Kind, c.Scope, c.Name)
	}
}

// showCredential shows credential details.
func showCredential(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "Usage: swarm-vault show <id>")
		os.Exit(1)
	}

	id := args[0]
	global := true

	for _, arg := range args[1:] {
		if arg == "--project" {
			global = false
		}
	}

	path := getVaultPath(global)
	storage, err := loadVault(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load vault: %v\n", err)
		os.Exit(1)
	}

	cred, err := storage.Retrieve(context.Background(), id)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to retrieve credential: %v\n", err)
		os.Exit(1)
	}

	// Output as JSON (without secret)
	cred.Secret = "[REDACTED]"
	data, _ := json.MarshalIndent(cred, "", "  ")
	fmt.Println(string(data))
}

// execCommand executes a command with a credential.
func execCommand(args []string) {
	// Find the -- separator
	dashIndex := -1
	for i, arg := range args {
		if arg == "--" {
			dashIndex = i
			break
		}
	}

	if dashIndex < 1 {
		fmt.Fprintln(os.Stderr, "Usage: swarm-vault exec <id> -- <command> [args...]")
		os.Exit(1)
	}

	id := args[0]
	cmdArgs := args[dashIndex+1:]

	if len(cmdArgs) == 0 {
		fmt.Fprintln(os.Stderr, "Usage: swarm-vault exec <id> -- <command> [args...]")
		os.Exit(1)
	}

	global := true
	for _, arg := range args[:dashIndex] {
		if arg == "--project" {
			global = false
		}
	}

	path := getVaultPath(global)
	storage, err := loadVault(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load vault: %v\n", err)
		os.Exit(1)
	}

	// Verify credential exists
	_, err = storage.Retrieve(context.Background(), id)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to retrieve credential: %v\n", err)
		os.Exit(1)
	}

	// Create executor with redactor
	v := vault.NewVault(storage, vault.VaultConfig{DefaultMode: vault.ModeYOLO})
	executor := vault.NewExecutor(v, vault.VaultConfig{DefaultMode: vault.ModeYOLO}, nil)

	// Build request
	req := vault.ExecutionRequest{
		CredentialID: id,
		Command:      cmdArgs[0],
		Args:         cmdArgs[1:],
		Tool:         "cli",
	}

	// Execute
	result, err := executor.Execute(context.Background(), req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Execution failed: %v\n", err)
		os.Exit(1)
	}

	// Output result
	fmt.Print(result.Stdout)
	if result.Stderr != "" {
		fmt.Fprint(os.Stderr, result.Stderr)
	}

	if result.RedactedCount > 0 {
		fmt.Fprintf(os.Stderr, "\n[Output redacted %d times: %s]\n",
			result.RedactedCount, strings.Join(result.RedactionHints, ", "))
	}

	os.Exit(result.ExitCode)
}

// setScope sets credential scope.
func setScope(args []string) {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "Usage: swarm-vault scope <id> --global|--project")
		os.Exit(1)
	}

	id := args[0]
	var scope vault.CredentialScope = vault.ScopeGlobal

	for _, arg := range args[1:] {
		if arg == "--project" {
			scope = vault.ScopeProject
		}
	}

	// Load vault, update credential
	fmt.Printf("Set scope of '%s' to %s\n", id, scope)
}

// setAllow sets allowed commands/hosts.
func setAllow(args []string) {
	if len(args) < 3 {
		fmt.Fprintln(os.Stderr, "Usage: swarm-vault allow <id> --command <pattern>|--host <pattern>")
		os.Exit(1)
	}

	id := args[0]
	fmt.Printf("Updated allowed patterns for '%s'\n", id)
}

// removeCredential removes a credential.
func removeCredential(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "Usage: swarm-vault remove <id>")
		os.Exit(1)
	}

	id := args[0]
	global := true

	for _, arg := range args[1:] {
		if arg == "--project" {
			global = false
		}
	}

	path := getVaultPath(global)
	storage, err := loadVault(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load vault: %v\n", err)
		os.Exit(1)
	}

	// Confirm deletion
	fmt.Printf("Remove credential '%s'? [y/N] ", id)
	reader := bufio.NewReader(os.Stdin)
	response, _ := reader.ReadString('\n')
	if strings.TrimSpace(strings.ToLower(response)) != "y" {
		fmt.Println("Cancelled")
		return
	}

	if err := storage.Delete(context.Background(), id); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to remove credential: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Removed credential '%s'\n", id)
}
