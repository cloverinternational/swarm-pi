package runtime

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/chrome"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/chrome/bridge"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/chrome/protocol"
)

const (
	defaultIdleTimeout   = 30 * time.Minute
	defaultStartupGrace  = 20 * time.Second
	defaultRecoveryGrace = 5 * time.Second
	tokenBytes           = 24
)

// Config contains the process-wide Chrome authority's composition-root
// dependencies. Listener must already be bound to 127.0.0.1.
type Config struct {
	Store *Store
	// Listener is a test seam. Production callers should leave it nil so the
	// persisted loopback endpoint is bound by Listen.
	Listener      net.Listener
	Listen        func(network, address string) (net.Listener, error)
	Launcher      chrome.Launcher
	ExtensionID   string
	IdleTimeout   time.Duration
	StartupGrace  time.Duration
	RecoveryGrace time.Duration
	Clock         chrome.Clock
	Random        io.Reader

	BridgeQueueSize    int
	BridgePendingLimit int
	MaxPendingLaunches int
	HandshakeTimeout   time.Duration
	ReadTimeout        time.Duration
	WriteTimeout       time.Duration
}

// PairingInfo is the minimum credential material needed by the trusted
// extension pairing path.
type PairingInfo struct {
	Endpoint           string
	InstallationID     string
	InstallationSecret string
}

type claimRecord struct {
	persisted  Claim
	family     chrome.FamilyContext
	token      chrome.GenerationToken
	windowID   int64
	capability string
	challenge  string
	proved     bool
	seen       bool
	blocked    bool
}

type launchRecord struct {
	id         string
	family     chrome.FamilyContext
	token      chrome.GenerationToken
	generation uint64
	windowID   int64
	claimID    string
	capability string
	phase      string
	timer      chrome.Timer
}

type closeRecord struct {
	family chrome.FamilyContext
	req    chrome.CloseRequest
	claim  string
}

// Service is the single process-wide owner of the manager, bridge, persisted
// authority, launcher, and lifetime host lock.
type Service struct {
	mu       sync.Mutex
	randomMu sync.Mutex

	store       *Store
	lock        *HostLock
	state       State
	credential  InstallationCredential
	manager     *chrome.Manager
	bridge      *bridge.Server
	launcher    chrome.Launcher
	clock       chrome.Clock
	random      io.Reader
	startup     time.Duration
	recovery    time.Duration
	maxLaunches int

	families map[chrome.FamilyID]chrome.FamilyContext
	claims   map[string]*claimRecord
	launches map[string]*launchRecord
	closes   map[string]*closeRecord

	shutdownMu     sync.Mutex
	managerStopped bool
	bridgeStopped  bool
	closed         bool
}

