// Package presence owns versioned local and remote presence records,
// validates them, manages leases, and provides atomic registry operations
// and queries. Presence reports discoverable facts; it does not decide
// lifecycle transitions or transport routes (docs/architecture/swarm-attach/
// package-boundaries.md, "## presence").
//
// This package is Phase 08 (package decomposition) worker P08.1's
// extraction target. Its Registry interface is FIXED byte-for-byte by
// .swarmflow/swarm-attach-architecture/p08-package-decomposition/CONTRACT.md
// section 3 and by package-boundaries.md's own "presence" section. Local and
// remote records are deliberately DIFFERENT types (LocalRecord vs
// AuthenticatedRemoteRecord) per that contract -- see
// TestRegistryLocalVsRemoteAreDistinctTypes in presence_test.go for a
// runtime-reflective proof that the Registry interface itself enforces this
// separation, not just a comment.
//
// Allowed dependencies (package-boundaries.md, verbatim): lifecycle value
// types, neutral identity/contract values, standard library, and an
// injected clock/filesystem boundary. This package currently imports only
// the Go standard library -- it does not yet need a lifecycle value type,
// and deliberately does NOT import internal/a2a, internal/daemon,
// attachclient, presentationcontrol, supervisor, concrete lan, CLI/TUI,
// gateway proxy, or engine packages (forbidden dependencies, same section).
//
// Extraction provenance: this package's LocalRecord/AuthenticatedRemoteRecord
// field shapes are modeled directly on internal/a2a/discovery.go's
// PeerPresence struct and its unmarshalPeerTolerant/writeFileAtomic
// persistence logic (schema version, canonical daemon/process identity,
// expiry/lease metadata, authenticated provenance for remote records) so
// this extraction is a genuine behavior-preserving move rather than an
// invented redesign -- see package-boundaries.md's "Current-source
// compatibility constraints" bullet on internal/a2a/discovery.go.
//
// discovery.go's PeerPresence type itself is NOT redefined in terms of
// this package's types in this phase: PeerPresence is referenced by name
// from many other internal/a2a files this worker's file allowlist forbids
// touching (registry_ops.go, lan_registry.go, lan_registry_v2.go,
// build_provenance.go, presence_version.go, card.go, peers.go, peer_mute.go,
// poller.go, hub.go, runtime.go, and the registryFS adapter feeding
// internal/inbox). Renaming or restructuring PeerPresence would ripple into
// every one of those out-of-scope files and violate CONTRACT.md's disjoint
// file-ownership rule and this worker's explicit "zero behavior change for
// discovery.go's 3 existing inbox/identity callers" invariant. Per this
// worker's own brief ("if you cannot achieve this without touching code
// outside your file/package list, STOP that specific sub-change and leave a
// clear TODO comment"), discovery.go instead delegates its one safely
// isolated presence-schema concern -- unmarshalPeerTolerant's tolerant/
// versioned decode logic -- to this package's DecodeFirstJSON, and carries a
// TODO citing this comment block for the remaining PeerPresence-wide
// redefinition, deferred to a later phase once presence's daemon/lan/a2a
// callers are themselves being migrated (package-boundaries.md's own
// extraction order places full internal/a2a migration off legacy presence
// well after this phase, gated on internal/lan's authenticated-parity work).
package presence

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ─── Core value types ───────────────────────────────────────────────────

// Handle is the stable, opaque presence-registry key for a record (matches
// discovery.go's PeerPresence.Handle / on-disk "{handle}.json" naming).
type Handle string

// InstanceID identifies one specific process instance publishing local
// presence, so RemoveLocal and lease renewal can be conditioned on matching
// instance identity rather than acting on Handle alone -- mirroring
// discovery.go's token-conditional RemovePeerIfInstance semantics (a2a
// InstanceToken/ProcessStart/Executable evidence, "missing evidence never
// authorizes ... automatic cleanup").
type InstanceID struct {
	Handle       Handle
	Token        string
	ProcessStart string
	Executable   string
	PID          int
}

// RecordKind distinguishes how a Record entered the registry, mirroring
// discovery.go's PeerType (PeerTypeLocal/PeerTypeDaemon/PeerTypeRemote).
type RecordKind string

const (
	KindLocal  RecordKind = "local"
	KindDaemon RecordKind = "daemon"
	KindRemote RecordKind = "remote"
)

