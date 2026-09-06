package chat

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	tea "charm.land/bubbletea/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/builtin"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/vault"
)

var (
	errVaultUnlockCanceled = errors.New("vault unlock canceled")
	errVaultUnlockBusy     = errors.New("another interactive dialog is active")
)

type vaultUnlockRequest struct {
	ID    string
	Tool  string
	Scope string
}

type vaultUnlockRequestMsg struct {
	request vaultUnlockRequest
}

type vaultUnlockResolvedMsg struct {
	requestID string
}

type vaultUnlockWaiter struct {
	result chan error
}

// VaultUnlockBroker bridges a vault tool goroutine to the Bubble Tea event
// loop. Requests are serialized so concurrent tools share the first successful
// unlock instead of opening competing passphrase prompts.
type VaultUnlockBroker struct {
	mu       sync.Mutex
	waiters  map[string]*vaultUnlockWaiter
	dispatch func(tea.Msg)
	serial   chan struct{}
	nextID   atomic.Uint64
}

func NewVaultUnlockBroker() *VaultUnlockBroker {
	return &VaultUnlockBroker{
		waiters: make(map[string]*vaultUnlockWaiter),
		serial:  make(chan struct{}, 1),
	}
}

func (b *VaultUnlockBroker) SetDispatcher(dispatch func(tea.Msg)) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.dispatch = dispatch
}

func (b *VaultUnlockBroker) Request(ctx context.Context, tool, scope string) error {
	select {
	case b.serial <- struct{}{}:
		defer func() { <-b.serial }()
	case <-ctx.Done():
		return ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	if vault.IsProviderScopeEnabled(
		vault.GetDefaultVaultProvider(),
		vault.CredentialScope(scope),
	) {
		return nil
	}

	req := vaultUnlockRequest{
		ID:    fmt.Sprintf("vault-unlock-%d", b.nextID.Add(1)),
		Tool:  tool,
		Scope: scope,
	}
	waiter := &vaultUnlockWaiter{result: make(chan error, 1)}

	b.mu.Lock()
	dispatch := b.dispatch
	if dispatch != nil {
		b.waiters[req.ID] = waiter
	}
	b.mu.Unlock()
	if dispatch == nil {
		return fmt.Errorf("vault unlock dispatcher unavailable")
	}

	dispatch(vaultUnlockRequestMsg{request: req})
	select {
	case err := <-waiter.result:
		return err
	case <-ctx.Done():
		b.mu.Lock()
		delete(b.waiters, req.ID)
		dispatch = b.dispatch
		b.mu.Unlock()
		if dispatch != nil {
			dispatch(vaultUnlockResolvedMsg{requestID: req.ID})
		}
		return ctx.Err()
	}
}

// UnlockVault implements builtin.VaultUnlocker.
func (b *VaultUnlockBroker) UnlockVault(
	ctx context.Context,
	req builtin.VaultUnlockRequest,
) (builtin.VaultUnlockResult, error) {
	err := b.Request(ctx, req.Operation, req.Scope)
	if errors.Is(err, errVaultUnlockCanceled) {
		return builtin.VaultUnlockResult{Unlocked: false}, nil
	}
	return builtin.VaultUnlockResult{Unlocked: err == nil}, err
}

func (b *VaultUnlockBroker) Respond(requestID string, err error) error {
	b.mu.Lock()
	waiter, ok := b.waiters[requestID]
	if ok {
		delete(b.waiters, requestID)
	}
	b.mu.Unlock()
	if !ok {
		return fmt.Errorf("unknown vault unlock request: %s", requestID)
	}
	select {
	case waiter.result <- err:
	default:
	}
	return nil
}
