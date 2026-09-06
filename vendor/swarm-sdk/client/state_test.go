// Package client — state_test.go
//
// Tests migrated from session/client_session_test.go.  These exercise the
// session-equivalent surface of *client.Client (Start/Stop, Subscribe,
// Snapshot, SetMode/Model/Provider, SwitchProfile, applyAgentUpdate)
// directly against the package, allowing access to private helpers.
package client

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/configbundle"
)

// newSessClient returns a bare-bones *Client suitable for testing
// lifecycle/subscribe/snapshot/mode/profile changes without any provider
// configured.
func newSessClient(t *testing.T) *Client {
	t.Helper()
	c := &Client{}
	c.SetSessionState("act", "", "")
	return c
}

// newRealSessClient builds a real *Client configured with a dummy
// provider/API key so methods that call Reconfigure (SetModel/SetProvider/
// SwitchProfile) succeed without hitting the network.
func newRealSessClient(t *testing.T, cfgMgr *configbundle.Manager, initialMode, initialAgent, initialProfile string) *Client {
	t.Helper()
	opts := []Option{
		WithoutAutoConfig(),
		WithProvider("anthropic", "claude-sonnet-4-5"),
		WithAPIKey("dummy"),
	}
	if cfgMgr != nil {
		opts = append(opts, WithConfigManager(cfgMgr))
	}
	if initialMode != "" {
		opts = append(opts, WithActiveMode(initialMode))
	}
	if initialAgent != "" {
		opts = append(opts, WithActiveAgent(initialAgent))
	}
	if initialProfile != "" {
		opts = append(opts, WithActiveProfile(initialProfile))
	}
	c, err := New(opts...)
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	if initialMode != "" || initialAgent != "" || initialProfile != "" {
		mode := initialMode
		if mode == "" {
			mode = "act"
		}
		c.SetSessionState(mode, initialAgent, initialProfile)
	}
	return c
}

func TestClient_DefaultMode(t *testing.T) {
	c := &Client{}
	c.SetSessionState("", "", "")
	if got := c.Snapshot().OperatingMode; got != "act" {
		t.Errorf("default mode: got %q want act", got)
	}
}

func TestClient_HonoursInitialFields(t *testing.T) {
	c := &Client{}
	c.SetSessionState("plan", "alpha", "fast")
	st := c.Snapshot()
	if st.OperatingMode != "plan" {
		t.Errorf("OperatingMode: got %q want plan", st.OperatingMode)
	}
	if st.ActiveAgent != "alpha" {
		t.Errorf("ActiveAgent: got %q want alpha", st.ActiveAgent)
	}
	if st.ActiveProfile != "fast" {
		t.Errorf("ActiveProfile: got %q want fast", st.ActiveProfile)
	}
}

func TestClient_SyncActiveConversationIsImmediatelyVisible(t *testing.T) {
	c := newSessClient(t)
	before := c.Snapshot().UpdatedAt

	c.SyncActiveConversation("  conv-validated  ")

	if got := c.ActiveConversation(); got != "conv-validated" {
		t.Fatalf("ActiveConversation: got %q want %q", got, "conv-validated")
	}
	snapshot := c.Snapshot()
	if snapshot.ActiveConvID != "conv-validated" {
		t.Fatalf("Snapshot.ActiveConvID: got %q want %q", snapshot.ActiveConvID, "conv-validated")
	}
	if snapshot.UpdatedAt.Before(before) || snapshot.UpdatedAt.IsZero() {
		t.Fatalf("UpdatedAt was not refreshed: before=%v after=%v", before, snapshot.UpdatedAt)
	}
}

func TestClient_RapidSetClearActiveConversationDoesNotRestoreStaleID(t *testing.T) {
	c := newSessClient(t)
	for i := 0; i < 1000; i++ {
		c.SyncActiveConversation("conv-stale")
		c.ClearActiveConversation()
	}

	if got := c.ActiveConversation(); got != "" {
		t.Fatalf("ActiveConversation after final clear: got %q", got)
	}
	if got := c.Snapshot().ActiveConvID; got != "" {
		t.Fatalf("Snapshot.ActiveConvID after final clear: got %q", got)
	}
}