// NewService acquires the lifetime host lock before advancing the authority
// epoch and creating the one manager and bridge.
func NewService(cfg Config) (*Service, error) {
	if cfg.Store == nil || cfg.Launcher == nil || cfg.ExtensionID == "" {
		return nil, fmt.Errorf("chrome runtime: store, launcher, and extension ID are required")
	}
	if cfg.Clock == nil {
		cfg.Clock = chrome.RealClock()
	}
	if cfg.Random == nil {
		cfg.Random = rand.Reader
	}
	if cfg.Listen == nil {
		cfg.Listen = net.Listen
	}
	if cfg.IdleTimeout == 0 {
		cfg.IdleTimeout = defaultIdleTimeout
	}
	if cfg.StartupGrace == 0 {
		cfg.StartupGrace = defaultStartupGrace
	}
	if cfg.RecoveryGrace == 0 {
		cfg.RecoveryGrace = defaultRecoveryGrace
	}
	if cfg.IdleTimeout < 0 || cfg.StartupGrace < 0 || cfg.RecoveryGrace < 0 {
		return nil, fmt.Errorf("chrome runtime: grace periods and idle timeout must be positive")
	}
	if cfg.MaxPendingLaunches == 0 {
		cfg.MaxPendingLaunches = protocol.MaxListEntries
	}
	if cfg.MaxPendingLaunches < 0 || cfg.MaxPendingLaunches > protocol.MaxListEntries {
		return nil, fmt.Errorf("chrome runtime: pending launch bound must be between 1 and %d", protocol.MaxListEntries)
	}
	lock, err := cfg.Store.Acquire()
	if err != nil {
		return nil, err
	}
	var listener net.Listener
	fail := func(cause error) (*Service, error) {
		var cleanup error
		if listener != nil {
			cleanup = errors.Join(cleanup, listener.Close())
		}
		cleanup = errors.Join(cleanup, lock.Close())
		return nil, errors.Join(cause, cleanup)
	}
	credential, err := cfg.Store.LoadCredential()
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fail(err)
	}
	newCredential := errors.Is(err, os.ErrNotExist)
	if newCredential {
		id, tokenErr := randomTokenFrom(cfg.Random)
		if tokenErr != nil {
			return fail(tokenErr)
		}
		secret, tokenErr := randomTokenFrom(cfg.Random)
		if tokenErr != nil {
			return fail(tokenErr)
		}
		credential = InstallationCredential{
			Version: CredentialVersion, InstallationID: id, Secret: secret,
		}
	}
	listener = cfg.Listener
	if listener == nil {
		address := "127.0.0.1:0"
		if !newCredential {
			address = net.JoinHostPort("127.0.0.1", fmt.Sprint(credential.LoopbackPort))
		}
		listener, err = cfg.Listen("tcp4", address)
		if err != nil {
			return fail(fmt.Errorf("chrome runtime: bind persisted loopback endpoint %s: %w", address, err))
		}
	}
	tcpAddress, ok := listener.Addr().(*net.TCPAddr)
	if !ok || !tcpAddress.IP.Equal(net.IPv4(127, 0, 0, 1)) || tcpAddress.Port <= 0 || tcpAddress.Port > 65535 {
		return fail(fmt.Errorf("chrome runtime: listener must be bound to 127.0.0.1"))
	}
	if newCredential {
		credential.LoopbackPort = uint16(tcpAddress.Port)
		if err := cfg.Store.SaveCredential(lock, credential); err != nil {
			return fail(err)
		}
	} else if uint16(tcpAddress.Port) != credential.LoopbackPort {
		return fail(fmt.Errorf("chrome runtime: listener port %d does not match persisted port %d", tcpAddress.Port, credential.LoopbackPort))
	}
	state, err := cfg.Store.AdvanceEpoch(lock)
	if err != nil {
		return fail(err)
	}
	s := &Service{
		store: cfg.Store, lock: lock, state: state, credential: credential,
		launcher: cfg.Launcher, clock: cfg.Clock, random: cfg.Random,
		startup: cfg.StartupGrace, recovery: cfg.RecoveryGrace, maxLaunches: cfg.MaxPendingLaunches,
		families: make(map[chrome.FamilyID]chrome.FamilyContext),
		claims:   make(map[string]*claimRecord), launches: make(map[string]*launchRecord),
		closes: make(map[string]*closeRecord),
	}
	for _, persisted := range state.Claims {
		copy := persisted
		s.claims[persisted.ClaimID] = &claimRecord{persisted: copy}
	}
	manager, err := chrome.NewManager(chrome.ManagerConfig{
		Clock: cfg.Clock, IdleTimeout: cfg.IdleTimeout,
		OnLaunch: s.onLaunch, OnClose: s.onClose, OnCancelClose: s.onCancelClose,
	})
	if err != nil {
		return fail(err)
	}
	s.manager = manager
	instanceID, err := s.randomToken()
	if err != nil {
		return fail(err)
	}
	srv, err := bridge.New(bridge.Config{
		ExtensionID: cfg.ExtensionID, InstallationID: credential.InstallationID,
		InstallationSecret: []byte(credential.Secret), ManagerEpoch: state.ManagerEpoch,
		ManagerInstanceID: instanceID,
		Supported: protocol.Advertisement{
			RPC:    []protocol.VersionRange{{Major: 1, MinMinor: 0, MaxMinor: 0}},
			Events: []protocol.VersionRange{{Major: 1, MinMinor: 0, MaxMinor: 0}},
			Capabilities: []string{"claim_v1", "execution_journal_v1", "close_epoch_v1",
				"ownership_rebind_v1", "ownership_reconcile_v1"},
		},
		RequiredCapabilities: []string{"claim_v1", "close_epoch_v1", "ownership_rebind_v1", "ownership_reconcile_v1"},
		QueueSize:            cfg.BridgeQueueSize, PendingLimit: cfg.BridgePendingLimit,
		HandshakeTimeout: cfg.HandshakeTimeout, ReadTimeout: cfg.ReadTimeout, WriteTimeout: cfg.WriteTimeout,
		OnControl: s.onControl, OnEvent: s.onEvent, OnDisconnect: s.onDisconnect,
		AuthorizeEvent: s.authorizeEvent,
	})
	if err != nil {
		return fail(err)
	}
	s.bridge = srv
	if err := srv.Listen(listener); err != nil {
		return fail(err)
	}
	listener = nil // bridge owns it after Listen succeeds
	return s, nil
}

func (s *Service) PairingInfo() (PairingInfo, error) {
	if s == nil {
		return PairingInfo{}, chrome.NewError(chrome.ErrSessionClosed, "Chrome runtime service is unavailable.")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return PairingInfo{}, chrome.NewError(chrome.ErrSessionClosed, "Chrome runtime service is shut down.")
	}
	return PairingInfo{
		Endpoint: s.bridge.URL(), InstallationID: s.credential.InstallationID,
		InstallationSecret: s.credential.Secret,
	}, nil
}

func (s *Service) Manager() *chrome.Manager {
	if s == nil {
		return nil
	}
	return s.manager
}

func (s *Service) Bridge() *bridge.Server {
	if s == nil {
		return nil
	}
	return s.bridge
}

