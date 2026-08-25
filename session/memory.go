package session

import (
	"context"
	"fmt"
	"math"
	"sync"

	"github.com/JIAOZAI1/acore/model"
)

// MemoryService stores conversation history in process memory. Its zero value
// is ready for use. Data is lost when the process exits.
type MemoryService struct {
	mu    sync.RWMutex
	byKey map[Key]Snapshot
}

// NewMemoryService creates an empty in-memory Session service.
func NewMemoryService() *MemoryService {
	return &MemoryService{byKey: make(map[Key]Snapshot)}
}

// Load returns an isolated snapshot for key. An absent conversation is
// represented by revision zero and no messages.
func (s *MemoryService) Load(ctx context.Context, key Key) (Snapshot, error) {
	if err := validateContext(ctx); err != nil {
		return Snapshot{}, err
	}
	if err := validateKey(key); err != nil {
		return Snapshot{}, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}

	snapshot, ok := s.byKey[key]
	if !ok {
		return Snapshot{}, nil
	}
	snapshot.Messages = cloneMessages(snapshot.Messages)
	return snapshot, nil
}

// Append atomically appends messages when expected matches the current
// revision and returns the new revision.
func (s *MemoryService) Append(ctx context.Context, key Key, expected Revision, messages []model.Message) (Revision, error) {
	if err := validateContext(ctx); err != nil {
		return 0, err
	}
	if err := validateKey(key); err != nil {
		return 0, err
	}
	if len(messages) == 0 {
		return 0, ErrInvalidMessages
	}
	clonedMessages := cloneMessages(messages)

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if s.byKey == nil {
		s.byKey = make(map[Key]Snapshot)
	}

	current := s.byKey[key]
	if current.Revision != expected {
		return 0, fmt.Errorf("%w: expected revision %d", ErrConflict, expected)
	}
	if current.Revision == Revision(math.MaxUint64) {
		return 0, ErrRevisionExhausted
	}

	newRevision := current.Revision + 1
	combined := make([]model.Message, 0, len(current.Messages)+len(clonedMessages))
	combined = append(combined, current.Messages...)
	combined = append(combined, clonedMessages...)
	s.byKey[key] = Snapshot{Revision: newRevision, Messages: combined}
	return newRevision, nil
}

func validateContext(ctx context.Context) error {
	if ctx == nil {
		return ErrInvalidContext
	}
	return ctx.Err()
}

func validateKey(key Key) error {
	if key.Scope == "" {
		return fmt.Errorf("%w: empty scope", ErrInvalidKey)
	}
	if key.ID == "" {
		return fmt.Errorf("%w: empty ID", ErrInvalidKey)
	}
	return nil
}

var _ Service = (*MemoryService)(nil)