func TestClient_StartStop(t *testing.T) {
	c := newSessClient(t)
	ctx := context.Background()

	preGoroutines := runtime.NumGoroutine()

	if err := c.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := c.Start(ctx); err != nil {
		t.Fatalf("Start (second call): %v", err)
	}

	if err := c.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if err := c.Stop(ctx); err != nil {
		t.Fatalf("Stop (second call): %v", err)
	}

	time.Sleep(20 * time.Millisecond)
	postGoroutines := runtime.NumGoroutine()
	if delta := postGoroutines - preGoroutines; delta > 2 {
		t.Errorf("goroutine leak: pre=%d post=%d delta=%d", preGoroutines, postGoroutines, delta)
	}
}

func TestClient_SubscribeFanout(t *testing.T) {
	c := newSessClient(t)
	if err := c.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer c.Stop(context.Background())

	var aCount, bCount, cCount atomic.Int32
	unsubA := c.Subscribe(func(ev Event) error { aCount.Add(1); return nil })
	unsubB := c.Subscribe(func(ev Event) error { bCount.Add(1); return nil })
	unsubC := c.Subscribe(func(ev Event) error { cCount.Add(1); return nil })

	defer unsubA()
	defer unsubB()
	defer unsubC()

	if err := c.SetMode(context.Background(), "auto"); err != nil {
		t.Fatalf("SetMode: %v", err)
	}

	if got := aCount.Load(); got != 1 {
		t.Errorf("subscriber A: got %d want 1", got)
	}
	if got := bCount.Load(); got != 1 {
		t.Errorf("subscriber B: got %d want 1", got)
	}
	if got := cCount.Load(); got != 1 {
		t.Errorf("subscriber C: got %d want 1", got)
	}

	unsubB()
	if err := c.SetMode(context.Background(), "act"); err != nil {
		t.Fatalf("SetMode: %v", err)
	}
	if got := aCount.Load(); got != 2 {
		t.Errorf("after unsub B, A: got %d want 2", got)
	}
	if got := bCount.Load(); got != 1 {
		t.Errorf("after unsub B, B: got %d want 1 (no new events)", got)
	}
	if got := cCount.Load(); got != 2 {
		t.Errorf("after unsub B, C: got %d want 2", got)
	}
}

func TestClient_SubscribeNilHandlerIsNoop(t *testing.T) {
	c := newSessClient(t)
	unsub := c.Subscribe(nil)
	if unsub == nil {
		t.Fatal("Subscribe(nil) returned nil unsubscribe")
	}
	unsub()
}

func TestClient_UnsubscribeIdempotent(t *testing.T) {
	c := newSessClient(t)
	unsub := c.Subscribe(func(Event) error { return nil })
	unsub()
	unsub()
}

func TestClient_HandlerErrorDoesNotBlockOthers(t *testing.T) {
	c := newSessClient(t)
	if err := c.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer c.Stop(context.Background())

	var goodCount atomic.Int32
	c.Subscribe(func(ev Event) error { return errors.New("bad") })
	c.Subscribe(func(ev Event) error { goodCount.Add(1); return nil })

	if err := c.SetMode(context.Background(), "plan"); err != nil {
		t.Fatalf("SetMode: %v", err)
	}
	if got := goodCount.Load(); got != 1 {
		t.Errorf("good subscriber should still fire after a failing one; got %d want 1", got)
	}
}

func TestClient_SetMode_UpdatesStateAndDispatches(t *testing.T) {
	c := newSessClient(t)
	if err := c.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer c.Stop(context.Background())

	got := captureClientFirstEvent(t, c, func() {
		if err := c.SetMode(context.Background(), "plan"); err != nil {
			t.Fatalf("SetMode: %v", err)
		}
	})

	if got.Kind != EventModeChanged {
		t.Errorf("Kind: got %q want %q", got.Kind, EventModeChanged)
	}
	mp, ok := got.Payload.(ModePayload)
	if !ok {
		t.Fatalf("Payload type: got %T want ModePayload", got.Payload)
	}
	if mp.Mode != "plan" {
		t.Errorf("ModePayload.Mode: got %q want plan", mp.Mode)
	}
	if st := c.Snapshot(); st.OperatingMode != "plan" {
		t.Errorf("Snapshot.OperatingMode: got %q want plan", st.OperatingMode)
	}
}

