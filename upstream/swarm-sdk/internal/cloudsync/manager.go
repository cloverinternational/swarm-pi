package cloudsync

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Swarm-Code/mono/swarm-core/core"
	sdkanalytics "github.com/Swarm-Code/mono/swarm-sdk/internal/analytics"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/cloud"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation/storage"
)

// ProfileStore is the minimal profile-management surface required by the
// cloud sync manager. Both the TUI's *profile.ProfileStore and the SDK's
// *profiles.Manager satisfy this interface.
type ProfileStore interface {
	// GetActiveProfileID returns the ID of the currently active (default)
	// profile, or "" when none is configured.
	GetActiveProfileID() string
	// SetDefaultProfile switches the active profile and persists the change.
	SetDefaultProfile(id string) error
}

type ManagerConfig struct {
	ConfigManager     core.ConfigManager
	ProfileStore      ProfileStore
	PolicyApplier     PolicyApplier
	ConversationStore storage.Storage
	HTTPClient        *http.Client
	// Tracker receives analytics events from the sync manager.
	// If nil, a NoopTracker is used (no analytics recorded).
	Tracker sdkanalytics.Tracker
}

type PolicyApplier interface {
	SetPolicyLayer(layer *core.PolicyLayer)
	ClearPolicyLayer()
	GetPolicyLayer() *core.PolicyLayer
}

type SyncStatus struct {
	InProgress bool   `json:"inProgress"`
	LastSyncAt int64  `json:"lastSyncAt"`
	LastError  string `json:"lastError,omitempty"`
}

type TeamSettingsChange struct {
	Section string `json:"section"`
	Key     string `json:"key"`
	Before  string `json:"before"`
	After   string `json:"after"`
}

type TeamSettingsPending struct {
	TeamID    string               `json:"teamId"`
	Changes   []TeamSettingsChange `json:"changes"`
	UpdatedAt int64                `json:"updatedAt"`
	UpdatedBy string               `json:"updatedBy"`
	DeviceID  string               `json:"deviceId"`
	Version   int64                `json:"version"`
	ETag      string               `json:"etag"`
	Payload   *SettingsPayload     `json:"-"`
}

type Manager struct {
	configManager     core.ConfigManager
	profileStore      ProfileStore
	policyApplier     PolicyApplier
	conversationStore storage.Storage
	stateManager      *StateManager
	httpClient        *http.Client
	tracker           sdkanalytics.Tracker

	stateMu sync.Mutex
	state   *SyncState

	settingsQueue     chan struct{}
	profilesQueue     chan struct{}
	conversationQueue chan conversationJob

	statusMu  sync.Mutex
	status    SyncStatus
	activeOps int

	pendingMu   sync.Mutex
	pendingTeam *TeamSettingsPending

	keyCacheMu sync.Mutex
	keyCache   *keyCache

	backoffMu       sync.Mutex
	backoffUntil    time.Time
	backoffFailures int
}

type conversationJob struct {
	Action string
	ID     string
}

type keyCache struct {
	Keys      map[string]*rsa.PublicKey
	UpdatedAt time.Time
}

const (
	syncBackoffBase = 5 * time.Second
	syncBackoffMax  = 5 * time.Minute
)

type syncWarning struct {
	message string
}

func (w syncWarning) Error() string {
	return w.message
}

func newSyncWarning(err error) error {
	if err == nil {
		return nil
	}
	return syncWarning{message: err.Error()}
}

func NewManager(cfg ManagerConfig) (*Manager, error) {
	if cfg.ConfigManager == nil {
		return nil, fmt.Errorf("config manager is required")
	}

	stateManager := NewStateManager(cfg.ConfigManager.ConfigDir())
	state, err := stateManager.Load()
	if err != nil {
		return nil, err
	}

	manager := &Manager{
		configManager:     cfg.ConfigManager,
		profileStore:      cfg.ProfileStore,
		policyApplier:     cfg.PolicyApplier,
		conversationStore: cfg.ConversationStore,
		stateManager:      stateManager,
		httpClient:        cfg.HTTPClient,
		tracker:           cfg.Tracker,
		state:             state,
		status: SyncStatus{
			LastSyncAt: state.LastSyncAt,
		},
		settingsQueue:     make(chan struct{}, 1),
		profilesQueue:     make(chan struct{}, 1),
		conversationQueue: make(chan conversationJob, 128),
	}

	if manager.tracker == nil {
		manager.tracker = sdkanalytics.NoopTracker{}
	}

	return manager, nil
}

func (m *Manager) Start(ctx context.Context) {
	go m.settingsWorker(ctx)
	go m.profilesWorker(ctx)
	go m.conversationWorker(ctx)
}

func (m *Manager) Status() SyncStatus {
	m.statusMu.Lock()
	defer m.statusMu.Unlock()
	return m.status
}

func (m *Manager) MarkSettingsDirty() {
	now := time.Now().UTC().Unix()
	cfg := m.configManager.GetConfig()

	m.stateMu.Lock()
	if cfg != nil && cfg.SyncSettingsScope == "team" {
		teamID := normalizeTeamID(cfg.SyncSettingsTeamID)
		if m.state.TeamSettings == nil {
			m.state.TeamSettings = map[string]SyncItemState{}
		}
		entry := m.state.TeamSettings[teamID]
		entry.LocalUpdatedAt = now
		m.state.TeamSettings[teamID] = entry
	} else {
		m.state.Settings.LocalUpdatedAt = now
	}
	_ = m.stateManager.Save(m.state)
	m.stateMu.Unlock()
}

func (m *Manager) MarkProfilesDirty() {
	m.stateMu.Lock()
	m.state.Profiles.LocalUpdatedAt = time.Now().UTC().Unix()
	_ = m.stateManager.Save(m.state)
	m.stateMu.Unlock()
}