// SchemaVersion identifies the on-disk/wire shape of a presence record,
// mirroring discovery.go's PresenceSchemaVersion (presence_version.go). A
// legacy record decoded without an explicit "schema_version" key naturally
// zero-values to SchemaVersionLegacy -- never an error.
type SchemaVersion int

const (
	// SchemaVersionLegacy is the implicit version carried by every presence
	// record written before schema versioning existed. Go's zero value for
	// SchemaVersion equals this constant.
	SchemaVersionLegacy SchemaVersion = 0

	// SchemaVersionBuildProvenance is the schema that adds a populated
	// BuildProvenance field, authoritative over the legacy BinaryModTime
	// wall-clock heuristic for same-build/different-build decisions.
	SchemaVersionBuildProvenance SchemaVersion = 1

	// CurrentSchemaVersion is the newest schema version this package
	// understands and the version it stamps on every record it writes.
	CurrentSchemaVersion = SchemaVersionBuildProvenance
)

// KnownOrLegacy reports whether v is a value this package understands:
// either the legacy default or a defined version at or below
// CurrentSchemaVersion. A record claiming a newer, unrecognized version is
// rejected by Decode{Local,Remote} rather than silently trusted.
func (v SchemaVersion) KnownOrLegacy() bool {
	return v >= SchemaVersionLegacy && v <= CurrentSchemaVersion
}

// BuildProvenance captures the identity of the binary that produced a local
// presence record. Mirrors discovery.go/build_provenance.go's BuildProvenance
// shape. Deliberately never included in AuthenticatedRemoteRecord: build/
// commit identity is local-presence-only, matching remotePresenceProjection's
// allow-list philosophy in discovery.go.
type BuildProvenance struct {
	Available   bool   `json:"available"`
	GoVersion   string `json:"go_version,omitempty"`
	MainVersion string `json:"main_version,omitempty"`
	VCSRevision string `json:"vcs_revision,omitempty"`
	VCSModified bool   `json:"vcs_modified,omitempty"`
}

// Lease is a local record's expiry/renewal metadata. RenewLocal refreshes
// it; List/ReapExpired treat a Lease whose ExpiresAt has passed as stale.
type Lease struct {
	Instance      InstanceID
	ExpiresAt     time.Time
	LastRenewedAt time.Time
}

// Expired reports whether the lease's ExpiresAt has passed as of now. A
// zero ExpiresAt means "no expiry" (never expires) -- matching local/daemon
// peers today, which are reaped by PID liveness rather than a lease clock.
func (l Lease) Expired(now time.Time) bool {
	return !l.ExpiresAt.IsZero() && now.After(l.ExpiresAt)
}

// LocalRecord is a locally published (same-machine) presence record --
// PublishLocal's input type. It is a distinct Go type from
// AuthenticatedRemoteRecord (package-boundaries.md: "Local and remote
// records are different types" -- do not collapse them).
type LocalRecord struct {
	Handle          Handle
	Name            string
	PID             int
	EndpointURL     string
	CardURL         string
	Workspace       string
	Model           string
	Status          string
	CurrentTask     string
	StartedAt       time.Time
	LastSeenAt      time.Time
	Kind            RecordKind // KindLocal or KindDaemon; empty defaults to KindLocal
	ControlSocket   string
	ServeURL        string
	Version         string
	BinaryModTime   time.Time
	SchemaVersion   SchemaVersion
	BuildProvenance BuildProvenance
	Instance        InstanceID
	Lease           Lease
}

// AuthenticatedProvenance is the authenticated origin evidence a remote
// record must carry before it may be upserted (package-boundaries.md
// invariant: "Remote records require authenticated provenance and expiry").
type AuthenticatedProvenance struct {
	// OriginHandle is the authenticated peer/opaque handle this record was
	// received from (mirrors discovery.go's opaqueRemoteHandle contract:
	// "Only a strict process-generated origin~alias handle ... is ever
	// preserved").
	OriginHandle string
	// SignerID identifies the authenticated channel/signer that vouched for
	// this record (for example a LAN gossip signing key identity).
	SignerID string
	// VerifiedAt is when this package's caller authenticated the record.
	VerifiedAt time.Time
}

