package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/JIAOZAI1/acore/agent"
	"github.com/JIAOZAI1/acore/model"
	"github.com/JIAOZAI1/acore/tool"
)

func TestToolLoopStrategyRunsToolsAndBuildsReplayableResult(t *testing.T) {
	service := &fakeToolService{
		specs: []tool.Spec{
			{Name: "first", Parameters: json.RawMessage(`{"type":"object"}`)},
			{Name: "second", Parameters: json.RawMessage(`{"type":"object"}`)},
		},
	}
	var executed []tool.Call
	service.execute = func(_ context.Context, call tool.Call) (tool.Result, error) {
		executed = append(executed, call)
		return tool.Result{Content: "result-" + call.Name}, nil
	}

	turn := 0
	var requests []model.Request
	llm := &fakeLLM{generate: func(_ context.Context, req model.Request) (model.Stream, error) {
		turn++
		requests = append(requests, req)
		if turn == 1 {
			return doneResultStream(&model.Result{
				Message: assistantToolCalls(
					model.ToolCall{ID: "call-1", Name: "first", Arguments: json.RawMessage(`{"value":1}`)},
					model.ToolCall{ID: "call-2", Name: "second", Arguments: json.RawMessage(`{"value":2}`)},
				),
				Usage: model.Usage{
					InputTokens: 1, OutputTokens: 2, CacheRead: 3,
					CacheWrite: 4, ReasoningTokens: 5, TotalTokens: 6,
				},
				StopReason: model.ReasonToolUse,
				ModelID:    "intermediate-model",
				ProviderID: "intermediate-response",
			}), nil
		}
		return doneResultStream(&model.Result{
			Message: model.Message{
				Role:    model.RoleAssistant,
				Content: []model.ContentBlock{{Kind: model.ContentText, Text: "final answer"}},
			},
			Usage: model.Usage{
				InputTokens: 10, OutputTokens: 20, CacheRead: 30,
				CacheWrite: 40, ReasoningTokens: 50, TotalTokens: 60,
			},
			StopReason: model.ReasonStop,
			ModelID:    "final-model",
			ProviderID: "final-response",
		}), nil
	}}
	strategy := buildToolLoopStrategy(t, service, agent.DefaultToolLoopLimits(), agent.ToolErrorModeFeedback)
	stream, err := strategy.Run(context.Background(), agent.RunInput{
		LLM:          llm,
		SystemPrompt: "be useful",
		Request:      agent.Request{Messages: userMessages("question")},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	var events []agent.Event
	for event, streamErr := range stream {
		if streamErr != nil {
			t.Fatalf("stream error = %v", streamErr)
		}
		events = append(events, event)
		if event.Type == agent.EventModel && event.ModelTurn == 1 && event.ModelEvent.Type == model.EventDone {
			event.ModelEvent.Result.Message.Content[0].ToolCall.Name = "mutated-model-event"
			event.ModelEvent.Result.Message.Content[0].ToolCall.Arguments[0] = '['
		}
		if event.Type == agent.EventToolStart {
			event.Tool.Call.Name = "mutated-tool-event"
			event.Tool.Call.Arguments[0] = '['
		}
		if event.Type == agent.EventToolDone {
			event.Tool.Result.Content = "mutated result event"
		}
	}

	wantTypes := []agent.EventType{
		agent.EventRunStart,
		agent.EventModel,
		agent.EventToolStart,
		agent.EventToolDone,
		agent.EventToolStart,
		agent.EventToolDone,
		agent.EventModel,
		agent.EventRunDone,
	}
	if len(events) != len(wantTypes) {
		t.Fatalf("event count = %d, want %d", len(events), len(wantTypes))
	}
	for index, want := range wantTypes {
		if events[index].Type != want {
			t.Fatalf("event %d type = %v, want %v", index, events[index].Type, want)
		}
	}
	for _, index := range []int{1, 2, 3, 4, 5} {
		if events[index].ModelTurn != 1 {
			t.Errorf("event %d model turn = %d, want 1", index, events[index].ModelTurn)
		}
	}
	if events[6].ModelTurn != 2 {
		t.Errorf("second model event turn = %d, want 2", events[6].ModelTurn)
	}

	if len(executed) != 2 || executed[0].Name != "first" || executed[1].Name != "second" {
		t.Fatalf("executed calls = %+v", executed)
	}
	if got := string(executed[0].Arguments); got != `{"value":1}` {
		t.Fatalf("first executed arguments = %s", got)
	}
	if len(requests) != 2 {
		t.Fatalf("model requests = %d, want 2", len(requests))
	}
	if requests[0].Context.SystemPrompt != "be useful" || len(requests[0].Context.Tools) != 2 {
		t.Fatalf("first request context = %+v", requests[0].Context)
	}
	secondMessages := requests[1].Context.Messages
	if len(secondMessages) != 4 {
		t.Fatalf("second request messages = %d, want 4", len(secondMessages))
	}
	if secondMessages[1].Role != model.RoleAssistant || secondMessages[2].Role != model.RoleTool || secondMessages[3].Role != model.RoleTool {
		t.Fatalf("second request roles = %v, %v, %v", secondMessages[1].Role, secondMessages[2].Role, secondMessages[3].Role)
	}
	if secondMessages[2].ToolCallID != "call-1" || secondMessages[2].Content[0].Text != "result-first" {
		t.Fatalf("first tool message = %+v", secondMessages[2])
	}

	result := events[len(events)-1].Result
	if result == nil {
		t.Fatal("RunDone result is nil")
	}
	if result.Output.Content[0].Text != "final answer" || result.StopReason != model.ReasonStop {
		t.Fatalf("terminal output = %+v", result)
	}
	if result.ModelID != "final-model" || result.ProviderID != "final-response" {
		t.Fatalf("terminal identifiers = %+v", result)
	}
	if result.ModelTurns != 2 || result.ToolCalls != 2 || result.ToolErrors != 0 {
		t.Fatalf("terminal counts = %+v", result)
	}
	wantUsage := model.Usage{
		InputTokens: 11, OutputTokens: 22, CacheRead: 33,
		CacheWrite: 44, ReasoningTokens: 55, TotalTokens: 66,
	}
	if result.Usage != wantUsage {
		t.Fatalf("usage = %+v, want %+v", result.Usage, wantUsage)
	}
	if len(result.GeneratedMessages) != 4 {
		t.Fatalf("generated messages = %d, want 4", len(result.GeneratedMessages))
	}
	wantRoles := []model.Role{model.RoleAssistant, model.RoleTool, model.RoleTool, model.RoleAssistant}
	for index, want := range wantRoles {
		if result.GeneratedMessages[index].Role != want {
			t.Errorf("generated message %d role = %v, want %v", index, result.GeneratedMessages[index].Role, want)
		}
	}
}

func TestToolLoopStrategyFeedbackSanitizesToolErrors(t *testing.T) {
	wantInternal := errors.New("database password leaked internally")
	service := &fakeToolService{execute: func(_ context.Context, call tool.Call) (tool.Result, error) {
		switch call.Name {
		case "missing":
			return tool.Result{}, fmt.Errorf("resolve: %w", tool.ErrToolNotFound)
		case "invalid":
			return tool.Result{}, fmt.Errorf("validate: %w", tool.ErrInvalidArguments)
		default:
			return tool.Result{}, wantInternal
		}
	}}
	turn := 0
	var feedback []model.Message
	llm := &fakeLLM{generate: func(_ context.Context, req model.Request) (model.Stream, error) {
		turn++
		if turn == 1 {
			return doneResultStream(&model.Result{
				Message: assistantToolCalls(
					model.ToolCall{ID: "1", Name: "missing", Arguments: json.RawMessage(`{}`)},
					model.ToolCall{ID: "2", Name: "invalid", Arguments: json.RawMessage(`{}`)},
					model.ToolCall{ID: "3", Name: "internal", Arguments: json.RawMessage(`{}`)},
				),
				StopReason: model.ReasonStop,
			}), nil
		}
		feedback = req.Context.Messages
		return doneModelStream("recovered"), nil
	}}
	strategy := buildToolLoopStrategy(t, service, agent.DefaultToolLoopLimits(), agent.ToolErrorModeFeedback)
	stream, err := strategy.Run(context.Background(), validToolLoopInput(llm))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	events, err := collect(stream)
	if err != nil {
		t.Fatalf("stream error = %v", err)
	}

	var doneMessages []string
	for _, event := range events {
		if event.Type == agent.EventToolDone {
			if !event.Tool.IsError {
				t.Errorf("ToolDone IsError = false for %+v", event.Tool.Call)
			}
			doneMessages = append(doneMessages, event.Tool.Result.Content)
		}
	}
	want := []string{"tool not found", "invalid tool arguments", "tool execution failed"}
	if fmt.Sprint(doneMessages) != fmt.Sprint(want) {
		t.Fatalf("sanitized ToolDone messages = %v, want %v", doneMessages, want)
	}
	if len(feedback) != 5 {
		t.Fatalf("feedback messages = %d, want 5", len(feedback))
	}
	for index, text := range want {
		message := feedback[index+2]
		if message.Role != model.RoleTool || !message.IsError || message.Content[0].Text != text {
			t.Errorf("feedback message %d = %+v, want %q", index, message, text)
		}
	}
	result := events[len(events)-1].Result
	if result.ToolCalls != 3 || result.ToolErrors != 3 {
		t.Fatalf("result counts = %+v", result)
	}
}

func TestToolLoopStrategyFailFastPreservesErrorChain(t *testing.T) {
	want := errors.New("trusted execution detail")
	executions := 0
	service := &fakeToolService{execute: func(context.Context, tool.Call) (tool.Result, error) {
		executions++
		return tool.Result{}, want
	}}
	llm := &fakeLLM{generate: func(context.Context, model.Request) (model.Stream, error) {
		return doneResultStream(&model.Result{
			Message: assistantToolCalls(
				model.ToolCall{ID: "first", Name: "fail", Arguments: json.RawMessage(`{}`)},
				model.ToolCall{ID: "second", Name: "unused", Arguments: json.RawMessage(`{}`)},
			),
			StopReason: model.ReasonToolUse,
		}), nil
	}}
	strategy := buildToolLoopStrategy(t, service, agent.DefaultToolLoopLimits(), agent.ToolErrorModeFailFast)
	stream, err := strategy.Run(context.Background(), validToolLoopInput(llm))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	events, err := collect(stream)
	if !errors.Is(err, want) {
		t.Fatalf("stream error = %v, want original error chain", err)
	}
	if executions != 1 {
		t.Fatalf("tool executions = %d, want 1", executions)
	}
	if len(events) == 0 || events[len(events)-1].Type != agent.EventToolDone || !events[len(events)-1].Tool.IsError {
		t.Fatalf("events before failure = %+v", events)
	}
	for _, event := range events {
		if event.Type == agent.EventRunDone {
			t.Fatal("fail-fast stream emitted RunDone")
		}
	}
}

func TestToolLoopStrategyChecksBatchLimitsBeforeSideEffects(t *testing.T) {
	tests := []struct {
		name   string
		limits agent.ToolLoopLimits
		calls  []model.ToolCall
		want   error
	}{
		{
			name:   "model turn limit",
			limits: agent.ToolLoopLimits{MaxModelTurns: 1, MaxToolCalls: 4, MaxToolResultBytes: 100},
			calls:  []model.ToolCall{{ID: "1", Name: "one", Arguments: json.RawMessage(`{}`)}},
			want:   agent.ErrModelTurnLimitExceeded,
		},
		{
			name:   "tool call limit",
			limits: agent.ToolLoopLimits{MaxModelTurns: 2, MaxToolCalls: 1, MaxToolResultBytes: 100},
			calls: []model.ToolCall{
				{ID: "1", Name: "one", Arguments: json.RawMessage(`{}`)},
				{ID: "2", Name: "two", Arguments: json.RawMessage(`{}`)},
			},
			want: agent.ErrToolCallLimitExceeded,
		},
		{
			name:   "model limit has priority",
			limits: agent.ToolLoopLimits{MaxModelTurns: 1, MaxToolCalls: 1, MaxToolResultBytes: 100},
			calls: []model.ToolCall{
				{ID: "1", Name: "one", Arguments: json.RawMessage(`{}`)},
				{ID: "2", Name: "two", Arguments: json.RawMessage(`{}`)},
			},
			want: agent.ErrModelTurnLimitExceeded,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			executions := 0
			service := &fakeToolService{execute: func(context.Context, tool.Call) (tool.Result, error) {
				executions++
				return tool.Result{}, nil
			}}
			llm := &fakeLLM{generate: func(context.Context, model.Request) (model.Stream, error) {
				return doneResultStream(&model.Result{Message: assistantToolCalls(test.calls...), StopReason: model.ReasonToolUse}), nil
			}}
			strategy := buildToolLoopStrategy(t, service, test.limits, agent.ToolErrorModeFeedback)
			stream, err := strategy.Run(context.Background(), validToolLoopInput(llm))
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			events, err := collect(stream)
			if !errors.Is(err, test.want) {
				t.Fatalf("stream error = %v, want %v", err, test.want)
			}
			if executions != 0 {
				t.Fatalf("tool executions = %d, want 0", executions)
			}
			for _, event := range events {
				if event.Type == agent.EventToolStart {
					t.Fatal("limit failure emitted ToolStart")
				}
			}
		})
	}
}

func TestToolLoopStrategyRejectsInvalidModelToolCallsBeforeExecution(t *testing.T) {
	tests := []struct {
		name       string
		message    model.Message
		stopReason model.StopReason
		want       error
	}{
		{
			name:    "non-assistant result",
			message: model.Message{Role: model.RoleUser, Content: []model.ContentBlock{{Kind: model.ContentText, Text: "wrong"}}},
			want:    agent.ErrInvalidModelResult,
		},
		{
			name:    "nil call",
			message: model.Message{Role: model.RoleAssistant, Content: []model.ContentBlock{{Kind: model.ContentToolCall}}},
			want:    agent.ErrInvalidToolCall,
		},
		{
			name:    "empty ID",
			message: assistantToolCalls(model.ToolCall{Name: "tool", Arguments: json.RawMessage(`{}`)}),
			want:    agent.ErrInvalidToolCall,
		},
		{
			name:    "empty name",
			message: assistantToolCalls(model.ToolCall{ID: "1", Arguments: json.RawMessage(`{}`)}),
			want:    agent.ErrInvalidToolCall,
		},
		{
			name:    "invalid arguments",
			message: assistantToolCalls(model.ToolCall{ID: "1", Name: "tool", Arguments: json.RawMessage(`[]`)}),
			want:    agent.ErrInvalidToolCall,
		},
		{
			name: "duplicate batch ID",
			message: assistantToolCalls(
				model.ToolCall{ID: "same", Name: "one", Arguments: json.RawMessage(`{}`)},
				model.ToolCall{ID: "same", Name: "two", Arguments: json.RawMessage(`{}`)},
			),
			want: agent.ErrInvalidToolCall,
		},
		{
			name:       "tool-use reason without call",
			message:    model.Message{Role: model.RoleAssistant, Content: []model.ContentBlock{{Kind: model.ContentText, Text: "none"}}},
			stopReason: model.ReasonToolUse,
			want:       agent.ErrInvalidToolCall,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			executions := 0
			service := &fakeToolService{execute: func(context.Context, tool.Call) (tool.Result, error) {
				executions++
				return tool.Result{}, nil
			}}
			llm := &fakeLLM{generate: func(context.Context, model.Request) (model.Stream, error) {
				return doneResultStream(&model.Result{Message: test.message, StopReason: test.stopReason}), nil
			}}
			strategy := buildToolLoopStrategy(t, service, agent.DefaultToolLoopLimits(), agent.ToolErrorModeFeedback)
			stream, err := strategy.Run(context.Background(), validToolLoopInput(llm))
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			_, err = collect(stream)
			if !errors.Is(err, test.want) {
				t.Fatalf("stream error = %v, want %v", err, test.want)
			}
			if executions != 0 {
				t.Fatalf("tool executions = %d, want 0", executions)
			}
		})
	}
}

