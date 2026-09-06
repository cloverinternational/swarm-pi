package presence

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"
)

// ─── Golden v0/v1 decode fixtures ───────────────────────────────────────
//
// These fixtures are hand-constructed raw JSON byte sequences (not produced
// by this package's own Encode* functions) so a regression in Decode*
// cannot silently "fix itself" by symmetry with a changed Encode*. v0 is
// the legacy shape (no "schema_version" key at all, matching every presence
// record written before schema versioning existed in discovery.go). v1 adds
// an explicit "schema_version": 1 and a populated "build_provenance" object.

const goldenLocalV0 = `{
  "handle": "alice",
  "kind": "local",
  "status": "idle",
  "pid": 4242,
  "last_seen_at": "2024-01-01T00:00:00Z"
}`

const goldenLocalV1 = `{
  "schema_version": 1,
  "handle": "bob",
  "kind": "daemon",
  "status": "working",
  "pid": 9000,
  "last_seen_at": "2024-06-15T12:30:00Z",
  "started_at": "2024-06-15T12:00:00Z",
  "instance_token": "tok-abc123",
  "process_start": "1234567890",
  "executable": "/usr/local/bin/swarmos",
  "build_provenance": {
    "available": true,
    "go_version": "go1.26",
    "main_version": "(devel)",
    "vcs_revision": "deadbeef",
    "vcs_modified": false
  }
}`

const goldenRemoteV1 = `{
  "schema_version": 1,
  "handle": "carol~laptop",
  "kind": "remote",
  "status": "idle",
  "last_seen_at": "2024-06-15T12:31:00Z",
  "expires_at": "2024-06-15T12:41:00Z",
  "provenance_origin": "carol~laptop",
  "provenance_signer": "signer-xyz",
  "provenance_verified_at": "2024-06-15T12:31:00Z"
}`

func mustParseTime(t *testing.T, s string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("parse golden fixture time %q: %v", s, err)
	}
	return ts
}

func TestGoldenDecodeLocalV0Legacy(t *testing.T) {
	rec, err := DecodeLocal([]byte(goldenLocalV0))
	if err != nil {
		t.Fatalf("DecodeLocal(v0 legacy fixture) returned error: %v", err)
	}
	if rec.SchemaVersion != SchemaVersionLegacy {
		t.Fatalf("expected SchemaVersionLegacy (zero value) for a record with no schema_version key, got %d", rec.SchemaVersion)
	}
	if rec.Handle != "alice" {
		t.Fatalf("expected handle %q, got %q", "alice", rec.Handle)
	}
	if rec.Kind != KindLocal {
		t.Fatalf("expected KindLocal, got %q", rec.Kind)
	}
	if rec.Status != "idle" {
		t.Fatalf("expected status idle, got %q", rec.Status)
	}
	if rec.PID != 4242 {
		t.Fatalf("expected pid 4242, got %d", rec.PID)
	}
	wantLastSeen := mustParseTime(t, "2024-01-01T00:00:00Z")
	if !rec.LastSeenAt.Equal(wantLastSeen) {
		t.Fatalf("expected last_seen_at %v, got %v", wantLastSeen, rec.LastSeenAt)
	}
	if rec.BuildProvenance.Available {
		t.Fatalf("legacy v0 fixture must decode with BuildProvenance.Available == false, got true")
	}
}

func TestGoldenDecodeLocalV1BuildProvenance(t *testing.T) {
	rec, err := DecodeLocal([]byte(goldenLocalV1))
	if err != nil {
		t.Fatalf("DecodeLocal(v1 fixture) returned error: %v", err)
	}
	if rec.SchemaVersion != SchemaVersionBuildProvenance {
		t.Fatalf("expected SchemaVersionBuildProvenance (1), got %d", rec.SchemaVersion)
	}
	if rec.Handle != "bob" {
		t.Fatalf("expected handle %q, got %q", "bob", rec.Handle)
	}
	if rec.Kind != KindDaemon {
		t.Fatalf("expected KindDaemon, got %q", rec.Kind)
	}
	if rec.Instance.Token != "tok-abc123" {
		t.Fatalf("expected instance token tok-abc123, got %q", rec.Instance.Token)
	}
	if rec.Instance.Executable != "/usr/local/bin/swarmos" {
		t.Fatalf("expected executable /usr/local/bin/swarmos, got %q", rec.Instance.Executable)
	}
	if !rec.BuildProvenance.Available {
		t.Fatalf("expected BuildProvenance.Available true for v1 fixture")
	}
	if rec.BuildProvenance.VCSRevision != "deadbeef" {
		t.Fatalf("expected vcs_revision deadbeef, got %q", rec.BuildProvenance.VCSRevision)
	}
	if rec.BuildProvenance.VCSModified {
		t.Fatalf("expected vcs_modified false")
	}
}