// AuthenticatedRemoteRecord is an authenticated, cross-machine presence
// record -- UpsertRemote's input type. It intentionally exposes far fewer
// fields than LocalRecord: no ControlSocket, no BuildProvenance, no
// Instance evidence -- matching discovery.go's remotePresenceProjection
// fail-closed allow-list ("A local control socket is never copied into a
// remote record").
type AuthenticatedRemoteRecord struct {
	Handle        Handle
	Status        string
	LastSeenAt    time.Time
	ExpiresAt     time.Time
	SchemaVersion SchemaVersion
	Provenance    AuthenticatedProvenance
}

// Record is the read-model returned by Get/List: a kind-tagged view over
// exactly one of Local or Remote (never both), so callers never need to
// type-assert a union.
type Record struct {
	Handle     Handle
	Kind       RecordKind
	Status     string
	LastSeenAt time.Time
	Local      *LocalRecord
	Remote     *AuthenticatedRemoteRecord
}

// Filter narrows a List query. The zero Filter matches every non-expired
// record.
type Filter struct {
	// Kind restricts results to one RecordKind. Empty means "all kinds".
	Kind RecordKind
	// IncludeExpired, when true, includes remote records past ExpiresAt and
	// local records past their Lease's ExpiresAt that a plain List would
	// otherwise omit (List never deletes on the caller's behalf; only
	// ReapExpired does).
	IncludeExpired bool
}

// ─── Registry ───────────────────────────────────────────────────────────

// Registry is package-boundaries.md's exact target interface for this
// package (also fixed verbatim in this phase's CONTRACT.md section 3).
// Implementations own atomic local/remote registry operations and queries;
// they never expose registry paths, os.File, mutable maps, or LAN packets.
type Registry interface {
	PublishLocal(context.Context, LocalRecord) error
	RenewLocal(context.Context, Lease) error
	RemoveLocal(context.Context, InstanceID) error
	UpsertRemote(context.Context, AuthenticatedRemoteRecord) error
	Get(context.Context, Handle) (Record, error)
	List(context.Context, Filter) ([]Record, error)
}

// ─── Errors ─────────────────────────────────────────────────────────────

var (
	// ErrNotFound is returned by Get/RenewLocal when no record exists for
	// the requested handle.
	ErrNotFound = errors.New("presence: record not found")
	// ErrUnknownSchemaVersion is returned by Decode{Local,Remote} and by
	// PublishLocal/UpsertRemote when a record's SchemaVersion is newer than
	// CurrentSchemaVersion -- a future schema this build does not understand
	// is rejected rather than silently trusted or truncated.
	ErrUnknownSchemaVersion = errors.New("presence: unknown schema version")
	// ErrInstanceMismatch is returned by RenewLocal/RemoveLocal when the
	// caller-supplied instance token does not match the stored record's
	// token -- mirroring discovery.go's RemovePeerIfInstance token-conditional
	// cleanup ("missing legacy evidence denies destructive cleanup").
	ErrInstanceMismatch = errors.New("presence: instance identity mismatch")
	// ErrRemoteRequiresProvenance is returned by UpsertRemote when
	// Provenance.OriginHandle or Provenance.SignerID is empty.
	ErrRemoteRequiresProvenance = errors.New("presence: remote record requires authenticated provenance")
	// ErrRemoteRequiresExpiry is returned by UpsertRemote when ExpiresAt is
	// the zero value -- an unauthenticated-forever remote record is never
	// accepted.
	ErrRemoteRequiresExpiry = errors.New("presence: remote record requires expiry")
)

// ─── Clock and filesystem boundaries ───────────────────────────────────

// Clock is an injected time source (package-boundaries.md: "an injected
// clock/filesystem boundary"). Production code uses systemClock; tests use
// a fake to make lease-expiry assertions deterministic.
type Clock interface{ Now() time.Time }

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

// FileSystem is the injected persistence boundary FileRegistry uses for
// every read/write. Its WriteFileAtomic implementation MUST provide
// temp-file-in-the-same-directory + rename semantics -- see osFileSystem for
// the production implementation and discovery.go's pre-extraction
// writeFileAtomic for the behavior this mirrors.
type FileSystem interface {
	MkdirAll(dir string) error
	WriteFileAtomic(path string, data []byte) error
	ReadFile(path string) ([]byte, error)
	// ReadDir returns the base names of regular files (not subdirectories)
	// directly inside dir. A missing dir returns a *PathError satisfying
	// os.IsNotExist.
	ReadDir(dir string) ([]string, error)
	// Remove deletes path. Removing an already-absent path is not an error.
	Remove(path string) error
}

