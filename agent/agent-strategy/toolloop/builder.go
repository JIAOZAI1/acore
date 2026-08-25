package toolloop

import (
	"fmt"

	"github.com/JIAOZAI1/acore/contextwindow"
	"github.com/JIAOZAI1/acore/internal/nilcheck"
	"github.com/JIAOZAI1/acore/session"
	"github.com/JIAOZAI1/acore/tool"
)

const (
	defaultMaxModelTurns      = 8
	defaultMaxToolCalls       = 32
	defaultMaxToolResultBytes = 64 * 1024
)

// ToolLoopLimits bounds model turns, tool side effects, and tool-result data
// accepted by one ToolLoopStrategy run.
type ToolLoopLimits struct {
	MaxModelTurns      int `json:"maxModelTurns"`
	MaxToolCalls       int `json:"maxToolCalls"`
	MaxToolResultBytes int `json:"maxToolResultBytes"`
}

// DefaultToolLoopLimits returns conservative defaults for one tool loop.
func DefaultToolLoopLimits() ToolLoopLimits {
	return ToolLoopLimits{
		MaxModelTurns:      defaultMaxModelTurns,
		MaxToolCalls:       defaultMaxToolCalls,
		MaxToolResultBytes: defaultMaxToolResultBytes,
	}
}

// ToolErrorMode controls how ToolLoopStrategy handles tool execution errors.
type ToolErrorMode uint8

const (
	// ToolErrorModeFeedback sends a sanitized failure message to the model and
	// continues the loop.
	ToolErrorModeFeedback ToolErrorMode = iota
	// ToolErrorModeFailFast reports the sanitized tool event and then terminates
	// the stream with the original error in its error chain.
	ToolErrorModeFailFast
)

// String returns the stable lower-camel-case name of m.
func (m ToolErrorMode) String() string {
	switch m {
	case ToolErrorModeFeedback:
		return "feedback"
	case ToolErrorModeFailFast:
		return "failFast"
	default:
		return "unknown"
	}
}

// ToolLoopBuilder assembles an immutable ToolLoopStrategy during application
// startup. It is intended for single-goroutine setup. A successful Build
// freezes it.
type ToolLoopBuilder struct {
	tools            tool.Service
	session          session.Service
	contextWindow    contextwindow.Reducer
	limits           ToolLoopLimits
	errorMode        ToolErrorMode
	toolsSet         bool
	sessionSet       bool
	contextWindowSet bool
	limitsSet        bool
	errorModeSet     bool
	built            bool
}

// UseSession configures conversation history for the built strategy.
func (b *ToolLoopBuilder) UseSession(service session.Service) error {
	if b.built {
		return ErrToolLoopBuilderBuilt
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
func (b *ToolLoopBuilder) UseContextWindow(reducer contextwindow.Reducer) error {
	if b.built {
		return ErrToolLoopBuilderBuilt
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

// NewToolLoopBuilder creates a builder with default limits and feedback error
// handling. A tool service must be configured before Build.
func NewToolLoopBuilder() *ToolLoopBuilder {
	return &ToolLoopBuilder{
		limits:    DefaultToolLoopLimits(),
		errorMode: ToolErrorModeFeedback,
	}
}

// NewBuilder creates an empty ToolLoop builder.
func NewBuilder() *ToolLoopBuilder { return NewToolLoopBuilder() }

// UseTools configures the catalog and execution service used by the strategy.
func (b *ToolLoopBuilder) UseTools(service tool.Service) error {
	if b.built {
		return ErrToolLoopBuilderBuilt
	}
	if b.toolsSet {
		return ErrToolServiceAlreadySet
	}
	if nilcheck.IsNil(service) {
		return ErrNilToolService
	}

	b.tools = service
	b.toolsSet = true
	return nil
}

// SetLimits replaces all per-run ToolLoop limits.
func (b *ToolLoopBuilder) SetLimits(limits ToolLoopLimits) error {
	if b.built {
		return ErrToolLoopBuilderBuilt
	}
	if b.limitsSet {
		return fmt.Errorf("%w: tool loop limits", ErrConfigAlreadySet)
	}
	if err := validateToolLoopLimits(limits); err != nil {
		return err
	}

	b.limits = limits
	b.limitsSet = true
	return nil
}

// SetToolErrorMode configures tool execution failure handling.
func (b *ToolLoopBuilder) SetToolErrorMode(mode ToolErrorMode) error {
	if b.built {
		return ErrToolLoopBuilderBuilt
	}
	if b.errorModeSet {
		return fmt.Errorf("%w: tool error mode", ErrConfigAlreadySet)
	}
	if !validToolErrorMode(mode) {
		return fmt.Errorf("%w: %d", ErrInvalidToolErrorMode, mode)
	}

	b.errorMode = mode
	b.errorModeSet = true
	return nil
}

// Build validates and snapshots the tool catalog into a ToolLoopStrategy.
func (b *ToolLoopBuilder) Build() (*ToolLoopStrategy, error) {
	if b.built {
		return nil, ErrToolLoopBuilderBuilt
	}
	if !b.toolsSet || nilcheck.IsNil(b.tools) {
		return nil, ErrMissingToolService
	}

	limits := b.limits
	if !b.limitsSet {
		limits = DefaultToolLoopLimits()
	}
	if err := validateToolLoopLimits(limits); err != nil {
		return nil, err
	}
	errorMode := b.errorMode
	if !b.errorModeSet {
		errorMode = ToolErrorModeFeedback
	}
	if !validToolErrorMode(errorMode) {
		return nil, fmt.Errorf("%w: %d", ErrInvalidToolErrorMode, errorMode)
	}

	specs, err := convertToolSpecs(b.tools.Specs())
	if err != nil {
		return nil, err
	}

	strategy := &ToolLoopStrategy{
		tools:         b.tools,
		session:       b.session,
		contextWindow: b.contextWindow,
		toolSpecs:     specs,
		limits:        limits,
		errorMode:     errorMode,
	}
	b.built = true
	return strategy, nil
}

func validateToolLoopLimits(limits ToolLoopLimits) error {
	if limits.MaxModelTurns <= 0 {
		return fmt.Errorf("%w: max model turns must be positive", ErrInvalidToolLoopLimits)
	}
	if limits.MaxToolCalls <= 0 {
		return fmt.Errorf("%w: max tool calls must be positive", ErrInvalidToolLoopLimits)
	}
	if limits.MaxToolResultBytes <= 0 {
		return fmt.Errorf("%w: max tool result bytes must be positive", ErrInvalidToolLoopLimits)
	}
	return nil
}

func validToolErrorMode(mode ToolErrorMode) bool {
	return mode == ToolErrorModeFeedback || mode == ToolErrorModeFailFast
}
