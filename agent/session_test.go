package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/JIAOZAI1/acore/agent"
	"github.com/JIAOZAI1/acore/model"
	"github.com/JIAOZAI1/acore/session"
	"github.com/JIAOZAI1/acore/tool"
)

type fakeSessionService struct {
	loadFn      func(context.Context, session.Key) (session.Snapshot, error)
	appendFn    func(context.Context, session.Key, session.Revision, []model.Message) (session.Revision, error)
	loadCalls   atomic.Int64
	appendCalls atomic.Int64
}

func (f *fakeSessionService) Load(ctx context.Context, key session.Key) (session.Snapshot, error) {
	f.loadCalls.Add(1)
	if f.loadFn == nil {
		return session.Snapshot{}, nil
	}
	return f.loadFn(ctx, key)
}

func (f *fakeSessionService) Append(ctx context.Context, key session.Key, expected session.Revision, messages []model.Message) (session.Revision, error) {
	f.appendCalls.Add(1)
	if f.appendFn == nil {
		return expected + 1, nil
	}
	return f.appendFn(ctx, key, expected, messages)
}

var _ session.Service = (*fakeSessionService)(nil)

func TestStrategyBuildersConfigureSessionAndFreeze(t *testing.T) {
	t.Run("single turn", func(t *testing.T) {
		builder := agent.NewSingleTurnBuilder()
		if err := builder.UseSession(nil); !errors.Is(err, agent.ErrNilSessionService) {
			t.Fatalf("UseSession(nil) error = %v, want ErrNilSessionService", err)
		}
		var typedNil *fakeSessionService
		if err := builder.UseSession(typedNil); !errors.Is(err, agent.ErrNilSessionService) {
			t.Fatalf("UseSession(typed nil) error = %v, want ErrNilSessionService", err)
		}

		service := &fakeSessionService{}
		if err := builder.UseSession(service); err != nil {
			t.Fatalf("UseSession() error = %v", err)
		}
		if err := builder.UseSession(service); !errors.Is(err, agent.ErrSessionServiceAlreadySet) {
			t.Fatalf("second UseSession() error = %v, want ErrSessionServiceAlreadySet", err)
		}
		strategy, err := builder.Build()
		if err != nil {
			t.Fatalf("Build() error = %v", err)
		}
		if strategy == nil {
			t.Fatal("Build() strategy is nil")
		}
		if err := builder.UseSession(service); !errors.Is(err, agent.ErrSingleTurnBuilderBuilt) {
			t.Fatalf("UseSession() after Build error = %v, want ErrSingleTurnBuilderBuilt", err)
		}
		if _, err := builder.Build(); !errors.Is(err, agent.ErrSingleTurnBuilderBuilt) {
			t.Fatalf("second Build() error = %v, want ErrSingleTurnBuilderBuilt", err)
		}
	})

	t.Run("tool loop", func(t *testing.T) {
		builder := agent.NewToolLoopBuilder()
		if err := builder.UseSession(nil); !errors.Is(err, agent.ErrNilSessionService) {
			t.Fatalf("UseSession(nil) error = %v, want ErrNilSessionService", err)
		}
		var typedNil *fakeSessionService
		if err := builder.UseSession(typedNil); !errors.Is(err, agent.ErrNilSessionService) {
			t.Fatalf("UseSession(typed nil) error = %v, want ErrNilSessionService", err)
		}

		service := &fakeSessionService{}
		if err := builder.UseSession(service); err != nil {
			t.Fatalf("UseSession() error = %v", err)
		}
		if err := builder.UseSession(service); !errors.Is(err, agent.ErrSessionServiceAlreadySet) {
			t.Fatalf("second UseSession() error = %v, want ErrSessionServiceAlreadySet", err)
		}
		if err := builder.UseTools(&fakeToolService{}); err != nil {
			t.Fatalf("UseTools() error = %v", err)
		}
		if _, err := builder.Build(); err != nil {
			t.Fatalf("Build() error = %v", err)
		}
		if err := builder.UseSession(service); !errors.Is(err, agent.ErrToolLoopBuilderBuilt) {
			t.Fatalf("UseSession() after Build error = %v, want ErrToolLoopBuilderBuilt", err)
		}
	})

	t.Run("stateless single turn", func(t *testing.T) {
		strategy, err := agent.NewSingleTurnBuilder().Build()
		if err != nil {
			t.Fatalf("Build() error = %v", err)
		}
		stream, err := strategy.Run(context.Background(), agent.RunInput{
			LLM: &fakeLLM{generate: func(context.Context, model.Request) (model.Stream, error) {
				return doneModelStream("ok"), nil
			}},
			Request: agent.Request{Messages: userMessages("hello")},
		})
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
		if _, err := collect(stream); err != nil {
			t.Fatalf("stream error = %v", err)
		}
	})
}

