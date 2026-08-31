package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/vault"
)

// runVaultCLI dispatches the `swarmos vault <subcommand>` roster/identity
// commands. Credential add/exec live in the agent tool surface and the TUI;
// this CLI focuses on the team roster and identity lifecycle.
func runVaultCLI(args []string) error {
	if len(args) == 0 {
		printVaultUsage()
		return nil
	}
	switch args[0] {
	case "identity":
		return vaultIdentity(args[1:])
	case "members":
		return vaultMembers(args[1:])
	case "allow":
		return vaultAllow(args[1:])
	case "revoke":
		return vaultRevoke(args[1:])
	case "-h", "--help", "help":
		printVaultUsage()
		return nil
	default:
		return fmt.Errorf("unknown vault subcommand %q (try: identity, members, allow, revoke)", args[0])
	}
}

func printVaultUsage() {
	fmt.Println(`swarmos vault - team credential roster & identity

USAGE:
  swarmos vault identity            Print your age public key (share with your team)
  swarmos vault members             List roster members (recipients.txt)
  swarmos vault allow <pubkey> [name]  Add a member to the roster
  swarmos vault revoke <pubkey>     Remove a member from the roster

NOTES:
  - Your private identity lives at ~/.swarm/vault/identity (never shared).
  - The roster (recipients.txt) and encrypted vault live under
    <workspace>/.swarm/vault/ and are safe to commit to git.
  - Adding/removing a member affects NEW credentials immediately. EXISTING
    two-person credentials must be re-sealed (a two-person ceremony), and
    revoking a member requires rotating the secrets they could access.`)
}

func identityPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".swarm", "vault", "identity")
}

func recipientsPath() string {
	ws, _ := os.Getwd()
	return filepath.Join(ws, ".swarm", "vault", "recipients.txt")
}

// vaultIdentity ensures an identity exists and prints the public key.
func vaultIdentity(_ []string) error {
	path := identityPath()
	id, created, err := vault.EnsureIdentity(path)
	if err != nil {
		return fmt.Errorf("identity: %w", err)
	}
	if created {
		fmt.Fprintf(os.Stderr, "Created new identity at %s (keep the private key safe)\n", path)
	}
	fmt.Println(id.Recipient().String())
	return nil
}

func vaultMembers(_ []string) error {
	rp := recipientsPath()
	members, err := vault.Members(rp)
	if err != nil {
		return err
	}
	if len(members) == 0 {
		fmt.Printf("No roster yet at %s\n", rp)
		return nil
	}
	fmt.Printf("Roster (%d members) — %s\n", len(members), rp)
	for _, m := range members {
		name := m.Comment
		if name == "" {
			name = "(no name)"
		}
		fmt.Printf("  %-24s %s\n", name, m.PublicKey)
	}
	return nil
}

func vaultAllow(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: swarmos vault allow <pubkey> [name]")
	}
	pubkey := args[0]
	name := strings.Join(args[1:], " ")
	note, err := vault.AllowMember(recipientsPath(), pubkey, name)
	if err != nil {
		return err
	}
	fmt.Printf("Added %s\n%s\n", pubkey, note)
	return nil
}

func vaultRevoke(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: swarmos vault revoke <pubkey>")
	}
	note, err := vault.RevokeMember(recipientsPath(), args[0])
	if err != nil {
		return err
	}
	fmt.Printf("Removed %s\n%s\n", args[0], note)
	return nil
}