func TestToolLoopStrategyRejectsDuplicateCallIDAcrossTurns(t *testing.T) {
	executions := 0
	service := &fakeToolService{execute: func(context.Context, tool.Call) (tool.Result, error) {
		executions++
		return tool.Result{Content: "ok"}, nil
	}}
	turn := 0
	llm := &fakeLLM{generate: func(context.Context, model.Request) (model.Stream, error) {
		turn++
		return doneResultStream(&model.Result{
			Message:    assistantToolCalls(model.ToolCall{ID: "same", Name: "tool", Arguments: json.RawMessage(`{}`)}),
			StopReason: model.ReasonToolUse,
		}), nil
	}}
	strategy := buildToolLoopStrategy(t, service, agent.DefaultToolLoopLimits(), agent.ToolErrorModeFeedback)
	stream, err := strategy.Run(context.Background(), validToolLoopInput(llm))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	_, err = collect(stream)
	if !errors.Is(err, agent.ErrInvalidToolCall) {
		t.Fatalf("stream error = %v, want ErrInvalidToolCall", err)
	}
	if turn != 2 || executions != 1 {
		t.Fatalf("turns = %d, executions = %d, want 2 and 1", turn, executions)
	}
}

func TestToolLoopStrategyRejectsOversizedToolResult(t *testing.T) {
	service := &fakeToolService{execute: func(context.Context, tool.Call) (tool.Result, error) {
		return tool.Result{Content: "too large"}, nil
	}}
	llm := oneToolCallLLM()
	limits := agent.ToolLoopLimits{MaxModelTurns: 2, MaxToolCalls: 1, MaxToolResultBytes: 3}
	strategy := buildToolLoopStrategy(t, service, limits, agent.ToolErrorModeFeedback)
	stream, err := strategy.Run(context.Background(), validToolLoopInput(llm))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	events, err := collect(stream)
	if !errors.Is(err, agent.ErrToolResultTooLarge) {
		t.Fatalf("stream error = %v, want ErrToolResultTooLarge", err)
	}
	last := events[len(events)-1]
	if last.Type != agent.EventToolDone || !last.Tool.IsError || last.Tool.Result.Content != "tool result too large" {
		t.Fatalf("last successful event = %+v", last)
	}
}