func (m *Manager) QueueSettingsSync() {
	cfg := m.configManager.GetConfig()
	if cfg == nil || !cfg.SyncSettings {
		return
	}
	select {
	case m.settingsQueue <- struct{}{}:
	default:
	}
}

func (m *Manager) QueueProfilesSync() {
	cfg := m.configManager.GetConfig()
	if cfg == nil || !cfg.SyncProfiles {
		return
	}
	select {
	case m.profilesQueue <- struct{}{}:
	default:
	}
}

func (m *Manager) QueueConversationUpload(id string) {
	cfg := m.configManager.GetConfig()
	if cfg == nil || !cfg.SyncConversations || m.conversationStore == nil {
		return
	}
	m.conversationQueue <- conversationJob{Action: "upload", ID: id}
}

func (m *Manager) QueueConversationDelete(id string) {
	cfg := m.configManager.GetConfig()
	if cfg == nil || !cfg.SyncConversations || m.conversationStore == nil {
		return
	}
	m.conversationQueue <- conversationJob{Action: "delete", ID: id}
}

func (m *Manager) InitialSync(ctx context.Context) {
	cfg := m.configManager.GetConfig()
	if cfg == nil {
		return
	}

	if cfg.SyncSettings {
		if cfg.SyncSettingsScope == "team" {
			_ = m.pullTeamSettings(ctx, cfg.SyncSettingsTeamID)
		} else {
			if m.policyApplier != nil {
				m.policyApplier.ClearPolicyLayer()
			}
			_ = m.pullPersonalSettings(ctx)
		}
	}
	if cfg.SyncProfiles {
		_ = m.pullProfiles(ctx)
	}
	if cfg.SyncConversations && m.conversationStore != nil {
		_ = m.pullConversations(ctx)
	}
}

func (m *Manager) settingsWorker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-m.settingsQueue:
			cfg := m.configManager.GetConfig()
			if cfg == nil || !cfg.SyncSettings {
				continue
			}
			if cfg.SyncSettingsScope == "team" {
				_ = m.pushTeamSettings(ctx, cfg.SyncSettingsTeamID)
			} else {
				if m.policyApplier != nil {
					m.policyApplier.ClearPolicyLayer()
				}
				_ = m.pushPersonalSettings(ctx)
			}
		}
	}
}

func (m *Manager) profilesWorker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-m.profilesQueue:
			cfg := m.configManager.GetConfig()
			if cfg == nil || !cfg.SyncProfiles {
				continue
			}
			_ = m.pushProfiles(ctx)
		}
	}
}

func (m *Manager) conversationWorker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case job := <-m.conversationQueue:
			switch job.Action {
			case "upload":
				_ = m.uploadConversation(ctx, job.ID)
			case "delete":
				_ = m.deleteConversation(ctx, job.ID)
			}
		}
	}
}

func (m *Manager) beginSync(operation string) func(err error, warning string) {
	m.statusMu.Lock()
	m.activeOps++
	m.status.InProgress = true
	m.statusMu.Unlock()

	return func(err error, warning string) {
		m.statusMu.Lock()
		m.activeOps--
		failed := err != nil || warning != ""
		if err != nil {
			m.status.LastError = err.Error()
		} else if warning != "" {
			m.status.LastError = warning
			m.status.LastSyncAt = time.Now().UTC().Unix()
		} else {
			m.status.LastError = ""
			m.status.LastSyncAt = time.Now().UTC().Unix()
		}
		if m.activeOps <= 0 {
			m.activeOps = 0
			m.status.InProgress = false
		}
		m.statusMu.Unlock()

		m.updateBackoff(failed)
		statusValue := "success"
		details := map[string]any{"operation": operation}
		if err != nil {
			statusValue = "failure"
			details["error"] = err.Error()
		} else if warning != "" {
			statusValue = "warning"
			details["warning"] = warning
		}
		m.tracker.CaptureSyncOperation(operation, statusValue, details)
	}
}

func (m *Manager) runSync(operation string, fn func() error) error {
	m.waitForBackoff()
	finish := m.beginSync(operation)
	err := fn()
	if err != nil {
		if warning, ok := err.(syncWarning); ok {
			finish(nil, warning.Error())
			return nil
		}
	}
	finish(err, "")
	return err
}

func (m *Manager) waitForBackoff() {
	var delay time.Duration
	m.backoffMu.Lock()
	if !m.backoffUntil.IsZero() {
		delay = time.Until(m.backoffUntil)
	}
	m.backoffMu.Unlock()

	if delay > 0 {
		time.Sleep(delay)
	}
}

func (m *Manager) updateBackoff(failed bool) {
	m.backoffMu.Lock()
	defer m.backoffMu.Unlock()

	if !failed {
		m.backoffFailures = 0
		m.backoffUntil = time.Time{}
		return
	}

	if m.backoffFailures < 0 {
		m.backoffFailures = 0
	}
	m.backoffFailures++
	if m.backoffFailures > 10 {
		m.backoffFailures = 10
	}

	delay := min(syncBackoffBase*time.Duration(1<<uint(m.backoffFailures-1)), syncBackoffMax)
	m.backoffUntil = time.Now().Add(delay)
}

func (m *Manager) loadCloudClient(ctx context.Context) (*APIClient, *cloud.CloudConfig, error) {
	configManager, err := cloud.NewConfigManager()
	if err != nil {
		return nil, nil, err
	}
	cloudConfig, err := configManager.LoadConfig()
	if err != nil {
		return nil, nil, err
	}
	tokenManager, err := cloud.NewTokenManager()
	if err != nil {
		return nil, nil, err
	}

	client := NewAPIClient(cloudConfig, tokenManager, m.httpClient)
	m.stateMu.Lock()
	if cloudConfig.DeviceID != "" && m.state.DeviceID != cloudConfig.DeviceID {
		m.state.DeviceID = cloudConfig.DeviceID
		_ = m.stateManager.Save(m.state)
	}
	m.stateMu.Unlock()

	return client, cloudConfig, nil
}

