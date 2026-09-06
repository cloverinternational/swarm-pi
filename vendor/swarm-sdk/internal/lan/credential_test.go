package lan

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func writeCredentialFixture(t *testing.T, mode os.FileMode, value any) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "gateway-credentials.json")
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, mode); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadCredentialAuthoritySecureVersionedStorage(t *testing.T) {
	now := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	path := writeCredentialFixture(t, 0o600, credentialFile{
		Version: 1,
		Credentials: []CredentialRecord{{
			ID: "phone", Secret: "high-entropy-secret", Revision: 1,
			ExpiresAt: now.Add(time.Hour),
		}},
	})
	a, err := LoadCredentialAuthority(path, now)
	if err != nil {
		t.Fatalf("LoadCredentialAuthority: %v", err)
	}
	got := a.Authenticate("high-entropy-secret", now)
	if got.State != CredentialValid || got.Binding.ID != "phone" || got.Binding.Revision != 1 {
		t.Fatalf("decision = %+v", got)
	}
	if got := a.Authenticate("wrong", now).State; got != CredentialInvalid {
		t.Fatalf("wrong secret state = %s", got)
	}
}

func TestSecureCredentialFilePlatformContract(t *testing.T) {
	path := writeCredentialFixture(t, 0o600, credentialFile{
		Version: 1, Credentials: []CredentialRecord{{ID: "id", Secret: "secret", Revision: 1}},
	})

	f, err := openSecureCredentialFile(path)
	if runtime.GOOS == "windows" {
		if f != nil {
			f.Close()
			t.Fatal("Windows secure credential open returned a file without ACL validation")
		}
		if !errors.Is(err, ErrCredentialStorage) {
			t.Fatalf("Windows secure credential open err = %v, want storage denial", err)
		}
		return
	}
	if err != nil {
		t.Fatalf("secure credential open: %v", err)
	}
	if f == nil {
		t.Fatal("secure credential open returned a nil file")
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close secure credential file: %v", err)
	}
}

func TestLoadCredentialAuthorityRejectsUnsafeAndUnknownState(t *testing.T) {
	now := time.Now()
	t.Run("permissive file", func(t *testing.T) {
		path := writeCredentialFixture(t, 0o644, credentialFile{
			Version: 1, Credentials: []CredentialRecord{{ID: "id", Secret: "secret", Revision: 1}},
		})
		if _, err := LoadCredentialAuthority(path, now); !errors.Is(err, ErrCredentialStorage) {
			t.Fatalf("err = %v, want storage denial", err)
		}
	})
	t.Run("symlink", func(t *testing.T) {
		target := writeCredentialFixture(t, 0o600, credentialFile{
			Version: 1, Credentials: []CredentialRecord{{ID: "id", Secret: "secret", Revision: 1}},
		})
		dir := t.TempDir()
		if err := os.Chmod(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(dir, "credentials.json")
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadCredentialAuthority(link, now); !errors.Is(err, ErrCredentialStorage) {
			t.Fatalf("err = %v, want symlink denial", err)
		}
	})
	t.Run("unknown version", func(t *testing.T) {
		path := writeCredentialFixture(t, 0o600, credentialFile{
			Version: 2, Credentials: []CredentialRecord{{ID: "id", Secret: "secret", Revision: 1}},
		})
		if _, err := LoadCredentialAuthority(path, now); !errors.Is(err, ErrCredentialSchema) {
			t.Fatalf("err = %v, want schema denial", err)
		}
	})
	t.Run("no currently valid credential", func(t *testing.T) {
		path := writeCredentialFixture(t, 0o600, credentialFile{
			Version: 1,
			Credentials: []CredentialRecord{{
				ID: "id", Secret: "secret", Revision: 1, ExpiresAt: now.Add(-time.Second),
			}},
		})
		if _, err := LoadCredentialAuthority(path, now); !errors.Is(err, ErrNoValidCredential) {
			t.Fatalf("err = %v, want no-valid-credential denial", err)
		}
	})
}

func TestCredentialAuthorityRotationOverlapRevocationAndNotification(t *testing.T) {
	now := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	old := CredentialRecord{
		ID: "client", Secret: "old", Revision: 1,
		ExpiresAt: now.Add(time.Hour), OverlapUntil: now.Add(10 * time.Minute),
	}
	a, err := NewMemoryCredentialAuthority([]CredentialRecord{old})
	if err != nil {
		t.Fatal(err)
	}
	binding := a.Authenticate("old", now).Binding
	changed := a.Changes()
	current := CredentialRecord{ID: "client", Secret: "new", Revision: 2, ExpiresAt: now.Add(time.Hour)}
	if err := a.Replace([]CredentialRecord{old, current}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-changed:
	default:
		t.Fatal("authority change did not notify active bindings")
	}
	if got := a.ValidateBinding(binding, now.Add(5*time.Minute)); got != CredentialValid {
		t.Fatalf("old binding inside overlap = %s", got)
	}
	if got := a.ValidateBinding(binding, now.Add(10*time.Minute)); got != CredentialRevoked {
		t.Fatalf("old binding after overlap = %s", got)
	}
	current.Revoked = true
	if err := a.Replace([]CredentialRecord{current}); err != nil {
		t.Fatal(err)
	}
	if got := a.Authenticate("new", now).State; got != CredentialRevoked {
		t.Fatalf("revoked current credential = %s", got)
	}
}