func TestToolLoopStrategyDetectsUsageOverflow(t *testing.T) {
	maxValue := int64(^uint64(0) >> 1)
	minValue := -maxValue - 1
	tests := []struct {
		name   string
		first  int64
		second int64
	}{
		{name: "positive", first: maxValue, second: 1},
		{name: "negative", first: minValue, second: -1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &fakeToolService{execute: func(context.Context, tool.Call) (tool.Result, error) {
				return tool.Result{Content: "ok"}, nil
			}}
			turn := 0
			llm := &fakeLLM{generate: func(context.Context, model.Request) (model.Stream, error) {
				turn++
				if turn == 1 {
					return doneResultStream(&model.Result{
						Message:    assistantToolCalls(model.ToolCall{ID: "1", Name: "tool", Arguments: json.RawMessage(`{}`)}),
						Usage:      model.Usage{InputTokens: test.first},
						StopReason: model.ReasonToolUse,
					}), nil
				}
				return doneResultStream(&model.Result{
					Message: model.Message{Role: model.RoleAssistant, Content: []model.ContentBlock{{Kind: model.ContentText, Text: "done"}}},
					Usage:   model.Usage{InputTokens: test.second},
				}), nil
			}}
			strategy := buildToolLoopStrategy(t, service, agent.DefaultToolLoopLimits(), agent.ToolErrorModeFeedback)
			stream, err := strategy.Run(context.Background(), validToolLoopInput(llm))
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			_, err = collect(stream)
			if !errors.Is(err, agent.ErrUsageOverflow) {
				t.Fatalf("stream error = %v, want ErrUsageOverflow", err)
			}
		})
	}
}