// osFileSystem is the production FileSystem: real files, real atomic
// rename. Every write goes through a temp file created in the SAME
// directory as the destination (so the rename is same-filesystem and
// therefore atomic on every platform this package targets), fsync'd and
// chmod'd to 0600 before the rename, with the directory itself best-effort
// fsync'd afterward for durability -- mirroring discovery.go's
// writeFileAtomic contract exactly.
type osFileSystem struct{}

func (osFileSystem) MkdirAll(dir string) error { return os.MkdirAll(dir, 0o700) }

func (osFileSystem) WriteFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".presence-*.tmp")
	if err != nil {
		return fmt.Errorf("presence: create temp file: %w", err)
	}
	tmpName := tmp.Name()
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(tmpName)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("presence: write temp file: %w", err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("presence: chmod temp file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("presence: sync temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("presence: close temp file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("presence: rename temp file into place: %w", err)
	}
	committed = true

	if dirHandle, err := os.Open(dir); err == nil {
		_ = dirHandle.Sync() // best-effort directory durability
		_ = dirHandle.Close()
	}
	return nil
}

func (osFileSystem) ReadFile(path string) ([]byte, error) { return os.ReadFile(path) }

func (osFileSystem) ReadDir(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		names = append(names, entry.Name())
	}
	return names, nil
}

