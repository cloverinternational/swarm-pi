# journalredact

`swarm-sdk/internal/journalredact` — the secret-masking and path-sanitizing
redaction engine used by the execution journal, per
`docs/architecture/swarm-attach/adr-006-execution-journal-privacy.md`'s
"Redaction and access" section and the
`.swarmflow/swarm-attach-architecture/p05-til-execution-journal/CONTRACT.md`
"Shared type seam" for this package.

## Purpose

A dependency-free leaf that turns raw, potentially sensitive task-field text
and local filesystem paths into their sanitized, journal-safe string forms
before they are ever handed to `journal.Writer`. It masks recognized secret
shapes (API keys, `Authorization` header values, cookies, PEM private-key
blocks, URLs with embedded userinfo credentials, and values whose field
names indicate passwords/secrets/credentials/tokens) with the literal
`"[REDACTED]"`; normalizes free text to valid UTF-8 and bounds it to 4 KiB
with a truncation marker and change-detection digest; and reduces local
absolute paths to a workspace-relative form or the literal `"[LOCAL_PATH]"`
when that reduction is not safe. Secret masking always runs before any
truncation and before any digest is computed, so a secret spanning the 4 KiB
cut point is never half-masked, and the appended digest never encodes raw
secret bytes even indirectly.

**Redaction here is defense in depth per ADR-006, not a guarantee that
arbitrary prose contains no personal information.** (Quoting ADR-006
verbatim: "Redaction is defense in depth, not a guarantee that arbitrary
prose contains no personal information. UI and API documentation must state
that task subjects and descriptions are retained in sanitized form.")

## Import constraint (hard boundary)

This package imports **only the Go standard library**. It MUST NOT import
`internal/journal`, `internal/journalstore`, `internal/attachcontract`, or
any other package in this repository, so that `internal/journal` (P05.A) can
depend on it without any import-cycle risk. Verify with:

```sh
go list -deps ./internal/journalredact | grep -v '^github.com/Swarm-Code/mono/swarm-sdk/internal/journalredact$' | grep '\.'
```

(the second `grep` selects import paths containing a dot, i.e. non-stdlib;
this command's output must be empty.)

## Exported surface

### redact.go

- `func RedactText(s string) string` — normalizes `s` to valid UTF-8, masks
  recognized secret shapes with `"[REDACTED]"`, then truncates to 4 KiB and
  appends a `" [TRUNCATED sha256:<hex>]"` marker (digest of the
  already-redacted text) when the masked result still exceeds that bound.
- `func RedactPath(path, workspaceRoot string) string` — returns the
  workspace-relative suffix when `path` is genuinely `workspaceRoot` itself
  or a descendant of it (a proper, separator-aware prefix check via
  `filepath.Clean`/`filepath.Rel` — a sibling directory that merely shares a
  *string* prefix with `workspaceRoot`, e.g. `workspaceRoot + "-evil"`, is
  correctly rejected); otherwise returns the literal `"[LOCAL_PATH]"`. Never
  returns a raw absolute path outside `workspaceRoot`.
- `func RedactFieldValue(fieldName, rawValue, workspaceRoot string) string`
  — dispatches one journaled task field's value to the right handling: a
  field name that itself case-insensitively indicates sensitive content
  (`"password"`, `"secret"`, `"credential"`, `"token"`, `"apikey"`, or
  `"api_key"`) is masked outright as `"[REDACTED]"` regardless of value
  shape; a path-like field name routes through `RedactPath`; any other
  (including unrecognized) field name routes through `RedactText`, so raw
  content is never passed through unredacted for an unknown field.
- `func DigestOf(s string) string` — the lowercase-hex SHA-256 digest of
  `s`, with no `sha256:`-style prefix (callers that want the label, like
  `RedactText`'s truncation marker, add it themselves). Used for
  change-detection only (e.g.
  `journal.TaskFieldChangedPayload.PreviousValueDigest`); it never stands in
  for a retrievable value.

### patterns.go

Package-level compiled `*regexp.Regexp` detectors (compiled once at package
init, not per call) covering OpenAI-shaped (`sk-...`), GitHub (`ghp_...`
and sibling `gh[oprsu]_` prefixes), Slack (`xox[baprs]-...`), AWS
(`AKIA...`), Stripe (`sk_live_...`/`pk_test_...`), and Google (`AIza...`)
API-key shapes; `Authorization: Bearer`/`Authorization: Basic` header
values; `Cookie:`/`Set-Cookie:` header values; PEM private-key blocks;
URLs with embedded userinfo credentials; and generic `name: value`/
`name=value` assignments where `name` is a common secret-ish keyword. Every
token-shaped quantifier is upper-bounded (not open-ended), so a detector
cannot over-consume unrelated adjacent text that happens to abut a
secret-shaped prefix with no delimiter. Also defines the unexported
`fieldNameIndicatesSecret`/`fieldNameIsPathLike` field-name heuristics that
`RedactFieldValue` uses, and the unexported `maskSecrets` helper `RedactText`
uses to apply every detector in order.

## Known limitation relative to ADR-006 (deferred this phase)

This package implements ADR-006's redaction *rules* only. It does not
implement ADR-006's crash-safe staged transaction, privacy-barrier rebase,
or dual head-anchor lineage — those are `internal/journalstore`'s (P05.C)
concern and are explicitly out of scope for this phase per
`p05-til-execution-journal/CONTRACT.md`'s "Deferred this phase" section.
This package has no persistence, no retention clock, and no knowledge of
journal transactions; it is a pure string-in/string-out sanitizer.

## Tests

`redact_test.go` is table-driven and non-tautological: it asserts exact
expected output strings (not merely "does not contain X"). Coverage
includes an API-key fixture, a `Bearer` token fixture, a PEM private-key
fixture, a cookie fixture, and a password-named-field fixture (each
asserted fully masked); a 5000-byte input asserted truncated with a valid
`sha256:` hex digest appended; a secret positioned to straddle the 4 KiB
truncation boundary, proving mask-before-truncate ordering; invalid UTF-8
input asserted to produce valid UTF-8 output; `RedactPath` cases for a
genuine workspace descendant, a same-string-prefix sibling directory, and an
unrelated absolute path; `RedactFieldValue` routing for path-like and
unknown field names; `DigestOf` determinism/format checks; and a
sentinel self-check (mirroring
`attachcontract/capabilities_test.go`'s `TestCompatibilityErrorNeverLeaksSecrets`
pattern) proving both that a literal marker embedded in a realistic API-key
fixture never appears in `RedactText`'s output, and that the assertion
mechanism itself would catch it if it did.
