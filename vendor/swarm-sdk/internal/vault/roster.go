package vault

// Roster management for team vaults.
//
// A team roster is the set of age recipient public keys (recipients.txt) that
// can participate in a project's credentials. This file adds a thin, safe API
// over the existing identity.go primitives plus the re-seal semantics that
// Two-Person Integrity requires.
//
// IMPORTANT re-seal constraint: adding or removing a recipient for an EXISTING
// two-person credential needs a fresh Shamir share for the new recipient set,
// which requires the data key — i.e. a two-person reconstruct. You cannot
// re-wrap shares with public keys alone. Therefore:
//   - AllowMember / RevokeMember only edit recipients.txt (affecting NEW creds).
//   - Existing two-person creds must be re-sealed via ResealTwoPerson AFTER a
//     reconstruct ceremony (see broker). RevokeMember additionally requires the
//     underlying SECRET to be rotated — removing a roster entry does not undo a
//     copy an ex-member already pulled from git history.

import (
	"context"
	"fmt"
)

// RosterMember is a public roster entry.
type RosterMember struct {
	PublicKey string `json:"publicKey"`
	Comment   string `json:"comment,omitempty"`
}

// Members returns the current roster (public keys + optional name comments).
func Members(recipientsPath string) ([]RosterMember, error) {
	if !RecipientsExist(recipientsPath) {
		return nil, nil
	}
	infos, err := ParseRecipientsWithComments(recipientsPath)
	if err != nil {
		return nil, err
	}
	out := make([]RosterMember, 0, len(infos))
	for _, i := range infos {
		out = append(out, RosterMember{PublicKey: i.PublicKey, Comment: i.Comment})
	}
	return out, nil
}

// AllowMember adds a public key (with optional comment) to the roster. It
// validates the key parses as an age recipient before writing. Returns a
// human-facing note about the re-seal requirement for existing creds.
func AllowMember(recipientsPath, publicKey, comment string) (note string, err error) {
	if _, perr := parseRecipient(publicKey); perr != nil {
		return "", fmt.Errorf("invalid age public key: %w", perr)
	}
	infos, _ := ParseRecipientsWithComments(recipientsPath)
	for _, i := range infos {
		if i.PublicKey == publicKey {
			return "", fmt.Errorf("recipient already in roster")
		}
	}
	infos = append(infos, RecipientInfo{PublicKey: publicKey, Comment: comment})
	if err := WriteRecipientsFileWithComments(recipientsPath, infos); err != nil {
		return "", err
	}
	return "Added to roster. New credentials will include this member. " +
		"EXISTING two-person credentials must be re-sealed (a two-person reconstruct " +
		"ceremony) before this member can access them.", nil
}

// RevokeMember removes a public key from the roster. Returns a note stressing
// that the affected secrets MUST be rotated — removing a roster entry does not
// invalidate a copy the ex-member already holds.
func RevokeMember(recipientsPath, publicKey string) (note string, err error) {
	if !RecipientsExist(recipientsPath) {
		return "", fmt.Errorf("no roster at %s", recipientsPath)
	}
	if err := RemoveRecipient(recipientsPath, publicKey); err != nil {
		return "", err
	}
	return "Removed from roster. IMPORTANT: rotate any secrets this member could " +
		"access — removing a roster entry does NOT invalidate a copy they already " +
		"pulled from git history. Re-seal remaining two-person creds to the new roster.", nil
}

// ResealTwoPerson re-seals a two-person credential to the CURRENT roster using a
// secret recovered via a two-person reconstruct ceremony. This is the only way
// to change the recipient set of an existing two-person credential. The caller
// is responsible for zeroizing secret after this returns.
func ResealTwoPerson(store *TwoPersonStorage, recipientsPath, credID string, secret []byte, threshold int) error {
	recipients, err := loadRosterRecipients(recipientsPath)
	if err != nil {
		return err
	}
	if len(recipients) < threshold {
		return fmt.Errorf("roster has %d recipients, need >= %d for threshold %d", len(recipients), threshold, threshold)
	}
	meta, err := store.Metadata(credID)
	if err != nil {
		return err
	}
	meta.Secret = string(secret)
	return store.SealAndStore(context.Background(), *meta, threshold, recipients)
}

// loadRosterRecipients parses recipients.txt into recipientWithKey entries for
// sealing.
func loadRosterRecipients(recipientsPath string) ([]recipientWithKey, error) {
	if !RecipientsExist(recipientsPath) {
		return nil, fmt.Errorf("no roster at %s", recipientsPath)
	}
	infos, err := ParseRecipientsWithComments(recipientsPath)
	if err != nil {
		return nil, err
	}
	out := make([]recipientWithKey, 0, len(infos))
	for _, i := range infos {
		rec, perr := parseRecipient(i.PublicKey)
		if perr != nil {
			return nil, fmt.Errorf("roster entry %q invalid: %w", i.PublicKey, perr)
		}
		out = append(out, recipientWithKey{recipient: rec, key: i.PublicKey, comment: i.Comment})
	}
	return out, nil
}

// SealNewTwoPerson seals a brand-new sensitive credential to the current roster.
// Used by the vault_add sensitive path. Caller zeroizes secret after.
func SealNewTwoPerson(store *TwoPersonStorage, recipientsPath string, cred Credential, threshold int) error {
	recipients, err := loadRosterRecipients(recipientsPath)
	if err != nil {
		return err
	}
	if len(recipients) < threshold {
		return fmt.Errorf("roster has %d recipients, need >= %d for two-person threshold %d", len(recipients), threshold, threshold)
	}
	return store.SealAndStore(context.Background(), cred, threshold, recipients)
}