func TestToolLoopStrategyEarlyStopAtToolStartHasNoSideEffect(t *testing.T) {
	executions := 0
	service := &fakeToolService{execute: func(context.Context, tool.Call) (tool.Result, error) {
		executions++
		return tool.Result{Content: "unexpected"}, nil
	}}
	strategy := buildToolLoopStrategy(t, service, agent.DefaultToolLoopLimits(), agent.ToolErrorModeFeedback)
	stream, err := strategy.Run(context.Background(), validToolLoopInput(oneToolCallLLM()))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	for event, streamErr := range stream {
		if streamErr != nil {
			t.Fatalf("stream error = %v", streamErr)
		}
		if event.Type == agent.EventToolStart {
			break
		}
	}
	if executions != 0 {
		t.Fatalf("tool executions = %d, want 0", executions)
	}
}

func TestToolLoopStrategyPrefersContextCancellationAfterToolExecution(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	service := &fakeToolService{execute: func(context.Context, tool.Call) (tool.Result, error) {
		cancel()
		return tool.Result{}, errors.New("late tool error")
	}}
	strategy := buildToolLoopStrategy(t, service, agent.DefaultToolLoopLimits(), agent.ToolErrorModeFeedback)
	stream, err := strategy.Run(ctx, validToolLoopInput(oneToolCallLLM()))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	events, err := collect(stream)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("stream error = %v, want context.Canceled", err)
	}
	for _, event := range events {
		if event.Type == agent.EventToolDone {
			t.Fatal("context cancellation emitted a false ToolDone")
		}
	}
}

