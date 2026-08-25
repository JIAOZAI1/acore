// Package agent defines the public contract and assembly entry point for
// provider-independent AI agents.
package agentstrategy

import (
	"context"
	"errors"
	"fmt"
	"iter"

	"github.com/JIAOZAI1/acore/internal/nilcheck"
	"github.com/JIAOZAI1/acore/model"
	"github.com/JIAOZAI1/acore/prompt"
	"github.com/JIAOZAI1/acore/session"
	"github.com/JIAOZAI1/acore/tool"
)

// Agent runs one agent request and returns a pull-based event stream.
type Agent interface {
	Run(ctx context.Context, req Request) (Stream, error)
}

// ModelOptions contains portable model-generation options. Nil fields inherit
// defaults configured on Builder.
type ModelOptions struct {
	Temperature *float64              `json:"temperature,omitempty"`
	MaxTokens   *int                  `json:"maxTokens,omitempty"`
	Reasoning   *model.ReasoningLevel `json:"reasoning,omitempty"`
}

// SessionInput contains one conversation key and the new messages not yet
// stored for a stateful run.
type SessionInput struct {
	Key      session.Key     `json:"key"`
	Messages []model.Message `json:"messages"`
}

// Request contains exactly one input form: Messages is the complete history
// for a stateless run, while Session contains new messages for a stateful run.
// Code constructing Request should use keyed fields.
type Request struct {
	Messages     []model.Message `json:"messages,omitempty"`
	Session      *SessionInput   `json:"session,omitempty"`
	Options      ModelOptions    `json:"options,omitempty"`
	PromptValues prompt.Values   `json:"promptValues,omitempty"`
}

// RunInput is the normalized, per-run snapshot passed to a RunStrategy. Its
// Request contains merged model options and data isolated from the Agent
// caller. Code constructing RunInput directly should use keyed fields.
type RunInput struct {
	LLM          model.LLM
	SystemPrompt string
	Request      Request
}

// RunStrategy executes one normalized agent run. Implementations own the run
// algorithm and must support concurrent calls or synchronize their state.
type RunStrategy interface {
	Run(ctx context.Context, input RunInput) (Stream, error)
}

// Result is the successful terminal value of an agent run.
type Result struct {
	Output            model.Message    `json:"output"`
	GeneratedMessages []model.Message  `json:"generatedMessages"`
	Usage             model.Usage      `json:"usage"`
	StopReason        model.StopReason `json:"stopReason"`
	ModelID           string           `json:"modelId,omitempty"`
	ProviderID        string           `json:"providerId,omitempty"`
	ModelTurns        int              `json:"modelTurns"`
	ToolCalls         int              `json:"toolCalls"`
	ToolErrors        int              `json:"toolErrors"`
}

// EventType identifies a successful agent stream event. Runtime failures are
// carried by Stream's error value.
type EventType uint8

const (
	// EventUnknown represents an unspecified event type.
	EventUnknown EventType = iota
	// EventRunStart marks the start of an agent run.
	EventRunStart
	// EventModel wraps one event from a model generation.
	EventModel
	// EventRunDone carries the terminal agent Result.
	EventRunDone
	// EventToolStart marks a tool invocation accepted for execution.
	EventToolStart
	// EventToolDone reports the sanitized result of a tool invocation.
	EventToolDone
)

// String returns the stable lower-camel-case name of t.
func (t EventType) String() string {
	switch t {
	case EventRunStart:
		return "runStart"
	case EventModel:
		return "model"
	case EventRunDone:
		return "runDone"
	case EventToolStart:
		return "toolStart"
	case EventToolDone:
		return "toolDone"
	default:
		return "unknown"
	}
}

// ToolEvent describes one tool invocation boundary. Failed results contain
// only a sanitized message suitable for sending back to the model.
type ToolEvent struct {
	Call    tool.Call    `json:"call"`
	Result  *tool.Result `json:"result,omitempty"`
	IsError bool         `json:"isError,omitempty"`
}

