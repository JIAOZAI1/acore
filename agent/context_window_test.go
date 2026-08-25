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
	"github.com/JIAOZAI1/acore/contextwindow"
	"github.com/JIAOZAI1/acore/model"
	"github.com/JIAOZAI1/acore/session"
	"github.com/JIAOZAI1/acore/tool"
)

type fakeContextReducer struct {
	reduce func(context.Context, contextwindow.Input) (contextwindow.Result, error)
}

func (f *fakeContextReducer) Reduce(ctx context.Context, input contextwindow.Input) (contextwindow.Result, error) {
	return f.reduce(ctx, input)
}

func TestStrategyBuildersConfigureContextWindowAndFreeze(t *testing.T) {
	reducer := &fakeContextReducer{reduce: func(context.Context, contextwindow.Input) (contextwindow.Result, error) {
		return contextwindow.Result{}, nil
	}}

	t.Run("single turn", func(t *testing.T) {
		builder := agent.NewSingleTurnBuilder()
		if err := builder.UseContextWindow(nil); !errors.Is(err, agent.ErrNilContextWindowReducer) {
			t.Fatalf("UseContextWindow(nil) error = %v, want ErrNilContextWindowReducer", err)
		}
		var typedNil *fakeContextReducer
		if err := builder.UseContextWindow(typedNil); !errors.Is(err, agent.ErrNilContextWindowReducer) {
			t.Fatalf("UseContextWindow(typed nil) error = %v, want ErrNilContextWindowReducer", err)
		}
		if err := builder.UseContextWindow(reducer); err != nil {
			t.Fatalf("UseContextWindow() after failures error = %v", err)
		}
		if err := builder.UseContextWindow(reducer); !errors.Is(err, agent.ErrContextWindowAlreadySet) {
			t.Fatalf("second UseContextWindow() error = %v, want ErrContextWindowAlreadySet", err)
		}
		if _, err := builder.Build(); err != nil {
			t.Fatalf("Build() error = %v", err)
		}
		if err := builder.UseContextWindow(reducer); !errors.Is(err, agent.ErrSingleTurnBuilderBuilt) {
			t.Fatalf("UseContextWindow() after Build error = %v, want ErrSingleTurnBuilderBuilt", err)
		}
	})

	t.Run("tool loop", func(t *testing.T) {
		builder := agent.NewToolLoopBuilder()
		if err := builder.UseContextWindow(nil); !errors.Is(err, agent.ErrNilContextWindowReducer) {
			t.Fatalf("UseContextWindow(nil) error = %v, want ErrNilContextWindowReducer", err)
		}
		var typedNil *fakeContextReducer
		if err := builder.UseContextWindow(typedNil); !errors.Is(err, agent.ErrNilContextWindowReducer) {
			t.Fatalf("UseContextWindow(typed nil) error = %v, want ErrNilContextWindowReducer", err)
		}
		if err := builder.UseContextWindow(reducer); err != nil {
			t.Fatalf("UseContextWindow() after failures error = %v", err)
		}
		if err := builder.UseContextWindow(reducer); !errors.Is(err, agent.ErrContextWindowAlreadySet) {
			t.Fatalf("second UseContextWindow() error = %v, want ErrContextWindowAlreadySet", err)
		}
		if err := builder.UseTools(&fakeToolService{}); err != nil {
			t.Fatalf("UseTools() error = %v", err)
		}
		if _, err := builder.Build(); err != nil {
			t.Fatalf("Build() error = %v", err)
		}
		if err := builder.UseContextWindow(reducer); !errors.Is(err, agent.ErrToolLoopBuilderBuilt) {
			t.Fatalf("UseContextWindow() after Build error = %v, want ErrToolLoopBuilderBuilt", err)
		}
	})
}