func (osFileSystem) Remove(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// ─── Versioned codec ────────────────────────────────────────────────────

// DecodeFirstJSON decodes the first JSON object in data into v, tolerating
// trailing garbage left behind by a legacy non-atomic write that a shorter
// subsequent write only partially overwrote. Plain json.Unmarshal rejects
// such input ("invalid character after top-level value"); a streaming
// Decoder reads exactly one object and ignores anything after it.
//
// This is byte-for-byte the same decode strategy as discovery.go's
// pre-extraction unmarshalPeerTolerant, extracted here so discovery.go can
// delegate to it directly with zero behavior change.
func DecodeFirstJSON(data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	return dec.Decode(v)
}

// wireRecord is the on-disk/wire JSON envelope every persisted record
// (local or remote) is encoded as. schema_version is always stamped on
// write; a legacy file with no "schema_version" key decodes it as
// SchemaVersionLegacy (Go zero value) -- never an error.
type wireRecord struct {
	SchemaVersion SchemaVersion `json:"schema_version,omitempty"`
	Handle        Handle        `json:"handle"`
	Kind          RecordKind    `json:"kind,omitempty"`

	// Local-only fields (empty/zero on a remote record).
	Name            string          `json:"name,omitempty"`
	PID             int             `json:"pid,omitempty"`
	EndpointURL     string          `json:"endpoint_url,omitempty"`
	CardURL         string          `json:"card_url,omitempty"`
	Workspace       string          `json:"workspace,omitempty"`
	Model           string          `json:"model,omitempty"`
	CurrentTask     string          `json:"current_task,omitempty"`
	ControlSocket   string          `json:"control_socket,omitempty"`
	ServeURL        string          `json:"serve_url,omitempty"`
	Version         string          `json:"version,omitempty"`
	BinaryModTime   time.Time       `json:"binary_mtime,omitempty"`
	BuildProvenance BuildProvenance `json:"build_provenance,omitempty"`
	InstanceToken   string          `json:"instance_token,omitempty"`
	ProcessStart    string          `json:"process_start,omitempty"`
	Executable      string          `json:"executable,omitempty"`
	LeaseExpiresAt  time.Time       `json:"lease_expires_at,omitempty"`
	LeaseRenewedAt  time.Time       `json:"lease_renewed_at,omitempty"`

	// Shared fields.
	Status     string    `json:"status"`
	StartedAt  time.Time `json:"started_at,omitempty"`
	LastSeenAt time.Time `json:"last_seen_at"`

	// Remote-only fields (empty/zero on a local record).
	ExpiresAt            time.Time `json:"expires_at,omitempty"`
	ProvenanceOrigin     string    `json:"provenance_origin,omitempty"`
	ProvenanceSigner     string    `json:"provenance_signer,omitempty"`
	ProvenanceVerifiedAt time.Time `json:"provenance_verified_at,omitempty"`
}

// EncodeLocal marshals rec into this package's versioned on-disk JSON
// shape, always stamping rec.SchemaVersion (callers publishing a new
// record should set it to CurrentSchemaVersion).
func EncodeLocal(rec LocalRecord) ([]byte, error) {
	kind := rec.Kind
	if kind == "" {
		kind = KindLocal
	}
	w := wireRecord{
		SchemaVersion:   rec.SchemaVersion,
		Handle:          rec.Handle,
		Kind:            kind,
		Name:            rec.Name,
		PID:             rec.PID,
		EndpointURL:     rec.EndpointURL,
		CardURL:         rec.CardURL,
		Workspace:       rec.Workspace,
		Model:           rec.Model,
		Status:          rec.Status,
		CurrentTask:     rec.CurrentTask,
		StartedAt:       rec.StartedAt,
		LastSeenAt:      rec.LastSeenAt,
		ControlSocket:   rec.ControlSocket,
		ServeURL:        rec.ServeURL,
		Version:         rec.Version,
		BinaryModTime:   rec.BinaryModTime,
		BuildProvenance: rec.BuildProvenance,
		InstanceToken:   rec.Instance.Token,
		ProcessStart:    rec.Instance.ProcessStart,
		Executable:      rec.Instance.Executable,
		LeaseExpiresAt:  rec.Lease.ExpiresAt,
		LeaseRenewedAt:  rec.Lease.LastRenewedAt,
	}
	return json.MarshalIndent(w, "", "  ")
}

// DecodeLocal decodes data (tolerating trailing garbage, see
// DecodeFirstJSON) into a LocalRecord. It rejects a record whose
// SchemaVersion is newer than CurrentSchemaVersion with
// ErrUnknownSchemaVersion.
func DecodeLocal(data []byte) (LocalRecord, error) {
	var w wireRecord
	if err := DecodeFirstJSON(data, &w); err != nil {
		return LocalRecord{}, fmt.Errorf("presence: decode local record: %w", err)
	}
	if !w.SchemaVersion.KnownOrLegacy() {
		return LocalRecord{}, fmt.Errorf("%w: %d", ErrUnknownSchemaVersion, w.SchemaVersion)
	}
	kind := w.Kind
	if kind == "" {
		kind = KindLocal
	}
	return LocalRecord{
		Handle:          w.Handle,
		Name:            w.Name,
		PID:             w.PID,
		EndpointURL:     w.EndpointURL,
		CardURL:         w.CardURL,
		Workspace:       w.Workspace,
		Model:           w.Model,
		Status:          w.Status,
		CurrentTask:     w.CurrentTask,
		StartedAt:       w.StartedAt,
		LastSeenAt:      w.LastSeenAt,
		Kind:            kind,
		ControlSocket:   w.ControlSocket,
		ServeURL:        w.ServeURL,
		Version:         w.Version,
		BinaryModTime:   w.BinaryModTime,
		SchemaVersion:   w.SchemaVersion,
		BuildProvenance: w.BuildProvenance,
		Instance: InstanceID{
			Handle:       w.Handle,
			Token:        w.InstanceToken,
			ProcessStart: w.ProcessStart,
			Executable:   w.Executable,
			PID:          w.PID,
		},
		Lease: Lease{
			Instance:      InstanceID{Handle: w.Handle, Token: w.InstanceToken},
			ExpiresAt:     w.LeaseExpiresAt,
			LastRenewedAt: w.LeaseRenewedAt,
		},
	}, nil
}

// EncodeRemote marshals rec into this package's versioned on-disk JSON
// shape. Kind is always stamped KindRemote regardless of any caller-set
// zero value, since AuthenticatedRemoteRecord has no Kind field of its own.
func EncodeRemote(rec AuthenticatedRemoteRecord) ([]byte, error) {
	w := wireRecord{
		SchemaVersion:        rec.SchemaVersion,
		Handle:               rec.Handle,
		Kind:                 KindRemote,
		Status:               rec.Status,
		LastSeenAt:           rec.LastSeenAt,
		ExpiresAt:            rec.ExpiresAt,
		ProvenanceOrigin:     rec.Provenance.OriginHandle,
		ProvenanceSigner:     rec.Provenance.SignerID,
		ProvenanceVerifiedAt: rec.Provenance.VerifiedAt,
	}
	return json.MarshalIndent(w, "", "  ")
}

// DecodeRemote decodes data (tolerating trailing garbage) into an
// AuthenticatedRemoteRecord. It rejects an unknown future SchemaVersion the
// same way DecodeLocal does.
func DecodeRemote(data []byte) (AuthenticatedRemoteRecord, error) {
	var w wireRecord
	if err := DecodeFirstJSON(data, &w); err != nil {
		return AuthenticatedRemoteRecord{}, fmt.Errorf("presence: decode remote record: %w", err)
	}
	if !w.SchemaVersion.KnownOrLegacy() {
		return AuthenticatedRemoteRecord{}, fmt.Errorf("%w: %d", ErrUnknownSchemaVersion, w.SchemaVersion)
	}
	return AuthenticatedRemoteRecord{
		Handle:        w.Handle,
		Status:        w.Status,
		LastSeenAt:    w.LastSeenAt,
		ExpiresAt:     w.ExpiresAt,
		SchemaVersion: w.SchemaVersion,
		Provenance: AuthenticatedProvenance{
			OriginHandle: w.ProvenanceOrigin,
			SignerID:     w.ProvenanceSigner,
			VerifiedAt:   w.ProvenanceVerifiedAt,
		},
	}, nil
}

// decodeRecord decodes data into the Get/List read-model, dispatching to
// DecodeRemote or DecodeLocal based on the wire envelope's Kind.
func decodeRecord(data []byte) (Record, error) {
	var w wireRecord
	if err := DecodeFirstJSON(data, &w); err != nil {
		return Record{}, fmt.Errorf("presence: decode record: %w", err)
	}
	if !w.SchemaVersion.KnownOrLegacy() {
		return Record{}, fmt.Errorf("%w: %d", ErrUnknownSchemaVersion, w.SchemaVersion)
	}
	if w.Kind == KindRemote {
		remote, err := DecodeRemote(data)
		if err != nil {
			return Record{}, err
		}
		return Record{
			Handle:     remote.Handle,
			Kind:       KindRemote,
			Status:     remote.Status,
			LastSeenAt: remote.LastSeenAt,
			Remote:     &remote,
		}, nil
	}
	local, err := DecodeLocal(data)
	if err != nil {
		return Record{}, err
	}
	return Record{
		Handle:     local.Handle,
		Kind:       local.Kind,
		Status:     local.Status,
		LastSeenAt: local.LastSeenAt,
		Local:      &local,
	}, nil
}

// ─── FileRegistry: concrete Registry implementation ────────────────────

// FileRegistry is a concrete, file-backed Registry. Persistence mirrors
// discovery.go's pre-extraction atomic write/rename contract exactly: every
// mutating operation writes through FileSystem.WriteFileAtomic (temp file
// in the same directory + rename), and every operation on a given Handle is
// serialized by a per-handle in-process mutex (mirroring discovery.go's
// registryHandleMutex/sync.Map pattern) so concurrent updates to different
// handles proceed independently while updates to the SAME handle never
// interleave or lose each other's write.
type FileRegistry struct {
	root  string
	fs    FileSystem
	clock Clock

	mu          sync.Mutex // guards handleLocks map construction only
	handleLocks map[Handle]*sync.Mutex
}

// Option configures a FileRegistry constructed by NewFileRegistry.
type Option func(*FileRegistry)

// WithFileSystem overrides the default OS-backed FileSystem -- used by
// tests to inject a deterministic or fault-injecting filesystem.
func WithFileSystem(fs FileSystem) Option {
	return func(r *FileRegistry) { r.fs = fs }
}

// WithClock overrides the default system Clock -- used by tests to make
// lease-expiry assertions deterministic without sleeping.
func WithClock(c Clock) Option {
	return func(r *FileRegistry) { r.clock = c }
}

// NewFileRegistry constructs a FileRegistry persisting one JSON file per
// handle directly inside root (root/{handle}.json), matching discovery.go's
// flat per-handle-file layout.
func NewFileRegistry(root string, opts ...Option) *FileRegistry {
	r := &FileRegistry{
		root:        root,
		fs:          osFileSystem{},
		clock:       systemClock{},
		handleLocks: make(map[Handle]*sync.Mutex),
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

var _ Registry = (*FileRegistry)(nil)

func (r *FileRegistry) lockFor(h Handle) *sync.Mutex {
	r.mu.Lock()
	defer r.mu.Unlock()
	m, ok := r.handleLocks[h]
	if !ok {
		m = &sync.Mutex{}
		r.handleLocks[h] = m
	}
	return m
}

func (r *FileRegistry) pathFor(h Handle) string {
	return filepath.Join(r.root, string(h)+".json")
}

// PublishLocal validates and atomically writes rec, stamping LastSeenAt
// (and StartedAt, if unset) from the injected clock.
func (r *FileRegistry) PublishLocal(ctx context.Context, rec LocalRecord) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if rec.Handle == "" {
		return fmt.Errorf("presence: local record handle must not be empty")
	}
	if !rec.SchemaVersion.KnownOrLegacy() {
		return fmt.Errorf("%w: %d", ErrUnknownSchemaVersion, rec.SchemaVersion)
	}
	if rec.Kind == "" {
		rec.Kind = KindLocal
	}
	now := r.clock.Now()
	if rec.StartedAt.IsZero() {
		rec.StartedAt = now
	}
	rec.LastSeenAt = now
	if rec.Instance.Handle == "" {
		rec.Instance.Handle = rec.Handle
	}

	lock := r.lockFor(rec.Handle)
	lock.Lock()
	defer lock.Unlock()

	if err := r.fs.MkdirAll(r.root); err != nil {
		return fmt.Errorf("presence: create registry root: %w", err)
	}
	data, err := EncodeLocal(rec)
	if err != nil {
		return err
	}
	return r.fs.WriteFileAtomic(r.pathFor(rec.Handle), data)
}

// RenewLocal refreshes an existing local record's Lease and LastSeenAt. It
// fails with ErrInstanceMismatch if lease.Instance.Token is set and differs
// from the stored record's token, and ErrNotFound if no record exists yet.
func (r *FileRegistry) RenewLocal(ctx context.Context, lease Lease) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	handle := lease.Instance.Handle
	if handle == "" {
		return fmt.Errorf("presence: lease instance handle must not be empty")
	}

	lock := r.lockFor(handle)
	lock.Lock()
	defer lock.Unlock()

	path := r.pathFor(handle)
	data, err := r.fs.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return ErrNotFound
		}
		return fmt.Errorf("presence: read local record for renew: %w", err)
	}
	rec, err := DecodeLocal(data)
	if err != nil {
		return err
	}
	if rec.Instance.Token != "" && lease.Instance.Token != "" && rec.Instance.Token != lease.Instance.Token {
		return ErrInstanceMismatch
	}
	rec.LastSeenAt = r.clock.Now()
	lease.LastRenewedAt = rec.LastSeenAt
	rec.Lease = lease
	newData, err := EncodeLocal(rec)
	if err != nil {
		return err
	}
	return r.fs.WriteFileAtomic(path, newData)
}

