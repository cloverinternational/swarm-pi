package serve

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/client"
)

// client.setWorkspace must be registered so a thin UI / the one global daemon
// can re-scope the engine to the directory the user opened (new conversations
// scope to the client's workspace).
func TestSetWorkspace_Registered(t *testing.T) {
	m := newTestMux()
	if _, ok := m.Lookup("client.setWorkspace"); !ok {
		t.Fatal("client.setWorkspace must be a registered method")
	}
}

func TestSetWorkspace_SetsAndReturnsWorkspace(t *testing.T) {
	m := NewMux(&client.Client{})
	ctx := context.Background()
	dir := t.TempDir()

	raw := json.RawMessage(`{"dir":` + strconv.Quote(dir) + `}`)
	res, err := m.Dispatch(ctx, "client.setWorkspace", raw)
	if err != nil {
		t.Fatalf("dispatch setWorkspace: %v", err)
	}
	got, ok := res.(map[string]any)
	if !ok {
		t.Fatalf("expected map result, got %T", res)
	}
	if got["workspace"] != dir {
		t.Errorf("workspace = %v, want %q", got["workspace"], dir)
	}
}

func TestSetWorkspace_RejectsEmptyDir(t *testing.T) {
	m := NewMux(&client.Client{})
	if _, err := m.Dispatch(context.Background(), "client.setWorkspace", json.RawMessage(`{"dir":""}`)); err == nil {
		t.Error("expected error for empty dir")
	}
	if _, err := m.Dispatch(context.Background(), "client.setWorkspace", json.RawMessage(`{"dir":"   "}`)); err == nil {
		t.Error("expected error for whitespace-only dir")
	}
}