func TestSingleTurnContextWindowUsesNormalizedInput(t *testing.T) {
	maxTokens := 40
	messages := []model.Message{
		textMessage(model.RoleUser, "old question"),
		textMessage(model.RoleAssistant, "old answer"),
		textMessage(model.RoleUser, "current question"),
	}
	reducerCalls := 0
	reducer := &fakeContextReducer{reduce: func(_ context.Context, input contextwindow.Input) (contextwindow.Result, error) {
		reducerCalls++
		if input.Model.ID != "window-model" || input.Model.ContextWindow != 100 || input.RequestedOutputTokens != 40 {
			t.Fatalf("Reducer model/output input = %+v/%d", input.Model, input.RequestedOutputTokens)
		}
		if input.Context.SystemPrompt != "system prompt" || len(input.Context.Messages) != 3 {
			t.Fatalf("Reducer context = %+v", input.Context)
		}
		if input.ProtectedMessages != 1 {
			t.Fatalf("Reducer protected messages = %d, want 1", input.ProtectedMessages)
		}
		input.Context.Messages[2].Content[0].Text = "mutated by reducer"
		return contextwindow.Result{MessageStart: 2}, nil
	}}
	var request model.Request
	llm := &fakeLLM{
		descriptor: model.Model{ID: "window-model", ContextWindow: 100, MaxOutputTokens: 50},
		generate: func(_ context.Context, got model.Request) (model.Stream, error) {
			request = got
			return doneModelStream("answer"), nil
		},
	}
	strategy := buildSingleTurnStrategyWithContextWindow(t, nil, reducer)
	builder := agent.NewBuilder()
	if err := builder.UseLLM(llm); err != nil {
		t.Fatalf("UseLLM() error = %v", err)
	}
	if err := builder.UseRunStrategy(strategy); err != nil {
		t.Fatalf("UseRunStrategy() error = %v", err)
	}
	if err := builder.SetSystemPrompt("system prompt"); err != nil {
		t.Fatalf("SetSystemPrompt() error = %v", err)
	}
	if err := builder.SetModelOptions(agent.ModelOptions{MaxTokens: &maxTokens}); err != nil {
		t.Fatalf("SetModelOptions() error = %v", err)
	}
	value, err := builder.Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	stream, err := value.Run(context.Background(), agent.Request{Messages: messages})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if _, err := collect(stream); err != nil {
		t.Fatalf("stream error = %v", err)
	}
	if reducerCalls != 1 {
		t.Fatalf("Reducer calls = %d, want 1", reducerCalls)
	}
	if len(request.Context.Messages) != 1 || request.Context.Messages[0].Content[0].Text != "current question" {
		t.Fatalf("model messages = %+v, want isolated current message", request.Context.Messages)
	}
	if request.Context.SystemPrompt != "system prompt" || request.MaxTokens == nil || *request.MaxTokens != 40 {
		t.Fatalf("model request = %+v", request)
	}
	if messages[2].Content[0].Text != "current question" {
		t.Fatal("Reducer modified complete working messages")
	}
}

func TestSingleTurnContextWindowKeepsSessionHistoryComplete(t *testing.T) {
	key := session.Key{Scope: "tenant", ID: "conversation"}
	history := []model.Message{
		textMessage(model.RoleUser, "old question"),
		textMessage(model.RoleAssistant, "old answer"),
	}
	var appended []model.Message
	service := &fakeSessionService{
		loadFn: func(context.Context, session.Key) (session.Snapshot, error) {
			return session.Snapshot{Revision: 3, Messages: history}, nil
		},
		appendFn: func(_ context.Context, _ session.Key, expected session.Revision, messages []model.Message) (session.Revision, error) {
			if expected != 3 {
				t.Fatalf("Append() revision = %d, want 3", expected)
			}
			appended = messages
			return 4, nil
		},
	}
	reducer := &fakeContextReducer{reduce: func(_ context.Context, input contextwindow.Input) (contextwindow.Result, error) {
		if len(input.Context.Messages) != 3 || input.ProtectedMessages != 1 {
			t.Fatalf("Reducer messages/protected = %d/%d, want 3/1", len(input.Context.Messages), input.ProtectedMessages)
		}
		return contextwindow.Result{MessageStart: 2}, nil
	}}
	strategy := buildSingleTurnStrategyWithContextWindow(t, service, reducer)
	var modelMessages []model.Message
	llm := &fakeLLM{generate: func(_ context.Context, request model.Request) (model.Stream, error) {
		modelMessages = request.Context.Messages
		return doneModelStream("new answer"), nil
	}}

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
	if _, err := collect(stream); err != nil {
		t.Fatalf("stream error = %v", err)
	}
	if got := fmt.Sprint(messageTexts(modelMessages)); got != fmt.Sprint([]string{"new question"}) {
		t.Fatalf("model messages = %v, want only new question", messageTexts(modelMessages))
	}
	if got := fmt.Sprint(messageTexts(appended)); got != fmt.Sprint([]string{"new question", "new answer"}) {
		t.Fatalf("appended messages = %v, want complete run delta", messageTexts(appended))
	}
	if got := fmt.Sprint(messageTexts(history)); got != fmt.Sprint([]string{"old question", "old answer"}) {
		t.Fatalf("loaded history was modified: %v", messageTexts(history))
	}
}