func TestToolLoopStrategyDoesNotContinueAfterCancellationAtToolDone(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	executions := 0
	modelTurns := 0
	service := &fakeToolService{execute: func(context.Context, tool.Call) (tool.Result, error) {
		executions++
		return tool.Result{Content: "done"}, nil
	}}
	llm := &fakeLLM{generate: func(context.Context, model.Request) (model.Stream, error) {
		modelTurns++
		return doneResultStream(&model.Result{
			Message: assistantToolCalls(
				model.ToolCall{ID: "first", Name: "one", Arguments: json.RawMessage(`{}`)},
				model.ToolCall{ID: "second", Name: "two", Arguments: json.RawMessage(`{}`)},
			),
			StopReason: model.ReasonToolUse,
		}), nil
	}}
	strategy := buildToolLoopStrategy(t, service, agent.DefaultToolLoopLimits(), agent.ToolErrorModeFeedback)
	stream, err := strategy.Run(ctx, validToolLoopInput(llm))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	var streamErr error
	for event, eventErr := range stream {
		if eventErr != nil {
			streamErr = eventErr
			break
		}
		if event.Type == agent.EventToolDone {
			cancel()
		}
	}
	if !errors.Is(streamErr, context.Canceled) {
		t.Fatalf("stream error = %v, want context.Canceled", streamErr)
	}
	if executions != 1 || modelTurns != 1 {
		t.Fatalf("executions = %d, model turns = %d, want 1 and 1", executions, modelTurns)
	}
}

