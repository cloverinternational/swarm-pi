package client

import (
	"errors"
	"fmt"

	"github.com/Swarm-Code/mono/swarm-sdk/harness"
)

// ErrHarnessExecutableConsentRequired identifies an opted-in host refusal to
// construct or apply a plan whose local executable supply chain was not
// acknowledged with the exact compiled plan digest.
var ErrHarnessExecutableConsentRequired = errors.New("harness: executable supply-chain consent required")

// HarnessPreflightOptions contains host-owned posture for the public preflight
// gate. A manifest cannot set either field.
type HarnessPreflightOptions struct {
	Bindings                 HarnessBindings
	RequireExecutableConsent bool
	ExecutableConsentDigest  string
}

func preflightHarnessExecutableConsent(plan *harness.Plan, required bool, supplied string) error {
	if plan == nil {
		return fmt.Errorf("harness: executable consent preflight: nil plan")
	}
	if !required || !plan.HasExecutableSupplyChain() {
		return nil
	}
	if !validHarnessDigest(supplied) {
		return fmt.Errorf("%w: supply an exact sha256 plan digest", ErrHarnessExecutableConsentRequired)
	}
	if supplied != plan.Digest() {
		return fmt.Errorf("%w: supplied digest does not match the compiled plan", ErrHarnessExecutableConsentRequired)
	}
	return nil
}

func validHarnessDigest(digest string) bool {
	const prefix = "sha256:"
	if len(digest) != len(prefix)+64 || digest[:len(prefix)] != prefix {
		return false
	}
	for _, c := range digest[len(prefix):] {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
