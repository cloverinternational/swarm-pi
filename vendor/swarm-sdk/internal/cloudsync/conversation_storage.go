package cloudsync

import (
	"context"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation/storage"
)

// SyncStorage wraps conversation storage to enqueue cloud sync operations.
type SyncStorage struct {
	base    storage.Storage
	manager *Manager
}

// NewSyncStorage creates a sync-enabled storage wrapper.
func NewSyncStorage(base storage.Storage, manager *Manager) *SyncStorage {
	return &SyncStorage{base: base, manager: manager}
}

func (s *SyncStorage) Save(ctx context.Context, conv *conversation.Conversation) error {
	if err := s.base.Save(ctx, conv); err != nil {
		return err
	}
	if s.manager != nil {
		s.manager.QueueConversationUpload(conv.ID)
	}
	return nil
}

func (s *SyncStorage) Load(ctx context.Context, id string) (*conversation.Conversation, error) {
	return s.base.Load(ctx, id)
}

func (s *SyncStorage) Delete(ctx context.Context, id string) error {
	if err := s.base.Delete(ctx, id); err != nil {
		return err
	}
	if s.manager != nil {
		s.manager.QueueConversationDelete(id)
	}
	return nil
}

func (s *SyncStorage) Query(ctx context.Context, filter storage.Filter) ([]*conversation.Conversation, error) {
	return s.base.Query(ctx, filter)
}

func (s *SyncStorage) Stream(ctx context.Context, id string) (<-chan *conversation.Message, error) {
	return s.base.Stream(ctx, id)
}

func (s *SyncStorage) List(ctx context.Context) ([]*conversation.Conversation, error) {
	return s.base.List(ctx)
}

func (s *SyncStorage) Exists(ctx context.Context, id string) (bool, error) {
	return s.base.Exists(ctx, id)
}

func (s *SyncStorage) Close() error {
	return s.base.Close()
}
