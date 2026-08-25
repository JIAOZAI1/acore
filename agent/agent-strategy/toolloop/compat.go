package toolloop

import (
	"context"
	"fmt"
	"math"

	"github.com/JIAOZAI1/acore/agent/agent-strategy"
	"github.com/JIAOZAI1/acore/contextwindow"
	"github.com/JIAOZAI1/acore/model"
	"github.com/JIAOZAI1/acore/session"
	"github.com/JIAOZAI1/acore/tool"
)

type Event = agentstrategy.Event
type ModelOptions = agentstrategy.ModelOptions
type Request = agentstrategy.Request
type Result = agentstrategy.Result
type RunInput = agentstrategy.RunInput
type RunStrategy = agentstrategy.RunStrategy
type sessionRunState = agentstrategy.SessionRunState
type Stream = agentstrategy.Stream
type ToolEvent = agentstrategy.ToolEvent

const (
	EventRunStart  = agentstrategy.EventRunStart
	EventModel     = agentstrategy.EventModel
	EventRunDone   = agentstrategy.EventRunDone
	EventToolStart = agentstrategy.EventToolStart
	EventToolDone  = agentstrategy.EventToolDone
	ReasonToolUse  = model.ReasonToolUse
)

var (
	ErrInvalidModelDoneEvent    = agentstrategy.ErrInvalidModelDoneEvent
	ErrInvalidModelResult       = agentstrategy.ErrInvalidModelResult
	ErrInvalidRequest           = agentstrategy.ErrInvalidRequest
	ErrInvalidToolCall          = agentstrategy.ErrInvalidToolCall
	ErrInvalidToolErrorMode     = agentstrategy.ErrInvalidToolErrorMode
	ErrMissingToolService       = agentstrategy.ErrMissingToolService
	ErrModelTurnLimitExceeded   = agentstrategy.ErrModelTurnLimitExceeded
	ErrNilLLM                   = agentstrategy.ErrNilLLM
	ErrNilRunStrategy           = agentstrategy.ErrNilRunStrategy
	ErrToolCallLimitExceeded    = agentstrategy.ErrToolCallLimitExceeded
	ErrToolResultTooLarge       = agentstrategy.ErrToolResultTooLarge
	ErrUnexpectedModelStreamEnd = agentstrategy.ErrUnexpectedModelStreamEnd
	ErrToolLoopBuilderBuilt     = agentstrategy.ErrToolLoopBuilderBuilt
	ErrNilToolService           = agentstrategy.ErrNilToolService
	ErrToolServiceAlreadySet    = agentstrategy.ErrToolServiceAlreadySet
	ErrInvalidToolCatalog       = agentstrategy.ErrInvalidToolCatalog
	ErrInvalidToolLoopLimits    = agentstrategy.ErrInvalidToolLoopLimits
	ErrNilSessionService        = agentstrategy.ErrNilSessionService
	ErrSessionServiceAlreadySet = agentstrategy.ErrSessionServiceAlreadySet
	ErrNilContextWindowReducer  = agentstrategy.ErrNilContextWindowReducer
	ErrContextWindowAlreadySet  = agentstrategy.ErrContextWindowAlreadySet
	ErrSessionUnsupported       = agentstrategy.ErrSessionUnsupported
	ErrLoadSession              = agentstrategy.ErrLoadSession
	ErrCommitSession            = agentstrategy.ErrCommitSession
	ErrInvalidOptions           = agentstrategy.ErrInvalidOptions
	ErrUsageOverflow            = agentstrategy.ErrUsageOverflow
	ErrConfigAlreadySet         = agentstrategy.ErrConfigAlreadySet
)

func prepareSessionRun(ctx context.Context, service session.Service, req Request) (Request, *sessionRunState, error) {
	return agentstrategy.PrepareSessionRun(ctx, service, req)
}
func commitSessionRun(ctx context.Context, state *sessionRunState, result *Result) error {
	return agentstrategy.CommitSessionRun(ctx, state, result)
}
func applyContextWindow(ctx context.Context, reducer contextwindow.Reducer, selected model.Model, request model.Request, protectedMessages int) (model.Request, error) {
	return agentstrategy.ApplyContextWindow(ctx, reducer, selected, request, protectedMessages)
}
func initialProtectedMessageCount(messages []model.Message, state *sessionRunState) int {
	return agentstrategy.InitialProtectedMessageCount(messages, state)
}
func cloneMessages(messages []model.Message) []model.Message {
	return agentstrategy.CloneMessages(messages)
}
func cloneMessage(message model.Message) model.Message { return agentstrategy.CloneMessage(message) }
func cloneModelEvent(value model.Event) model.Event    { return agentstrategy.CloneModelEvent(value) }
func cloneModelResult(value *model.Result) *model.Result {
	return agentstrategy.CloneModelResult(value)
}
func cloneModelOptions(value ModelOptions) ModelOptions {
	return agentstrategy.CloneModelOptions(value)
}
func cloneToolCall(value tool.Call) tool.Call { return agentstrategy.CloneToolCall(value) }
func cloneResult(value *Result) *Result       { return agentstrategy.CloneResult(value) }

func releaseUnstartedModelStream(cancel context.CancelFunc, stream model.Stream) {
	cancel()
	stream(func(model.Event, error) bool { return false })
}

func validateModelOptions(options ModelOptions) error {
	if options.Temperature != nil && (math.IsNaN(*options.Temperature) || math.IsInf(*options.Temperature, 0)) {
		return fmt.Errorf("%w: temperature must be finite", ErrInvalidOptions)
	}
	if options.MaxTokens != nil && *options.MaxTokens <= 0 {
		return fmt.Errorf("%w: max tokens must be positive", ErrInvalidOptions)
	}
	if options.Reasoning != nil {
		switch *options.Reasoning {
		case model.ReasoningDefault, model.ReasoningOff, model.ReasoningLow, model.ReasoningMedium, model.ReasoningHigh:
		default:
			return fmt.Errorf("%w: unknown reasoning level %d", ErrInvalidOptions, *options.Reasoning)
		}
	}
	return nil
}