func TestToolLoopStrategyReportsModelFailuresAtStreamBoundary(t *testing.T) {
	wantFirst := errors.New("first setup failed")
	firstFailure := buildToolLoopStrategy(t, &fakeToolService{}, agent.DefaultToolLoopLimits(), agent.ToolErrorModeFeedback)
	_, err := firstFailure.Run(context.Background(), validToolLoopInput(&fakeLLM{generate: func(context.Context, model.Request) (model.Stream, error) {
		return nil, wantFirst
	}}))
	if !errors.Is(err, wantFirst) {
		t.Fatalf("first Run() error = %v, want original setup error", err)
	}

	wantLater := errors.New("second setup failed")
	service := &fakeToolService{execute: func(context.Context, tool.Call) (tool.Result, error) {
		return tool.Result{Content: "ok"}, nil
	}}
	turn := 0
	llm := &fakeLLM{generate: func(context.Context, model.Request) (model.Stream, error) {
		turn++
		if turn == 1 {
			return doneResultStream(&model.Result{
				Message:    assistantToolCalls(model.ToolCall{ID: "1", Name: "tool", Arguments: json.RawMessage(`{}`)}),
				StopReason: model.ReasonToolUse,
			}), nil
		}
		return nil, wantLater
	}}
	strategy := buildToolLoopStrategy(t, service, agent.DefaultToolLoopLimits(), agent.ToolErrorModeFeedback)
	stream, err := strategy.Run(context.Background(), validToolLoopInput(llm))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	_, err = collect(stream)
	if !errors.Is(err, wantLater) {
		t.Fatalf("later stream error = %v, want original setup error", err)
	}
}

