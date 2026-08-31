package autogenskills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInspectPlacementByOperation(t *testing.T) {
	t.Parallel()

	type setupFunc func(t *testing.T, active, archived string)
	mkdir := func(t *testing.T, path string) {
		t.Helper()
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	setups := map[string]setupFunc{
		"active only": func(t *testing.T, active, _ string) {
			mkdir(t, active)
		},
		"archive only": func(t *testing.T, _, archived string) {
			mkdir(t, archived)
		},
		"neither": func(t *testing.T, _, _ string) {},
		"both": func(t *testing.T, active, archived string) {
			mkdir(t, active)
			mkdir(t, archived)
		},
		"both with active symlink": func(t *testing.T, active, archived string) {
			target := filepath.Join(filepath.Dir(active), "symlink-target")
			mkdir(t, target)
			mkdir(t, archived)
			if err := os.Symlink(target, active); err != nil {
				t.Skipf("symlinks unavailable: %v", err)
			}
		},
		"both with archive file": func(t *testing.T, active, archived string) {
			mkdir(t, active)
			mkdir(t, filepath.Dir(archived))
			if err := os.WriteFile(archived, []byte("not a directory"), 0o644); err != nil {
				t.Fatal(err)
			}
		},
	}

	tests := []struct {
		name          string
		setup         string
		operation     placementOperation
		wantPlacement string
		wantRoot      string
		wantErr       bool
	}{
		{name: "read active only", setup: "active only", operation: placementOperationRead, wantPlacement: "active", wantRoot: "active"},
		{name: "mutation active only", setup: "active only", operation: placementOperationMutation, wantPlacement: "active", wantRoot: "active"},
		{name: "read archive only", setup: "archive only", operation: placementOperationRead, wantPlacement: "archived", wantRoot: "archived"},
		{name: "mutation archive only", setup: "archive only", operation: placementOperationMutation, wantPlacement: "archived", wantRoot: "archived"},
		{name: "read neither", setup: "neither", operation: placementOperationRead, wantPlacement: "absent"},
		{name: "mutation neither", setup: "neither", operation: placementOperationMutation, wantPlacement: "absent"},
		{name: "read both prefers active", setup: "both", operation: placementOperationRead, wantPlacement: "active", wantRoot: "active"},
		{name: "mutation both fails closed", setup: "both", operation: placementOperationMutation, wantErr: true},
		{name: "read both rejects active symlink", setup: "both with active symlink", operation: placementOperationRead, wantErr: true},
		{name: "mutation both rejects active symlink", setup: "both with active symlink", operation: placementOperationMutation, wantErr: true},
		{name: "read both rejects archive file", setup: "both with archive file", operation: placementOperationRead, wantErr: true},
		{name: "mutation both rejects archive file", setup: "both with archive file", operation: placementOperationMutation, wantErr: true},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			autogenDir := t.TempDir()
			active := filepath.Join(autogenDir, "example")
			archived := filepath.Join(autogenDir, "archive", "example")
			setups[test.setup](t, active, archived)

			placement, root, err := inspectPlacement(autogenDir, "example", test.operation)
			if test.wantErr {
				if err == nil {
					t.Fatalf("inspectPlacement() error = nil, want error")
				}
				if !strings.Contains(err.Error(), "remove") {
					t.Errorf("inspectPlacement() error %q does not explain how to reconcile the paths", err)
				}
				for _, path := range []string{active, archived} {
					if !strings.Contains(err.Error(), path) {
						t.Errorf("inspectPlacement() error %q does not name path %q", err, path)
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("inspectPlacement() error = %v", err)
			}
			if placement != test.wantPlacement {
				t.Errorf("inspectPlacement() placement = %q, want %q", placement, test.wantPlacement)
			}
			wantRoot := ""
			switch test.wantRoot {
			case "active":
				wantRoot = active
			case "archived":
				wantRoot = archived
			}
			if root != wantRoot {
				t.Errorf("inspectPlacement() root = %q, want %q", root, wantRoot)
			}
		})
	}
}
