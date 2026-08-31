package cloudsync

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

const syncStateFileName string = "cloud_sync_state.json"

type SyncItemState struct {
	ETag           string `json:"etag,omitempty"`
	Version        int64  `json:"version,omitempty"`
	UpdatedAt      int64  `json:"updated_at,omitempty"`
	LocalUpdatedAt int64  `json:"local_updated_at,omitempty"`
}

type SyncConversationState struct {
	Since int64             `json:"since,omitempty"`
	ETags map[string]string `json:"etags,omitempty"`
}

type SyncState struct {
	DeviceID               string                   `json:"device_id,omitempty"`
	Settings               SyncItemState            `json:"settings"`
	Profiles               SyncItemState            `json:"profiles"`
	TeamSettings           map[string]SyncItemState `json:"team_settings,omitempty"`
	TeamSettingsRejections map[string]string        `json:"team_settings_rejections,omitempty"`
	Conversations          SyncConversationState    `json:"conversations"`
	LastSyncAt             int64                    `json:"last_sync_at,omitempty"`
}

type StateManager struct {
	path string
	mu   sync.Mutex
}

func NewStateManager(configDir string) *StateManager {
	return &StateManager{path: filepath.Join(configDir, syncStateFileName)}
}

func (sm *StateManager) Load() (*SyncState, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	data, err := os.ReadFile(sm.path)
	if err != nil {
		if os.IsNotExist(err) {
			return &SyncState{
				Conversations:          SyncConversationState{ETags: map[string]string{}},
				TeamSettings:           map[string]SyncItemState{},
				TeamSettingsRejections: map[string]string{},
			}, nil
		}
		return nil, fmt.Errorf("failed to read sync state: %w", err)
	}

	var state SyncState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("failed to parse sync state: %w", err)
	}

	if state.Conversations.ETags == nil {
		state.Conversations.ETags = map[string]string{}
	}
	if state.TeamSettings == nil {
		state.TeamSettings = map[string]SyncItemState{}
	}
	if state.TeamSettingsRejections == nil {
		state.TeamSettingsRejections = map[string]string{}
	}

	return &state, nil
}

func (sm *StateManager) Save(state *SyncState) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if state == nil {
		return fmt.Errorf("sync state is nil")
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal sync state: %w", err)
	}

	if err := os.WriteFile(sm.path, data, 0644); err != nil {
		return fmt.Errorf("failed to write sync state: %w", err)
	}

	return nil
}