// StartFamily idempotently registers a verified family. A matching persisted
// claim is restored as launching and must complete ownership proof before it
// can become ready; otherwise a fresh generation is launched.
func (s *Service) StartFamily(family chrome.FamilyContext) error {
	if s == nil || !family.Valid() {
		return chrome.ErrBrowserFamilyAuthority
	}
	hash := familyHash(family)
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return chrome.NewError(chrome.ErrSessionClosed, "Chrome runtime service is shut down.")
	}
	if existing, ok := s.families[family.ID()]; ok {
		s.mu.Unlock()
		if !existing.SameFamily(family) {
			return chrome.ErrBrowserFamilyAuthority
		}
		snapshot, err := s.manager.EnsureFamily(family)
		if err != nil {
			return err
		}
		if snapshot.State == chrome.SessionFailed || snapshot.State == chrome.SessionClosed {
			s.mu.Lock()
			blocked := false
			for _, claim := range s.claims {
				if claim.persisted.FamilyHash == hash && claim.blocked {
					blocked = true
					break
				}
			}
			s.mu.Unlock()
			if !blocked {
				_, err = s.manager.BeginLaunch(family)
			}
		}
		return err
	}
	if len(s.families) >= protocol.MaxListEntries {
		s.mu.Unlock()
		return chrome.NewError(chrome.ErrInvalidArguments, "Chrome browser family capacity is exhausted.")
	}
	var restored *claimRecord
	var baseline *claimRecord
	for _, claim := range s.claims {
		if claim.persisted.FamilyHash == hash && claim.persisted.InstallationID == s.credential.InstallationID {
			if baseline == nil || claim.persisted.Generation > baseline.persisted.Generation {
				baseline = claim
			}
			if claim.persisted.Lifecycle != "closed" && claim.persisted.Lifecycle != "quarantined" {
				if restored == nil || claim.persisted.Generation > restored.persisted.Generation {
					restored = claim
				}
			}
		}
	}
	s.families[family.ID()] = family
	if restored != nil {
		restored.family = family
	}
	s.mu.Unlock()
	if restored == nil {
		if baseline != nil {
			token, err := s.manager.RestoreLaunching(family, baseline.persisted.Generation)
			if err != nil {
				return err
			}
			if err := s.manager.MarkWindowAbsent(family, token); err != nil {
				return err
			}
		}
		_, err := s.manager.BeginLaunch(family)
		return err
	}
	if restored.persisted.Lifecycle == "closing" {
		token, req, err := s.manager.RestoreClosing(family, restored.persisted.Generation,
			restored.persisted.CloseEpoch, chrome.ClosePhase(restored.persisted.ClosePhase))
		if err != nil {
			return err
		}
		s.mu.Lock()
		restored.token = token
		s.closes[closeKey(restored.persisted.ClaimID, restored.persisted.Generation)] =
			&closeRecord{family: family, req: req, claim: restored.persisted.ClaimID}
		s.mu.Unlock()
		return nil
	}
	token, err := s.manager.RestoreLaunching(family, restored.persisted.Generation)
	if err != nil {
		return err
	}
	s.mu.Lock()
	restored.token = token
	proved := restored.proved && restored.seen && !restored.blocked
	s.mu.Unlock()
	if proved {
		return s.manager.MarkReady(family, token)
	}
	s.armRestoredTimeout(restored.persisted.ClaimID, family, token)
	return nil
}

func (s *Service) Snapshot(family chrome.FamilyContext) (chrome.Snapshot, error) {
	if s == nil {
		return chrome.Snapshot{}, chrome.NewError(chrome.ErrSessionClosed, "Chrome runtime service is unavailable.")
	}
	return s.manager.Snapshot(family)
}

func (s *Service) Admit(ctx context.Context, family chrome.FamilyContext, opts chrome.ActionOptions) (*chrome.Lease, error) {
	if s == nil {
		return nil, chrome.NewError(chrome.ErrSessionClosed, "Chrome runtime service is unavailable.")
	}
	s.mu.Lock()
	blocked := false
	hash := familyHash(family)
	for _, claim := range s.claims {
		if claim.persisted.FamilyHash == hash && claim.blocked {
			blocked = true
			break
		}
	}
	s.mu.Unlock()
	if blocked {
		return nil, launchTimeoutError(0)
	}
	return s.manager.Admit(ctx, family, opts)
}

func (s *Service) onLaunch(req chrome.LaunchRequest) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	if len(s.launches) >= s.maxLaunches {
		s.mu.Unlock()
		_ = s.manager.MarkLaunchFailed(req.Family(), req.Token(),
			chrome.NewError(chrome.ErrLaunchFailed, "Chrome pending launch capacity is exhausted."))
		return
	}
	s.mu.Unlock()
	id, err := s.randomToken()
	if err != nil {
		_ = s.manager.MarkLaunchFailed(req.Family(), req.Token(),
			chrome.NewError(chrome.ErrLaunchFailed, "Could not create a Chrome launch correlation ID."))
		return
	}
	record := &launchRecord{id: id, family: req.Family(), token: req.Token(), generation: req.Generation()}
	s.mu.Lock()
	if s.closed || len(s.launches) >= s.maxLaunches {
		s.mu.Unlock()
		_ = s.manager.MarkLaunchFailed(req.Family(), req.Token(),
			chrome.NewError(chrome.ErrLaunchFailed, "Chrome pending launch capacity is exhausted."))
		return
	}
	if _, exists := s.launches[id]; exists {
		s.mu.Unlock()
		_ = s.manager.MarkLaunchFailed(req.Family(), req.Token(),
			chrome.NewError(chrome.ErrLaunchFailed, "Chrome launch correlation ID collided."))
		return
	}
	s.launches[id] = record
	record.timer = s.clock.AfterFunc(s.startup, func() { s.launchTimedOut(id) })
	s.mu.Unlock()
	control := &protocol.CreateOwnedWindow{Contract: protocol.ControlContractV1,
		Kind: protocol.ControlCreateOwnedWindow, LaunchID: id}
	if err := s.bridge.SendControl(control); err == nil {
		return
	}
	if err := s.launcher.Launch(context.Background(), chrome.LauncherRequest{LaunchID: id}); err != nil {
		s.failLaunch(id, launchError(err, req.Generation()))
	}
}