func (m *Manager) pullPersonalSettings(ctx context.Context) error {
	return m.runSync("pull_personal_settings", func() error {
		client, _, err := m.loadCloudClient(ctx)
		if err != nil {
			return err
		}

		etag := ""
		m.stateMu.Lock()
		etag = m.state.Settings.ETag
		m.stateMu.Unlock()

		response, err := client.GetSettingsPersonal(ctx, etag)
		if err != nil {
			if errors.Is(err, ErrNotModified) {
				return nil
			}
			return err
		}

		payload, err := ParseSettingsPayload(response.Payload)
		if err != nil {
			return err
		}

		if err := m.applySettings(payload); err != nil {
			return err
		}

		m.stateMu.Lock()
		m.state.Settings = SyncItemState{
			ETag:           response.ETag,
			Version:        response.Version,
			UpdatedAt:      response.UpdatedAt,
			LocalUpdatedAt: 0,
		}
		m.state.LastSyncAt = time.Now().UTC().Unix()
		_ = m.stateManager.Save(m.state)
		m.stateMu.Unlock()

		return nil
	})
}

func (m *Manager) pushPersonalSettings(ctx context.Context) error {
	return m.runSync("push_personal_settings", func() error {
		client, cloudConfig, err := m.loadCloudClient(ctx)
		if err != nil {
			return err
		}

		baseVersion := int64(0)
		m.stateMu.Lock()
		baseVersion = m.state.Settings.Version
		m.stateMu.Unlock()

		payload := BuildSettingsPayload(m.configManager.GetConfig(), m.configManager.GetRenderSettings())
		request := map[string]any{
			"payload_format": payloadFormatJSON,
			"payload":        json.RawMessage(mustJSON(payload.Raw)),
			"base_version":   baseVersion,
			"device_id":      cloudConfig.DeviceID,
			"client_version": "swarmos-headless",
		}

		response, err := client.PutSettingsPersonal(ctx, request)
		if err != nil {
			if _, ok := isConflict(err); ok {
				return m.resolvePersonalSettingsConflict(ctx, client, cloudConfig, payload)
			}
			return err
		}

		m.stateMu.Lock()
		m.state.Settings = SyncItemState{
			ETag:           response.ETag,
			Version:        response.Version,
			UpdatedAt:      response.UpdatedAt,
			LocalUpdatedAt: 0,
		}
		m.state.LastSyncAt = time.Now().UTC().Unix()
		_ = m.stateManager.Save(m.state)
		m.stateMu.Unlock()

		return nil
	})
}

func (m *Manager) pullTeamSettings(ctx context.Context, teamID string) error {
	return m.runSync("pull_team_settings", func() error {
		client, _, err := m.loadCloudClient(ctx)
		if err != nil {
			return err
		}

		teamID = normalizeTeamID(teamID)
		etag := ""
		m.stateMu.Lock()
		etag = m.state.TeamSettings[teamID].ETag
		m.stateMu.Unlock()

		response, err := client.GetSettingsTeam(ctx, teamID, etag)
		if err != nil {
			if errors.Is(err, ErrNotModified) {
				return nil
			}
			return err
		}
		if m.isTeamSettingsRejected(teamID, response.ETag) {
			return nil
		}
		if m.isPendingTeamSettings(teamID, response.ETag) {
			return nil
		}

		payload, err := ParseSettingsPayload(response.Payload)
		if err != nil {
			return err
		}

		if response.Signature == nil {
			return errors.New("missing signature")
		}

		if err := m.verifySignature(ctx, payload.Raw, response.Signature); err != nil {
			return err
		}

		changes := diffTeamSettings(m.currentPolicyLayer(), payload)
		if len(changes) == 0 {
			if m.policyApplier != nil {
				layer := &core.PolicyLayer{
					Config:         payload.Config,
					RenderSettings: payload.RenderSettings,
				}
				m.policyApplier.SetPolicyLayer(layer)
			}
		} else {
			m.setPendingTeamSettings(&TeamSettingsPending{
				TeamID:    teamID,
				Changes:   changes,
				UpdatedAt: response.UpdatedAt,
				UpdatedBy: response.UpdatedBy,
				DeviceID:  response.DeviceID,
				Version:   response.Version,
				ETag:      response.ETag,
				Payload:   payload,
			})
			return nil
		}

		m.stateMu.Lock()
		if m.state.TeamSettings == nil {
			m.state.TeamSettings = map[string]SyncItemState{}
		}
		m.state.TeamSettings[teamID] = SyncItemState{
			ETag:           response.ETag,
			Version:        response.Version,
			UpdatedAt:      response.UpdatedAt,
			LocalUpdatedAt: 0,
		}
		m.state.LastSyncAt = time.Now().UTC().Unix()
		_ = m.stateManager.Save(m.state)
		m.stateMu.Unlock()

		return nil
	})
}

