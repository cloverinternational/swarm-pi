// Package configformat is the single source of truth for how swarm config
// files are serialized on disk. It lets every config surface read either JSON
// or YAML transparently and write YAML by default, WITHOUT requiring any
// `yaml:` struct tags.
//
// # Why sigs.k8s.io/yaml
//
// The config structs (SwarmOSConfig, ConfigBundle, core.Config, ...) are tagged
// only with `json:`. The popular gopkg.in/yaml.v3 keys off `yaml:` tags or the
// lowercased Go field name, so it would silently rename `SchemaVersion
// json:"schemaVersion"` to `schemaversion:` and break round-trips. sigs.k8s.io/yaml
// instead marshals Go -> JSON (honoring the json tags) -> YAML, and unmarshals
// YAML -> JSON -> Go. Because YAML 1.2 is a JSON superset, the same code path
// also parses legacy `.json` files. Result: one codec for both formats, zero
// struct retagging, identical omitempty / no-omitempty-bool / time.Time
// behavior to encoding/json.
//
// # Format policy
//
//   - Read precedence: baseName.yaml -> baseName.yml -> baseName.json
//   - Write default: baseName.yaml
//   - Existing .json files are NEVER deleted; once a .yaml exists it wins by
//     precedence (the .json is left in place, shadowed, for easy rollback).
package configformat

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	sigsyaml "sigs.k8s.io/yaml"
)

// Format identifies an on-disk serialization.
type Format int

const (
	// FormatJSON is the legacy format. Read+write go through encoding/json so
	// error messages and strictness are byte-for-byte what they were before.
	FormatJSON Format = iota
	// FormatYAML is the new default format. Read+write go through
	// sigs.k8s.io/yaml, which honors json struct tags.
	FormatYAML
)

// configExts lists the recognized config extensions in READ precedence order.
// YAML wins because it is the new default; .json is the legacy fallback.
var configExts = []string{".yaml", ".yml", ".json"}

// FormatForPath maps a file path to its Format by extension. Anything that is
// not a recognized YAML extension is treated as JSON (the safe legacy default).
func FormatForPath(path string) Format {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".yaml", ".yml":
		return FormatYAML
	default:
		return FormatJSON
	}
}

// BaseName strips a recognized config extension from a file name/path, returning
// the extension-less base (e.g. "bundle.json" -> "bundle", "config" -> "config").
func BaseName(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	for _, e := range configExts {
		if ext == e {
			return strings.TrimSuffix(path, filepath.Ext(path))
		}
	}
	return path
}

// Marshal encodes v in the requested format.
//
// JSON uses encoding/json.MarshalIndent (2-space) to keep legacy output stable.
// YAML uses sigs.k8s.io/yaml, which produces block-style YAML keyed on the json
// tag names.
func Marshal(v any, format Format) ([]byte, error) {
	switch format {
	case FormatYAML:
		return sigsyaml.Marshal(v)
	default:
		return json.MarshalIndent(v, "", "  ")
	}
}

// Unmarshal decodes data (in the given format) into v. Both branches honor the
// json struct tags. The JSON branch routes straight to encoding/json so strict
// callers keep their exact behavior.
func Unmarshal(data []byte, format Format, v any) error {
	switch format {
	case FormatYAML:
		return sigsyaml.Unmarshal(data, v)
	default:
		return json.Unmarshal(data, v)
	}
}

// ToJSON converts config bytes of any supported format to canonical JSON. It
// lets JSON-only strict decoders (e.g. DisallowUnknownFields validators) accept
// YAML without being rewritten: convert first, then feed the existing decoder.
// For an already-JSON input the bytes are returned unchanged so strict decoding
// sees the original document (not a re-canonicalized one).
func ToJSON(data []byte, format Format) ([]byte, error) {
	if format == FormatJSON {
		return data, nil
	}
	return sigsyaml.YAMLToJSON(data)
}

