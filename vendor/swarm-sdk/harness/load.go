package harness

import (
	"os"
	"path/filepath"
)

// DefaultManifestName is the exact local manifest looked up when no explicit
// path is given. Discovery NEVER walks parent directories.
const DefaultManifestName = "harness.yaml"

// discoverManifest implements explicit path discovery:
//
//   - a non-empty explicitPath always wins (used verbatim);
//   - otherwise exactly "./harness.yaml" is used IF it exists;
//   - parents are never walked and no legacy store is merged.
//
// It returns the chosen path (not yet read). A missing default file yields a
// not-found diagnostic rather than a silent fallback.
func discoverManifest(explicitPath string) (string, *Diagnostic) {
	if explicitPath != "" {
		return explicitPath, nil
	}
	if _, err := os.Stat(DefaultManifestName); err == nil {
		return DefaultManifestName, nil
	}
	d := newDiag("harness.load.notFound", "",
		"no harness path given and "+quote(DefaultManifestName)+" was not found in the current directory (parents are never searched)", "")
	return "", &d
}

// Load discovers, reads, and strict-decodes a harness Document. It performs no
// resolution and no side effects beyond reading the single manifest file. The
// returned source path is the file that was actually read.
func Load(explicitPath string) (doc *Document, sourcePath string, err error) {
	path, d := discoverManifest(explicitPath)
	if d != nil {
		return nil, "", Diagnostics{*d}
	}

	raw, rerr := os.ReadFile(path)
	if rerr != nil {
		if os.IsNotExist(rerr) {
			return nil, path, Diagnostics{newDiag("harness.load.notFound", "",
				"harness file does not exist", path)}
		}
		return nil, path, Diagnostics{newDiag("harness.load.unreadable", "",
			"harness file could not be read", path)}
	}

	parsed, ds := strictDecode(path, raw)
	if ds.HasErrors() {
		return nil, path, ds
	}
	return parsed, path, nil
}

// manifestDir returns the absolute directory containing the manifest, used as
// the root for manifest-relative asset resolution.
func manifestDir(sourcePath string) (string, error) {
	abs, err := filepath.Abs(sourcePath)
	if err != nil {
		return "", err
	}
	return filepath.Dir(abs), nil
}