func TestGoldenDecodeRemoteV1AuthenticatedProvenance(t *testing.T) {
	rec, err := DecodeRemote([]byte(goldenRemoteV1))
	if err != nil {
		t.Fatalf("DecodeRemote(v1 fixture) returned error: %v", err)
	}
	if rec.Handle != "carol~laptop" {
		t.Fatalf("expected handle carol~laptop, got %q", rec.Handle)
	}
	if rec.Provenance.OriginHandle != "carol~laptop" {
		t.Fatalf("expected provenance origin carol~laptop, got %q", rec.Provenance.OriginHandle)
	}
	if rec.Provenance.SignerID != "signer-xyz" {
		t.Fatalf("expected provenance signer signer-xyz, got %q", rec.Provenance.SignerID)
	}
	wantExpiry := mustParseTime(t, "2024-06-15T12:41:00Z")
	if !rec.ExpiresAt.Equal(wantExpiry) {
		t.Fatalf("expected expires_at %v, got %v", wantExpiry, rec.ExpiresAt)
	}
}

// TestGoldenEncodeDecodeRoundTrip proves EncodeLocal/DecodeLocal and
// EncodeRemote/DecodeRemote are inverses for a fully-populated record of
// each kind -- not just that the hand-written golden fixtures above parse.
func TestGoldenEncodeDecodeRoundTrip(t *testing.T) {
	local := LocalRecord{
		Handle:        "dave",
		Name:          "Dave",
		PID:           555,
		EndpointURL:   "http://127.0.0.1:9/a2a",
		Workspace:     "/home/dave/work",
		Status:        "working",
		StartedAt:     mustParseTime(t, "2024-01-01T00:00:00Z"),
		LastSeenAt:    mustParseTime(t, "2024-01-01T00:05:00Z"),
		Kind:          KindDaemon,
		SchemaVersion: CurrentSchemaVersion,
		BuildProvenance: BuildProvenance{
			Available:   true,
			GoVersion:   "go1.26",
			MainVersion: "v1.2.3",
		},
		Instance: InstanceID{Handle: "dave", Token: "tok-1", PID: 555},
	}
	data, err := EncodeLocal(local)
	if err != nil {
		t.Fatalf("EncodeLocal: %v", err)
	}
	decoded, err := DecodeLocal(data)
	if err != nil {
		t.Fatalf("DecodeLocal(EncodeLocal(x)): %v", err)
	}
	if decoded.Handle != local.Handle || decoded.Status != local.Status || decoded.Instance.Token != local.Instance.Token {
		t.Fatalf("round-trip mismatch: got %+v, want fields from %+v", decoded, local)
	}
	if !decoded.BuildProvenance.Available || decoded.BuildProvenance.MainVersion != "v1.2.3" {
		t.Fatalf("round-trip lost BuildProvenance: got %+v", decoded.BuildProvenance)
	}

	remote := AuthenticatedRemoteRecord{
		Handle:        "eve~phone",
		Status:        "idle",
		LastSeenAt:    mustParseTime(t, "2024-01-01T00:05:00Z"),
		ExpiresAt:     mustParseTime(t, "2024-01-01T00:15:00Z"),
		SchemaVersion: CurrentSchemaVersion,
		Provenance: AuthenticatedProvenance{
			OriginHandle: "eve~phone",
			SignerID:     "signer-1",
			VerifiedAt:   mustParseTime(t, "2024-01-01T00:05:00Z"),
		},
	}
	rdata, err := EncodeRemote(remote)
	if err != nil {
		t.Fatalf("EncodeRemote: %v", err)
	}
	rdecoded, err := DecodeRemote(rdata)
	if err != nil {
		t.Fatalf("DecodeRemote(EncodeRemote(x)): %v", err)
	}
	if rdecoded != remote {
		t.Fatalf("remote round-trip mismatch: got %+v, want %+v", rdecoded, remote)
	}
}