func (s *Service) onControl(control protocol.Control) {
	switch value := control.(type) {
	case *protocol.WindowClaimRequested:
		s.claimRequested(*value)
	case *protocol.ClaimControl:
		switch value.Kind {
		case protocol.ControlClaimStaged:
			s.claimStaged(*value)
		case protocol.ControlWindowClaimed:
			s.windowClaimed(*value)
		}
	case *protocol.OwnershipSnapshot:
		s.ownershipSnapshot(*value)
	case *protocol.CapabilityProof:
		s.capabilityProof(*value)
	case *protocol.CloseControl:
		s.closeControl(*value)
	}
}

func (s *Service) claimRequested(msg protocol.WindowClaimRequested) {
	s.mu.Lock()
	launch := s.launches[msg.LaunchID]
	if launch == nil || launch.phase != "" || msg.WindowID <= 0 {
		s.mu.Unlock()
		return
	}
	claimID, err := s.randomToken()
	if err != nil {
		s.mu.Unlock()
		s.failLaunch(msg.LaunchID, launchError(err, launch.generation))
		return
	}
	launch.windowID, launch.claimID, launch.phase = msg.WindowID, claimID, "prepared"
	s.mu.Unlock()
	_ = s.bridge.SendControl(&protocol.ClaimControl{
		Contract: protocol.ControlContractV1, Kind: protocol.ControlClaimPrepare,
		LaunchID: msg.LaunchID, ClaimID: claimID, Generation: launch.generation, WindowID: msg.WindowID,
	})
}

func (s *Service) claimStaged(msg protocol.ClaimControl) {
	s.mu.Lock()
	launch := s.launches[msg.LaunchID]
	if !matchesLaunch(launch, msg, "prepared") {
		s.mu.Unlock()
		return
	}
	if len(s.claims) >= protocol.MaxListEntries {
		s.mu.Unlock()
		s.failLaunch(msg.LaunchID, launchError(errors.New("claim capacity is exhausted"), msg.Generation))
		return
	}
	capability, err := s.randomToken()
	if err != nil {
		s.mu.Unlock()
		s.failLaunch(msg.LaunchID, launchError(err, msg.Generation))
		return
	}
	launch.capability, launch.phase = capability, "committing"
	persisted := Claim{
		FamilyHash: familyHash(launch.family), Generation: msg.Generation, ClaimID: msg.ClaimID,
		InstallationID: s.credential.InstallationID, Lifecycle: "claiming", UpdatedAt: s.nowUTC(),
	}
	s.claims[msg.ClaimID] = &claimRecord{
		persisted: persisted, family: launch.family, token: launch.token, windowID: msg.WindowID,
		capability: capability,
	}
	err = s.saveStateLocked()
	s.mu.Unlock()
	if err != nil {
		s.failLaunch(msg.LaunchID, launchError(err, msg.Generation))
		return
	}
	if err := s.bridge.Bind(msg.ClaimID, msg.Generation, capability); err != nil {
		s.failLaunch(msg.LaunchID, launchError(err, msg.Generation))
		return
	}
	_ = s.bridge.SendControl(&protocol.ClaimControl{
		Contract: protocol.ControlContractV1, Kind: protocol.ControlClaimCommitted,
		LaunchID: msg.LaunchID, ClaimID: msg.ClaimID, Generation: msg.Generation,
		WindowID: msg.WindowID, FamilyCapability: capability,
	})
}

func (s *Service) windowClaimed(msg protocol.ClaimControl) {
	s.mu.Lock()
	launch := s.launches[msg.LaunchID]
	if !matchesLaunch(launch, msg, "committing") {
		s.mu.Unlock()
		return
	}
	claim := s.claims[msg.ClaimID]
	if claim == nil {
		s.mu.Unlock()
		return
	}
	claim.persisted.Lifecycle, claim.persisted.UpdatedAt = "open", s.nowUTC()
	claim.seen, claim.proved = true, true
	if launch.timer != nil {
		launch.timer.Stop()
	}
	delete(s.launches, msg.LaunchID)
	err := s.saveStateLocked()
	s.mu.Unlock()
	if err != nil {
		_ = s.manager.MarkLaunchFailed(launch.family, launch.token, launchError(err, msg.Generation))
		return
	}
	_ = s.manager.MarkReady(launch.family, launch.token)
}

func matchesLaunch(launch *launchRecord, msg protocol.ClaimControl, phase string) bool {
	return launch != nil && launch.phase == phase && launch.id == msg.LaunchID &&
		launch.claimID == msg.ClaimID && launch.generation == msg.Generation &&
		launch.windowID == msg.WindowID
}

