package singleturn

import (
	"github.com/JIAOZAI1/acore/contextwindow"
	"github.com/JIAOZAI1/acore/internal/nilcheck"
	"github.com/JIAOZAI1/acore/session"
)

// SingleTurnBuilder assembles a SingleTurnStrategy during application setup.
// Session support is optional. A successful Build freezes the Builder.
type SingleTurnBuilder struct {
	session          session.Service
	contextWindow    contextwindow.Reducer
	sessionSet       bool
	contextWindowSet bool
	built            bool
}

// NewSingleTurnBuilder creates an empty SingleTurnBuilder.
func NewSingleTurnBuilder() *SingleTurnBuilder {
	return &SingleTurnBuilder{}
}

// NewBuilder creates an empty SingleTurn builder.
func NewBuilder() *SingleTurnBuilder { return NewSingleTurnBuilder() }

// UseSession configures conversation history for the built strategy.
func (b *SingleTurnBuilder) UseSession(service session.Service) error {
	if b.built {
		return ErrSingleTurnBuilderBuilt
	}
	if b.sessionSet {
		return ErrSessionServiceAlreadySet
	}
	if nilcheck.IsNil(service) {
		return ErrNilSessionService
	}

	b.session = service
	b.sessionSet = true
	return nil
}

// UseContextWindow configures message-history reduction for every model call.
func (b *SingleTurnBuilder) UseContextWindow(reducer contextwindow.Reducer) error {
	if b.built {
		return ErrSingleTurnBuilderBuilt
	}
	if b.contextWindowSet {
		return ErrContextWindowAlreadySet
	}
	if nilcheck.IsNil(reducer) {
		return ErrNilContextWindowReducer
	}

	b.contextWindow = reducer
	b.contextWindowSet = true
	return nil
}

// Build snapshots the configuration into an immutable SingleTurnStrategy.
func (b *SingleTurnBuilder) Build() (*SingleTurnStrategy, error) {
	if b.built {
		return nil, ErrSingleTurnBuilderBuilt
	}
	strategy := &SingleTurnStrategy{
		session:       b.session,
		contextWindow: b.contextWindow,
	}
	b.built = true
	return strategy, nil
}