// ─── Local/remote type separation ───────────────────────────────────────
//
// package-boundaries.md: "Local and remote records are different types."
// This test proves the Registry interface's method signatures themselves
// enforce that separation -- PublishLocal's second parameter type and
// UpsertRemote's second parameter type are genuinely distinct reflect.Types,
// so passing a LocalRecord where an AuthenticatedRemoteRecord is required
// (or vice versa) is a Go compile error, not merely a naming convention.

func TestRegistryLocalVsRemoteAreDistinctTypes(t *testing.T) {
	registryType := reflect.TypeOf((*Registry)(nil)).Elem()

	publishLocal, ok := registryType.MethodByName("PublishLocal")
	if !ok {
		t.Fatal("Registry interface has no PublishLocal method")
	}
	upsertRemote, ok := registryType.MethodByName("UpsertRemote")
	if !ok {
		t.Fatal("Registry interface has no UpsertRemote method")
	}

	// Interface method Type has no receiver: In(0) is context.Context,
	// In(1) is the record type.
	if publishLocal.Type.NumIn() != 2 {
		t.Fatalf("PublishLocal must take exactly (context.Context, LocalRecord), got %d params", publishLocal.Type.NumIn())
	}
	if upsertRemote.Type.NumIn() != 2 {
		t.Fatalf("UpsertRemote must take exactly (context.Context, AuthenticatedRemoteRecord), got %d params", upsertRemote.Type.NumIn())
	}

	localParamType := publishLocal.Type.In(1)
	remoteParamType := upsertRemote.Type.In(1)

	wantLocal := reflect.TypeOf(LocalRecord{})
	wantRemote := reflect.TypeOf(AuthenticatedRemoteRecord{})

	if localParamType != wantLocal {
		t.Fatalf("PublishLocal's record parameter must be exactly LocalRecord, got %v", localParamType)
	}
	if remoteParamType != wantRemote {
		t.Fatalf("UpsertRemote's record parameter must be exactly AuthenticatedRemoteRecord, got %v", remoteParamType)
	}
	if localParamType == remoteParamType {
		t.Fatalf("LocalRecord and AuthenticatedRemoteRecord must be distinct types, but PublishLocal and UpsertRemote share reflect.Type %v", localParamType)
	}

	// Also assert they are not aliases of one another and not structurally
	// convertible-without-cast in a way that would let a caller pass one to
	// the other's field-for-field-identical shape by accident: identical
	// field count/order would still be a DIFFERENT declared type in Go
	// (assignability requires either identical named type or one side being
	// unnamed), so distinct reflect.Type identity above is already
	// dispositive. This second check additionally guards against a future
	// edit accidentally making one a defined alias ("type X = Y") of the
	// other, which reflect.TypeOf would collapse to a single identical Type
	// -- exactly what the check above would then catch.
	if wantLocal.Name() == wantRemote.Name() {
		t.Fatalf("LocalRecord and AuthenticatedRemoteRecord must have distinct type names, got %q for both", wantLocal.Name())
	}
}

// TestFileRegistryImplementsRegistry is a compile-time-adjacent smoke test:
// FileRegistry already has `var _ Registry = (*FileRegistry)(nil)` in
// presence.go, but this also exercises it through the interface value at
// runtime to ensure the assertion isn't dead code eliminated from coverage.
func TestFileRegistryImplementsRegistry(t *testing.T) {
	var reg Registry = NewFileRegistry(t.TempDir())
	if reg == nil {
		t.Fatal("NewFileRegistry returned a nil Registry")
	}
}

// ─── Real atomic concurrent update test (-race) ─────────────────────────
//
// Spawns many goroutines, each owning its OWN distinct handle, calling
// PublishLocal/UpsertRemote/RenewLocal concurrently and repeatedly. Then
// asserts List sees every handle's LATEST write with no lost update --
// package-boundaries.md's presence invariant: "Registry updates cannot lose
// an unrelated peer's update." Run with `go test -race` to additionally
// prove there is no data race in FileRegistry's per-handle locking.