func (m *Manager) pushTeamSettings(ctx context.Context, teamID string) error {
	return m.runSync("push_team_settings", func() error {
		if m.hasPendingTeamSettings(teamID) {
			return errors.New("pending team settings require review")
		}
		identity, err := m.loadIdentity()
		if err != nil {
			return err
		}
		if !identity.IsAdmin {
			return errors.New("admin required for team settings")
		}

		client, cloudConfig, err := m.loadCloudClient(ctx)
		if err != nil {
			return err
		}

		teamID = normalizeTeamID(teamID)
		baseVersion := int64(0)
		m.stateMu.Lock()
		baseVersion = m.state.TeamSettings[teamID].Version
		m.stateMu.Unlock()

		payload := BuildSettingsPayload(m.configManager.GetConfig(), m.configManager.GetRenderSettings())
		request := map[string]any{
			"payload_format": payloadFormatJSON,
			"payload":        json.RawMessage(mustJSON(payload.Raw)),
			"base_version":   baseVersion,
			"device_id":      cloudConfig.DeviceID,
			"client_version": "swarmos-headless",
		}

		response, err := client.PutSettingsTeam(ctx, teamID, request)
		if err != nil {
			if _, ok := isConflict(err); ok {
				return m.resolveTeamSettingsConflict(ctx, client, cloudConfig, teamID, payload)
			}
			return err
		}

		m.stateMu.Lock()
		if m.state.TeamSettings == nil {
			m.state.TeamSettings = map[string]SyncItemState{}
		}
		m.state.TeamSettings[teamID] = SyncItemState{
			ETag:           response.ETag,
			Version:        response.Version,
			UpdatedAt:      response.UpdatedAt,
			LocalUpdatedAt: 0,
		}
		m.state.LastSyncAt = time.Now().UTC().Unix()
		_ = m.stateManager.Save(m.state)
		m.stateMu.Unlock()

		return nil
	})
}

func (m *Manager) pullProfiles(ctx context.Context) error {
	return m.runSync("pull_profiles", func() error {
		client, _, err := m.loadCloudClient(ctx)
		if err != nil {
			return err
		}

		etag := ""
		m.stateMu.Lock()
		etag = m.state.Profiles.ETag
		m.stateMu.Unlock()

		response, err := client.GetProfiles(ctx, etag)
		if err != nil {
			if errors.Is(err, ErrNotModified) {
				return nil
			}
			return err
		}

		payload, err := ParseProfilesPayload(response.Payload)
		if err != nil {
			return err
		}

		if err := m.applyProfiles(payload); err != nil {
			return err
		}

		m.stateMu.Lock()
		m.state.Profiles = SyncItemState{
			ETag:           response.ETag,
			Version:        response.Version,
			UpdatedAt:      response.UpdatedAt,
			LocalUpdatedAt: 0,
		}
		m.state.LastSyncAt = time.Now().UTC().Unix()
		_ = m.stateManager.Save(m.state)
		m.stateMu.Unlock()

		return nil
	})
}

func (m *Manager) pushProfiles(ctx context.Context) error {
	return m.runSync("push_profiles", func() error {
		client, cloudConfig, err := m.loadCloudClient(ctx)
		if err != nil {
			return err
		}

		baseVersion := int64(0)
		m.stateMu.Lock()
		baseVersion = m.state.Profiles.Version
		m.stateMu.Unlock()

		payload := BuildProfilesPayload(m.configManager.GetProfiles(), "")
		if m.profileStore != nil {
			payload.ActiveProfile = m.profileStore.GetActiveProfileID()
		}
		request := map[string]any{
			"payload_format": payloadFormatJSON,
			"payload":        json.RawMessage(mustJSON(payloadToMap(payload))),
			"base_version":   baseVersion,
			"device_id":      cloudConfig.DeviceID,
			"client_version": "swarmos-headless",
		}

		response, err := client.PutProfiles(ctx, request)
		if err != nil {
			if _, ok := isConflict(err); ok {
				return m.resolveProfilesConflict(ctx, client, cloudConfig, payload)
			}
			return err
		}

		m.stateMu.Lock()
		m.state.Profiles = SyncItemState{
			ETag:           response.ETag,
			Version:        response.Version,
			UpdatedAt:      response.UpdatedAt,
			LocalUpdatedAt: 0,
		}
		m.state.LastSyncAt = time.Now().UTC().Unix()
		_ = m.stateManager.Save(m.state)
		m.stateMu.Unlock()

		return nil
	})
}

func (m *Manager) pullConversations(ctx context.Context) error {
	return m.runSync("pull_conversations", func() error {
		if m.conversationStore == nil {
			return nil
		}

		warningCount := 0
		recordWarning := func() {
			warningCount++
		}

		client, _, err := m.loadCloudClient(ctx)
		if err != nil {
			return err
		}

		since := int64(0)
		m.stateMu.Lock()
		since = m.state.Conversations.Since
		m.stateMu.Unlock()

		for {
			response, err := client.ListConversations(ctx, since, 100)
			if err != nil {
				return err
			}

			for _, item := range response.Items {
				if item.DeletedAt > 0 {
					_ = m.conversationStore.Delete(ctx, item.ID)
					m.stateMu.Lock()
					delete(m.state.Conversations.ETags, item.ID)
					m.stateMu.Unlock()
					continue
				}

				m.stateMu.Lock()
				etag := m.state.Conversations.ETags[item.ID]
				m.stateMu.Unlock()
				if etag != "" && etag == item.ETag {
					continue
				}

				conversationResp, err := client.GetConversation(ctx, item.ID)
				if err != nil {
					return err
				}
				var conv conversation.Conversation
				switch conversationResp.PayloadFormat {
				case payloadFormatJSON:
					if err := json.Unmarshal(conversationResp.Payload, &conv); err != nil {
						recordWarning()
						continue
					}
				case payloadFormatEncrypted:
					var encryptedPayload string
					if err := json.Unmarshal(conversationResp.Payload, &encryptedPayload); err != nil {
						recordWarning()
						continue
					}
					plaintext, err := decryptPayload(m.configManager.ConfigDir(), encryptedPayload, conversationResp.Encryption)
					if err != nil {
						recordWarning()
						continue
					}
					if err := json.Unmarshal(plaintext, &conv); err != nil {
						recordWarning()
						continue
					}
				default:
					continue
				}
				if err := m.conversationStore.Save(ctx, &conv); err != nil {
					return err
				}

				m.stateMu.Lock()
				m.state.Conversations.ETags[item.ID] = item.ETag
				m.stateMu.Unlock()
			}

			if response.NextSince == since || len(response.Items) == 0 {
				since = response.NextSince
				break
			}
			since = response.NextSince
		}

		m.stateMu.Lock()
		m.state.Conversations.Since = since
		m.state.LastSyncAt = time.Now().UTC().Unix()
		_ = m.stateManager.Save(m.state)
		m.stateMu.Unlock()

		if warningCount > 0 {
			return newSyncWarning(fmt.Errorf("failed to decode %d conversation payload(s)", warningCount))
		}
		return nil
	})
}