// Event describes agent execution progress.
type Event struct {
	Type       EventType    `json:"type"`
	ModelTurn  int          `json:"modelTurn,omitempty"`
	ModelEvent *model.Event `json:"modelEvent,omitempty"`
	Tool       *ToolEvent   `json:"tool,omitempty"`
	Result     *Result      `json:"result,omitempty"`
}

// Stream is a pull-based agent event stream. Returning from iteration early
// propagates cancellation to the wrapped model iterator through its yield
// result, allowing generator defers to release resources.
type Stream = iter.Seq2[Event, error]

var (
	// ErrBuilderBuilt indicates that a successful Build froze the Builder.
	ErrBuilderBuilt = errors.New("agent: builder already built")
	// ErrNilLLM indicates that UseLLM received nil, including a typed nil.
	ErrNilLLM = errors.New("agent: nil LLM")
	// ErrLLMAlreadySet indicates that an LLM is already configured.
	ErrLLMAlreadySet = errors.New("agent: LLM already set")
	// ErrMissingLLM indicates that Build was called without an LLM.
	ErrMissingLLM = errors.New("agent: missing LLM")
	// ErrNilRunStrategy indicates that UseRunStrategy received nil, including a
	// typed nil.
	ErrNilRunStrategy = errors.New("agent: nil run strategy")
	// ErrRunStrategyAlreadySet indicates that a run strategy is already
	// configured.
	ErrRunStrategyAlreadySet = errors.New("agent: run strategy already set")
	// ErrMissingRunStrategy indicates that Build was called without a run
	// strategy.
	ErrMissingRunStrategy = errors.New("agent: missing run strategy")
	// ErrConfigAlreadySet indicates that a singleton configuration was repeated.
	ErrConfigAlreadySet = errors.New("agent: config already set")
	// ErrNilPromptRenderer indicates that UsePrompt received nil, including a
	// typed nil.
	ErrNilPromptRenderer = errors.New("agent: nil prompt renderer")
	// ErrRenderPrompt indicates that the configured prompt renderer failed.
	ErrRenderPrompt = errors.New("agent: render prompt")
	// ErrInvalidOptions indicates invalid portable model options.
	ErrInvalidOptions = errors.New("agent: invalid model options")
	// ErrInvalidRequest indicates invalid input for an agent run.
	ErrInvalidRequest = errors.New("agent: invalid request")
	// ErrNilAgent indicates that Complete received nil, including a typed nil.
	ErrNilAgent = errors.New("agent: nil agent")
	// ErrUnexpectedModelStreamEnd indicates that the model stream ended without
	// a terminal done event.
	ErrUnexpectedModelStreamEnd = errors.New("agent: model stream ended without done")
	// ErrInvalidModelDoneEvent indicates that a model done event has no result.
	ErrInvalidModelDoneEvent = errors.New("agent: invalid model done event")
	// ErrUnexpectedStreamEnd indicates that an agent stream ended without a done
	// event.
	ErrUnexpectedStreamEnd = errors.New("agent: stream ended without done")
	// ErrInvalidDoneEvent indicates that an agent done event has no result.
	ErrInvalidDoneEvent = errors.New("agent: done event has no result")
	// ErrToolLoopBuilderBuilt indicates that a successful Build froze the
	// ToolLoopBuilder.
	ErrToolLoopBuilderBuilt = errors.New("agent: tool loop builder already built")
	// ErrNilToolService indicates that UseTools received nil, including a typed
	// nil.
	ErrNilToolService = errors.New("agent: nil tool service")
	// ErrToolServiceAlreadySet indicates that a tool service is already
	// configured.
	ErrToolServiceAlreadySet = errors.New("agent: tool service already set")
	// ErrMissingToolService indicates that no tool service was configured.
	ErrMissingToolService = errors.New("agent: missing tool service")
	// ErrInvalidToolCatalog indicates that a tool catalog cannot be presented to
	// a model safely.
	ErrInvalidToolCatalog = errors.New("agent: invalid tool catalog")
	// ErrInvalidToolLoopLimits indicates that a ToolLoop limit is not positive.
	ErrInvalidToolLoopLimits = errors.New("agent: invalid tool loop limits")
	// ErrInvalidToolErrorMode indicates an unknown ToolErrorMode.
	ErrInvalidToolErrorMode = errors.New("agent: invalid tool error mode")
	// ErrInvalidModelResult indicates that a model result is not an assistant
	// message.
	ErrInvalidModelResult = errors.New("agent: invalid model result")
	// ErrInvalidToolCall indicates a malformed or inconsistent model tool call.
	ErrInvalidToolCall = errors.New("agent: invalid model tool call")
	// ErrModelTurnLimitExceeded indicates that another model turn is required but
	// the configured model-turn budget is exhausted.
	ErrModelTurnLimitExceeded = errors.New("agent: model turn limit exceeded")
	// ErrToolCallLimitExceeded indicates that a model-requested batch exceeds the
	// configured tool-call budget.
	ErrToolCallLimitExceeded = errors.New("agent: tool call limit exceeded")
	// ErrToolResultTooLarge indicates that a tool result exceeds the configured
	// byte limit.
	ErrToolResultTooLarge = errors.New("agent: tool result too large")
	// ErrUsageOverflow indicates that accumulated model usage exceeded int64.
	ErrUsageOverflow = errors.New("agent: usage overflow")
	// ErrSingleTurnBuilderBuilt indicates that a successful Build froze the
	// SingleTurnBuilder.
	ErrSingleTurnBuilderBuilt = errors.New("agent: single turn builder already built")
	// ErrNilSessionService indicates that UseSession received nil, including a
	// typed nil.
	ErrNilSessionService = errors.New("agent: nil session service")
	// ErrSessionServiceAlreadySet indicates that a Session service is already
	// configured on a strategy Builder.
	ErrSessionServiceAlreadySet = errors.New("agent: session service already set")
	// ErrSessionUnsupported indicates that a stateful request reached a
	// strategy built without a Session service.
	ErrSessionUnsupported = errors.New("agent: session input unsupported")
	// ErrLoadSession indicates that a Session service failed to load history.
	ErrLoadSession = errors.New("agent: load session")
	// ErrCommitSession indicates that a Session service failed to commit a
	// successful run.
	ErrCommitSession = errors.New("agent: commit session")
	// ErrNilContextWindowReducer indicates that UseContextWindow received nil,
	// including a typed nil.
	ErrNilContextWindowReducer = errors.New("agent: nil context window reducer")
	// ErrContextWindowAlreadySet indicates that a context-window Reducer is
	// already configured on a strategy Builder.
	ErrContextWindowAlreadySet = errors.New("agent: context window reducer already set")
	// ErrReduceContextWindow indicates that context-window reduction failed.
	ErrReduceContextWindow = errors.New("agent: reduce context window")
)

// Complete consumes an Agent stream and returns its terminal result.
func Complete(ctx context.Context, value Agent, req Request) (*Result, error) {
	if nilcheck.IsNil(value) {
		return nil, ErrNilAgent
	}
	if ctx == nil {
		return nil, fmt.Errorf("%w: nil context", ErrInvalidRequest)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	stream, err := value.Run(ctx, req)
	if err != nil {
		return nil, err
	}
	if stream == nil {
		return nil, ErrUnexpectedStreamEnd
	}

	for event, streamErr := range stream {
		if streamErr != nil {
			return nil, streamErr
		}
		if event.Type != EventRunDone {
			continue
		}
		if event.Result == nil {
			return nil, ErrInvalidDoneEvent
		}
		return cloneResult(event.Result), nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return nil, ErrUnexpectedStreamEnd
}