func TestToolLoopContextWindowRunsBeforeEveryModelTurn(t *testing.T) {
	service := &fakeToolService{
		specs: []tool.Spec{{Name: "lookup", Parameters: json.RawMessage(`{"type":"object"}`)}},
		execute: func(context.Context, tool.Call) (tool.Result, error) {
			return tool.Result{Content: "tool result"}, nil
		},
	}
	var reducerCalls int
	var reducerMessageCounts []int
	var protectedCounts []int
	reducer := &fakeContextReducer{reduce: func(_ context.Context, input contextwindow.Input) (contextwindow.Result, error) {
		reducerCalls++
		reducerMessageCounts = append(reducerMessageCounts, len(input.Context.Messages))
		protectedCounts = append(protectedCounts, input.ProtectedMessages)
		if input.Context.SystemPrompt != "system" || len(input.Context.Tools) != 1 || input.Context.Tools[0].Name != "lookup" {
			t.Fatalf("Reducer fixed context = %+v", input.Context)
		}
		if input.RequestedOutputTokens != 20 {
			t.Fatalf("Reducer requested output = %d, want 20", input.RequestedOutputTokens)
		}
		return contextwindow.Result{MessageStart: 2}, nil
	}}
	strategy := buildToolLoopStrategyWithContextWindow(t, service, nil, reducer)
	turn := 0
	var requests []model.Request
	llm := &fakeLLM{
		descriptor: model.Model{ID: "tool-model", ContextWindow: 100},
		generate: func(_ context.Context, request model.Request) (model.Stream, error) {
			turn++
			requests = append(requests, request)
			if turn == 1 {
				return doneResultStream(&model.Result{
					Message:    assistantToolCalls(model.ToolCall{ID: "call-1", Name: "lookup", Arguments: json.RawMessage(`{}`)}),
					StopReason: model.ReasonToolUse,
				}), nil
			}
			return doneModelStream("final"), nil
		},
	}
	maxTokens := 20
	stream, err := strategy.Run(context.Background(), agent.RunInput{
		LLM:          llm,
		SystemPrompt: "system",
		Request: agent.Request{
			Messages: []model.Message{
				textMessage(model.RoleUser, "old question"),
				textMessage(model.RoleAssistant, "old answer"),
				textMessage(model.RoleUser, "current question"),
			},
			Options: agent.ModelOptions{MaxTokens: &maxTokens},
		},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if _, err := collect(stream); err != nil {
		t.Fatalf("stream error = %v", err)
	}
	if reducerCalls != 2 || fmt.Sprint(reducerMessageCounts) != fmt.Sprint([]int{3, 5}) {
		t.Fatalf("Reducer calls/message counts = %d/%v, want 2/[3 5]", reducerCalls, reducerMessageCounts)
	}
	if fmt.Sprint(protectedCounts) != fmt.Sprint([]int{1, 3}) {
		t.Fatalf("Reducer protected counts = %v, want [1 3]", protectedCounts)
	}
	if len(requests) != 2 || len(requests[0].Context.Messages) != 1 || len(requests[1].Context.Messages) != 3 {
		t.Fatalf("model request message counts = %d/%d", len(requests[0].Context.Messages), len(requests[1].Context.Messages))
	}
	wantRoles := []model.Role{model.RoleUser, model.RoleAssistant, model.RoleTool}
	if fmt.Sprint(messageRoles(requests[1].Context.Messages)) != fmt.Sprint(wantRoles) {
		t.Fatalf("second request roles = %v, want %v", messageRoles(requests[1].Context.Messages), wantRoles)
	}
}

func TestContextWindowErrorsPreserveRunAndStreamSemantics(t *testing.T) {
	want := errors.New("reducer unavailable")

	t.Run("first turn failure", func(t *testing.T) {
		calls := 0
		reducer := &fakeContextReducer{reduce: func(context.Context, contextwindow.Input) (contextwindow.Result, error) {
			return contextwindow.Result{}, want
		}}
		strategy := buildSingleTurnStrategyWithContextWindow(t, nil, reducer)
		llm := &fakeLLM{generate: func(context.Context, model.Request) (model.Stream, error) {
			calls++
			return doneModelStream("unused"), nil
		}}
		_, err := strategy.Run(context.Background(), agent.RunInput{LLM: llm, Request: agent.Request{Messages: userMessages("current")}})
		if !errors.Is(err, agent.ErrReduceContextWindow) || !errors.Is(err, want) {
			t.Fatalf("Run() error = %v, want ErrReduceContextWindow and original", err)
		}
		if calls != 0 {
			t.Fatalf("LLM calls = %d, want 0", calls)
		}
	})

	t.Run("invalid reducer result", func(t *testing.T) {
		reducer := &fakeContextReducer{reduce: func(context.Context, contextwindow.Input) (contextwindow.Result, error) {
			return contextwindow.Result{MessageStart: 1}, nil
		}}
		strategy := buildSingleTurnStrategyWithContextWindow(t, nil, reducer)
		_, err := strategy.Run(context.Background(), agent.RunInput{
			LLM: &fakeLLM{generate: func(context.Context, model.Request) (model.Stream, error) {
				t.Fatal("LLM called for invalid reducer result")
				return nil, nil
			}},
			Request: agent.Request{Messages: []model.Message{
				textMessage(model.RoleUser, "old"),
				textMessage(model.RoleAssistant, "current"),
			}},
		})
		if !errors.Is(err, agent.ErrReduceContextWindow) || !errors.Is(err, contextwindow.ErrInvalidResult) {
			t.Fatalf("Run() error = %v, want ErrReduceContextWindow and ErrInvalidResult", err)
		}
	})

	t.Run("later tool loop failure does not commit", func(t *testing.T) {
		key := session.Key{Scope: "tenant", ID: "conversation"}
		sessions := &fakeSessionService{}
		tools := &fakeToolService{execute: func(context.Context, tool.Call) (tool.Result, error) {
			return tool.Result{Content: "side effect completed"}, nil
		}}
		reducerCalls := 0
		reducer := &fakeContextReducer{reduce: func(context.Context, contextwindow.Input) (contextwindow.Result, error) {
			reducerCalls++
			if reducerCalls == 1 {
				return contextwindow.Result{}, nil
			}
			return contextwindow.Result{}, want
		}}
		strategy := buildToolLoopStrategyWithContextWindow(t, tools, sessions, reducer)
		modelCalls := 0
		llm := &fakeLLM{generate: func(context.Context, model.Request) (model.Stream, error) {
			modelCalls++
			return doneResultStream(&model.Result{
				Message:    assistantToolCalls(model.ToolCall{ID: "call", Name: "write", Arguments: json.RawMessage(`{}`)}),
				StopReason: model.ReasonToolUse,
			}), nil
		}}
		stream, err := strategy.Run(context.Background(), agent.RunInput{
			LLM: llm,
			Request: agent.Request{Session: &agent.SessionInput{
				Key:      key,
				Messages: userMessages("write"),
			}},
		})
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
		events, err := collect(stream)
		if !errors.Is(err, agent.ErrReduceContextWindow) || !errors.Is(err, want) {
			t.Fatalf("stream error = %v, want ErrReduceContextWindow and original", err)
		}
		if modelCalls != 1 || reducerCalls != 2 || sessions.appendCalls.Load() != 0 {
			t.Fatalf("calls = model %d, reducer %d, Append %d; want 1/2/0", modelCalls, reducerCalls, sessions.appendCalls.Load())
		}
		for _, event := range events {
			if event.Type == agent.EventRunDone {
				t.Fatal("later reducer failure emitted RunDone")
			}
		}
	})

	t.Run("context cancellation takes priority", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		reducer := &fakeContextReducer{reduce: func(context.Context, contextwindow.Input) (contextwindow.Result, error) {
			cancel()
			return contextwindow.Result{}, want
		}}
		strategy := buildSingleTurnStrategyWithContextWindow(t, nil, reducer)
		_, err := strategy.Run(ctx, agent.RunInput{
			LLM: &fakeLLM{generate: func(context.Context, model.Request) (model.Stream, error) {
				t.Fatal("LLM called after context cancellation")
				return nil, nil
			}},
			Request: agent.Request{Messages: userMessages("current")},
		})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run() error = %v, want context.Canceled", err)
		}
	})
}