func TestConcurrentUpdatesNoLostUpdateForUnrelatedPeers(t *testing.T) {
	dir := t.TempDir()
	reg := NewFileRegistry(dir)
	ctx := context.Background()

	const numLocalPeers = 12
	const numRemotePeers = 12
	const writesPerPeer = 20

	var wg sync.WaitGroup

	for i := 0; i < numLocalPeers; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			handle := Handle(fmt.Sprintf("local-%d", i))
			for w := 0; w < writesPerPeer; w++ {
				rec := LocalRecord{
					Handle:        handle,
					Status:        fmt.Sprintf("status-%d", w),
					SchemaVersion: CurrentSchemaVersion,
					Instance:      InstanceID{Handle: handle, Token: "tok"},
				}
				if err := reg.PublishLocal(ctx, rec); err != nil {
					t.Errorf("PublishLocal(%s, write %d): %v", handle, w, err)
					return
				}
			}
		}()
	}

	for i := 0; i < numRemotePeers; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			handle := Handle(fmt.Sprintf("remote-%d~origin", i))
			for w := 0; w < writesPerPeer; w++ {
				rec := AuthenticatedRemoteRecord{
					Handle:        handle,
					Status:        fmt.Sprintf("status-%d", w),
					LastSeenAt:    time.Now(),
					ExpiresAt:     time.Now().Add(time.Hour),
					SchemaVersion: CurrentSchemaVersion,
					Provenance: AuthenticatedProvenance{
						OriginHandle: string(handle),
						SignerID:     "signer",
					},
				}
				if err := reg.UpsertRemote(ctx, rec); err != nil {
					t.Errorf("UpsertRemote(%s, write %d): %v", handle, w, err)
					return
				}
			}
		}()
	}

	wg.Wait()

	records, err := reg.List(ctx, Filter{})
	if err != nil {
		t.Fatalf("List after concurrent writes: %v", err)
	}
	if len(records) != numLocalPeers+numRemotePeers {
		t.Fatalf("expected %d records (no lost peer), got %d", numLocalPeers+numRemotePeers, len(records))
	}

	// Every peer's FINAL write must be the last write issued for that
	// handle (status-19), proving no interleaved/lost update overwrote a
	// later write with an earlier one.
	wantFinalStatus := fmt.Sprintf("status-%d", writesPerPeer-1)
	seen := map[Handle]bool{}
	for _, rec := range records {
		seen[rec.Handle] = true
		if rec.Status != wantFinalStatus {
			t.Errorf("handle %s: expected final status %q (last write wins, no lost update), got %q", rec.Handle, wantFinalStatus, rec.Status)
		}
	}
	for i := 0; i < numLocalPeers; i++ {
		h := Handle(fmt.Sprintf("local-%d", i))
		if !seen[h] {
			t.Errorf("local peer %s missing from List results after concurrent writes", h)
		}
	}
	for i := 0; i < numRemotePeers; i++ {
		h := Handle(fmt.Sprintf("remote-%d~origin", i))
		if !seen[h] {
			t.Errorf("remote peer %s missing from List results after concurrent writes", h)
		}
	}
}

