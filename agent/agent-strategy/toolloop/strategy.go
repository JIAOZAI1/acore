package toolloop

import (
	"context"
	"fmt"

	"github.com/JIAOZAI1/acore/contextwindow"
	"github.com/JIAOZAI1/acore/internal/nilcheck"
	"github.com/JIAOZAI1/acore/model"
	"github.com/JIAOZAI1/acore/session"
	"github.com/JIAOZAI1/acore/tool"
)

const toolResultTooLargeMessage = "tool result too large"

// ToolLoopStrategy repeatedly generates model responses and executes requested
// tools until the model produces an assistant message without tool calls.
// Instances built by ToolLoopBuilder are immutable and safe for concurrent
// runs when their LLM, tool, Session, and context-window dependencies are also
// concurrency-safe.
type ToolLoopStrategy struct {
	tools         tool.Service
	session       session.Service
	contextWindow contextwindow.Reducer
	toolSpecs     []model.ToolSpec
	limits        ToolLoopLimits
	errorMode     ToolErrorMode
}

type toolLoopRunState struct {
	workingMessages   []model.Message
	generatedMessages []model.Message
	usage             model.Usage
	modelTurns        int
	toolCalls         int
	toolErrors        int
	protectedMessages int
	seenToolCallIDs   map[string]struct{}
}

// Run executes a bounded, sequential model-tool loop using input.
func (s *ToolLoopStrategy) Run(ctx context.Context, input RunInput) (Stream, error) {
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
	if nilcheck.IsNil(s.tools) {
		return nil, ErrMissingToolService
	}
	if err := validateToolLoopLimits(s.limits); err != nil {
		return nil, err
	}
	if !validToolErrorMode(s.errorMode) {
		return nil, fmt.Errorf("%w: %d", ErrInvalidToolErrorMode, s.errorMode)
	}
	request, sessionState, err := prepareSessionRun(ctx, s.session, input.Request)
	if err != nil {
		return nil, err
	}
	input.Request = request

	workingMessages := cloneMessages(input.Request.Messages)
	protectedMessages := initialProtectedMessageCount(workingMessages, sessionState)
	firstRequest, err := s.modelRequest(ctx, input, workingMessages, protectedMessages)
	if err != nil {
		return nil, err
	}
	runCtx, cancel := context.WithCancel(ctx)
	firstStream, err := input.LLM.Generate(runCtx, firstRequest)
	if ctxErr := runCtx.Err(); ctxErr != nil {
		if firstStream != nil {
			releaseUnstartedModelStream(cancel, firstStream)
		} else {
			cancel()
		}
		return nil, ctxErr
	}
	if err != nil {
		if firstStream != nil {
			releaseUnstartedModelStream(cancel, firstStream)
		} else {
			cancel()
		}
		return nil, fmt.Errorf("agent: generate model turn 1: %w", err)
	}
	if firstStream == nil {
		cancel()
		return nil, ErrUnexpectedModelStreamEnd
	}

	return s.runStream(runCtx, cancel, input, workingMessages, protectedMessages, firstStream, sessionState), nil
}

func (s *ToolLoopStrategy) modelRequest(ctx context.Context, input RunInput, messages []model.Message, protectedMessages int) (model.Request, error) {
	options := cloneModelOptions(input.Request.Options)
	request := model.Request{
		Context: model.Context{
			SystemPrompt: input.SystemPrompt,
			Messages:     cloneMessages(messages),
			Tools:        cloneToolSpecs(s.toolSpecs),
		},
		Temperature: options.Temperature,
		MaxTokens:   options.MaxTokens,
		Reasoning:   options.Reasoning,
	}
	return applyContextWindow(ctx, s.contextWindow, input.LLM.Model(), request, protectedMessages)
}