// ResolvePath probes dir/baseName.{yaml,yml,json} in precedence order and
// returns the first path that exists with found=true. baseName MUST be
// extension-less. If none exist it returns the canonical write target
// (dir/baseName.yaml) with found=false, so callers keep their existing
// lazy-create / default-without-save behavior.
func ResolvePath(dir, baseName string) (path string, found bool) {
	for _, ext := range configExts {
		p := filepath.Join(dir, baseName+ext)
		if _, err := os.Stat(p); err == nil {
			return p, true
		}
	}
	return WritePath(dir, baseName), false
}

// WritePath returns the canonical write target for a baseName: dir/baseName.yaml.
func WritePath(dir, baseName string) string {
	return filepath.Join(dir, baseName+".yaml")
}

// Load resolves dir/baseName by read precedence, reads the file, and unmarshals
// into v using the format implied by the resolved extension. If no file exists
// it returns a path-wrapped os.ErrNotExist so callers can keep their
// default-without-save behavior. It returns the resolved path so callers (e.g.
// an mtime cache) can record exactly which file was read.
func Load(dir, baseName string, v any) (resolvedPath string, err error) {
	path, found := ResolvePath(dir, baseName)
	if !found {
		return path, &os.PathError{Op: "open", Path: path, Err: os.ErrNotExist}
	}
	if err := LoadFile(path, v); err != nil {
		return path, err
	}
	return path, nil
}

// LoadFile reads an explicit path and unmarshals it by its extension. Useful for
// the direct-os.ReadFile call sites that already hold a full path.
func LoadFile(path string, v any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := Unmarshal(data, FormatForPath(path), v); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	return nil
}

type saveOpts struct {
	preserveFormat bool
	atomic         bool
}

// SaveOption tunes Save behavior.
type SaveOption func(*saveOpts)

// PreserveFormat keeps writing the same extension that was read (no migration to
// YAML). Used for files shared with external consumers that cannot read YAML yet
// (e.g. providers.json read raw by the TypeScript desktop app).
func PreserveFormat() SaveOption { return func(o *saveOpts) { o.preserveFormat = true } }

// Atomic writes via a temp file + rename in the same directory.
func Atomic() SaveOption { return func(o *saveOpts) { o.atomic = true } }

// Save writes v for dir/baseName. By default it writes baseName.yaml (migrating
// on next save) and leaves any existing baseName.json untouched on disk —
// precedence makes the new .yaml authoritative while the .json remains for
// rollback. With PreserveFormat() it writes back to whichever extension already
// exists (or .json if none and the caller's surface is JSON-pinned). Returns the
// path actually written.
func Save(dir, baseName string, v any, mode os.FileMode, opts ...SaveOption) (writtenPath string, err error) {
	var o saveOpts
	for _, opt := range opts {
		opt(&o)
	}

	target := WritePath(dir, baseName) // default: baseName.yaml
	if o.preserveFormat {
		if existing, found := ResolvePath(dir, baseName); found {
			target = existing
		} else {
			// No file yet and caller wants to preserve a JSON surface.
			target = filepath.Join(dir, baseName+".json")
		}
	}

	data, err := Marshal(v, FormatForPath(target))
	if err != nil {
		return "", err
	}
	if err := writeFile(target, data, mode, o.atomic); err != nil {
		return "", err
	}
	return target, nil
}

// SaveAs writes v to an explicit path, choosing the codec by the path's
// extension. It does not apply precedence/migration — the caller fully controls
// the destination. Used by surfaces that track an exact file path.
func SaveAs(path string, v any, mode os.FileMode, opts ...SaveOption) error {
	var o saveOpts
	for _, opt := range opts {
		opt(&o)
	}
	data, err := Marshal(v, FormatForPath(path))
	if err != nil {
		return err
	}
	return writeFile(path, data, mode, o.atomic)
}

func writeFile(path string, data []byte, mode os.FileMode, atomic bool) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if !atomic {
		return os.WriteFile(path, data, mode)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