func (m *Manager) uploadConversation(ctx context.Context, id string) error {
	return m.runSync("upload_conversation", func() error {
		if m.conversationStore == nil {
			return nil
		}

		client, cloudConfig, err := m.loadCloudClient(ctx)
		if err != nil {
			return err
		}

		conv, err := m.conversationStore.Load(ctx, id)
		if err != nil {
			return err
		}

		payloadBytes, err := json.Marshal(conv)
		if err != nil {
			return err
		}

		payloadFormat := payloadFormatJSON
		var payload any = json.RawMessage(payloadBytes)
		var encryption *EncryptionInfo
		if cfg := m.configManager.GetConfig(); cfg != nil && cfg.EncryptCloudData {
			encryptedPayload, info, err := encryptPayload(m.configManager.ConfigDir(), payloadBytes)
			if err != nil {
				return err
			}
			payloadFormat = payloadFormatEncrypted
			payload = encryptedPayload
			encryption = info
		}

		baseETag := ""
		m.stateMu.Lock()
		baseETag = m.state.Conversations.ETags[id]
		m.stateMu.Unlock()

		request := map[string]any{
			"payload_format": payloadFormat,
			"payload":        payload,
			"base_etag":      baseETag,
			"device_id":      cloudConfig.DeviceID,
		}
		if encryption != nil {
			request["encryption"] = encryption
		}

		response, err := client.PutConversation(ctx, id, request)
		if err != nil {
			if _, ok := isConflict(err); ok {
				return m.resolveConversationConflict(ctx, client, id, conv)
			}
			return err
		}

		m.stateMu.Lock()
		m.state.Conversations.ETags[id] = response.ETag
		m.state.LastSyncAt = time.Now().UTC().Unix()
		_ = m.stateManager.Save(m.state)
		m.stateMu.Unlock()

		return nil
	})
}

func (m *Manager) deleteConversation(ctx context.Context, id string) error {
	return m.runSync("delete_conversation", func() error {
		if m.conversationStore == nil {
			return nil
		}

		client, _, err := m.loadCloudClient(ctx)
		if err != nil {
			return err
		}

		_, err = client.DeleteConversation(ctx, id)
		if err != nil {
			return err
		}

		m.stateMu.Lock()
		delete(m.state.Conversations.ETags, id)
		m.state.LastSyncAt = time.Now().UTC().Unix()
		_ = m.stateManager.Save(m.state)
		m.stateMu.Unlock()

		return nil
	})
}

func (m *Manager) applySettings(payload *SettingsPayload) error {
	cfg := m.configManager.GetConfig()
	for key, value := range payload.Config {
		core.ApplyConfigValue(cfg, key, value)
	}

	render := m.configManager.GetRenderSettings()
	for key, value := range payload.RenderSettings {
		core.ApplyRenderValue(render, key, value)
	}

	if err := m.configManager.SetConfig(cfg); err != nil {
		return err
	}
	if err := m.configManager.SetRenderSettings(render); err != nil {
		return err
	}

	return m.configManager.Save(context.Background())
}

func (m *Manager) applyProfiles(payload *ProfilesPayload) error {
	profiles := m.configManager.GetProfiles()
	for _, profile := range profiles {
		_ = m.configManager.DeleteProfile(profile.Name)
	}
	for _, profile := range payload.Profiles {
		if err := m.configManager.SetProfile(profile); err != nil {
			return err
		}
	}
	if payload.ActiveProfile != "" && m.profileStore != nil {
		_ = m.profileStore.SetDefaultProfile(payload.ActiveProfile)
	}
	return m.configManager.Save(context.Background())
}

func (m *Manager) verifySignature(ctx context.Context, payload map[string]any, signature *signatureInfo) error {
	if signature == nil {
		return errors.New("signature missing")
	}
	if signature.Alg != "RSASSA_PSS_SHA_256" {
		return fmt.Errorf("unsupported signature algorithm %s", signature.Alg)
	}

	publicKey, err := m.getPublicKey(ctx, signature.KeyID)
	if err != nil {
		return err
	}

	canonical, err := CanonicalJSON(payload)
	if err != nil {
		return err
	}

	hash := sha256.Sum256(canonical)
	sigBytes, err := base64.StdEncoding.DecodeString(signature.Signature)
	if err != nil {
		return err
	}

	return rsa.VerifyPSS(publicKey, crypto.SHA256, hash[:], sigBytes, nil)
}

func (m *Manager) getPublicKey(ctx context.Context, keyID string) (*rsa.PublicKey, error) {
	m.keyCacheMu.Lock()
	cache := m.keyCache
	m.keyCacheMu.Unlock()

	if cache != nil {
		if key, ok := cache.Keys[keyID]; ok {
			return key, nil
		}
		if time.Since(cache.UpdatedAt) < 10*time.Minute {
			return nil, fmt.Errorf("unknown key %s", keyID)
		}
	}

	client, _, err := m.loadCloudClient(ctx)
	if err != nil {
		return nil, err
	}

	keysResponse, err := client.GetKeys(ctx)
	if err != nil {
		return nil, err
	}

	keys := map[string]*rsa.PublicKey{}
	for _, keyInfo := range keysResponse.Keys {
		block, _ := pem.Decode([]byte(keyInfo.PublicKeyPEM))
		if block == nil {
			continue
		}
		parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
		if err != nil {
			continue
		}
		rsaKey, ok := parsed.(*rsa.PublicKey)
		if !ok {
			continue
		}
		keys[keyInfo.KeyID] = rsaKey
	}

	m.keyCacheMu.Lock()
	m.keyCache = &keyCache{Keys: keys, UpdatedAt: time.Now()}
	m.keyCacheMu.Unlock()

	if key, ok := keys[keyID]; ok {
		return key, nil
	}

	return nil, fmt.Errorf("unknown key %s", keyID)
}

