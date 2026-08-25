// Package agent defines the public contract and assembly entry point for
// provider-independent AI agents.
package agent

import (
	"context"

	agentstrategy "github.com/JIAOZAI1/acore/agent/agent-strategy"
	"github.com/JIAOZAI1/acore/agent/agent-strategy/singleturn"
	"github.com/JIAOZAI1/acore/agent/agent-strategy/toolloop"
)

type Agent = agentstrategy.Agent
type ModelOptions = agentstrategy.ModelOptions
type SessionInput = agentstrategy.SessionInput
type Request = agentstrategy.Request
type RunInput = agentstrategy.RunInput
type RunStrategy = agentstrategy.RunStrategy
type Result = agentstrategy.Result
type EventType = agentstrategy.EventType
type ToolEvent = agentstrategy.ToolEvent
type Event = agentstrategy.Event
type Stream = agentstrategy.Stream

const (
	EventUnknown   = agentstrategy.EventUnknown
	EventRunStart  = agentstrategy.EventRunStart
	EventModel     = agentstrategy.EventModel
	EventRunDone   = agentstrategy.EventRunDone
	EventToolStart = agentstrategy.EventToolStart
	EventToolDone  = agentstrategy.EventToolDone
)

var (
	ErrBuilderBuilt             = agentstrategy.ErrBuilderBuilt
	ErrNilLLM                   = agentstrategy.ErrNilLLM
	ErrLLMAlreadySet            = agentstrategy.ErrLLMAlreadySet
	ErrMissingLLM               = agentstrategy.ErrMissingLLM
	ErrNilRunStrategy           = agentstrategy.ErrNilRunStrategy
	ErrRunStrategyAlreadySet    = agentstrategy.ErrRunStrategyAlreadySet
	ErrMissingRunStrategy       = agentstrategy.ErrMissingRunStrategy
	ErrConfigAlreadySet         = agentstrategy.ErrConfigAlreadySet
	ErrNilPromptRenderer        = agentstrategy.ErrNilPromptRenderer
	ErrRenderPrompt             = agentstrategy.ErrRenderPrompt
	ErrInvalidOptions           = agentstrategy.ErrInvalidOptions
	ErrInvalidRequest           = agentstrategy.ErrInvalidRequest
	ErrNilAgent                 = agentstrategy.ErrNilAgent
	ErrUnexpectedModelStreamEnd = agentstrategy.ErrUnexpectedModelStreamEnd
	ErrInvalidModelDoneEvent    = agentstrategy.ErrInvalidModelDoneEvent
	ErrUnexpectedStreamEnd      = agentstrategy.ErrUnexpectedStreamEnd
	ErrInvalidDoneEvent         = agentstrategy.ErrInvalidDoneEvent
	ErrToolLoopBuilderBuilt     = agentstrategy.ErrToolLoopBuilderBuilt
	ErrNilToolService           = agentstrategy.ErrNilToolService
	ErrToolServiceAlreadySet    = agentstrategy.ErrToolServiceAlreadySet
	ErrMissingToolService       = agentstrategy.ErrMissingToolService
	ErrInvalidToolCatalog       = agentstrategy.ErrInvalidToolCatalog
	ErrInvalidToolLoopLimits    = agentstrategy.ErrInvalidToolLoopLimits
	ErrInvalidToolErrorMode     = agentstrategy.ErrInvalidToolErrorMode
	ErrInvalidModelResult       = agentstrategy.ErrInvalidModelResult
	ErrInvalidToolCall          = agentstrategy.ErrInvalidToolCall
	ErrModelTurnLimitExceeded   = agentstrategy.ErrModelTurnLimitExceeded
	ErrToolCallLimitExceeded    = agentstrategy.ErrToolCallLimitExceeded
	ErrToolResultTooLarge       = agentstrategy.ErrToolResultTooLarge
	ErrUsageOverflow            = agentstrategy.ErrUsageOverflow
	ErrSingleTurnBuilderBuilt   = agentstrategy.ErrSingleTurnBuilderBuilt
	ErrNilSessionService        = agentstrategy.ErrNilSessionService
	ErrSessionServiceAlreadySet = agentstrategy.ErrSessionServiceAlreadySet
	ErrSessionUnsupported       = agentstrategy.ErrSessionUnsupported
	ErrLoadSession              = agentstrategy.ErrLoadSession
	ErrCommitSession            = agentstrategy.ErrCommitSession
	ErrNilContextWindowReducer  = agentstrategy.ErrNilContextWindowReducer
	ErrContextWindowAlreadySet  = agentstrategy.ErrContextWindowAlreadySet
	ErrReduceContextWindow      = agentstrategy.ErrReduceContextWindow
)

// Complete consumes an Agent stream and returns its terminal result.
func Complete(ctx context.Context, value Agent, req Request) (*Result, error) {
	return agentstrategy.Complete(ctx, value, req)
}

// SingleTurnStrategy is the compatibility alias for the SingleTurn strategy.
type SingleTurnStrategy = singleturn.SingleTurnStrategy

// SingleTurnBuilder is the compatibility alias for the SingleTurn builder.
type SingleTurnBuilder = singleturn.SingleTurnBuilder

// NewSingleTurnStrategy creates a stateless single-turn strategy.
func NewSingleTurnStrategy() *SingleTurnStrategy { return singleturn.NewSingleTurnStrategy() }

// NewSingleTurnBuilder creates an empty SingleTurn builder.
func NewSingleTurnBuilder() *SingleTurnBuilder { return singleturn.NewSingleTurnBuilder() }

type ToolLoopStrategy = toolloop.ToolLoopStrategy
type ToolLoopBuilder = toolloop.ToolLoopBuilder
type ToolLoopLimits = toolloop.ToolLoopLimits
type ToolErrorMode = toolloop.ToolErrorMode

const (
	ToolErrorModeFeedback = toolloop.ToolErrorModeFeedback
	ToolErrorModeFailFast = toolloop.ToolErrorModeFailFast
)

func DefaultToolLoopLimits() ToolLoopLimits { return toolloop.DefaultToolLoopLimits() }
func NewToolLoopBuilder() *ToolLoopBuilder  { return toolloop.NewToolLoopBuilder() }