func (s *ToolLoopStrategy) runStream(
	ctx context.Context,
	cancel context.CancelFunc,
	input RunInput,
	workingMessages []model.Message,
	protectedMessages int,
	firstStream model.Stream,
	sessionState *sessionRunState,
) Stream {
	return func(yield func(Event, error) bool) {
		defer cancel()
		if err := ctx.Err(); err != nil {
			releaseUnstartedModelStream(cancel, firstStream)
			yield(Event{}, err)
			return
		}
		if !yield(Event{Type: EventRunStart}, nil) {
			releaseUnstartedModelStream(cancel, firstStream)
			return
		}
		if err := ctx.Err(); err != nil {
			releaseUnstartedModelStream(cancel, firstStream)
			yield(Event{}, err)
			return
		}

		state := toolLoopRunState{
			workingMessages:   cloneMessages(workingMessages),
			protectedMessages: protectedMessages,
			seenToolCallIDs:   make(map[string]struct{}),
		}
		modelStream := firstStream

		for {
			turn := state.modelTurns + 1
			modelResult, ok := consumeToolLoopModelTurn(ctx, modelStream, turn, yield)
			if !ok {
				return
			}
			if modelResult.Message.Role != model.RoleAssistant {
				yield(Event{}, fmt.Errorf("%w: model turn %d did not return an assistant message", ErrInvalidModelResult, turn))
				return
			}

			usage, err := addUsage(state.usage, modelResult.Usage)
			if err != nil {
				yield(Event{}, err)
				return
			}
			state.usage = usage
			state.modelTurns = turn
			assistantMessage := cloneMessage(modelResult.Message)
			state.workingMessages = append(state.workingMessages, assistantMessage)
			state.generatedMessages = append(state.generatedMessages, cloneMessage(assistantMessage))
			state.protectedMessages++

			calls, err := extractToolCalls(assistantMessage, state.seenToolCallIDs)
			if err != nil {
				yield(Event{}, err)
				return
			}
			if len(calls) == 0 {
				if modelResult.StopReason == model.ReasonToolUse {
					yield(Event{}, fmt.Errorf("%w: model turn %d reported tool use without a call", ErrInvalidToolCall, turn))
					return
				}
				if err := ctx.Err(); err != nil {
					yield(Event{}, err)
					return
				}
				result := state.result(modelResult)
				if err := commitSessionRun(ctx, sessionState, result); err != nil {
					yield(Event{}, err)
					return
				}
				yield(Event{Type: EventRunDone, Result: result}, nil)
				return
			}
			if turn >= s.limits.MaxModelTurns {
				yield(Event{}, fmt.Errorf("%w: limit %d", ErrModelTurnLimitExceeded, s.limits.MaxModelTurns))
				return
			}
			if len(calls) > s.limits.MaxToolCalls-state.toolCalls {
				yield(Event{}, fmt.Errorf("%w: limit %d", ErrToolCallLimitExceeded, s.limits.MaxToolCalls))
				return
			}
			for _, call := range calls {
				state.seenToolCallIDs[call.ID] = struct{}{}
			}

			if !s.executeToolBatch(ctx, turn, calls, &state, yield) {
				return
			}
			if err := ctx.Err(); err != nil {
				yield(Event{}, err)
				return
			}

			nextRequest, requestErr := s.modelRequest(ctx, input, state.workingMessages, state.protectedMessages)
			if requestErr != nil {
				yield(Event{}, requestErr)
				return
			}
			modelStream, err = input.LLM.Generate(ctx, nextRequest)
			if ctxErr := ctx.Err(); ctxErr != nil {
				if modelStream != nil {
					releaseUnstartedModelStream(cancel, modelStream)
				}
				yield(Event{}, ctxErr)
				return
			}
			if err != nil {
				if modelStream != nil {
					releaseUnstartedModelStream(cancel, modelStream)
				}
				yield(Event{}, fmt.Errorf("agent: generate model turn %d: %w", turn+1, err))
				return
			}
			if modelStream == nil {
				yield(Event{}, ErrUnexpectedModelStreamEnd)
				return
			}
		}
	}
}