func (m *Manager) loadIdentity() (cloud.CloudIdentity, error) {
	tokenManager, err := cloud.NewTokenManager()
	if err != nil {
		return cloud.CloudIdentity{}, err
	}
	return cloud.ResolveIdentity(tokenManager), nil
}

func normalizeTeamID(teamID string) string {
	if strings.TrimSpace(teamID) == "" {
		return "Administrators"
	}
	return teamID
}

func payloadToMap(payload ProfilesPayload) map[string]any {
	profiles := make([]map[string]any, 0, len(payload.Profiles))
	for _, profile := range payload.Profiles {
		profiles = append(profiles, map[string]any{
			"name":         profile.Name,
			"description":  profile.Description,
			"systemPrompt": profile.SystemPrompt,
			"temperature":  profile.Temperature,
			"maxTokens":    profile.MaxTokens,
		})
	}

	return map[string]any{
		"activeProfile": payload.ActiveProfile,
		"profiles":      profiles,
	}
}

func mustJSON(value any) []byte {
	data, _ := json.Marshal(value)
	return data
}

func isConflict(err error) (*apiError, bool) {
	var apiErr *apiError
	if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusConflict {
		return apiErr, true
	}
	return nil, false
}

func shouldPreferLocal(localUpdatedAt int64, remoteUpdatedAt int64, localDeviceID string, remoteDeviceID string) bool {
	if localUpdatedAt <= 0 {
		return false
	}
	if localUpdatedAt > remoteUpdatedAt {
		return true
	}
	if localUpdatedAt < remoteUpdatedAt {
		return false
	}
	if localDeviceID == "" {
		return false
	}
	if remoteDeviceID == "" {
		return true
	}
	return localDeviceID >= remoteDeviceID
}

func (m *Manager) resolvePersonalSettingsConflict(ctx context.Context, client *APIClient, cloudConfig *cloud.CloudConfig, payload SettingsPayload) error {
	response, err := client.GetSettingsPersonal(ctx, "")
	if err != nil {
		return err
	}

	localUpdatedAt := int64(0)
	m.stateMu.Lock()
	localUpdatedAt = m.state.Settings.LocalUpdatedAt
	m.stateMu.Unlock()

	if shouldPreferLocal(localUpdatedAt, response.UpdatedAt, cloudConfig.DeviceID, response.DeviceID) {
		request := map[string]any{
			"payload_format": payloadFormatJSON,
			"payload":        json.RawMessage(mustJSON(payload.Raw)),
			"base_version":   response.Version,
			"device_id":      cloudConfig.DeviceID,
			"client_version": "swarmos-headless",
		}
		updated, err := client.PutSettingsPersonal(ctx, request)
		if err != nil {
			return err
		}
		m.stateMu.Lock()
		m.state.Settings = SyncItemState{
			ETag:           updated.ETag,
			Version:        updated.Version,
			UpdatedAt:      updated.UpdatedAt,
			LocalUpdatedAt: 0,
		}
		m.state.LastSyncAt = time.Now().UTC().Unix()
		_ = m.stateManager.Save(m.state)
		m.stateMu.Unlock()
		return nil
	}

	serverPayload, err := ParseSettingsPayload(response.Payload)
	if err != nil {
		return err
	}
	if err := m.applySettings(serverPayload); err != nil {
		return err
	}

	m.stateMu.Lock()
	m.state.Settings = SyncItemState{
		ETag:           response.ETag,
		Version:        response.Version,
		UpdatedAt:      response.UpdatedAt,
		LocalUpdatedAt: 0,
	}
	m.state.LastSyncAt = time.Now().UTC().Unix()
	_ = m.stateManager.Save(m.state)
	m.stateMu.Unlock()
	return nil
}

func (m *Manager) resolveTeamSettingsConflict(ctx context.Context, client *APIClient, cloudConfig *cloud.CloudConfig, teamID string, payload SettingsPayload) error {
	response, err := client.GetSettingsTeam(ctx, teamID, "")
	if err != nil {
		return err
	}
	if response.Signature == nil {
		return errors.New("missing signature")
	}

	localUpdatedAt := int64(0)
	m.stateMu.Lock()
	localUpdatedAt = m.state.TeamSettings[teamID].LocalUpdatedAt
	m.stateMu.Unlock()

	if shouldPreferLocal(localUpdatedAt, response.UpdatedAt, cloudConfig.DeviceID, response.DeviceID) {
		request := map[string]any{
			"payload_format": payloadFormatJSON,
			"payload":        json.RawMessage(mustJSON(payload.Raw)),
			"base_version":   response.Version,
			"device_id":      cloudConfig.DeviceID,
			"client_version": "swarmos-headless",
		}
		updated, err := client.PutSettingsTeam(ctx, teamID, request)
		if err != nil {
			return err
		}
		m.stateMu.Lock()
		if m.state.TeamSettings == nil {
			m.state.TeamSettings = map[string]SyncItemState{}
		}
		m.state.TeamSettings[teamID] = SyncItemState{
			ETag:           updated.ETag,
			Version:        updated.Version,
			UpdatedAt:      updated.UpdatedAt,
			LocalUpdatedAt: 0,
		}
		m.state.LastSyncAt = time.Now().UTC().Unix()
		_ = m.stateManager.Save(m.state)
		m.stateMu.Unlock()
		return nil
	}

	serverPayload, err := ParseSettingsPayload(response.Payload)
	if err != nil {
		return err
	}
	if err := m.verifySignature(ctx, serverPayload.Raw, response.Signature); err != nil {
		return err
	}
	changes := diffTeamSettings(m.currentPolicyLayer(), serverPayload)
	if len(changes) == 0 {
		if m.policyApplier != nil {
			m.policyApplier.SetPolicyLayer(&core.PolicyLayer{
				Config:         serverPayload.Config,
				RenderSettings: serverPayload.RenderSettings,
			})
		}
		m.stateMu.Lock()
		if m.state.TeamSettings == nil {
			m.state.TeamSettings = map[string]SyncItemState{}
		}
		m.state.TeamSettings[teamID] = SyncItemState{
			ETag:           response.ETag,
			Version:        response.Version,
			UpdatedAt:      response.UpdatedAt,
			LocalUpdatedAt: 0,
		}
		m.state.LastSyncAt = time.Now().UTC().Unix()
		_ = m.stateManager.Save(m.state)
		m.stateMu.Unlock()
		return nil
	}

	m.setPendingTeamSettings(&TeamSettingsPending{
		TeamID:    teamID,
		Changes:   changes,
		UpdatedAt: response.UpdatedAt,
		UpdatedBy: response.UpdatedBy,
		DeviceID:  response.DeviceID,
		Version:   response.Version,
		ETag:      response.ETag,
		Payload:   serverPayload,
	})
	return nil
}