// RemoveLocal deletes the local record for id.Handle. A non-empty stored
// instance token must match id.Token, mirroring discovery.go's
// RemovePeerIfInstance token-conditional cleanup ("missing legacy evidence
// denies destructive cleanup"). Removing an absent record is not an error
// (idempotent).
func (r *FileRegistry) RemoveLocal(ctx context.Context, id InstanceID) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if id.Handle == "" {
		return fmt.Errorf("presence: instance handle must not be empty")
	}

	lock := r.lockFor(id.Handle)
	lock.Lock()
	defer lock.Unlock()

	path := r.pathFor(id.Handle)
	data, err := r.fs.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("presence: read local record for remove: %w", err)
	}
	rec, err := DecodeLocal(data)
	if err != nil {
		return err
	}
	if rec.Instance.Token != "" && id.Token != "" && rec.Instance.Token != id.Token {
		return ErrInstanceMismatch
	}
	return r.fs.Remove(path)
}

// UpsertRemote validates rec's authenticated-provenance and expiry
// invariants, then atomically writes it. Callers (for example internal/lan)
// must have already authenticated rec's origin before calling this -- this
// package only enforces that the required evidence fields are non-empty, it
// does not itself verify a signature.
func (r *FileRegistry) UpsertRemote(ctx context.Context, rec AuthenticatedRemoteRecord) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if rec.Handle == "" {
		return fmt.Errorf("presence: remote record handle must not be empty")
	}
	if rec.Provenance.OriginHandle == "" || rec.Provenance.SignerID == "" {
		return ErrRemoteRequiresProvenance
	}
	if rec.ExpiresAt.IsZero() {
		return ErrRemoteRequiresExpiry
	}
	if !rec.SchemaVersion.KnownOrLegacy() {
		return fmt.Errorf("%w: %d", ErrUnknownSchemaVersion, rec.SchemaVersion)
	}

	lock := r.lockFor(rec.Handle)
	lock.Lock()
	defer lock.Unlock()

	if err := r.fs.MkdirAll(r.root); err != nil {
		return fmt.Errorf("presence: create registry root: %w", err)
	}
	data, err := EncodeRemote(rec)
	if err != nil {
		return err
	}
	return r.fs.WriteFileAtomic(r.pathFor(rec.Handle), data)
}