func (s *Service) ownershipSnapshot(snapshot protocol.OwnershipSnapshot) {
	type challenge struct {
		claim, value string
		generation   uint64
	}
	var challenges []challenge
	var absent []*closeRecord
	unknown := false
	s.mu.Lock()
	for _, claim := range s.claims {
		claim.seen = false
	}
	for _, record := range snapshot.Records {
		claim := s.claims[record.ClaimID]
		if claim == nil || claim.persisted.Generation != record.Generation ||
			claim.persisted.InstallationID != s.credential.InstallationID {
			unknown = true
			continue
		}
		sameProvedWindow := claim.proved && claim.windowID == record.WindowID
		claim.seen, claim.windowID = true, record.WindowID
		if sameProvedWindow {
			continue
		}
		value, err := s.randomToken()
		if err != nil {
			continue
		}
		claim.challenge = value
		challenges = append(challenges, challenge{record.ClaimID, value, record.Generation})
	}
	for _, claim := range s.claims {
		if claim.seen || claim.persisted.Lifecycle != "closing" ||
			(claim.persisted.ClosePhase != string(chrome.CloseSent) &&
				claim.persisted.ClosePhase != string(chrome.CloseCommitted)) || !claim.family.Valid() {
			continue
		}
		if record := s.closes[closeKey(claim.persisted.ClaimID, claim.persisted.Generation)]; record != nil {
			absent = append(absent, record)
		}
	}
	s.mu.Unlock()
	for _, record := range absent {
		s.completeAbsentClose(record)
	}
	for _, item := range challenges {
		_ = s.bridge.SendControl(&protocol.CapabilityChallenge{
			Contract: protocol.ControlContractV1, Kind: protocol.ControlCapabilityChallenge,
			ClaimID: item.claim, Generation: item.generation, Challenge: item.value,
		})
	}
	if unknown {
		s.clock.AfterFunc(s.recovery, s.sendOwnershipReconcile)
	} else {
		s.sendOwnershipReconcile()
	}
}

func (s *Service) capabilityProof(proof protocol.CapabilityProof) {
	if !s.bridge.VerifyCapabilityProof(proof) {
		return
	}
	s.mu.Lock()
	claim := s.claims[proof.ClaimID]
	if claim == nil || claim.persisted.Generation != proof.Generation || claim.windowID != proof.WindowID ||
		claim.challenge == "" || claim.challenge != proof.Challenge || !claim.seen ||
		claim.persisted.InstallationID != proof.InstallationID {
		s.mu.Unlock()
		return
	}
	capability, err := s.randomToken()
	if err != nil {
		s.mu.Unlock()
		return
	}
	claim.challenge, claim.capability, claim.proved = "", capability, true
	family, token, registered := claim.family, claim.token,
		claim.family.Valid() && claim.persisted.Lifecycle != "closing"
	var restoredClose *closeRecord
	closeCommitted := false
	if claim.persisted.Lifecycle == "closing" {
		restoredClose = s.closes[closeKey(proof.ClaimID, proof.Generation)]
		closeCommitted = claim.persisted.ClosePhase == string(chrome.CloseCommitted)
	}
	s.mu.Unlock()
	if err := s.bridge.Bind(proof.ClaimID, proof.Generation, capability); err != nil {
		s.rollbackCapabilityProof(proof, capability, false)
		return
	}
	if err := s.bridge.SendControl(&protocol.CapabilityRebind{
		Contract: protocol.ControlContractV1, Kind: protocol.ControlCapabilityRebind,
		ClaimID: proof.ClaimID, Generation: proof.Generation, FamilyCapability: capability,
	}); err != nil {
		s.rollbackCapabilityProof(proof, capability, true)
		return
	}
	if registered {
		_ = s.manager.MarkReady(family, token)
	} else if restoredClose != nil {
		kind := protocol.ControlClosePrepare
		if closeCommitted {
			kind = protocol.ControlCloseCommit
		}
		_ = s.bridge.SendControl(closeMessage(kind, proof.ClaimID, restoredClose.req))
	}
}

func (s *Service) rollbackCapabilityProof(proof protocol.CapabilityProof, capability string, revoke bool) {
	s.mu.Lock()
	claim := s.claims[proof.ClaimID]
	if claim == nil || claim.persisted.Generation != proof.Generation ||
		claim.windowID != proof.WindowID || claim.capability != capability ||
		claim.challenge != "" || !claim.proved {
		s.mu.Unlock()
		return
	}
	claim.capability, claim.challenge, claim.proved = "", "", false
	s.mu.Unlock()
	if revoke {
		s.bridge.Revoke(proof.ClaimID, proof.Generation)
	}
}

func (s *Service) completeAbsentClose(record *closeRecord) {
	if record == nil {
		return
	}
	s.mu.Lock()
	claim := s.claims[record.claim]
	closeSent := claim != nil &&
		claim.persisted.Generation == record.req.Generation() &&
		claim.persisted.Lifecycle == "closing" &&
		claim.persisted.CloseEpoch == record.req.Epoch() &&
		claim.persisted.ClosePhase == string(chrome.CloseSent)
	s.mu.Unlock()
	if closeSent && s.markCloseCommittedDurably(record) != nil {
		return
	}
	if s.manager.ReconcileClose(record.family, record.req, true) != nil {
		return
	}
	s.mu.Lock()
	if claim := s.claims[record.claim]; claim != nil &&
		claim.persisted.Generation == record.req.Generation() {
		claim.persisted.Lifecycle, claim.persisted.UpdatedAt = "closed", s.nowUTC()
		claim.persisted.CloseEpoch, claim.persisted.ClosePhase = 0, ""
		claim.capability, claim.challenge = "", ""
		claim.proved, claim.seen = false, false
		_ = s.saveStateLocked()
	}
	delete(s.closes, closeKey(record.claim, record.req.Generation()))
	s.mu.Unlock()
	s.bridge.Revoke(record.claim, record.req.Generation())
}

