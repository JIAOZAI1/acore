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
)

type fakeLLM struct {
	descriptor model.Model
	generate   func(context.Context, model.Request) (model.Stream, error)
}

func (f *fakeLLM) Model() model.Model { return f.descriptor }

func (f *fakeLLM) Generate(ctx context.Context, req model.Request) (model.Stream, error) {
	return f.generate(ctx, req)
}

type fakeAgent struct {
	run func(context.Context, agent.Request) (agent.Stream, error)
}

func (f *fakeAgent) Run(ctx context.Context, req agent.Request) (agent.Stream, error) {
	return f.run(ctx, req)
}

type fakeRunStrategy struct {
	run func(context.Context, agent.RunInput) (agent.Stream, error)
}

func (f *fakeRunStrategy) Run(ctx context.Context, input agent.RunInput) (agent.Stream, error) {
	return f.run(ctx, input)
}

var _ agent.Agent = (*fakeAgent)(nil)
var _ agent.RunStrategy = (*fakeRunStrategy)(nil)

func TestAgentRunWrapsModelStreamAndBuildsResult(t *testing.T) {
	signature := "provider-signature"
	modelResult := &model.Result{
		Message: model.Message{
			Role: model.RoleAssistant,
			Content: []model.ContentBlock{
				{Kind: model.ContentText, Text: "answer"},
				{Kind: model.ContentToolCall, ToolCall: &model.ToolCall{ID: "call-1", Name: "first", Arguments: json.RawMessage(`{"value":1}`)}},
				{Kind: model.ContentToolCall, ToolCall: &model.ToolCall{ID: "call-2", Name: "second", Arguments: json.RawMessage(`{"value":2}`)}},
			},
		},
		Usage: model.Usage{
			InputTokens:  7,
			OutputTokens: 3,
			TotalTokens:  10,
		},
		StopReason: model.ReasonToolUse,
		ModelID:    "test-model",
		ProviderID: "response-1",
	}
	llm := &fakeLLM{generate: func(context.Context, model.Request) (model.Stream, error) {
		return func(yield func(model.Event, error) bool) {
			if !yield(model.Event{Type: model.EventStart}, nil) {
				return
			}
			if !yield(model.Event{
				Type: model.EventContentStart,
				Block: &model.ContentBlock{
					Kind:      model.ContentThinking,
					Text:      "thinking",
					Signature: &signature,
				},
			}, nil) {
				return
			}
			yield(model.Event{Type: model.EventDone, Result: modelResult}, nil)
		}, nil
	}}
	value := buildAgent(t, llm)

	stream, err := value.Run(context.Background(), agent.Request{Messages: userMessages("question")})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	var events []agent.Event
	for event, streamErr := range stream {
		if streamErr != nil {
			t.Fatalf("stream error = %v", streamErr)
		}
		events = append(events, event)

		if event.Type != agent.EventModel || event.ModelEvent == nil {
			continue
		}
		if event.ModelEvent.Block != nil {
			event.ModelEvent.Block.Text = "mutated block"
			*event.ModelEvent.Block.Signature = "mutated signature"
		}
		if event.ModelEvent.Result != nil {
			event.ModelEvent.Result.Message.Content[0].Text = "mutated done"
			event.ModelEvent.Result.Message.Content[1].ToolCall.Arguments[0] = '['
		}
	}

	wantTypes := []agent.EventType{
		agent.EventRunStart,
		agent.EventModel,
		agent.EventModel,
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
	for index := 1; index <= 3; index++ {
		if events[index].ModelTurn != 1 {
			t.Fatalf("event %d model turn = %d, want 1", index, events[index].ModelTurn)
		}
	}

	result := events[len(events)-1].Result
	if result == nil {
		t.Fatal("RunDone result is nil")
	}
	if result.Output.Content[0].Text != "answer" {
		t.Fatalf("Output text = %q, want answer", result.Output.Content[0].Text)
	}
	if got := string(result.Output.Content[1].ToolCall.Arguments); got != `{"value":1}` {
		t.Fatalf("tool arguments = %s, want original JSON", got)
	}
	if len(result.GeneratedMessages) != 1 || result.GeneratedMessages[0].Content[0].Text != "answer" {
		t.Fatalf("GeneratedMessages = %+v", result.GeneratedMessages)
	}
	if result.StopReason != model.ReasonToolUse || result.ModelID != "test-model" || result.ProviderID != "response-1" {
		t.Fatalf("terminal metadata = %+v", result)
	}
	if result.ModelTurns != 1 || result.ToolCalls != 2 {
		t.Fatalf("counts = turns %d, tool calls %d", result.ModelTurns, result.ToolCalls)
	}
	if result.Usage.TotalTokens != 10 {
		t.Fatalf("usage = %+v", result.Usage)
	}

	result.Output.Content[0].Text = "changed output"
	if result.GeneratedMessages[0].Content[0].Text != "answer" {
		t.Fatal("Output and GeneratedMessages share mutable content")
	}
}

func TestAgentRunRejectsInvalidInputBeforeCallingLLM(t *testing.T) {
	called := false
	llm := &fakeLLM{generate: func(context.Context, model.Request) (model.Stream, error) {
		called = true
		return doneModelStream("unused"), nil
	}}
	value := buildAgent(t, llm)

	tests := []struct {
		name string
		ctx  context.Context
		req  agent.Request
		want error
	}{
		{name: "nil context", ctx: nil, req: agent.Request{Messages: userMessages("hello")}, want: agent.ErrInvalidRequest},
		{name: "empty messages", ctx: context.Background(), req: agent.Request{}, want: agent.ErrInvalidRequest},
		{
			name: "invalid run options",
			ctx:  context.Background(),
			req: agent.Request{
				Messages: userMessages("hello"),
				Options:  agent.ModelOptions{MaxTokens: intPointer(0)},
			},
			want: agent.ErrInvalidOptions,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := value.Run(test.ctx, test.req)
			if !errors.Is(err, test.want) {
				t.Fatalf("Run() error = %v, want %v", err, test.want)
			}
		})
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := value.Run(ctx, agent.Request{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run(canceled) error = %v, want context.Canceled", err)
	}
	if called {
		t.Fatal("LLM was called for invalid input")
	}
}

func TestAgentRunReturnsModelSetupErrors(t *testing.T) {
	want := errors.New("provider unavailable")
	value := buildAgent(t, &fakeLLM{generate: func(context.Context, model.Request) (model.Stream, error) {
		return nil, want
	}})
	_, err := value.Run(context.Background(), agent.Request{Messages: userMessages("hello")})
	if !errors.Is(err, want) {
		t.Fatalf("Run() error = %v, want %v", err, want)
	}

	value = buildAgent(t, &fakeLLM{generate: func(context.Context, model.Request) (model.Stream, error) {
		return nil, nil
	}})
	_, err = value.Run(context.Background(), agent.Request{Messages: userMessages("hello")})
	if !errors.Is(err, agent.ErrUnexpectedModelStreamEnd) {
		t.Fatalf("Run() nil stream error = %v", err)
	}
}

func TestAgentStreamReportsProtocolAndRuntimeErrors(t *testing.T) {
	wantStreamError := errors.New("connection reset")
	tests := []struct {
		name   string
		stream model.Stream
		want   error
	}{
		{
			name: "stream error",
			stream: func(yield func(model.Event, error) bool) {
				yield(model.Event{}, wantStreamError)
			},
			want: wantStreamError,
		},
		{
			name:   "silent stream",
			stream: func(func(model.Event, error) bool) {},
			want:   agent.ErrUnexpectedModelStreamEnd,
		},
		{
			name: "done without result",
			stream: func(yield func(model.Event, error) bool) {
				yield(model.Event{Type: model.EventDone}, nil)
			},
			want: agent.ErrInvalidModelDoneEvent,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := buildAgent(t, &fakeLLM{generate: func(context.Context, model.Request) (model.Stream, error) {
				return test.stream, nil
			}})
			stream, err := value.Run(context.Background(), agent.Request{Messages: userMessages("hello")})
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			_, err = collect(stream)
			if !errors.Is(err, test.want) {
				t.Fatalf("stream error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestAgentStreamPrefersContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	value := buildAgent(t, &fakeLLM{generate: func(context.Context, model.Request) (model.Stream, error) {
		return func(yield func(model.Event, error) bool) {
			if !yield(model.Event{Type: model.EventStart}, nil) {
				return
			}
			cancel()
			yield(model.Event{Type: model.EventDone, Result: modelResult("late")}, nil)
		}, nil
	}})
	stream, err := value.Run(ctx, agent.Request{Messages: userMessages("hello")})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	_, err = collect(stream)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("stream error = %v, want context.Canceled", err)
	}
}

func TestAgentStreamEarlyStopReleasesModelGenerator(t *testing.T) {
	released := make(chan struct{})
	value := buildAgent(t, &fakeLLM{generate: func(context.Context, model.Request) (model.Stream, error) {
		return func(yield func(model.Event, error) bool) {
			defer close(released)
			if !yield(model.Event{Type: model.EventStart}, nil) {
				return
			}
			yield(model.Event{Type: model.EventDone, Result: modelResult("unused")}, nil)
		}, nil
	}})
	stream, err := value.Run(context.Background(), agent.Request{Messages: userMessages("hello")})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	for event, streamErr := range stream {
		if streamErr != nil {
			t.Fatalf("stream error = %v", streamErr)
		}
		if event.Type == agent.EventModel {
			break
		}
	}
	select {
	case <-released:
	default:
		t.Fatal("model generator defer did not run after early stop")
	}
}

func TestAgentStreamEarlyStopAtRunStartReleasesModelGenerator(t *testing.T) {
	released := make(chan struct{})
	modelContextCanceled := false
	value := buildAgent(t, &fakeLLM{generate: func(ctx context.Context, _ model.Request) (model.Stream, error) {
		return func(yield func(model.Event, error) bool) {
			defer close(released)
			modelContextCanceled = errors.Is(ctx.Err(), context.Canceled)
			yield(model.Event{Type: model.EventStart}, nil)
		}, nil
	}})
	stream, err := value.Run(context.Background(), agent.Request{Messages: userMessages("hello")})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	for event, streamErr := range stream {
		if streamErr != nil {
			t.Fatalf("stream error = %v", streamErr)
		}
		if event.Type == agent.EventRunStart {
			break
		}
	}
	select {
	case <-released:
	default:
		t.Fatal("unstarted model generator defer did not run after early stop")
	}
	if !modelContextCanceled {
		t.Fatal("model context was not canceled before releasing unstarted generator")
	}
}

func TestCompleteHandlesAgentContract(t *testing.T) {
	result := &agent.Result{
		Output: model.Message{
			Role:    model.RoleAssistant,
			Content: []model.ContentBlock{{Kind: model.ContentText, Text: "complete"}},
		},
	}
	success := &fakeAgent{run: func(context.Context, agent.Request) (agent.Stream, error) {
		return func(yield func(agent.Event, error) bool) {
			yield(agent.Event{Type: agent.EventRunDone, Result: result}, nil)
		}, nil
	}}
	got, err := agent.Complete(context.Background(), success, agent.Request{})
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	result.Output.Content[0].Text = "mutated source"
	if got.Output.Content[0].Text != "complete" {
		t.Fatal("Complete() returned shared result state")
	}

	var typedNil *fakeAgent
	tests := []struct {
		name  string
		value agent.Agent
		want  error
	}{
		{name: "nil agent", value: nil, want: agent.ErrNilAgent},
		{name: "typed nil agent", value: typedNil, want: agent.ErrNilAgent},
		{
			name: "nil stream",
			value: &fakeAgent{run: func(context.Context, agent.Request) (agent.Stream, error) {
				return nil, nil
			}},
			want: agent.ErrUnexpectedStreamEnd,
		},
		{
			name: "silent stream",
			value: &fakeAgent{run: func(context.Context, agent.Request) (agent.Stream, error) {
				return func(func(agent.Event, error) bool) {}, nil
			}},
			want: agent.ErrUnexpectedStreamEnd,
		},
		{
			name: "done without result",
			value: &fakeAgent{run: func(context.Context, agent.Request) (agent.Stream, error) {
				return func(yield func(agent.Event, error) bool) {
					yield(agent.Event{Type: agent.EventRunDone}, nil)
				}, nil
			}},
			want: agent.ErrInvalidDoneEvent,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := agent.Complete(context.Background(), test.value, agent.Request{})
			if !errors.Is(err, test.want) {
				t.Fatalf("Complete() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestCompletePropagatesRunStreamAndContextErrors(t *testing.T) {
	wantRunError := errors.New("run failed")
	runFailure := &fakeAgent{run: func(context.Context, agent.Request) (agent.Stream, error) {
		return nil, wantRunError
	}}
	if _, err := agent.Complete(context.Background(), runFailure, agent.Request{}); !errors.Is(err, wantRunError) {
		t.Fatalf("Complete() run error = %v, want %v", err, wantRunError)
	}

	wantStreamError := errors.New("stream failed")
	streamFailure := &fakeAgent{run: func(context.Context, agent.Request) (agent.Stream, error) {
		return func(yield func(agent.Event, error) bool) {
			yield(agent.Event{}, wantStreamError)
		}, nil
	}}
	if _, err := agent.Complete(context.Background(), streamFailure, agent.Request{}); !errors.Is(err, wantStreamError) {
		t.Fatalf("Complete() stream error = %v, want %v", err, wantStreamError)
	}

	if _, err := agent.Complete(nil, streamFailure, agent.Request{}); !errors.Is(err, agent.ErrInvalidRequest) {
		t.Fatalf("Complete(nil context) error = %v, want ErrInvalidRequest", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := agent.Complete(ctx, streamFailure, agent.Request{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Complete(canceled context) error = %v, want context.Canceled", err)
	}
}

func TestConfiguredAgentProtectsStrategyContract(t *testing.T) {
	wantRunError := errors.New("strategy setup failed")
	wantStreamError := errors.New("strategy stream failed")
	tests := []struct {
		name       string
		strategy   agent.RunStrategy
		directWant error
		streamWant error
	}{
		{
			name: "setup error",
			strategy: &fakeRunStrategy{run: func(context.Context, agent.RunInput) (agent.Stream, error) {
				return nil, wantRunError
			}},
			directWant: wantRunError,
		},
		{
			name: "nil stream",
			strategy: &fakeRunStrategy{run: func(context.Context, agent.RunInput) (agent.Stream, error) {
				return nil, nil
			}},
			directWant: agent.ErrUnexpectedStreamEnd,
		},
		{
			name: "stream error",
			strategy: &fakeRunStrategy{run: func(context.Context, agent.RunInput) (agent.Stream, error) {
				return func(yield func(agent.Event, error) bool) {
					yield(agent.Event{}, wantStreamError)
				}, nil
			}},
			streamWant: wantStreamError,
		},
		{
			name: "silent stream",
			strategy: &fakeRunStrategy{run: func(context.Context, agent.RunInput) (agent.Stream, error) {
				return func(func(agent.Event, error) bool) {}, nil
			}},
			streamWant: agent.ErrUnexpectedStreamEnd,
		},
		{
			name: "done without result",
			strategy: &fakeRunStrategy{run: func(context.Context, agent.RunInput) (agent.Stream, error) {
				return func(yield func(agent.Event, error) bool) {
					yield(agent.Event{Type: agent.EventRunDone}, nil)
				}, nil
			}},
			streamWant: agent.ErrInvalidDoneEvent,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := buildAgentWithStrategy(t, test.strategy)
			stream, err := value.Run(context.Background(), agent.Request{Messages: userMessages("hello")})
			if test.directWant != nil {
				if !errors.Is(err, test.directWant) {
					t.Fatalf("Run() error = %v, want %v", err, test.directWant)
				}
				return
			}
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			_, err = collect(stream)
			if !errors.Is(err, test.streamWant) {
				t.Fatalf("stream error = %v, want %v", err, test.streamWant)
			}
		})
	}
}

func TestConfiguredAgentPrefersContextErrorAfterStrategySetup(t *testing.T) {
	wantStrategyError := errors.New("late strategy failure")
	ctx, cancel := context.WithCancel(context.Background())
	strategy := &fakeRunStrategy{run: func(context.Context, agent.RunInput) (agent.Stream, error) {
		cancel()
		return nil, wantStrategyError
	}}
	value := buildAgentWithStrategy(t, strategy)
	_, err := value.Run(ctx, agent.Request{Messages: userMessages("hello")})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error = %v, want context.Canceled", err)
	}
}

func TestConfiguredAgentSnapshotsStrategyEvents(t *testing.T) {
	sourceResult := &agent.Result{
		Output: model.Message{
			Role:    model.RoleAssistant,
			Content: []model.ContentBlock{{Kind: model.ContentText, Text: "source"}},
		},
		GeneratedMessages: []model.Message{{
			Role:    model.RoleAssistant,
			Content: []model.ContentBlock{{Kind: model.ContentText, Text: "source"}},
		}},
	}
	strategy := &fakeRunStrategy{run: func(context.Context, agent.RunInput) (agent.Stream, error) {
		return func(yield func(agent.Event, error) bool) {
			if !yield(agent.Event{Type: agent.EventRunStart}, nil) {
				return
			}
			yield(agent.Event{Type: agent.EventRunDone, Result: sourceResult}, nil)
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
	result := events[len(events)-1].Result
	result.Output.Content[0].Text = "mutated output"
	result.GeneratedMessages[0].Content[0].Text = "mutated generated"
	if sourceResult.Output.Content[0].Text != "source" || sourceResult.GeneratedMessages[0].Content[0].Text != "source" {
		t.Fatal("configured Agent exposed strategy result state")
	}
}

func TestConfiguredAgentEarlyStopReleasesStrategyGenerator(t *testing.T) {
	released := make(chan struct{})
	strategy := &fakeRunStrategy{run: func(context.Context, agent.RunInput) (agent.Stream, error) {
		return func(yield func(agent.Event, error) bool) {
			defer close(released)
			if !yield(agent.Event{Type: agent.EventRunStart}, nil) {
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
	for range stream {
		break
	}
	select {
	case <-released:
	default:
		t.Fatal("strategy generator defer did not run after early stop")
	}
}

func TestAgentSupportsConcurrentRuns(t *testing.T) {
	llm := &fakeLLM{generate: func(_ context.Context, req model.Request) (model.Stream, error) {
		text := req.Context.Messages[0].Content[0].Text
		return doneModelStream(text), nil
	}}
	value := buildAgent(t, llm)

	const runs = 32
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

func TestEventTypeString(t *testing.T) {
	tests := []struct {
		value agent.EventType
		want  string
	}{
		{value: agent.EventUnknown, want: "unknown"},
		{value: agent.EventRunStart, want: "runStart"},
		{value: agent.EventModel, want: "model"},
		{value: agent.EventRunDone, want: "runDone"},
		{value: agent.EventToolStart, want: "toolStart"},
		{value: agent.EventToolDone, want: "toolDone"},
		{value: agent.EventType(255), want: "unknown"},
	}
	for _, test := range tests {
		if got := test.value.String(); got != test.want {
			t.Errorf("EventType(%d).String() = %q, want %q", test.value, got, test.want)
		}
	}
}

func buildAgent(t *testing.T, llm model.LLM) agent.Agent {
	t.Helper()
	builder := agent.NewBuilder()
	if err := builder.UseLLM(llm); err != nil {
		t.Fatalf("UseLLM() error = %v", err)
	}
	if err := builder.UseRunStrategy(agent.NewSingleTurnStrategy()); err != nil {
		t.Fatalf("UseRunStrategy() error = %v", err)
	}
	value, err := builder.Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	return value
}

func buildAgentWithStrategy(t *testing.T, strategy agent.RunStrategy) agent.Agent {
	t.Helper()
	llm := &fakeLLM{generate: func(context.Context, model.Request) (model.Stream, error) {
		t.Fatal("strategy unexpectedly called LLM")
		return nil, nil
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
	return value
}

func userMessages(text string) []model.Message {
	return []model.Message{{
		Role:    model.RoleUser,
		Content: []model.ContentBlock{{Kind: model.ContentText, Text: text}},
	}}
}

func modelResult(text string) *model.Result {
	return &model.Result{
		Message: model.Message{
			Role:    model.RoleAssistant,
			Content: []model.ContentBlock{{Kind: model.ContentText, Text: text}},
		},
		StopReason: model.ReasonStop,
	}
}

func doneModelStream(text string) model.Stream {
	return func(yield func(model.Event, error) bool) {
		yield(model.Event{Type: model.EventDone, Result: modelResult(text)}, nil)
	}
}

func collect(stream agent.Stream) ([]agent.Event, error) {
	var events []agent.Event
	for event, err := range stream {
		if err != nil {
			return events, err
		}
		events = append(events, event)
	}
	return events, nil
}

func intPointer(value int) *int {
	return &value
}

func successfulAgentStream(text string) agent.Stream {
	return func(yield func(agent.Event, error) bool) {
		if !yield(agent.Event{Type: agent.EventRunStart}, nil) {
			return
		}
		yield(agent.Event{
			Type: agent.EventRunDone,
			Result: &agent.Result{
				Output: model.Message{
					Role:    model.RoleAssistant,
					Content: []model.ContentBlock{{Kind: model.ContentText, Text: text}},
				},
			},
		}, nil)
	}
}