func TestConfiguredAgentSnapshotsToolEvents(t *testing.T) {
	source := &agent.ToolEvent{
		Call:   tool.Call{ID: "call", Name: "tool", Arguments: json.RawMessage(`{"value":1}`)},
		Result: &tool.Result{Content: "source"},
	}
	strategy := &fakeRunStrategy{run: func(context.Context, agent.RunInput) (agent.Stream, error) {
		return func(yield func(agent.Event, error) bool) {
			if !yield(agent.Event{Type: agent.EventRunStart}, nil) {
				return
			}
			if !yield(agent.Event{Type: agent.EventToolDone, ModelTurn: 1, Tool: source}, nil) {
				return
			}
			yield(agent.Event{Type: agent.EventRunDone, Result: &agent.Result{}}, nil)
		}, nil
	}}
	value := buildAgentWithStrategy(t, strategy)
	stream, err := value.Run(context.Background(), agent.Request{Messages: userMessages("hello")})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	events, err := collect(stream)
	if err != nil {
		t.Fatalf("stream error = %v", err)
	}
	events[1].Tool.Call.Arguments[0] = '['
	events[1].Tool.Result.Content = "mutated"
	if got := string(source.Call.Arguments); got != `{"value":1}` || source.Result.Content != "source" {
		t.Fatalf("source ToolEvent was mutated: arguments %s, result %q", got, source.Result.Content)
	}
}

func TestToolLoopStrategyValidatesDirectInput(t *testing.T) {
	validLLM := &fakeLLM{generate: func(context.Context, model.Request) (model.Stream, error) {
		return doneModelStream("ok"), nil
	}}
	validInput := validToolLoopInput(validLLM)

	var nilStrategy *agent.ToolLoopStrategy
	if _, err := nilStrategy.Run(context.Background(), validInput); !errors.Is(err, agent.ErrNilRunStrategy) {
		t.Fatalf("nil ToolLoopStrategy.Run() error = %v, want ErrNilRunStrategy", err)
	}
	zeroStrategy := &agent.ToolLoopStrategy{}
	if _, err := zeroStrategy.Run(context.Background(), validInput); !errors.Is(err, agent.ErrMissingToolService) {
		t.Fatalf("zero ToolLoopStrategy.Run() error = %v, want ErrMissingToolService", err)
	}

	strategy := buildToolLoopStrategy(t, &fakeToolService{}, agent.DefaultToolLoopLimits(), agent.ToolErrorModeFeedback)
	if _, err := strategy.Run(nil, validInput); !errors.Is(err, agent.ErrInvalidRequest) {
		t.Fatalf("Run(nil context) error = %v, want ErrInvalidRequest", err)
	}
	if _, err := strategy.Run(context.Background(), agent.RunInput{}); !errors.Is(err, agent.ErrNilLLM) {
		t.Fatalf("Run(nil LLM) error = %v, want ErrNilLLM", err)
	}
	var typedNilLLM *fakeLLM
	if _, err := strategy.Run(context.Background(), agent.RunInput{LLM: typedNilLLM}); !errors.Is(err, agent.ErrNilLLM) {
		t.Fatalf("Run(typed nil LLM) error = %v, want ErrNilLLM", err)
	}
	if _, err := strategy.Run(context.Background(), agent.RunInput{LLM: validLLM}); !errors.Is(err, agent.ErrInvalidRequest) {
		t.Fatalf("Run(empty messages) error = %v, want ErrInvalidRequest", err)
	}
	invalidMaxTokens := 0
	invalidInput := validInput
	invalidInput.Request.Options.MaxTokens = &invalidMaxTokens
	if _, err := strategy.Run(context.Background(), invalidInput); !errors.Is(err, agent.ErrInvalidOptions) {
		t.Fatalf("Run(invalid options) error = %v, want ErrInvalidOptions", err)
	}
}