func TestConfiguredAgentValidatesAndCopiesSessionInput(t *testing.T) {
	strategyCalled := false
	strategy := &fakeRunStrategy{run: func(context.Context, agent.RunInput) (agent.Stream, error) {
		strategyCalled = true
		return successfulAgentStream("unused"), nil
	}}
	value := buildAgentWithStrategy(t, strategy)
	validKey := session.Key{Scope: "tenant-a", ID: "conversation-a"}

	tests := []struct {
		name string
		req  agent.Request
		want error
	}{
		{name: "both input forms", req: agent.Request{Messages: userMessages("complete"), Session: &agent.SessionInput{Key: validKey, Messages: userMessages("new")}}, want: agent.ErrInvalidRequest},
		{name: "empty scope", req: agent.Request{Session: &agent.SessionInput{Key: session.Key{ID: "conversation-a"}, Messages: userMessages("new")}}, want: session.ErrInvalidKey},
		{name: "empty ID", req: agent.Request{Session: &agent.SessionInput{Key: session.Key{Scope: "tenant-a"}, Messages: userMessages("new")}}, want: session.ErrInvalidKey},
		{name: "empty session messages", req: agent.Request{Session: &agent.SessionInput{Key: validKey}}, want: agent.ErrInvalidRequest},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := value.Run(context.Background(), test.req); !errors.Is(err, test.want) {
				t.Fatalf("Run() error = %v, want %v", err, test.want)
			}
		})
	}
	if strategyCalled {
		t.Fatal("strategy was called for invalid Session input")
	}

	signature := "signature"
	arguments := json.RawMessage(`{"value":1}`)
	req := agent.Request{Session: &agent.SessionInput{
		Key: validKey,
		Messages: []model.Message{{
			Role: model.RoleUser,
			Content: []model.ContentBlock{
				{Kind: model.ContentThinking, Text: "thinking", Signature: &signature},
				{Kind: model.ContentToolCall, ToolCall: &model.ToolCall{ID: "call-1", Name: "lookup", Arguments: arguments}},
			},
		}},
	}}
	var captured agent.RunInput
	strategy.run = func(_ context.Context, input agent.RunInput) (agent.Stream, error) {
		captured = input
		return successfulAgentStream("done"), nil
	}
	stream, err := value.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("Run(valid Session input) error = %v", err)
	}
	if _, err := collect(stream); err != nil {
		t.Fatalf("stream error = %v", err)
	}

	req.Session.Key.ID = "mutated"
	req.Session.Messages[0].Content[0].Text = "mutated"
	*req.Session.Messages[0].Content[0].Signature = "mutated"
	req.Session.Messages[0].Content[1].ToolCall.Arguments[0] = '['
	if captured.Request.Session == req.Session {
		t.Fatal("RunInput shares the SessionInput pointer with the caller")
	}
	if captured.Request.Session.Key != validKey {
		t.Fatalf("RunInput Session key = %+v, want %+v", captured.Request.Session.Key, validKey)
	}
	blocks := captured.Request.Session.Messages[0].Content
	if blocks[0].Text != "thinking" || blocks[0].Signature == nil || *blocks[0].Signature != "signature" {
		t.Fatalf("RunInput thinking block = %+v, want isolated original", blocks[0])
	}
	if got := string(blocks[1].ToolCall.Arguments); got != `{"value":1}` {
		t.Fatalf("RunInput tool arguments = %s, want original", got)
	}
}