func TestClient_SetMode_RequiresMode(t *testing.T) {
	c := newSessClient(t)
	if err := c.SetMode(context.Background(), ""); err == nil {
		t.Fatal("expected error for empty mode")
	}
}

func TestClient_SetModel_UpdatesStateAndDispatches(t *testing.T) {
	c := newRealSessClient(t, nil, "act", "", "")
	if err := c.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer c.Stop(context.Background())

	got := captureClientFirstEvent(t, c, func() {
		if err := c.SetModel(context.Background(), "claude-haiku-4-5"); err != nil {
			t.Fatalf("SetModel: %v", err)
		}
	})

	if got.Kind != EventModelChanged {
		t.Errorf("Kind: got %q want %q", got.Kind, EventModelChanged)
	}
	mp, ok := got.Payload.(ModelPayload)
	if !ok {
		t.Fatalf("Payload type: got %T want ModelPayload", got.Payload)
	}
	if mp.Model != "claude-haiku-4-5" {
		t.Errorf("ModelPayload.Model: got %q want claude-haiku-4-5", mp.Model)
	}
	if mp.Provider != "anthropic" {
		t.Errorf("ModelPayload.Provider: got %q want anthropic (preserved)", mp.Provider)
	}
	st := c.Snapshot()
	if st.Model != "claude-haiku-4-5" {
		t.Errorf("Snapshot.Model: got %q want claude-haiku-4-5", st.Model)
	}
}

func TestClient_SetProvider_UpdatesStateAndDispatches(t *testing.T) {
	c := newRealSessClient(t, nil, "act", "", "")
	if err := c.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer c.Stop(context.Background())

	got := captureClientFirstEvent(t, c, func() {
		if err := c.SetProvider(context.Background(), "anthropic", "claude-opus-4-5"); err != nil {
			t.Fatalf("SetProvider: %v", err)
		}
	})

	mp, ok := got.Payload.(ModelPayload)
	if !ok {
		t.Fatalf("Payload type: got %T want ModelPayload", got.Payload)
	}
	if mp.Provider != "anthropic" || mp.Model != "claude-opus-4-5" {
		t.Errorf("Payload: got %+v", mp)
	}
	st := c.Snapshot()
	if st.Provider != "anthropic" || st.Model != "claude-opus-4-5" {
		t.Errorf("Snapshot: got provider=%q model=%q", st.Provider, st.Model)
	}
}

func TestClient_SwitchProfile_UpdatesStateAndDispatches(t *testing.T) {
	tmpDir := t.TempDir()
	cfgMgr, err := configbundle.NewManager(context.Background(), configbundle.Options{
		WorkDir:    tmpDir,
		GlobalPath: tmpDir + "/config.json",
	})
	if err != nil {
		t.Fatalf("configbundle.NewManager: %v", err)
	}
	bundle := cfgMgr.Active()
	bundle.Profiles.Inline = []configbundle.ProfileDefinition{{
		ID:       "fast",
		Name:     "fast",
		Provider: "anthropic",
		Model:    "claude-haiku-4-5",
	}}

	c := newRealSessClient(t, cfgMgr, "act", "", "")
	if err := c.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer c.Stop(context.Background())

	got := captureClientFirstEvent(t, c, func() {
		if err := c.SwitchProfile(context.Background(), "fast"); err != nil {
			t.Fatalf("SwitchProfile: %v", err)
		}
	})

	if got.Kind != EventProfileChanged {
		t.Errorf("Kind: got %q want %q", got.Kind, EventProfileChanged)
	}
	pp, ok := got.Payload.(ProfilePayload)
	if !ok {
		t.Fatalf("Payload type: got %T want ProfilePayload", got.Payload)
	}
	if pp.ProfileID != "fast" {
		t.Errorf("ProfilePayload.ProfileID: got %q want fast", pp.ProfileID)
	}
	st := c.Snapshot()
	if st.ActiveProfile != "fast" {
		t.Errorf("Snapshot.ActiveProfile: got %q want fast", st.ActiveProfile)
	}
	if st.Model != "claude-haiku-4-5" {
		t.Errorf("Snapshot.Model: got %q want claude-haiku-4-5 (from profile)", st.Model)
	}
}

