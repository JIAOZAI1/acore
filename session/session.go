// Package session defines conversation-history contracts shared by agent run
// strategies and storage implementations.
package session

import (
	"context"
	"errors"

	"github.com/JIAOZAI1/acore/model"
)

// Key identifies one conversation in an application-defined isolation scope.
// Scope and ID must both be non-empty.
type Key struct {
	Scope string `json:"scope"`
	ID    string `json:"id"`
}

// Revision is the optimistic-concurrency version of one conversation.
// Revision zero represents an absent conversation.
type Revision uint64

// Snapshot is one conversation-history view returned by a Service.
type Snapshot struct {
	Revision Revision        `json:"revision"`
	Messages []model.Message `json:"messages"`
}

// Service loads and atomically appends replayable conversation messages.
// Implementations must be safe for concurrent use and must not share mutable
// message data with callers after a method returns.
type Service interface {
	Load(context.Context, Key) (Snapshot, error)
	Append(context.Context, Key, Revision, []model.Message) (Revision, error)
}

var (
	// ErrInvalidContext indicates that a Session operation received a nil
	// context.
	ErrInvalidContext = errors.New("session: invalid context")
	// ErrInvalidKey indicates that a Session key has an empty scope or ID.
	ErrInvalidKey = errors.New("session: invalid key")
	// ErrInvalidMessages indicates that an append batch has no messages.
	ErrInvalidMessages = errors.New("session: invalid messages")
	// ErrInvalidSnapshot indicates that a Service returned an inconsistent
	// snapshot.
	ErrInvalidSnapshot = errors.New("session: invalid snapshot")
	// ErrConflict indicates that the expected revision did not match the
	// current conversation revision.
	ErrConflict = errors.New("session: conflict")
	// ErrRevisionExhausted indicates that a conversation revision cannot be
	// incremented without overflow.
	ErrRevisionExhausted = errors.New("session: revision exhausted")
)
