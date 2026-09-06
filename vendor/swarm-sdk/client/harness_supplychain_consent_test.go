package client

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/harness"
)

func TestHarnessExecutableConsentConstructionIsOptInAndExact(t *testing.T) {
	dir := t.TempDir()
	executable := compilePlanInDir(t, dir, hooksBaseManifest)
	plain := compilePlanInDir(t, dir, hooksZeroManifest)

	if !executable.HasExecutableSupplyChain() {
		t.Fatal("test executable plan has no executable supply chain")
	}
	if plain.HasExecutableSupplyChain() {
		t.Fatal("test plain plan unexpectedly has an executable supply chain")
	}

	legacyCompatible, err := New(WithHarnessPlan(executable))
	if err != nil {
		t.Fatalf("host that did not opt into the gate changed behavior: %v", err)
	}
	t.Cleanup(func() { _ = legacyCompatible.Close() })

	plainClient, err := New(
		WithHarnessPlan(plain),
		WithHarnessRequireExecutableConsent(""),
	)
	if err != nil {
		t.Fatalf("non-executable plan should not require a digest: %v", err)
	}
	t.Cleanup(func() { _ = plainClient.Close() })

	for name, digest := range map[string]string{
		"missing":   "",
		"malformed": "SHA256:not-lowercase",
		"mismatch":  "sha256:" + strings.Repeat("0", 64),
	} {
		t.Run(name, func(t *testing.T) {
			c, err := New(
				WithHarnessPlan(executable),
				WithHarnessRequireExecutableConsent(digest),
			)
			if c != nil {
				_ = c.Close()
				t.Fatal("refused construction returned a client")
			}
			if !errors.Is(err, ErrHarnessExecutableConsentRequired) {
				t.Fatalf("construction error = %v, want ErrHarnessExecutableConsentRequired", err)
			}
		})
	}

	consented, err := New(
		WithHarnessPlan(executable),
		WithHarnessRequireExecutableConsent(executable.Digest()),
	)
	if err != nil {
		t.Fatalf("exact consent did not permit construction: %v", err)
	}
	t.Cleanup(func() { _ = consented.Close() })
}

func TestHarnessExecutableConsentPublicPreflight(t *testing.T) {
	plan := compilePlanInDir(t, t.TempDir(), hooksBaseManifest)

	err := PreflightHarnessPlanWithOptions(plan, HarnessPreflightOptions{
		RequireExecutableConsent: true,
	})
	if !errors.Is(err, ErrHarnessExecutableConsentRequired) {
		t.Fatalf("missing consent preflight error = %v", err)
	}

	err = PreflightHarnessPlanWithOptions(plan, HarnessPreflightOptions{
		RequireExecutableConsent: true,
		ExecutableConsentDigest:  plan.Digest(),
	})
	if err != nil {
		t.Fatalf("exact consent preflight failed: %v", err)
	}
}

func TestHarnessExecutableConsentHotApplyIsAtomicAndPersistent(t *testing.T) {
	dir := t.TempDir()
	plain := compilePlanInDir(t, dir, hooksZeroManifest)
	executable := compilePlanInDir(t, dir, hooksBaseManifest)

	c, err := New(
		WithHarnessPlan(plain),
		WithHarnessRequireExecutableConsent(""),
	)
	if err != nil {
		t.Fatalf("New(plain): %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	result, err := c.ApplyHarnessPlan(context.Background(), executable, ApplyHarnessOptions{})
	if !errors.Is(err, ErrHarnessExecutableConsentRequired) {
		t.Fatalf("missing-consent apply error = %v", err)
	}
	if result.Digest != plain.Digest() {
		t.Fatalf("refused apply digest = %q, want prior %q", result.Digest, plain.Digest())
	}
	if got := c.opts.harness.plan.Digest(); got != plain.Digest() {
		t.Fatalf("refused apply mutated authoritative plan to %q", got)
	}

	result, err = c.ApplyHarnessPlan(context.Background(), executable, ApplyHarnessOptions{
		ExecutableConsentDigest: executable.Digest(),
	})
	if err != nil {
		t.Fatalf("exact-consent apply failed: %v", err)
	}
	if result.Digest != executable.Digest() || c.opts.harness.plan.Digest() != executable.Digest() {
		t.Fatalf("exact-consent apply did not adopt executable plan: result=%q current=%q", result.Digest, c.opts.harness.plan.Digest())
	}

	changed := compilePlanInDir(t, dir, hooksCommandChangedManifest)
	_, err = c.ApplyHarnessPlan(context.Background(), changed, ApplyHarnessOptions{
		ExecutableConsentDigest: executable.Digest(),
	})
	if !errors.Is(err, ErrHarnessExecutableConsentRequired) {
		t.Fatalf("stale consent error = %v, want ErrHarnessExecutableConsentRequired", err)
	}
}

func TestHarnessExecutableConsentReloadUsesOnlyHostAllowlist(t *testing.T) {
	dir := t.TempDir()
	plain := compilePlanInDir(t, dir, hooksZeroManifest)
	executable := compilePlanInDir(t, dir, hooksBaseManifest)

	newClient := func(t *testing.T) *Client {
		t.Helper()
		c, err := New(
			WithHarnessPlan(plain),
			WithHarnessRequireExecutableConsent(""),
		)
		if err != nil {
			t.Fatalf("New(plain): %v", err)
		}
		t.Cleanup(func() { _ = c.Close() })
		return c
	}

	ev := harness.WatchEvent{Plan: executable}
	policy := DefaultReloadPolicy()

	refused := newClient(t).classifyAndApplyReload(
		context.Background(),
		ev,
		policy,
		false,
		true,
		map[string]struct{}{},
	)
	if !refused.Rejected || !errors.Is(refused.Err, ErrHarnessExecutableConsentRequired) {
		t.Fatalf("reload without allowlist consent = %+v", refused)
	}

	allowed := newClient(t).classifyAndApplyReload(
		context.Background(),
		ev,
		policy,
		false,
		true,
		map[string]struct{}{executable.Digest(): {}},
	)
	if !allowed.Applied || allowed.Err != nil {
		t.Fatalf("reload with exact allowlist consent = %+v", allowed)
	}
}