func (s *Service) sendOwnershipReconcile() {
	s.mu.Lock()
	message := &protocol.OwnershipReconcile{
		Contract: protocol.ControlContractV1, Kind: protocol.ControlOwnershipReconcile,
		LiveLaunchIDs: []string{}, Pending: []protocol.ClaimTuple{},
		Committed: []protocol.ClaimTuple{}, Authorized: []protocol.ClaimTuple{},
	}
	for id := range s.launches {
		message.LiveLaunchIDs = append(message.LiveLaunchIDs, id)
	}
	for _, claim := range s.claims {
		tuple := protocol.ClaimTuple{ClaimID: claim.persisted.ClaimID, Generation: claim.persisted.Generation}
		switch claim.persisted.Lifecycle {
		case "claiming":
			message.Pending = append(message.Pending, tuple)
		case "closing":
			message.Committed = append(message.Committed, tuple)
		case "open":
			message.Authorized = append(message.Authorized, tuple)
		}
	}
	sort.Strings(message.LiveLaunchIDs)
	sortTuples(message.Pending)
	sortTuples(message.Committed)
	sortTuples(message.Authorized)
	s.mu.Unlock()
	_ = s.bridge.SendControl(message)
}

func sortTuples(values []protocol.ClaimTuple) {
	sort.Slice(values, func(i, j int) bool {
		if values[i].ClaimID == values[j].ClaimID {
			return values[i].Generation < values[j].Generation
		}
		return values[i].ClaimID < values[j].ClaimID
	})
}

func (s *Service) onClose(req chrome.CloseRequest) {
	s.mu.Lock()
	claim := s.claimForFamilyLocked(req.Family(), req.Generation())
	if claim == nil {
		s.mu.Unlock()
		_ = s.manager.CloseFailedBeforeCommit(req.Family(), req,
			chrome.NewError(chrome.ErrWrongOwner, "No exact Chrome window claim authorizes close."))
		return
	}
	key := closeKey(claim.persisted.ClaimID, req.Generation())
	if _, exists := s.closes[key]; !exists && len(s.closes) >= protocol.MaxListEntries {
		s.mu.Unlock()
		_ = s.manager.CloseFailedBeforeCommit(req.Family(), req,
			chrome.NewError(chrome.ErrInvalidArguments, "Chrome close capacity is exhausted."))
		return
	}
	s.closes[key] =
		&closeRecord{family: req.Family(), req: req, claim: claim.persisted.ClaimID}
	claim.persisted.Lifecycle, claim.persisted.UpdatedAt = "closing", s.nowUTC()
	claim.persisted.CloseEpoch, claim.persisted.ClosePhase = req.Epoch(), string(chrome.CloseSent)
	err := s.saveStateLocked()
	if err != nil {
		delete(s.closes, key)
		claim.persisted.Lifecycle = "open"
		claim.persisted.CloseEpoch, claim.persisted.ClosePhase = 0, ""
	}
	s.mu.Unlock()
	if err != nil {
		_ = s.manager.CloseFailedBeforeCommit(req.Family(), req, launchError(err, req.Generation()))
		return
	}
	if err := s.bridge.SendControl(closeMessage(protocol.ControlClosePrepare, claim.persisted.ClaimID, req)); err != nil {
		s.mu.Lock()
		if current := s.claims[claim.persisted.ClaimID]; current == claim {
			current.persisted.Lifecycle, current.persisted.UpdatedAt = "open", s.nowUTC()
			current.persisted.CloseEpoch, current.persisted.ClosePhase = 0, ""
			if persistErr := s.saveStateLocked(); persistErr != nil {
				current.persisted.Lifecycle = "closing"
				current.persisted.CloseEpoch, current.persisted.ClosePhase = req.Epoch(), string(chrome.CloseSent)
			}
		}
		delete(s.closes, closeKey(claim.persisted.ClaimID, req.Generation()))
		s.mu.Unlock()
		_ = s.manager.CloseFailedBeforeCommit(req.Family(), req, launchError(err, req.Generation()))
	}
}

func (s *Service) onCancelClose(req chrome.CloseRequest) {
	s.mu.Lock()
	claim := s.claimForFamilyLocked(req.Family(), req.Generation())
	s.mu.Unlock()
	if claim != nil {
		_ = s.bridge.SendControl(closeMessage(protocol.ControlCloseCancel, claim.persisted.ClaimID, req))
	}
}

func closeMessage(kind, claimID string, req chrome.CloseRequest) *protocol.CloseControl {
	return &protocol.CloseControl{Contract: protocol.ControlContractV1, Kind: kind,
		ClaimID: claimID, Generation: req.Generation(), CloseEpoch: req.Epoch()}
}

