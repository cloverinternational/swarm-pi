package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/paths"
)

// TestDaemonLogPath_RootedUnderCanonicalRoot is a regression test for the
// bug where daemonLogPath() resolved a divergent "~/.swarmos/logs" root
// instead of this repo's single canonical on-disk root, paths.Root()
// (~/.swarm, honoring SWARM_HOME). It asserts the returned path is rooted
// under paths.Root() and never contains a literal ".swarmos" path segment.
func TestDaemonLogPath_RootedUnderCanonicalRoot(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("SWARM_HOME", filepath.Join(tmpHome, ".swarm"))

	got := daemonLogPath()

	root := paths.Root()
	rootWithSep := root + string(os.PathSeparator)
	if !strings.HasPrefix(got, rootWithSep) {
		t.Fatalf("daemonLogPath() = %q, want a path rooted under paths.Root() = %q", got, root)
	}

	if strings.Contains(got, ".swarmos") {
		t.Fatalf("daemonLogPath() = %q must never resolve under a literal \".swarmos\" path segment (regression: this is exactly the bug being fixed)", got)
	}

	if filepath.Base(got) != "daemon.log" {
		t.Fatalf("daemonLogPath() = %q, want base name %q", got, "daemon.log")
	}
}

// TestDaemonLogPath_NeverLiteralSwarmOSRoot guards against regressing to the
// old hardcoded "<home>/.swarmos/logs" resolution directly, independent of
// paths.Root()'s current value, by checking the well-known legacy
// suffix never appears in the returned path.
func TestDaemonLogPath_NeverLiteralSwarmOSRoot(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("SWARM_HOME", filepath.Join(tmpHome, ".swarm"))

	got := daemonLogPath()

	legacySuffix := filepath.Join(".swarmos", "logs", "daemon.log")
	if strings.HasSuffix(got, legacySuffix) {
		t.Fatalf("daemonLogPath() = %q resolved under the legacy .swarmos root; must resolve under paths.Root() instead", got)
	}
}
