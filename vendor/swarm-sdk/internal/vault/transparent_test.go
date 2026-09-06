package vault

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestTransparentStorageMutationsDoNotDeadlock(t *testing.T) {
	store, err := NewTransparentStorage(filepath.Join(t.TempDir(), "credentials.json"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	cred := Credential{
		ID:     "token",
		Kind:   CredentialKindBearerToken,
		Secret: "not-a-real-token",
		Scope:  ScopeGlobal,
		Inject: InjectConfig{Method: InjectEnv, Target: "TOKEN"},
	}

	runWithoutDeadlock(t, "Store", func() error {
		return store.Store(ctx, cred)
	})
	runWithoutDeadlock(t, "UpdateLastUsed", func() error {
		return store.UpdateLastUsed(ctx, cred.ID)
	})
	runWithoutDeadlock(t, "Delete", func() error {
		return store.Delete(ctx, cred.ID)
	})
}

func runWithoutDeadlock(t *testing.T, operation string, fn func() error) {
	t.Helper()
	done := make(chan error, 1)
	go func() {
		done <- fn()
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("%s: %v", operation, err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("%s deadlocked", operation)
	}
}