func TestContextWindowReducerSupportsConcurrentRuns(t *testing.T) {
	var calls atomic.Int64
	reducer := contextwindow.ReducerFunc(func(_ context.Context, input contextwindow.Input) (contextwindow.Result, error) {
		calls.Add(1)
		if input.ProtectedMessages != 1 {
			return contextwindow.Result{}, fmt.Errorf("protected messages = %d", input.ProtectedMessages)
		}
		return contextwindow.Result{MessageStart: 2}, nil
	})
	strategy := buildSingleTurnStrategyWithContextWindow(t, nil, reducer)
	llm := &fakeLLM{generate: func(_ context.Context, request model.Request) (model.Stream, error) {
		if len(request.Context.Messages) != 1 || request.Context.Messages[0].Role != model.RoleUser {
			return nil, fmt.Errorf("reduced messages = %+v", request.Context.Messages)
		}
		return doneModelStream(request.Context.Messages[0].Content[0].Text), nil
	}}

	const runs = 16
	errorsByRun := make(chan error, runs)
	var wait sync.WaitGroup
	for index := 0; index < runs; index++ {
		index := index
		wait.Add(1)
		go func() {
			defer wait.Done()
			current := fmt.Sprintf("current-%d", index)
			stream, err := strategy.Run(context.Background(), agent.RunInput{
				LLM: llm,
				Request: agent.Request{Messages: []model.Message{
					textMessage(model.RoleUser, "old"),
					textMessage(model.RoleAssistant, "answer"),
					textMessage(model.RoleUser, current),
				}},
			})
			if err != nil {
				errorsByRun <- err
				return
			}
			result, err := agent.Complete(context.Background(), &streamAgent{stream: stream}, agent.Request{Messages: userMessages("ignored")})
			if err != nil {
				errorsByRun <- err
				return
			}
			if result.Output.Content[0].Text != current {
				errorsByRun <- fmt.Errorf("output = %q, want %q", result.Output.Content[0].Text, current)
			}
		}()
	}
	wait.Wait()
	close(errorsByRun)
	for err := range errorsByRun {
		t.Error(err)
	}
	if calls.Load() != runs {
		t.Fatalf("Reducer calls = %d, want %d", calls.Load(), runs)
	}
}

