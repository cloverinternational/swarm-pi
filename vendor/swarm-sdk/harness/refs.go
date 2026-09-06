package harness

import (
	"os"
)

// Ref is a typed indirect reference to a value that lives outside the manifest.
// Exactly one field may be set. It is used for credentials today and is the
// single extension point for future prompt/file/vault indirection.
//
//   - Env:    read from a single, explicitly named environment variable.
//   - File:   read from a manifest-relative file (containment-guarded).
//   - Vault:  a named vault entry; NOT resolved to a value in Phase 1 (deferred).
//   - Inline: a literal value embedded in the manifest (discouraged for secrets).
type Ref struct {
	Env    string `json:"env,omitempty"`
	File   string `json:"file,omitempty"`
	Vault  string `json:"vault,omitempty"`
	Inline string `json:"inline,omitempty"`
}

// refKind names which single field is populated.
type refKind string

const (
	refNone   refKind = ""
	refEnv    refKind = "env"
	refFile   refKind = "file"
	refVault  refKind = "vault"
	refInline refKind = "inline"
)

// kind returns the single populated field, or an error count if the reference is
// under- or over-specified.
func (r *Ref) kind() (refKind, int) {
	var set []refKind
	if r.Env != "" {
		set = append(set, refEnv)
	}
	if r.File != "" {
		set = append(set, refFile)
	}
	if r.Vault != "" {
		set = append(set, refVault)
	}
	if r.Inline != "" {
		set = append(set, refInline)
	}
	if len(set) == 0 {
		return refNone, 0
	}
	return set[0], len(set)
}

// redactedRefLabel is a non-secret provenance token describing where a value
// came from. It never contains the value itself, only the reference name.
func (r *Ref) redactedRefLabel() string {
	k, _ := r.kind()
	switch k {
	case refEnv:
		return "env:" + r.Env
	case refFile:
		return "file:" + r.File
	case refVault:
		return "vault:" + r.Vault
	case refInline:
		return "inline"
	default:
		return "unset"
	}
}

// Secret is a tainted string. Its value is never serialized, logged, or hashed
// into any explained plan. The zero value is an unresolved secret.
//
// Provenance (Source) is safe to display: it names the reference (for example
// "env:ANTHROPIC_API_KEY") but not the value. Deferred secrets (for example a
// vault reference in Phase 1) are Present==false with Deferred==true.
type Secret struct {
	present  bool
	deferred bool
	value    string
	source   string
}

// Present reports whether a concrete value was resolved.
func (s Secret) Present() bool { return s.present }

// Deferred reports whether resolution was intentionally deferred to a later
// phase (for example an unresolved vault reference).
func (s Secret) Deferred() bool { return s.deferred }

// Source returns the non-secret provenance label (for example "env:NAME").
func (s Secret) Source() string { return s.source }

// Reveal returns the underlying value. This is the ONLY way to obtain it and is
// intended for a trusted later-phase consumer (the client constructor). It is
// deliberately verbose so call sites are auditable.
func (s Secret) Reveal() (value string, present bool) { return s.value, s.present }

// String implements fmt.Stringer with a redacted rendering.
func (s Secret) String() string {
	if s.present {
		return redactedPlaceholder
	}
	if s.deferred {
		return "[deferred]"
	}
	return "[unset]"
}

// MarshalJSON always redacts. A Secret can never leak through JSON output.
func (s Secret) MarshalJSON() ([]byte, error) {
	return []byte(`"` + s.String() + `"`), nil
}

// resolveSecret turns a credential-style Ref into a tainted Secret, reading only
// the explicitly named environment variable (no scanning, no fallback). Env and
// inline resolve to a value; vault is deferred; file reads a contained asset.
// Returns a diagnostic when the reference is invalid or unresolvable.
func resolveSecret(sourcePath, fieldPath, manifestDir string, r *Ref) (Secret, *Diagnostic) {
	if r == nil {
		return Secret{}, nil
	}
	k, n := r.kind()
	if k == refNone {
		d := newDiag("harness.ref.empty", fieldPath, "reference is empty; set exactly one of env/file/vault/inline", sourcePath)
		return Secret{}, &d
	}
	if n > 1 {
		d := newDiag("harness.ref.multiple", fieldPath, "reference over-specified; set exactly one of env/file/vault/inline", sourcePath)
		return Secret{}, &d
	}

	switch k {
	case refEnv:
		v, ok := os.LookupEnv(r.Env)
		if !ok {
			d := newDiag("harness.ref.envMissing", fieldPath, "environment variable "+quote(r.Env)+" is not set", sourcePath)
			return Secret{}, &d
		}
		return Secret{present: true, value: v, source: "env:" + r.Env}, nil
	case refInline:
		return Secret{present: true, value: r.Inline, source: "inline"}, nil
	case refVault:
		// Vault resolution is a later-phase concern; carry the reference only.
		return Secret{present: false, deferred: true, source: "vault:" + r.Vault}, nil
	case refFile:
		abs, derr := resolveContainedFile(sourcePath, fieldPath, manifestDir, r.File)
		if derr != nil {
			return Secret{}, derr
		}
		data, err := os.ReadFile(abs)
		if err != nil {
			d := newDiag("harness.ref.fileUnreadable", fieldPath, "credential file could not be read", sourcePath)
			return Secret{}, &d
		}
		return Secret{present: true, value: string(data), source: "file:" + r.File}, nil
	default:
		d := newDiag("harness.ref.unknown", fieldPath, "unknown reference kind", sourcePath)
		return Secret{}, &d
	}
}