func TestSingleTurnStrategySessionLifecycle(t *testing.T) {
	key := session.Key{Scope: "tenant-a", ID: "conversation-a"}
	history := []model.Message{
		textMessage(model.RoleUser, "old question"),
		textMessage(model.RoleAssistant, "old answer"),
	}
	inputMessages := userMessages("new question")
	var modelRequest model.Request
	var appended []model.Message
	committed := false
	service := &fakeSessionService{
		loadFn: func(_ context.Context, got session.Key) (session.Snapshot, error) {
			if got != key {
				t.Fatalf("Load() key = %+v, want %+v", got, key)
			}
			return session.Snapshot{Revision: 7, Messages: history}, nil
		},
		appendFn: func(_ context.Context, got session.Key, expected session.Revision, messages []model.Message) (session.Revision, error) {
			if got != key || expected != 7 {
				t.Fatalf("Append() key/revision = %+v/%d, want %+v/7", got, expected, key)
			}
			appended = messages
			committed = true
			return 8, nil
		},
	}
	strategy := buildSingleTurnStrategyWithSession(t, service)
	llm := &fakeLLM{generate: func(_ context.Context, req model.Request) (model.Stream, error) {
		modelRequest = req
		return doneModelStream("new answer"), nil
	}}
	stream, err := strategy.Run(context.Background(), agent.RunInput{
		LLM: llm,
		Request: agent.Request{Session: &agent.SessionInput{
			Key:      key,
			Messages: inputMessages,
		}},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	inputMessages[0].Content[0].Text = "mutated after Run"

	events, err := collect(stream)
	if err != nil {
		t.Fatalf("stream error = %v", err)
	}
	if len(modelRequest.Context.Messages) != 3 {
		t.Fatalf("model messages = %d, want 3", len(modelRequest.Context.Messages))
	}
	if got := messageTexts(modelRequest.Context.Messages); fmt.Sprint(got) != fmt.Sprint([]string{"old question", "old answer", "new question"}) {
		t.Fatalf("model message texts = %v", got)
	}
	if len(appended) != 2 || appended[0].Content[0].Text != "new question" || appended[1].Content[0].Text != "new answer" {
		t.Fatalf("appended messages = %+v, want new input and generated answer", appended)
	}
	if len(events) == 0 || events[len(events)-1].Type != agent.EventRunDone || !committed {
		t.Fatalf("terminal event/commit = %+v/%t, want committed RunDone", events, committed)
	}
}

func TestSingleTurnStrategySessionErrorsAndNoCommit(t *testing.T) {
	key := session.Key{Scope: "tenant-a", ID: "conversation-a"}
	request := agent.Request{Session: &agent.SessionInput{Key: key, Messages: userMessages("new")}}
	llmCalls := 0
	llm := &fakeLLM{generate: func(context.Context, model.Request) (model.Stream, error) {
		llmCalls++
		return doneModelStream("answer"), nil
	}}

	if _, err := agent.NewSingleTurnStrategy().Run(context.Background(), agent.RunInput{LLM: llm, Request: request}); !errors.Is(err, agent.ErrSessionUnsupported) {
		t.Fatalf("stateless strategy error = %v, want ErrSessionUnsupported", err)
	}
	if llmCalls != 0 {
		t.Fatalf("LLM calls after unsupported Session = %d, want 0", llmCalls)
	}
	toolLoop := buildToolLoopStrategy(t, &fakeToolService{}, agent.DefaultToolLoopLimits(), agent.ToolErrorModeFeedback)
	if _, err := toolLoop.Run(context.Background(), agent.RunInput{LLM: llm, Request: request}); !errors.Is(err, agent.ErrSessionUnsupported) {
		t.Fatalf("ToolLoop without Session service error = %v, want ErrSessionUnsupported", err)
	}
	if llmCalls != 0 {
		t.Fatalf("LLM calls after unsupported ToolLoop Session = %d, want 0", llmCalls)
	}

	wantLoad := errors.New("load unavailable")
	loadFailure := buildSingleTurnStrategyWithSession(t, &fakeSessionService{loadFn: func(context.Context, session.Key) (session.Snapshot, error) {
		return session.Snapshot{}, wantLoad
	}})
	if _, err := loadFailure.Run(context.Background(), agent.RunInput{LLM: llm, Request: request}); !errors.Is(err, agent.ErrLoadSession) || !errors.Is(err, wantLoad) {
		t.Fatalf("load failure = %v, want ErrLoadSession and original error", err)
	}
	if llmCalls != 0 {
		t.Fatalf("LLM calls after load failure = %d, want 0", llmCalls)
	}

	invalidSnapshots := []session.Snapshot{
		{Messages: userMessages("impossible")},
		{Revision: 1},
	}
	for _, snapshot := range invalidSnapshots {
		invalidSnapshot := buildSingleTurnStrategyWithSession(t, &fakeSessionService{loadFn: func(context.Context, session.Key) (session.Snapshot, error) {
			return snapshot, nil
		}})
		if _, err := invalidSnapshot.Run(context.Background(), agent.RunInput{LLM: llm, Request: request}); !errors.Is(err, session.ErrInvalidSnapshot) {
			t.Fatalf("invalid snapshot %+v error = %v, want ErrInvalidSnapshot", snapshot, err)
		}
	}

	commitService := &fakeSessionService{appendFn: func(context.Context, session.Key, session.Revision, []model.Message) (session.Revision, error) {
		return 0, session.ErrConflict
	}}
	commitFailure := buildSingleTurnStrategyWithSession(t, commitService)
	stream, err := commitFailure.Run(context.Background(), agent.RunInput{LLM: llm, Request: request})
	if err != nil {
		t.Fatalf("Run(commit failure) error = %v", err)
	}
	events, err := collect(stream)
	if !errors.Is(err, agent.ErrCommitSession) || !errors.Is(err, session.ErrConflict) {
		t.Fatalf("commit stream error = %v, want ErrCommitSession and ErrConflict", err)
	}
	for _, event := range events {
		if event.Type == agent.EventRunDone {
			t.Fatal("commit failure emitted RunDone")
		}
	}
	if llmCalls != 1 || commitService.appendCalls.Load() != 1 {
		t.Fatalf("commit failure calls = LLM %d, Append %d; want 1/1", llmCalls, commitService.appendCalls.Load())
	}

	earlyStopService := &fakeSessionService{}
	earlyStop := buildSingleTurnStrategyWithSession(t, earlyStopService)
	stream, err = earlyStop.Run(context.Background(), agent.RunInput{LLM: llm, Request: request})
	if err != nil {
		t.Fatalf("Run(early stop) error = %v", err)
	}
	for range stream {
		break
	}
	if earlyStopService.appendCalls.Load() != 0 {
		t.Fatalf("Append calls after early stop = %d, want 0", earlyStopService.appendCalls.Load())
	}
}

func TestSessionConfiguredStrategyKeepsStatelessRunsStateless(t *testing.T) {
	service := &fakeSessionService{
		loadFn: func(context.Context, session.Key) (session.Snapshot, error) {
			t.Fatal("stateless run called Load")
			return session.Snapshot{}, nil
		},
		appendFn: func(context.Context, session.Key, session.Revision, []model.Message) (session.Revision, error) {
			t.Fatal("stateless run called Append")
			return 0, nil
		},
	}
	strategy := buildSingleTurnStrategyWithSession(t, service)
	stream, err := strategy.Run(context.Background(), agent.RunInput{
		LLM: &fakeLLM{generate: func(context.Context, model.Request) (model.Stream, error) {
			return doneModelStream("answer"), nil
		}},
		Request: agent.Request{Messages: userMessages("complete history")},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if _, err := collect(stream); err != nil {
		t.Fatalf("stream error = %v", err)
	}
	if service.loadCalls.Load() != 0 || service.appendCalls.Load() != 0 {
		t.Fatalf("Session calls = Load %d, Append %d; want 0/0", service.loadCalls.Load(), service.appendCalls.Load())
	}

	toolLoop := buildToolLoopStrategyWithSession(t, &fakeToolService{}, service, agent.ToolErrorModeFeedback)
	stream, err = toolLoop.Run(context.Background(), agent.RunInput{
		LLM: &fakeLLM{generate: func(context.Context, model.Request) (model.Stream, error) {
			return doneModelStream("answer"), nil
		}},
		Request: agent.Request{Messages: userMessages("complete history")},
	})
	if err != nil {
		t.Fatalf("ToolLoop.Run() error = %v", err)
	}
	if _, err := collect(stream); err != nil {
		t.Fatalf("ToolLoop stream error = %v", err)
	}
	if service.loadCalls.Load() != 0 || service.appendCalls.Load() != 0 {
		t.Fatalf("ToolLoop Session calls = Load %d, Append %d; want 0/0", service.loadCalls.Load(), service.appendCalls.Load())
	}
}

func TestToolLoopStrategySessionLifecycleAndFailure(t *testing.T) {
	key := session.Key{Scope: "tenant-a", ID: "tool-loop"}
	history := []model.Message{
		textMessage(model.RoleUser, "old question"),
		textMessage(model.RoleAssistant, "old answer"),
	}
	var requests []model.Request
	var appended []model.Message
	committed := false
	sessionService := &fakeSessionService{
		loadFn: func(context.Context, session.Key) (session.Snapshot, error) {
			return session.Snapshot{Revision: 4, Messages: history}, nil
		},
		appendFn: func(_ context.Context, got session.Key, expected session.Revision, messages []model.Message) (session.Revision, error) {
			if got != key || expected != 4 {
				t.Fatalf("Append() key/revision = %+v/%d, want %+v/4", got, expected, key)
			}
			appended = messages
			committed = true
			return 5, nil
		},
	}
	toolService := &fakeToolService{execute: func(context.Context, tool.Call) (tool.Result, error) {
		return tool.Result{Content: "tool result"}, nil
	}}
	turn := 0
	llm := &fakeLLM{generate: func(_ context.Context, req model.Request) (model.Stream, error) {
		turn++
		requests = append(requests, req)
		if turn == 1 {
			return doneResultStream(&model.Result{
				Message:    assistantToolCalls(model.ToolCall{ID: "call-1", Name: "lookup", Arguments: json.RawMessage(`{}`)}),
				StopReason: model.ReasonToolUse,
			}), nil
		}
		return doneModelStream("final answer"), nil
	}}
	strategy := buildToolLoopStrategyWithSession(t, toolService, sessionService, agent.ToolErrorModeFeedback)
	stream, err := strategy.Run(context.Background(), agent.RunInput{
		LLM: llm,
		Request: agent.Request{Session: &agent.SessionInput{
			Key:      key,
			Messages: userMessages("new question"),
		}},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	events, err := collect(stream)
	if err != nil {
		t.Fatalf("stream error = %v", err)
	}
	if len(requests) != 2 {
		t.Fatalf("model requests = %d, want 2", len(requests))
	}
	if got := messageTexts(requests[0].Context.Messages); fmt.Sprint(got) != fmt.Sprint([]string{"old question", "old answer", "new question"}) {
		t.Fatalf("first model message texts = %v", got)
	}
	if roles := messageRoles(requests[1].Context.Messages); fmt.Sprint(roles) != fmt.Sprint([]model.Role{model.RoleUser, model.RoleAssistant, model.RoleUser, model.RoleAssistant, model.RoleTool}) {
		t.Fatalf("second model message roles = %v", roles)
	}
	if len(appended) != 4 {
		t.Fatalf("appended messages = %d, want 4", len(appended))
	}
	if roles := messageRoles(appended); fmt.Sprint(roles) != fmt.Sprint([]model.Role{model.RoleUser, model.RoleAssistant, model.RoleTool, model.RoleAssistant}) {
		t.Fatalf("appended message roles = %v", roles)
	}
	if appended[0].Content[0].Text != "new question" || appended[2].Content[0].Text != "tool result" || appended[3].Content[0].Text != "final answer" {
		t.Fatalf("appended messages = %+v", appended)
	}
	if len(events) == 0 || events[len(events)-1].Type != agent.EventRunDone || !committed {
		t.Fatalf("terminal event/commit = %+v/%t, want committed RunDone", events, committed)
	}

	wantTool := errors.New("tool failed")
	failingTools := &fakeToolService{execute: func(context.Context, tool.Call) (tool.Result, error) {
		return tool.Result{}, wantTool
	}}
	noCommit := &fakeSessionService{}
	failingStrategy := buildToolLoopStrategyWithSession(t, failingTools, noCommit, agent.ToolErrorModeFailFast)
	failingLLM := &fakeLLM{generate: func(context.Context, model.Request) (model.Stream, error) {
		return doneResultStream(&model.Result{
			Message:    assistantToolCalls(model.ToolCall{ID: "call-1", Name: "fail", Arguments: json.RawMessage(`{}`)}),
			StopReason: model.ReasonToolUse,
		}), nil
	}}
	stream, err = failingStrategy.Run(context.Background(), agent.RunInput{
		LLM: failingLLM,
		Request: agent.Request{Session: &agent.SessionInput{
			Key:      key,
			Messages: userMessages("will fail"),
		}},
	})
	if err != nil {
		t.Fatalf("Run(failing tool) error = %v", err)
	}
	events, err = collect(stream)
	if !errors.Is(err, wantTool) {
		t.Fatalf("tool failure stream error = %v, want original error", err)
	}
	if noCommit.appendCalls.Load() != 0 {
		t.Fatalf("Append calls after tool failure = %d, want 0", noCommit.appendCalls.Load())
	}
	for _, event := range events {
		if event.Type == agent.EventRunDone {
			t.Fatal("tool failure emitted RunDone")
		}
	}
}

func TestToolLoopStrategyCommitConflictDoesNotReplaySideEffects(t *testing.T) {
	key := session.Key{Scope: "tenant-a", ID: "conflict"}
	sessionService := &fakeSessionService{appendFn: func(context.Context, session.Key, session.Revision, []model.Message) (session.Revision, error) {
		return 0, session.ErrConflict
	}}
	toolCalls := 0
	toolService := &fakeToolService{execute: func(context.Context, tool.Call) (tool.Result, error) {
		toolCalls++
		return tool.Result{Content: "side effect completed"}, nil
	}}
	modelTurns := 0
	llm := &fakeLLM{generate: func(context.Context, model.Request) (model.Stream, error) {
		modelTurns++
		if modelTurns == 1 {
			return doneResultStream(&model.Result{
				Message:    assistantToolCalls(model.ToolCall{ID: "call-1", Name: "write", Arguments: json.RawMessage(`{}`)}),
				StopReason: model.ReasonToolUse,
			}), nil
		}
		return doneModelStream("finished"), nil
	}}
	strategy := buildToolLoopStrategyWithSession(t, toolService, sessionService, agent.ToolErrorModeFeedback)
	stream, err := strategy.Run(context.Background(), agent.RunInput{
		LLM: llm,
		Request: agent.Request{Session: &agent.SessionInput{
			Key:      key,
			Messages: userMessages("perform write"),
		}},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	events, err := collect(stream)
	if !errors.Is(err, agent.ErrCommitSession) || !errors.Is(err, session.ErrConflict) {
		t.Fatalf("stream error = %v, want ErrCommitSession and ErrConflict", err)
	}
	if modelTurns != 2 || toolCalls != 1 || sessionService.appendCalls.Load() != 1 {
		t.Fatalf("calls = model %d, tool %d, Append %d; want 2/1/1", modelTurns, toolCalls, sessionService.appendCalls.Load())
	}
	for _, event := range events {
		if event.Type == agent.EventRunDone {
			t.Fatal("commit conflict emitted RunDone")
		}
	}
}

func TestSingleTurnStrategyCancellationDoesNotCommit(t *testing.T) {
	key := session.Key{Scope: "tenant-a", ID: "canceled"}
	service := &fakeSessionService{}
	strategy := buildSingleTurnStrategyWithSession(t, service)
	ctx, cancel := context.WithCancel(context.Background())
	llm := &fakeLLM{generate: func(context.Context, model.Request) (model.Stream, error) {
		return func(yield func(model.Event, error) bool) {
			cancel()
			yield(model.Event{Type: model.EventDone, Result: modelResult("ignored")}, nil)
		}, nil
	}}
	stream, err := strategy.Run(ctx, agent.RunInput{
		LLM: llm,
		Request: agent.Request{Session: &agent.SessionInput{
			Key:      key,
			Messages: userMessages("new"),
		}},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	events, err := collect(stream)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("stream error = %v, want context.Canceled", err)
	}
	if service.appendCalls.Load() != 0 {
		t.Fatalf("Append calls after cancellation = %d, want 0", service.appendCalls.Load())
	}
	for _, event := range events {
		if event.Type == agent.EventRunDone {
			t.Fatal("canceled stream emitted RunDone")
		}
	}
}

func TestSingleTurnStrategySupportsConcurrentSessionRuns(t *testing.T) {
	store := session.NewMemoryService()
	strategy := buildSingleTurnStrategyWithSession(t, store)
	llm := &fakeLLM{generate: func(_ context.Context, req model.Request) (model.Stream, error) {
		messages := req.Context.Messages
		oldText := messages[0].Content[0].Text
		newText := messages[len(messages)-1].Content[0].Text
		return doneModelStream(oldText + "/" + newText), nil
	}}

	const runs = 16
	keys := make([]session.Key, runs)
	for index := range keys {
		keys[index] = session.Key{Scope: "tenant-a", ID: fmt.Sprintf("conversation-%d", index)}
		if _, err := store.Append(context.Background(), keys[index], 0, userMessages(fmt.Sprintf("old-%d", index))); err != nil {
			t.Fatalf("seed Append(%d) error = %v", index, err)
		}
	}

	errorsByRun := make(chan error, runs)
	var wait sync.WaitGroup
	for index := range keys {
		index := index
		wait.Add(1)
		go func() {
			defer wait.Done()
			newText := fmt.Sprintf("new-%d", index)
			stream, err := strategy.Run(context.Background(), agent.RunInput{
				LLM: llm,
				Request: agent.Request{Session: &agent.SessionInput{
					Key:      keys[index],
					Messages: userMessages(newText),
				}},
			})
			if err != nil {
				errorsByRun <- fmt.Errorf("Run(%d): %w", index, err)
				return
			}
			if _, err := collect(stream); err != nil {
				errorsByRun <- fmt.Errorf("stream(%d): %w", index, err)
			}
		}()
	}
	wait.Wait()
	close(errorsByRun)
	for err := range errorsByRun {
		t.Error(err)
	}

	for index, key := range keys {
		snapshot, err := store.Load(context.Background(), key)
		if err != nil {
			t.Fatalf("Load(%d) error = %v", index, err)
		}
		want := []string{
			fmt.Sprintf("old-%d", index),
			fmt.Sprintf("new-%d", index),
			fmt.Sprintf("old-%d/new-%d", index, index),
		}
		if snapshot.Revision != 2 || fmt.Sprint(messageTexts(snapshot.Messages)) != fmt.Sprint(want) {
			t.Errorf("snapshot %d = revision %d, messages %v; want revision 2, messages %v", index, snapshot.Revision, messageTexts(snapshot.Messages), want)
		}
	}
}

func buildSingleTurnStrategyWithSession(t *testing.T, service session.Service) *agent.SingleTurnStrategy {
	t.Helper()
	builder := agent.NewSingleTurnBuilder()
	if err := builder.UseSession(service); err != nil {
		t.Fatalf("UseSession() error = %v", err)
	}
	strategy, err := builder.Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	return strategy
}

func buildToolLoopStrategyWithSession(t *testing.T, tools tool.Service, sessions session.Service, mode agent.ToolErrorMode) *agent.ToolLoopStrategy {
	t.Helper()
	builder := agent.NewToolLoopBuilder()
	if err := builder.UseTools(tools); err != nil {
		t.Fatalf("UseTools() error = %v", err)
	}
	if err := builder.UseSession(sessions); err != nil {
		t.Fatalf("UseSession() error = %v", err)
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

func textMessage(role model.Role, text string) model.Message {
	return model.Message{Role: role, Content: []model.ContentBlock{{Kind: model.ContentText, Text: text}}}
}

func messageTexts(messages []model.Message) []string {
	texts := make([]string, 0, len(messages))
	for _, message := range messages {
		if len(message.Content) == 0 {
			texts = append(texts, "")
			continue
		}
		texts = append(texts, message.Content[0].Text)
	}
	return texts
}

func messageRoles(messages []model.Message) []model.Role {
	roles := make([]model.Role, len(messages))
	for index := range messages {
		roles[index] = messages[index].Role
	}
	return roles
}