type streamAgent struct {
	stream agent.Stream
}

func (s *streamAgent) Run(context.Context, agent.Request) (agent.Stream, error) {
	return s.stream, nil
}

func buildSingleTurnStrategyWithContextWindow(
	t *testing.T,
	sessions session.Service,
	reducer contextwindow.Reducer,
) *agent.SingleTurnStrategy {
	t.Helper()
	builder := agent.NewSingleTurnBuilder()
	if sessions != nil {
		if err := builder.UseSession(sessions); err != nil {
			t.Fatalf("UseSession() error = %v", err)
		}
	}
	if err := builder.UseContextWindow(reducer); err != nil {
		t.Fatalf("UseContextWindow() error = %v", err)
	}
	strategy, err := builder.Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	return strategy
}

func buildToolLoopStrategyWithContextWindow(
	t *testing.T,
	tools tool.Service,
	sessions session.Service,
	reducer contextwindow.Reducer,
) *agent.ToolLoopStrategy {
	t.Helper()
	builder := agent.NewToolLoopBuilder()
	if err := builder.UseTools(tools); err != nil {
		t.Fatalf("UseTools() error = %v", err)
	}
	if sessions != nil {
		if err := builder.UseSession(sessions); err != nil {
			t.Fatalf("UseSession() error = %v", err)
		}
	}
	if err := builder.UseContextWindow(reducer); err != nil {
		t.Fatalf("UseContextWindow() error = %v", err)
	}
	strategy, err := builder.Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	return strategy
}

var _ contextwindow.Reducer = (*fakeContextReducer)(nil)
