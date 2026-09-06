//go:build unix

package runtime

import (
	"errors"
	"os"
	"testing"
)

func TestLoadRejectsBroadPermissions(t *testing.T) {
	store := testStore(t)
	if err := os.WriteFile(store.paths.State, []byte(`{"version":1,"manager_epoch":1,"claims":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(store.paths.State, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LoadState(); !errors.Is(err, ErrInsecureStorage) {
		t.Fatalf("LoadState error = %v, want ErrInsecureStorage", err)
	}
}