func TestClient_SwitchProfile_RequiresConfigManager(t *testing.T) {
	c := newRealSessClient(t, nil, "act", "", "")
	if err := c.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer c.Stop(context.Background())

	if err := c.SwitchProfile(context.Background(), "any"); !errors.Is(err, ErrNoConfigManager) {
		t.Errorf("SwitchProfile without ConfigManager: got %v want ErrNoConfigManager", err)
	}
}

func TestClient_SwitchProfile_ProfileNotFound(t *testing.T) {
	tmpDir := t.TempDir()
	cfgMgr, err := configbundle.NewManager(context.Background(), configbundle.Options{
		WorkDir:    tmpDir,
		GlobalPath: tmpDir + "/config.json",
	})
	if err != nil {
		t.Fatalf("configbundle.NewManager: %v", err)
	}

	c := newRealSessClient(t, cfgMgr, "act", "", "")
	if err := c.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer c.Stop(context.Background())

	if err := c.SwitchProfile(context.Background(), "nonexistent"); err == nil {
		t.Fatal("SwitchProfile with unknown profile: expected error, got nil")
	}
}

func TestClient_Cancel_BeforeAnyTurn_IsNoop(t *testing.T) {
	c := newSessClient(t)
	if err := c.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer c.Stop(context.Background())

	if err := c.Cancel(context.Background()); err != nil {
		t.Errorf("Cancel: got %v want nil", err)
	}
}

func TestClient_Snapshot_IsImmutable(t *testing.T) {
	c := newSessClient(t)
	if err := c.SetMode(context.Background(), "plan"); err != nil {
		t.Fatalf("SetMode: %v", err)
	}
	st := c.Snapshot()
	st.OperatingMode = "auto"
	if got := c.Snapshot().OperatingMode; got != "plan" {
		t.Errorf("snapshot was not immutable; got %q want plan", got)
	}
}

func TestClient_ApplyAgentUpdate_RecordsTokenCounts(t *testing.T) {
	c := newSessClient(t)
	c.applyAgentUpdate(agent.TokenCountUpdate{InputTokens: 1000, OutputTokens: 200})
	st := c.Snapshot()
	if st.InputTokens != 1000 {
		t.Errorf("InputTokens: got %d want 1000", st.InputTokens)
	}
	if st.OutputTokens != 200 {
		t.Errorf("OutputTokens: got %d want 200", st.OutputTokens)
	}
	if st.TotalTokens != 1200 {
		t.Errorf("TotalTokens: got %d want 1200", st.TotalTokens)
	}
}

func TestClient_StopReleasesSubscribers(t *testing.T) {
	c := newSessClient(t)
	if err := c.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	var fired atomic.Int32
	c.Subscribe(func(Event) error { fired.Add(1); return nil })

	if err := c.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if err := c.SetMode(context.Background(), "plan"); err != nil {
		t.Fatalf("SetMode: %v", err)
	}
	if got := fired.Load(); got != 0 {
		t.Errorf("subscriber fired after Stop: got %d want 0", got)
	}
}

func TestClient_ActiveAgent_ReadFromState(t *testing.T) {
	c := &Client{}
	c.SetSessionState("act", "engineer", "")
	if got := c.ActiveAgent(); got != "engineer" {
		t.Errorf("ActiveAgent: got %q want engineer", got)
	}
}

// captureClientFirstEvent installs a handler, runs do(), and returns the first
// event that fires.
func captureClientFirstEvent(t *testing.T, c *Client, do func()) Event {
	t.Helper()
	ch := make(chan Event, 4)
	var once sync.Once
	unsub := c.Subscribe(func(ev Event) error {
		once.Do(func() { ch <- ev })
		return nil
	})
	defer unsub()
	do()
	select {
	case ev := <-ch:
		return ev
	case <-time.After(time.Second):
		t.Fatal("captureClientFirstEvent: timed out waiting for event")
		return Event{}
	}
}