func TestToolLoopStrategySupportsConcurrentAgentRuns(t *testing.T) {
	service := &fakeToolService{execute: func(_ context.Context, call tool.Call) (tool.Result, error) {
		var arguments struct {
			Value string `json:"value"`
		}
		if err := json.Unmarshal(call.Arguments, &arguments); err != nil {
			return tool.Result{}, err
		}
		return tool.Result{Content: arguments.Value}, nil
	}}
	strategy := buildToolLoopStrategy(t, service, agent.DefaultToolLoopLimits(), agent.ToolErrorModeFeedback)
	llm := &fakeLLM{generate: func(_ context.Context, req model.Request) (model.Stream, error) {
		last := req.Context.Messages[len(req.Context.Messages)-1]
		if last.Role == model.RoleUser {
			value := last.Content[0].Text
			arguments, err := json.Marshal(map[string]string{"value": value})
			if err != nil {
				return nil, err
			}
			return doneResultStream(&model.Result{
				Message:    assistantToolCalls(model.ToolCall{ID: "call-" + value, Name: "echo", Arguments: arguments}),
				StopReason: model.ReasonToolUse,
			}), nil
		}
		return doneModelStream(last.Content[0].Text), nil
	}}
	builder := agent.NewBuilder()
	if err := builder.UseLLM(llm); err != nil {
		t.Fatalf("UseLLM() error = %v", err)
	}
	if err := builder.UseRunStrategy(strategy); err != nil {
		t.Fatalf("UseRunStrategy() error = %v", err)
	}
	value, err := builder.Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	const runs = 16
	errorsByRun := make(chan error, runs)
	var wait sync.WaitGroup
	for index := 0; index < runs; index++ {
		index := index
		wait.Add(1)
		go func() {
			defer wait.Done()
			want := fmt.Sprintf("run-%d", index)
			result, err := agent.Complete(context.Background(), value, agent.Request{Messages: userMessages(want)})
			if err != nil {
				errorsByRun <- fmt.Errorf("%s: %w", want, err)
				return
			}
			if got := result.Output.Content[0].Text; got != want {
				errorsByRun <- fmt.Errorf("%s: output = %q", want, got)
			}
		}()
	}
	wait.Wait()
	close(errorsByRun)
	for err := range errorsByRun {
		t.Error(err)
	}
}

func buildToolLoopStrategy(
	t *testing.T,
	service tool.Service,
	limits agent.ToolLoopLimits,
	mode agent.ToolErrorMode,
) *agent.ToolLoopStrategy {
	t.Helper()
	builder := agent.NewToolLoopBuilder()
	if err := builder.UseTools(service); err != nil {
		t.Fatalf("UseTools() error = %v", err)
	}
	if err := builder.SetLimits(limits); err != nil {
		t.Fatalf("SetLimits() error = %v", err)
	}
	if err := builder.SetToolErrorMode(mode); err != nil {
		t.Fatalf("SetToolErrorMode() error = %v", err)
	}
	strategy, err := builder.Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	return strategy
}

func validToolLoopInput(llm model.LLM) agent.RunInput {
	return agent.RunInput{
		LLM:     llm,
		Request: agent.Request{Messages: userMessages("hello")},
	}
}

func assistantToolCalls(calls ...model.ToolCall) model.Message {
	content := make([]model.ContentBlock, 0, len(calls))
	for index := range calls {
		call := calls[index]
		content = append(content, model.ContentBlock{Kind: model.ContentToolCall, ToolCall: &call})
	}
	return model.Message{Role: model.RoleAssistant, Content: content}
}

func doneResultStream(result *model.Result) model.Stream {
	return func(yield func(model.Event, error) bool) {
		yield(model.Event{Type: model.EventDone, Result: result}, nil)
	}
}

func oneToolCallLLM() model.LLM {
	return &fakeLLM{generate: func(context.Context, model.Request) (model.Stream, error) {
		return doneResultStream(&model.Result{
			Message:    assistantToolCalls(model.ToolCall{ID: "call-1", Name: "tool", Arguments: json.RawMessage(`{}`)}),
			StopReason: model.ReasonToolUse,
		}), nil
	}}
}