func (m *Manager) resolveProfilesConflict(ctx context.Context, client *APIClient, cloudConfig *cloud.CloudConfig, payload ProfilesPayload) error {
	response, err := client.GetProfiles(ctx, "")
	if err != nil {
		return err
	}

	localUpdatedAt := int64(0)
	m.stateMu.Lock()
	localUpdatedAt = m.state.Profiles.LocalUpdatedAt
	m.stateMu.Unlock()

	if shouldPreferLocal(localUpdatedAt, response.UpdatedAt, cloudConfig.DeviceID, response.DeviceID) {
		request := map[string]any{
			"payload_format": payloadFormatJSON,
			"payload":        json.RawMessage(mustJSON(payloadToMap(payload))),
			"base_version":   response.Version,
			"device_id":      cloudConfig.DeviceID,
			"client_version": "swarmos-headless",
		}
		updated, err := client.PutProfiles(ctx, request)
		if err != nil {
			return err
		}
		m.stateMu.Lock()
		m.state.Profiles = SyncItemState{
			ETag:           updated.ETag,
			Version:        updated.Version,
			UpdatedAt:      updated.UpdatedAt,
			LocalUpdatedAt: 0,
		}
		m.state.LastSyncAt = time.Now().UTC().Unix()
		_ = m.stateManager.Save(m.state)
		m.stateMu.Unlock()
		return nil
	}

	serverPayload, err := ParseProfilesPayload(response.Payload)
	if err != nil {
		return err
	}
	if err := m.applyProfiles(serverPayload); err != nil {
		return err
	}

	m.stateMu.Lock()
	m.state.Profiles = SyncItemState{
		ETag:           response.ETag,
		Version:        response.Version,
		UpdatedAt:      response.UpdatedAt,
		LocalUpdatedAt: 0,
	}
	m.state.LastSyncAt = time.Now().UTC().Unix()
	_ = m.stateManager.Save(m.state)
	m.stateMu.Unlock()
	return nil
}

func (m *Manager) resolveConversationConflict(ctx context.Context, client *APIClient, id string, local *conversation.Conversation) error {
	response, err := client.GetConversation(ctx, id)
	if err != nil {
		return err
	}

	var server conversation.Conversation
	switch response.PayloadFormat {
	case payloadFormatJSON:
		if err := json.Unmarshal(response.Payload, &server); err != nil {
			return newSyncWarning(err)
		}
	case payloadFormatEncrypted:
		var encryptedPayload string
		if err := json.Unmarshal(response.Payload, &encryptedPayload); err != nil {
			return newSyncWarning(err)
		}
		plaintext, err := decryptPayload(m.configManager.ConfigDir(), encryptedPayload, response.Encryption)
		if err != nil {
			return newSyncWarning(err)
		}
		if err := json.Unmarshal(plaintext, &server); err != nil {
			return newSyncWarning(err)
		}
	default:
		return nil
	}

	if local.UpdatedAt.After(server.UpdatedAt) {
		payloadBytes, err := json.Marshal(local)
		if err != nil {
			return err
		}
		payloadFormat := payloadFormatJSON
		var payload any = json.RawMessage(payloadBytes)
		var encryption *EncryptionInfo
		if cfg := m.configManager.GetConfig(); cfg != nil && cfg.EncryptCloudData {
			encryptedPayload, info, err := encryptPayload(m.configManager.ConfigDir(), payloadBytes)
			if err != nil {
				return err
			}
			payloadFormat = payloadFormatEncrypted
			payload = encryptedPayload
			encryption = info
		}
		request := map[string]any{
			"payload_format": payloadFormat,
			"payload":        payload,
			"base_etag":      response.ETag,
			"device_id":      m.state.DeviceID,
		}
		if encryption != nil {
			request["encryption"] = encryption
		}
		updated, err := client.PutConversation(ctx, id, request)
		if err != nil {
			return err
		}
		m.stateMu.Lock()
		m.state.Conversations.ETags[id] = updated.ETag
		m.state.LastSyncAt = time.Now().UTC().Unix()
		_ = m.stateManager.Save(m.state)
		m.stateMu.Unlock()
		return nil
	}

	if err := m.conversationStore.Save(ctx, &server); err != nil {
		return err
	}
	m.stateMu.Lock()
	m.state.Conversations.ETags[id] = response.ETag
	m.state.LastSyncAt = time.Now().UTC().Unix()
	_ = m.stateManager.Save(m.state)
	m.stateMu.Unlock()
	return nil
}