// TestConcurrentRenewSamePeerNoCorruption hammers RenewLocal for the SAME
// handle from many goroutines simultaneously and asserts the file is never
// left partially written / corrupted (every read after the storm decodes
// cleanly), proving the per-handle mutex genuinely serializes same-handle
// writers rather than merely reducing the race window.
func TestConcurrentRenewSamePeerNoCorruption(t *testing.T) {
	dir := t.TempDir()
	reg := NewFileRegistry(dir)
	ctx := context.Background()

	handle := Handle("shared-peer")
	if err := reg.PublishLocal(ctx, LocalRecord{
		Handle:        handle,
		Status:        "idle",
		SchemaVersion: CurrentSchemaVersion,
		Instance:      InstanceID{Handle: handle, Token: "shared-tok"},
	}); err != nil {
		t.Fatalf("initial PublishLocal: %v", err)
	}

	var wg sync.WaitGroup
	const goroutines = 16
	const renewsEach = 15
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for r := 0; r < renewsEach; r++ {
				lease := Lease{
					Instance:  InstanceID{Handle: handle, Token: "shared-tok"},
					ExpiresAt: time.Now().Add(time.Hour),
				}
				if err := reg.RenewLocal(ctx, lease); err != nil {
					t.Errorf("RenewLocal: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()

	rec, err := reg.Get(ctx, handle)
	if err != nil {
		t.Fatalf("Get after concurrent renews: %v (record corrupted or lost)", err)
	}
	if rec.Local == nil {
		t.Fatalf("expected a local record after concurrent renews, got %+v", rec)
	}
}

// ─── Stale lease cleanup ─────────────────────────────────────────────────

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func TestStaleLeaseCleanupReapsOnlyExpired(t *testing.T) {
	dir := t.TempDir()
	clock := &fakeClock{now: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)}
	reg := NewFileRegistry(dir, WithClock(clock))
	ctx := context.Background()

	fresh := Handle("fresh-peer")
	stale := Handle("stale-peer")
	noLease := Handle("no-lease-peer")

	if err := reg.PublishLocal(ctx, LocalRecord{Handle: fresh, Status: "idle", SchemaVersion: CurrentSchemaVersion, Instance: InstanceID{Handle: fresh}}); err != nil {
		t.Fatalf("PublishLocal(fresh): %v", err)
	}
	if err := reg.RenewLocal(ctx, Lease{Instance: InstanceID{Handle: fresh}, ExpiresAt: clock.Now().Add(time.Hour)}); err != nil {
		t.Fatalf("RenewLocal(fresh): %v", err)
	}

	if err := reg.PublishLocal(ctx, LocalRecord{Handle: stale, Status: "idle", SchemaVersion: CurrentSchemaVersion, Instance: InstanceID{Handle: stale}}); err != nil {
		t.Fatalf("PublishLocal(stale): %v", err)
	}
	if err := reg.RenewLocal(ctx, Lease{Instance: InstanceID{Handle: stale}, ExpiresAt: clock.Now().Add(time.Minute)}); err != nil {
		t.Fatalf("RenewLocal(stale): %v", err)
	}

	if err := reg.PublishLocal(ctx, LocalRecord{Handle: noLease, Status: "idle", SchemaVersion: CurrentSchemaVersion, Instance: InstanceID{Handle: noLease}}); err != nil {
		t.Fatalf("PublishLocal(noLease): %v", err)
	}

	remoteExpiring := Handle("remote-expiring~origin")
	if err := reg.UpsertRemote(ctx, AuthenticatedRemoteRecord{
		Handle: remoteExpiring, Status: "idle", LastSeenAt: clock.Now(), ExpiresAt: clock.Now().Add(2 * time.Minute),
		SchemaVersion: CurrentSchemaVersion,
		Provenance:    AuthenticatedProvenance{OriginHandle: string(remoteExpiring), SignerID: "signer"},
	}); err != nil {
		t.Fatalf("UpsertRemote(remoteExpiring): %v", err)
	}

	// Advance the clock past stale's and remoteExpiring's expiry, but not
	// past fresh's.
	clock.Advance(10 * time.Minute)

	removed, err := reg.ReapExpired(ctx)
	if err != nil {
		t.Fatalf("ReapExpired: %v", err)
	}
	removedSet := map[Handle]bool{}
	for _, h := range removed {
		removedSet[h] = true
	}
	if !removedSet[stale] {
		t.Errorf("expected stale-peer to be reaped, removed=%v", removed)
	}
	if !removedSet[remoteExpiring] {
		t.Errorf("expected remote-expiring~origin to be reaped, removed=%v", removed)
	}
	if removedSet[fresh] {
		t.Errorf("fresh-peer must NOT be reaped (lease not yet expired), removed=%v", removed)
	}
	if removedSet[noLease] {
		t.Errorf("no-lease-peer must NOT be reaped (no Lease.ExpiresAt set means never expires), removed=%v", removed)
	}

	if _, err := reg.Get(ctx, stale); !errors.Is(err, ErrNotFound) {
		t.Errorf("expected stale-peer Get to return ErrNotFound after reap, got %v", err)
	}
	if _, err := reg.Get(ctx, fresh); err != nil {
		t.Errorf("expected fresh-peer to still be present after reap, Get returned: %v", err)
	}
	if _, err := reg.Get(ctx, noLease); err != nil {
		t.Errorf("expected no-lease-peer to still be present after reap, Get returned: %v", err)
	}
}

// ─── Malformed / unknown-version rejection ──────────────────────────────

func TestDecodeLocalRejectsUnknownFutureSchemaVersion(t *testing.T) {
	future := `{"schema_version": 99, "handle": "future-peer", "status": "idle", "last_seen_at": "2024-01-01T00:00:00Z"}`
	_, err := DecodeLocal([]byte(future))
	if err == nil {
		t.Fatal("expected DecodeLocal to reject an unknown future schema_version, got nil error")
	}
	if !errors.Is(err, ErrUnknownSchemaVersion) {
		t.Fatalf("expected error wrapping ErrUnknownSchemaVersion, got: %v", err)
	}
}

func TestDecodeRemoteRejectsUnknownFutureSchemaVersion(t *testing.T) {
	future := `{"schema_version": 42, "handle": "x~y", "kind": "remote", "status": "idle", "last_seen_at": "2024-01-01T00:00:00Z", "expires_at": "2024-01-01T01:00:00Z"}`
	_, err := DecodeRemote([]byte(future))
	if err == nil {
		t.Fatal("expected DecodeRemote to reject an unknown future schema_version, got nil error")
	}
	if !errors.Is(err, ErrUnknownSchemaVersion) {
		t.Fatalf("expected error wrapping ErrUnknownSchemaVersion, got: %v", err)
	}
}

func TestDecodeLocalRejectsMalformedJSON(t *testing.T) {
	malformed := []byte(`{"handle": "broken", "status": `) // truncated, invalid JSON
	if _, err := DecodeLocal(malformed); err == nil {
		t.Fatal("expected DecodeLocal to reject malformed/truncated JSON")
	}
}

func TestDecodeFirstJSONTolerantOfTrailingGarbage(t *testing.T) {
	// Simulates a legacy non-atomic write that a shorter subsequent write
	// only partially overwrote, leaving trailing bytes from the old
	// content after the new, complete JSON object.
	data := []byte(`{"handle":"trailing-garbage-peer","status":"idle"}GARBAGE_TAIL_BYTES`)
	var out struct {
		Handle string `json:"handle"`
		Status string `json:"status"`
	}
	if err := DecodeFirstJSON(data, &out); err != nil {
		t.Fatalf("DecodeFirstJSON must tolerate trailing garbage after the first JSON object, got error: %v", err)
	}
	if out.Handle != "trailing-garbage-peer" || out.Status != "idle" {
		t.Fatalf("unexpected decode result: %+v", out)
	}

	// A plain json.Unmarshal, by contrast, must reject the same input --
	// this documents exactly why DecodeFirstJSON's streaming-decoder
	// strategy exists instead of a simpler json.Unmarshal call.
	var plain struct {
		Handle string `json:"handle"`
	}
	if err := json.Unmarshal(data, &plain); err == nil {
		t.Fatal("expected plain json.Unmarshal to reject trailing garbage (sanity check on the fixture itself)")
	}
}

func TestUpsertRemoteRejectsMissingProvenanceOrExpiry(t *testing.T) {
	dir := t.TempDir()
	reg := NewFileRegistry(dir)
	ctx := context.Background()

	noProvenance := AuthenticatedRemoteRecord{
		Handle: "no-provenance~origin", Status: "idle", LastSeenAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour),
	}
	if err := reg.UpsertRemote(ctx, noProvenance); !errors.Is(err, ErrRemoteRequiresProvenance) {
		t.Fatalf("expected ErrRemoteRequiresProvenance, got %v", err)
	}

	noExpiry := AuthenticatedRemoteRecord{
		Handle: "no-expiry~origin", Status: "idle", LastSeenAt: time.Now(),
		Provenance: AuthenticatedProvenance{OriginHandle: "no-expiry~origin", SignerID: "signer"},
	}
	if err := reg.UpsertRemote(ctx, noExpiry); !errors.Is(err, ErrRemoteRequiresExpiry) {
		t.Fatalf("expected ErrRemoteRequiresExpiry, got %v", err)
	}
}

func TestRenewLocalRejectsInstanceTokenMismatch(t *testing.T) {
	dir := t.TempDir()
	reg := NewFileRegistry(dir)
	ctx := context.Background()

	handle := Handle("guarded-peer")
	if err := reg.PublishLocal(ctx, LocalRecord{
		Handle: handle, Status: "idle", SchemaVersion: CurrentSchemaVersion,
		Instance: InstanceID{Handle: handle, Token: "correct-token"},
	}); err != nil {
		t.Fatalf("PublishLocal: %v", err)
	}

	err := reg.RenewLocal(ctx, Lease{
		Instance:  InstanceID{Handle: handle, Token: "wrong-token"},
		ExpiresAt: time.Now().Add(time.Hour),
	})
	if !errors.Is(err, ErrInstanceMismatch) {
		t.Fatalf("expected ErrInstanceMismatch for a mismatched renew token, got %v", err)
	}
}

func TestRemoveLocalRejectsInstanceTokenMismatch(t *testing.T) {
	dir := t.TempDir()
	reg := NewFileRegistry(dir)
	ctx := context.Background()

	handle := Handle("guarded-peer-2")
	if err := reg.PublishLocal(ctx, LocalRecord{
		Handle: handle, Status: "idle", SchemaVersion: CurrentSchemaVersion,
		Instance: InstanceID{Handle: handle, Token: "correct-token"},
	}); err != nil {
		t.Fatalf("PublishLocal: %v", err)
	}

	err := reg.RemoveLocal(ctx, InstanceID{Handle: handle, Token: "wrong-token"})
	if !errors.Is(err, ErrInstanceMismatch) {
		t.Fatalf("expected ErrInstanceMismatch for a mismatched remove token, got %v", err)
	}

	if _, getErr := reg.Get(ctx, handle); getErr != nil {
		t.Fatalf("record must still exist after a rejected mismatched RemoveLocal, Get returned: %v", getErr)
	}

	if err := reg.RemoveLocal(ctx, InstanceID{Handle: handle, Token: "correct-token"}); err != nil {
		t.Fatalf("RemoveLocal with correct token must succeed: %v", err)
	}
	if _, getErr := reg.Get(ctx, handle); !errors.Is(getErr, ErrNotFound) {
		t.Fatalf("expected ErrNotFound after correctly-authorized RemoveLocal, got %v", getErr)
	}
}

// ─── Basic Get/List behavior ─────────────────────────────────────────────

func TestGetReturnsErrNotFoundForAbsentHandle(t *testing.T) {
	reg := NewFileRegistry(t.TempDir())
	if _, err := reg.Get(context.Background(), "does-not-exist"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestListEmptyRegistryReturnsNoError(t *testing.T) {
	reg := NewFileRegistry(t.TempDir())
	records, err := reg.List(context.Background(), Filter{})
	if err != nil {
		t.Fatalf("List on empty/nonexistent registry root must not error, got: %v", err)
	}
	if len(records) != 0 {
		t.Fatalf("expected no records, got %d", len(records))
	}
}

func TestListFilterByKind(t *testing.T) {
	dir := t.TempDir()
	reg := NewFileRegistry(dir)
	ctx := context.Background()

	if err := reg.PublishLocal(ctx, LocalRecord{Handle: "local-only", Status: "idle", Instance: InstanceID{Handle: "local-only"}}); err != nil {
		t.Fatalf("PublishLocal: %v", err)
	}
	if err := reg.UpsertRemote(ctx, AuthenticatedRemoteRecord{
		Handle: "remote-only~origin", Status: "idle", LastSeenAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour),
		Provenance: AuthenticatedProvenance{OriginHandle: "remote-only~origin", SignerID: "signer"},
	}); err != nil {
		t.Fatalf("UpsertRemote: %v", err)
	}

	localOnly, err := reg.List(ctx, Filter{Kind: KindLocal})
	if err != nil {
		t.Fatalf("List(KindLocal): %v", err)
	}
	if len(localOnly) != 1 || localOnly[0].Handle != "local-only" {
		t.Fatalf("expected exactly one local record, got %+v", localOnly)
	}

	remoteOnly, err := reg.List(ctx, Filter{Kind: KindRemote})
	if err != nil {
		t.Fatalf("List(KindRemote): %v", err)
	}
	if len(remoteOnly) != 1 || remoteOnly[0].Handle != "remote-only~origin" {
		t.Fatalf("expected exactly one remote record, got %+v", remoteOnly)
	}
}