// Get returns the current Record for h, or ErrNotFound if absent.
func (r *FileRegistry) Get(ctx context.Context, h Handle) (Record, error) {
	if err := ctx.Err(); err != nil {
		return Record{}, err
	}
	if h == "" {
		return Record{}, fmt.Errorf("presence: handle must not be empty")
	}

	lock := r.lockFor(h)
	lock.Lock()
	defer lock.Unlock()

	data, err := r.fs.ReadFile(r.pathFor(h))
	if err != nil {
		if os.IsNotExist(err) {
			return Record{}, ErrNotFound
		}
		return Record{}, fmt.Errorf("presence: read record: %w", err)
	}
	return decodeRecord(data)
}

// List returns every record matching filter. A record whose expiry has
// passed is skipped unless filter.IncludeExpired is true; List itself never
// deletes anything (use ReapExpired for that).
func (r *FileRegistry) List(ctx context.Context, filter Filter) ([]Record, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	names, err := r.fs.ReadDir(r.root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("presence: list registry directory: %w", err)
	}

	now := r.clock.Now()
	var out []Record
	for _, name := range names {
		if filepath.Ext(name) != ".json" {
			continue
		}
		handle := Handle(strings.TrimSuffix(name, ".json"))
		lock := r.lockFor(handle)
		lock.Lock()
		data, readErr := r.fs.ReadFile(filepath.Join(r.root, name))
		lock.Unlock()
		if readErr != nil {
			continue // skip unreadable/removed-between-list-and-read entries
		}
		rec, decodeErr := decodeRecord(data)
		if decodeErr != nil {
			continue // skip malformed/unknown-version entries
		}
		if !filter.IncludeExpired && recordExpired(rec, now) {
			continue
		}
		if filter.Kind != "" && rec.Kind != filter.Kind {
			continue
		}
		out = append(out, rec)
	}
	return out, nil
}