func (m *Manager) PendingTeamSettings() *TeamSettingsPending {
	m.pendingMu.Lock()
	defer m.pendingMu.Unlock()
	if m.pendingTeam == nil {
		return nil
	}
	pending := *m.pendingTeam
	if len(m.pendingTeam.Changes) > 0 {
		pending.Changes = append([]TeamSettingsChange{}, m.pendingTeam.Changes...)
	}
	return &pending
}

func (m *Manager) ApplyPendingTeamSettings(teamID string) error {
	m.pendingMu.Lock()
	pending := m.pendingTeam
	m.pendingMu.Unlock()
	if pending == nil {
		return errors.New("no pending team settings")
	}
	if teamID != "" && normalizeTeamID(teamID) != pending.TeamID {
		return errors.New("team settings pending for different team")
	}
	if pending.Payload == nil {
		return errors.New("pending payload missing")
	}

	if m.policyApplier != nil {
		m.policyApplier.SetPolicyLayer(&core.PolicyLayer{
			Config:         pending.Payload.Config,
			RenderSettings: pending.Payload.RenderSettings,
		})
	}

	m.stateMu.Lock()
	if m.state.TeamSettings == nil {
		m.state.TeamSettings = map[string]SyncItemState{}
	}
	m.state.TeamSettings[pending.TeamID] = SyncItemState{
		ETag:           pending.ETag,
		Version:        pending.Version,
		UpdatedAt:      pending.UpdatedAt,
		LocalUpdatedAt: 0,
	}
	delete(m.state.TeamSettingsRejections, pending.TeamID)
	m.state.LastSyncAt = time.Now().UTC().Unix()
	_ = m.stateManager.Save(m.state)
	m.stateMu.Unlock()

	m.pendingMu.Lock()
	m.pendingTeam = nil
	m.pendingMu.Unlock()
	return nil
}

func (m *Manager) RejectPendingTeamSettings(teamID string) error {
	m.pendingMu.Lock()
	pending := m.pendingTeam
	m.pendingMu.Unlock()
	if pending == nil {
		return errors.New("no pending team settings")
	}
	if teamID != "" && normalizeTeamID(teamID) != pending.TeamID {
		return errors.New("team settings pending for different team")
	}

	m.stateMu.Lock()
	if m.state.TeamSettingsRejections == nil {
		m.state.TeamSettingsRejections = map[string]string{}
	}
	m.state.TeamSettingsRejections[pending.TeamID] = pending.ETag
	_ = m.stateManager.Save(m.state)
	m.stateMu.Unlock()

	m.pendingMu.Lock()
	m.pendingTeam = nil
	m.pendingMu.Unlock()
	return nil
}

func (m *Manager) setPendingTeamSettings(pending *TeamSettingsPending) {
	m.pendingMu.Lock()
	m.pendingTeam = pending
	m.pendingMu.Unlock()
}

func (m *Manager) hasPendingTeamSettings(teamID string) bool {
	m.pendingMu.Lock()
	defer m.pendingMu.Unlock()
	if m.pendingTeam == nil {
		return false
	}
	if teamID == "" {
		return true
	}
	return m.pendingTeam.TeamID == normalizeTeamID(teamID)
}

func (m *Manager) isPendingTeamSettings(teamID string, etag string) bool {
	m.pendingMu.Lock()
	defer m.pendingMu.Unlock()
	if m.pendingTeam == nil {
		return false
	}
	return m.pendingTeam.TeamID == normalizeTeamID(teamID) && m.pendingTeam.ETag == etag
}

func (m *Manager) isTeamSettingsRejected(teamID string, etag string) bool {
	m.stateMu.Lock()
	defer m.stateMu.Unlock()
	if m.state.TeamSettingsRejections == nil {
		return false
	}
	return m.state.TeamSettingsRejections[normalizeTeamID(teamID)] == etag
}

func (m *Manager) currentPolicyLayer() *core.PolicyLayer {
	if m.policyApplier == nil {
		return nil
	}
	return m.policyApplier.GetPolicyLayer()
}

func diffTeamSettings(current *core.PolicyLayer, incoming *SettingsPayload) []TeamSettingsChange {
	changes := []TeamSettingsChange{}
	currentConfig := map[string]any{}
	currentRender := map[string]any{}
	if current != nil {
		currentConfig = current.Config
		currentRender = current.RenderSettings
	}

	changes = append(changes, diffSettingsSection("config", currentConfig, incoming.Config)...)
	changes = append(changes, diffSettingsSection("renderSettings", currentRender, incoming.RenderSettings)...)
	return changes
}

func diffSettingsSection(section string, before map[string]any, after map[string]any) []TeamSettingsChange {
	changes := []TeamSettingsChange{}
	seen := map[string]struct{}{}
	for key := range before {
		seen[key] = struct{}{}
	}
	for key := range after {
		seen[key] = struct{}{}
	}
	keys := make([]string, 0, len(seen))
	for key := range seen {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		beforeVal, beforeOk := before[key]
		afterVal, afterOk := after[key]
		if beforeOk && afterOk && valuesEqual(beforeVal, afterVal) {
			continue
		}
		changes = append(changes, TeamSettingsChange{
			Section: section,
			Key:     key,
			Before:  formatSettingValue(beforeOk, beforeVal),
			After:   formatSettingValue(afterOk, afterVal),
		})
	}
	return changes
}

func valuesEqual(a any, b any) bool {
	return fmt.Sprintf("%#v", a) == fmt.Sprintf("%#v", b)
}

func formatSettingValue(ok bool, value any) string {
	if !ok {
		return "unset"
	}
	switch typed := value.(type) {
	case string:
		return typed
	case bool:
		if typed {
			return "true"
		}
		return "false"
	case int:
		return fmt.Sprintf("%d", typed)
	case int64:
		return fmt.Sprintf("%d", typed)
	case float64:
		return fmt.Sprintf("%g", typed)
	default:
		return fmt.Sprintf("%v", typed)
	}
}
