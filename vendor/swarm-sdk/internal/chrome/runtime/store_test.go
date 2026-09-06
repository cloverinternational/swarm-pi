package runtime

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	dir := secureTestDir(t)
	store, err := NewStore(Paths{
		Lock:       filepath.Join(dir, "authority.lock"),
		State:      filepath.Join(dir, "authority.json"),
		Credential: filepath.Join(dir, "installation.json"),
	})
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func TestDefaultPathsStayUnderSwarmHome(t *testing.T) {
	root := secureTestDir(t)
	t.Setenv("SWARM_HOME", root)
	got := DefaultPaths()
	for _, path := range []string{got.Lock, got.State, got.Credential} {
		relative, err := filepath.Rel(root, path)
		if err != nil || relative == ".." || filepath.IsAbs(relative) {
			t.Fatalf("default path %q escaped root %q", path, root)
		}
	}
}

func validClaim(id string) Claim {
	return Claim{
		FamilyHash:     "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		Generation:     1,
		ClaimID:        id,
		InstallationID: "installation-1",
		Lifecycle:      "open",
		UpdatedAt:      time.Date(2026, 4, 14, 1, 2, 3, 0, time.UTC),
	}
}

func TestStoreRoundTripAndSeparateCredential(t *testing.T) {
	store := testStore(t)
	lock, err := store.Acquire()
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()

	state := State{Version: StateVersion, ManagerEpoch: 7, Claims: []Claim{validClaim("claim-1")}}
	if err := store.SaveState(lock, state); err != nil {
		t.Fatal(err)
	}
	credential := InstallationCredential{
		Version:        CredentialVersion,
		InstallationID: "installation-1",
		Secret:         "ZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZ",
		LoopbackPort:   43123,
	}
	if err := store.SaveCredential(lock, credential); err != nil {
		t.Fatal(err)
	}
	got, err := store.LoadState()
	if err != nil || got.ManagerEpoch != 7 || len(got.Claims) != 1 {
		t.Fatalf("LoadState = %+v, %v", got, err)
	}
	gotCredential, err := store.LoadCredential()
	if err != nil || gotCredential != credential {
		t.Fatalf("LoadCredential = %+v, %v", gotCredential, err)
	}
	stateData, err := os.ReadFile(store.paths.State)
	if err != nil {
		t.Fatal(err)
	}
	if stringContains(string(stateData), credential.Secret) {
		t.Fatal("non-secret state contains installation secret")
	}
	for _, path := range []string{store.paths.State, store.paths.Credential, store.paths.Lock} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("%s permissions = %04o, want 0600", path, info.Mode().Perm())
		}
	}
}

func TestStoreRequiresMatchingHeldLock(t *testing.T) {
	store := testStore(t)
	state := State{Version: StateVersion, ManagerEpoch: 1, Claims: []Claim{}}
	if err := store.SaveState(nil, state); !errors.Is(err, ErrLockNotHeld) {
		t.Fatalf("SaveState error = %v, want ErrLockNotHeld", err)
	}
	other := testStore(t)
	lock, err := other.Acquire()
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := store.SaveState(lock, state); !errors.Is(err, ErrLockNotHeld) {
		t.Fatalf("SaveState with wrong lock = %v, want ErrLockNotHeld", err)
	}
}

func TestAdvanceEpochIsMonotonicWithConcurrentCallers(t *testing.T) {
	store := testStore(t)
	lock, err := store.Acquire()
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()

	const calls = 32
	var wg sync.WaitGroup
	errs := make(chan error, calls)
	epochs := make(chan uint64, calls)
	for range calls {
		wg.Add(1)
		go func() {
			defer wg.Done()
			state, err := store.AdvanceEpoch(lock)
			if err != nil {
				errs <- err
				return
			}
			epochs <- state.ManagerEpoch
		}()
	}
	wg.Wait()
	close(errs)
	close(epochs)
	for err := range errs {
		t.Fatal(err)
	}
	seen := make(map[uint64]bool)
	for epoch := range epochs {
		seen[epoch] = true
	}
	for epoch := uint64(1); epoch <= calls; epoch++ {
		if !seen[epoch] {
			t.Fatalf("missing epoch %d from %v", epoch, seen)
		}
	}
	final, err := store.LoadState()
	if err != nil || final.ManagerEpoch != calls {
		t.Fatalf("final state = %+v, %v", final, err)
	}
}

func TestSaveStateRejectsEpochRollback(t *testing.T) {
	store := testStore(t)
	lock, err := store.Acquire()
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := store.SaveState(lock, State{Version: StateVersion, ManagerEpoch: 9, Claims: []Claim{}}); err != nil {
		t.Fatal(err)
	}
	err = store.SaveState(lock, State{Version: StateVersion, ManagerEpoch: 8, Claims: []Claim{}})
	if !errors.Is(err, ErrCorruptStore) {
		t.Fatalf("rollback error = %v, want ErrCorruptStore", err)
	}
}

func TestStrictDecodeAndValidation(t *testing.T) {
	tests := []string{
		`{"version":2,"manager_epoch":1,"claims":[]}`,
		`{"version":1,"manager_epoch":1,"claims":[],"capabilities":["forbidden"]}`,
		`{"version":1,"manager_epoch":1,"claims":[]} {}`,
		`{"version":1,"manager_epoch":1,"claims":[{"family_hash":"bad","generation":1,"claim_id":"c","installation_id":"i","lifecycle":"open","updated_at":"2026-04-14T00:00:00Z"}]}`,
	}
	for i, data := range tests {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			store := testStore(t)
			if err := os.WriteFile(store.paths.State, []byte(data), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := store.LoadState(); !errors.Is(err, ErrCorruptStore) {
				t.Fatalf("LoadState error = %v, want ErrCorruptStore", err)
			}
		})
	}
}

func TestLoadRejectsSymlink(t *testing.T) {
	store := testStore(t)
	target := filepath.Join(t.TempDir(), "target")
	if err := os.WriteFile(target, []byte(`{"version":1,"manager_epoch":1,"claims":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, store.paths.State); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := store.LoadState(); err == nil {
		t.Fatal("LoadState followed a symlink")
	}
}

func TestLoadRejectsOversizedStoreBeforeDecode(t *testing.T) {
	store := testStore(t)
	data := make([]byte, maxStoreFileBytes+1)
	for i := range data {
		data[i] = ' '
	}
	if err := os.WriteFile(store.paths.State, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LoadState(); !errors.Is(err, ErrCorruptStore) {
		t.Fatalf("LoadState error = %v, want ErrCorruptStore", err)
	}
}

func stringContains(value, substring string) bool {
	for i := 0; i+len(substring) <= len(value); i++ {
		if value[i:i+len(substring)] == substring {
			return true
		}
	}
	return false
}