// ReapExpired removes every record whose expiry (remote ExpiresAt, or
// local Lease.ExpiresAt) has passed as of the registry's clock, and returns
// the handles removed. A local record with no Lease set (ExpiresAt zero)
// never expires via this path -- matching today's PID-liveness-based local
// reaping, which this package does not itself perform (that remains
// discovery.go's isProcessAlive-based ListPeers behavior; a Lease is this
// package's OWN, independent expiry primitive for callers that opt in).
func (r *FileRegistry) ReapExpired(ctx context.Context) ([]Handle, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	names, err := r.fs.ReadDir(r.root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("presence: list registry directory: %w", err)
	}

	now := r.clock.Now()
	var removed []Handle
	for _, name := range names {
		if filepath.Ext(name) != ".json" {
			continue
		}
		handle := Handle(strings.TrimSuffix(name, ".json"))
		lock := r.lockFor(handle)
		lock.Lock()
		path := filepath.Join(r.root, name)
		data, readErr := r.fs.ReadFile(path)
		if readErr != nil {
			lock.Unlock()
			continue
		}
		rec, decodeErr := decodeRecord(data)
		if decodeErr != nil {
			lock.Unlock()
			continue
		}
		if recordExpired(rec, now) {
			_ = r.fs.Remove(path)
			removed = append(removed, handle)
		}
		lock.Unlock()
	}
	return removed, nil
}

// recordExpired reports whether rec's expiry evidence has passed as of now.
func recordExpired(rec Record, now time.Time) bool {
	switch {
	case rec.Remote != nil && !rec.Remote.ExpiresAt.IsZero():
		return now.After(rec.Remote.ExpiresAt)
	case rec.Local != nil && !rec.Local.Lease.ExpiresAt.IsZero():
		return now.After(rec.Local.Lease.ExpiresAt)
	default:
		return false
	}
}