func (s *Service) closeControl(msg protocol.CloseControl) {
	s.mu.Lock()
	record := s.closes[closeKey(msg.ClaimID, msg.Generation)]
	if record == nil || record.req.Epoch() != msg.CloseEpoch {
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()
	switch msg.Kind {
	case protocol.ControlClosePrepared:
		if s.manager.MarkCloseAccepted(record.family, record.req) == nil {
			_ = s.bridge.SendControl(closeMessage(protocol.ControlCloseCommit, msg.ClaimID, record.req))
		}
	case protocol.ControlCloseCancelled:
		if s.manager.AcknowledgeCloseCancellation(record.family, record.req, true) == nil {
			s.mu.Lock()
			if claim := s.claims[msg.ClaimID]; claim != nil {
				claim.persisted.Lifecycle, claim.persisted.UpdatedAt = "open", s.nowUTC()
				claim.persisted.CloseEpoch, claim.persisted.ClosePhase = 0, ""
				if err := s.saveStateLocked(); err != nil {
					claim.persisted.Lifecycle = "closing"
					claim.persisted.CloseEpoch, claim.persisted.ClosePhase = record.req.Epoch(), string(chrome.CloseSent)
				}
			}
			delete(s.closes, closeKey(msg.ClaimID, msg.Generation))
			s.mu.Unlock()
		}
	case protocol.ControlCloseCommitted:
		_ = s.markCloseCommittedDurably(record)
	}
}

func (s *Service) markCloseCommittedDurably(record *closeRecord) error {
	s.mu.Lock()
	claim := s.claims[record.claim]
	if claim == nil || claim.persisted.Generation != record.req.Generation() ||
		claim.persisted.Lifecycle != "closing" ||
		claim.persisted.CloseEpoch != record.req.Epoch() {
		s.mu.Unlock()
		return chrome.NewError(chrome.ErrWrongOwner, "No exact Chrome window claim authorizes close.")
	}
	if claim.persisted.ClosePhase != string(chrome.CloseSent) &&
		claim.persisted.ClosePhase != string(chrome.CloseCommitted) {
		s.mu.Unlock()
		return chrome.NewError(chrome.ErrInvalidArguments, "Close request is not awaiting commit.")
	}
	if claim.persisted.ClosePhase == string(chrome.CloseSent) {
		previous := claim.persisted
		claim.persisted.ClosePhase, claim.persisted.UpdatedAt = string(chrome.CloseCommitted), s.nowUTC()
		if err := s.saveStateLocked(); err != nil {
			claim.persisted = previous
			s.mu.Unlock()
			return err
		}
	}
	s.mu.Unlock()
	if err := s.manager.MarkCloseCommitted(record.family, record.req); err != nil {
		snapshot, snapshotErr := s.manager.Snapshot(record.family)
		if snapshotErr == nil && snapshot.Generation == record.req.Generation() &&
			snapshot.CloseEpoch == record.req.Epoch() && snapshot.ClosePhase == chrome.CloseCommitted {
			return nil
		}
		return err
	}
	return nil
}

func (s *Service) authorizeEvent(event protocol.Event) (string, string, bool) {
	var payload struct {
		ClaimID  string `json:"claim_id"`
		Category string `json:"category"`
		Activity string `json:"activity"`
	}
	if json.Unmarshal(event.Payload, &payload) != nil || payload.ClaimID == "" {
		return "", "", false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	claim := s.claims[payload.ClaimID]
	if claim == nil || claim.persisted.Generation != event.Generation || claim.capability == "" ||
		!claim.proved || !claim.seen {
		return "", "", false
	}
	return claim.persisted.ClaimID, claim.capability, true
}

func (s *Service) onEvent(event protocol.Event) {
	var payload struct {
		ClaimID    string `json:"claim_id"`
		Category   string `json:"category"`
		Activity   string `json:"activity"`
		CloseEpoch uint64 `json:"close_epoch"`
	}
	if json.Unmarshal(event.Payload, &payload) != nil {
		return
	}
	s.mu.Lock()
	claim := s.claims[payload.ClaimID]
	if claim == nil || claim.persisted.Generation != event.Generation || !claim.family.Valid() {
		s.mu.Unlock()
		return
	}
	family, token := claim.family, claim.token
	var close *closeRecord
	if payload.CloseEpoch != 0 {
		close = s.closes[closeKey(payload.ClaimID, event.Generation)]
	}
	s.mu.Unlock()
	switch event.Event {
	case "human_activity":
		category := payload.Category
		if category == "" {
			category = payload.Activity
		}
		if category == "keyboard" || category == "pointer" {
			_ = s.manager.RecordTrustedHumanActivity(family, token)
		}
	case "window_removed":
		if close != nil && close.req.Epoch() == payload.CloseEpoch {
			_ = s.manager.ReconcileClose(family, close.req, true)
		} else {
			_ = s.manager.MarkWindowAbsent(family, token)
		}
		s.mu.Lock()
		if current := s.claims[payload.ClaimID]; current == claim {
			current.persisted.Lifecycle, current.persisted.UpdatedAt = "closed", s.nowUTC()
			current.persisted.CloseEpoch, current.persisted.ClosePhase = 0, ""
			current.capability = ""
			_ = s.saveStateLocked()
		}
		delete(s.closes, closeKey(payload.ClaimID, event.Generation))
		s.mu.Unlock()
		s.bridge.Revoke(payload.ClaimID, event.Generation)
	}
}

// Mutating requests are never replayed on disconnect. A committed close is
// deliberately left closing for exact-window reconciliation after reconnect.
func (s *Service) onDisconnect(disconnect bridge.Disconnect) {
	_ = disconnect
	type tuple struct {
		claim      string
		generation uint64
	}
	s.mu.Lock()
	values := make([]tuple, 0, len(s.claims))
	for _, claim := range s.claims {
		if claim.capability != "" {
			values = append(values, tuple{claim.persisted.ClaimID, claim.persisted.Generation})
		}
		claim.capability, claim.challenge = "", ""
		claim.proved, claim.seen = false, false
	}
	s.mu.Unlock()
	for _, value := range values {
		s.bridge.Revoke(value.claim, value.generation)
	}
}

func (s *Service) claimForFamilyLocked(family chrome.FamilyContext, generation uint64) *claimRecord {
	for _, claim := range s.claims {
		if claim.family.SameFamily(family) && claim.persisted.Generation == generation &&
			claim.persisted.Lifecycle != "closed" && claim.persisted.Lifecycle != "quarantined" {
			return claim
		}
	}
	return nil
}

func (s *Service) launchTimedOut(id string) {
	s.failLaunch(id, launchTimeoutError(0))
}

func (s *Service) failLaunch(id string, cause *chrome.Error) {
	s.mu.Lock()
	launch := s.launches[id]
	if launch == nil {
		s.mu.Unlock()
		return
	}
	delete(s.launches, id)
	if launch.timer != nil {
		launch.timer.Stop()
	}
	if launch.claimID != "" {
		delete(s.claims, launch.claimID)
		_ = s.saveStateLocked()
	}
	s.mu.Unlock()
	cause.Generation = launch.generation
	_ = s.manager.MarkLaunchFailed(launch.family, launch.token, cause)
}

func (s *Service) armRestoredTimeout(claimID string, family chrome.FamilyContext, token chrome.GenerationToken) {
	s.clock.AfterFunc(s.startup, func() {
		s.mu.Lock()
		claim := s.claims[claimID]
		if claim == nil || claim.proved && claim.seen {
			s.mu.Unlock()
			return
		}
		claim.blocked = true
		s.mu.Unlock()
		_ = s.manager.MarkLaunchFailed(family, token, launchTimeoutError(claim.persisted.Generation))
	})
}

func launchTimeoutError(generation uint64) *chrome.Error {
	err := chrome.NewError(chrome.ErrHandshakeTimeout, "Chrome window ownership handshake timed out.")
	err.Generation = generation
	return err
}

func launchError(cause error, generation uint64) *chrome.Error {
	var typed *chrome.Error
	if errors.As(cause, &typed) {
		copy := *typed
		copy.Generation = generation
		return &copy
	}
	err := chrome.NewError(chrome.ErrLaunchFailed, fmt.Sprintf("Chrome runtime launch failed: %v", cause))
	err.Generation = generation
	return err
}

func (s *Service) randomToken() (string, error) {
	raw := make([]byte, tokenBytes)
	s.randomMu.Lock()
	defer s.randomMu.Unlock()
	if _, err := io.ReadFull(s.random, raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func randomTokenFrom(random io.Reader) (string, error) {
	raw := make([]byte, tokenBytes)
	if _, err := io.ReadFull(random, raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}
func familyHash(family chrome.FamilyContext) string {
	value := family.PersistenceHash()
	return hex.EncodeToString(value[:])
}

func closeKey(claimID string, generation uint64) string {
	return fmt.Sprintf("%s\x00%d", claimID, generation)
}

func (s *Service) nowUTC() time.Time {
	return s.clock.Now().UTC()
}

func (s *Service) saveStateLocked() error {
	claims := make([]Claim, 0, len(s.claims))
	for _, record := range s.claims {
		claims = append(claims, record.persisted)
	}
	sort.Slice(claims, func(i, j int) bool {
		if claims[i].FamilyHash != claims[j].FamilyHash {
			return claims[i].FamilyHash < claims[j].FamilyHash
		}
		if claims[i].Generation != claims[j].Generation {
			return claims[i].Generation < claims[j].Generation
		}
		return claims[i].ClaimID < claims[j].ClaimID
	})
	s.state.Claims = claims
	return s.store.SaveState(s.lock, s.state)
}

// Shutdown is retryable: timing out leaves the lifetime lock and bridge owned
// by the service, and a later call continues from the last completed stage.
func (s *Service) Shutdown(ctx context.Context) error {
	if s == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	s.shutdownMu.Lock()
	defer s.shutdownMu.Unlock()
	if s.closed {
		return nil
	}
	if !s.managerStopped {
		if err := s.manager.Shutdown(ctx); err != nil {
			return err
		}
		s.managerStopped = true
	}
	if !s.bridgeStopped {
		if err := s.bridge.Close(ctx); err != nil {
			return err
		}
		s.bridgeStopped = true
	}
	if err := s.lock.Close(); err != nil {
		return err
	}
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	return nil
}
