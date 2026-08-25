package singleturn

import (
	"context"
	"fmt"

	"github.com/JIAOZAI1/acore/contextwindow"
	"github.com/JIAOZAI1/acore/internal/nilcheck"
	"github.com/JIAOZAI1/acore/model"
	"github.com/JIAOZAI1/acore/session"
)

// SingleTurnStrategy executes exactly one model generation and does not
// execute tool calls returned by the model.
type SingleTurnStrategy struct {
	session       session.Service
	contextWindow contextwindow.Reducer
}

// NewSingleTurnStrategy creates a stateless single-turn run strategy.
func NewSingleTurnStrategy() *SingleTurnStrategy {
	return &SingleTurnStrategy{}
}

// NewStrategy creates a stateless single-turn strategy.
func NewStrategy() *SingleTurnStrategy { return NewSingleTurnStrategy() }

// Run executes one model generation using input and returns Agent events.
func (s *SingleTurnStrategy) Run(ctx context.Context, input RunInput) (Stream, error) {
	if s == nil {
		return nil, ErrNilRunStrategy
	}
	if ctx == nil {
		return nil, fmt.Errorf("%w: nil context", ErrInvalidRequest)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if nilcheck.IsNil(input.LLM) {
		return nil, ErrNilLLM
	}
	if err := validateModelOptions(input.Request.Options); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidRequest, err)
	}
	request, sessionState, err := prepareSessionRun(ctx, s.session, input.Request)
	if err != nil {
		return nil, err
	}
	input.Request = request

	options := cloneModelOptions(input.Request.Options)
	modelRequest := model.Request{
		Context: model.Context{
			SystemPrompt: input.SystemPrompt,
			Messages:     cloneMessages(input.Request.Messages),
		},
		Temperature: options.Temperature,
		MaxTokens:   options.MaxTokens,
		Reasoning:   options.Reasoning,
	}
	modelRequest, err = applyContextWindow(
		ctx,
		s.contextWindow,
		input.LLM.Model(),
		modelRequest,
		initialProtectedMessageCount(input.Request.Messages, sessionState),
	)
	if err != nil {
		return nil, err
	}
	runCtx, cancel := context.WithCancel(ctx)
	stream, err := input.LLM.Generate(runCtx, modelRequest)
	if ctxErr := runCtx.Err(); ctxErr != nil {
		if stream != nil {
			releaseUnstartedModelStream(cancel, stream)
		} else {
			cancel()
		}
		return nil, ctxErr
	}
	if err != nil {
		cancel()
		return nil, fmt.Errorf("agent: generate model: %w", err)
	}
	if stream == nil {
		cancel()
		return nil, ErrUnexpectedModelStreamEnd
	}

	return wrapModelStream(runCtx, cancel, stream, sessionState), nil
}

func wrapModelStream(ctx context.Context, cancel context.CancelFunc, stream model.Stream, sessionState *sessionRunState) Stream {
	return func(yield func(Event, error) bool) {
		defer cancel()
		if err := ctx.Err(); err != nil {
			releaseUnstartedModelStream(cancel, stream)
			yield(Event{}, err)
			return
		}
		if !yield(Event{Type: EventRunStart}, nil) {
			releaseUnstartedModelStream(cancel, stream)
			return
		}

		for modelEvent, streamErr := range stream {
			if streamErr != nil {
				yield(Event{}, streamErr)
				return
			}
			if err := ctx.Err(); err != nil {
				yield(Event{}, err)
				return
			}

			clonedEvent := cloneModelEvent(modelEvent)
			if !yield(Event{
				Type:       EventModel,
				ModelTurn:  1,
				ModelEvent: &clonedEvent,
			}, nil) {
				return
			}
			if modelEvent.Type != model.EventDone {
				continue
			}
			if modelEvent.Result == nil {
				yield(Event{}, ErrInvalidModelDoneEvent)
				return
			}

			result := resultFromModel(modelEvent.Result)
			if err := commitSessionRun(ctx, sessionState, result); err != nil {
				yield(Event{}, err)
				return
			}
			yield(Event{Type: EventRunDone, Result: result}, nil)
			return
		}

		if err := ctx.Err(); err != nil {
			yield(Event{}, err)
			return
		}
		yield(Event{}, ErrUnexpectedModelStreamEnd)
	}
}

func releaseUnstartedModelStream(cancel context.CancelFunc, stream model.Stream) {
	cancel()
	stream(func(model.Event, error) bool { return false })
}

func resultFromModel(result *model.Result) *Result {
	output := cloneMessage(result.Message)
	return &Result{
		Output:            output,
		GeneratedMessages: []model.Message{cloneMessage(result.Message)},
		Usage:             result.Usage,
		StopReason:        result.StopReason,
		ModelID:           result.ModelID,
		ProviderID:        result.ProviderID,
		ModelTurns:        1,
		ToolCalls:         countToolCalls(result.Message),
	}
}

func countToolCalls(message model.Message) int {
	count := 0
	for _, block := range message.Content {
		if block.Kind == model.ContentToolCall {
			count++
		}
	}
	return count
}

var _ RunStrategy = (*SingleTurnStrategy)(nil)