func consumeToolLoopModelTurn(ctx context.Context, stream model.Stream, turn int, yield func(Event, error) bool) (*model.Result, bool) {
	for modelEvent, streamErr := range stream {
		if err := ctx.Err(); err != nil {
			yield(Event{}, err)
			return nil, false
		}
		if streamErr != nil {
			yield(Event{}, streamErr)
			return nil, false
		}

		var result *model.Result
		if modelEvent.Type == model.EventDone {
			result = cloneModelResult(modelEvent.Result)
		}
		clonedEvent := cloneModelEvent(modelEvent)
		if !yield(Event{
			Type:       EventModel,
			ModelTurn:  turn,
			ModelEvent: &clonedEvent,
		}, nil) {
			return nil, false
		}
		if err := ctx.Err(); err != nil {
			yield(Event{}, err)
			return nil, false
		}
		if modelEvent.Type != model.EventDone {
			continue
		}
		if result == nil {
			yield(Event{}, ErrInvalidModelDoneEvent)
			return nil, false
		}
		return result, true
	}

	if err := ctx.Err(); err != nil {
		yield(Event{}, err)
		return nil, false
	}
	yield(Event{}, ErrUnexpectedModelStreamEnd)
	return nil, false
}

func (s *ToolLoopStrategy) executeToolBatch(
	ctx context.Context,
	modelTurn int,
	calls []tool.Call,
	state *toolLoopRunState,
	yield func(Event, error) bool,
) bool {
	for _, call := range calls {
		startCall := cloneToolCall(call)
		if !yield(Event{
			Type:      EventToolStart,
			ModelTurn: modelTurn,
			Tool:      &ToolEvent{Call: startCall},
		}, nil) {
			return false
		}
		if err := ctx.Err(); err != nil {
			yield(Event{}, err)
			return false
		}

		state.toolCalls++
		result, err := s.tools.Execute(ctx, cloneToolCall(call))
		if ctxErr := ctx.Err(); ctxErr != nil {
			yield(Event{}, ctxErr)
			return false
		}
		if err != nil {
			state.toolErrors++
			safeMessage := safeToolErrorMessage(err)
			if !yieldToolDone(modelTurn, call, safeMessage, true, yield) {
				return false
			}
			if ctxErr := ctx.Err(); ctxErr != nil {
				yield(Event{}, ctxErr)
				return false
			}
			if s.errorMode == ToolErrorModeFailFast {
				yield(Event{}, fmt.Errorf("agent: execute tool %q call %q: %w", call.Name, call.ID, err))
				return false
			}

			message := toolMessage(call, safeMessage, true)
			state.workingMessages = append(state.workingMessages, message)
			state.generatedMessages = append(state.generatedMessages, cloneMessage(message))
			state.protectedMessages++
			continue
		}
		if len(result.Content) > s.limits.MaxToolResultBytes {
			state.toolErrors++
			if !yieldToolDone(modelTurn, call, toolResultTooLargeMessage, true, yield) {
				return false
			}
			if ctxErr := ctx.Err(); ctxErr != nil {
				yield(Event{}, ctxErr)
				return false
			}
			yield(Event{}, fmt.Errorf("%w: tool %q call %q", ErrToolResultTooLarge, call.Name, call.ID))
			return false
		}

		if !yieldToolDone(modelTurn, call, result.Content, false, yield) {
			return false
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			yield(Event{}, ctxErr)
			return false
		}
		message := toolMessage(call, result.Content, false)
		state.workingMessages = append(state.workingMessages, message)
		state.generatedMessages = append(state.generatedMessages, cloneMessage(message))
		state.protectedMessages++
	}
	return true
}

func yieldToolDone(
	modelTurn int,
	call tool.Call,
	content string,
	isError bool,
	yield func(Event, error) bool,
) bool {
	result := &tool.Result{Content: content}
	return yield(Event{
		Type:      EventToolDone,
		ModelTurn: modelTurn,
		Tool: &ToolEvent{
			Call:    cloneToolCall(call),
			Result:  result,
			IsError: isError,
		},
	}, nil)
}

func (state *toolLoopRunState) result(modelResult *model.Result) *Result {
	return &Result{
		Output:            cloneMessage(modelResult.Message),
		GeneratedMessages: cloneMessages(state.generatedMessages),
		Usage:             state.usage,
		StopReason:        modelResult.StopReason,
		ModelID:           modelResult.ModelID,
		ProviderID:        modelResult.ProviderID,
		ModelTurns:        state.modelTurns,
		ToolCalls:         state.toolCalls,
		ToolErrors:        state.toolErrors,
	}
}

var _ RunStrategy = (*ToolLoopStrategy)(nil)
