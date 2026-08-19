package looper_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/JIAOZAI1/acore/event"
	"github.com/JIAOZAI1/acore/looper"
	"github.com/JIAOZAI1/acore/model"
	acruntime "github.com/JIAOZAI1/acore/runtime"
	"github.com/JIAOZAI1/acore/tool"
)

const (
	testProviderID = "fake"
	testModelID    = "test"
)

type fakeProvider struct {
	generate func(context.Context, model.Request) (model.Stream, error)
}

func (*fakeProvider) ID() string { return testProviderID }

func (*fakeProvider) Models() []model.Model {
	return []model.Model{{ID: testModelID, Provider: testProviderID}}
}

func (f *fakeProvider) Generate(ctx context.Context, _ model.Model, request model.Request) (model.Stream, error) {
	return f.generate(ctx, request)
}

type customEvent struct {
	turn int
}

func (customEvent) Name() string { return "test.custom" }

func doneStream(text string) model.Stream {
	return func(yield func(model.Event, error) bool) {
		if !yield(model.Event{Type: model.EventContentDelta, Delta: text}, nil) {
			return
		}
		yield(model.Event{Type: model.EventDone, Result: &model.Result{
			Message:    model.Message{Role: model.RoleAssistant},
			StopReason: model.ReasonStop,
		}}, nil)
	}
}

func newRuntime(t *testing.T, bus *event.Bus, provider model.Provider) *acruntime.Runtime {
	t.Helper()
	builder := acruntime.NewBuilder()
	if err := builder.AddProvider(provider); err != nil {
		t.Fatalf("AddProvider() error = %v", err)
	}
	if err := builder.UseEvents(bus); err != nil {
		t.Fatalf("UseEvents() error = %v", err)
	}
	tools, err := tool.NewBuilder().Build()
	if err != nil {
		t.Fatalf("build tools: %v", err)
	}
	if err := builder.UseTools(tools); err != nil {
		t.Fatalf("UseTools() error = %v", err)
	}
	runtime, err := builder.Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	return runtime
}

func input() looper.Input {
	return looper.Input{ProviderID: testProviderID, ModelID: testModelID}
}

func TestSingleTurnLoopStreamsModelEvents(t *testing.T) {
	bus := event.NewBus()
	var received []model.EventType
	_, err := event.Subscribe(bus, func(_ context.Context, notification looper.ModelEvent) error {
		received = append(received, notification.Event.Type)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	provider := &fakeProvider{generate: func(context.Context, model.Request) (model.Stream, error) {
		return doneStream("hello"), nil
	}}
	runner, err := looper.New(looper.SingleTurnLoop{}, newRuntime(t, bus, provider))
	if err != nil {
		t.Fatal(err)
	}
	if err := runner.Run(context.Background(), input()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	want := []model.EventType{model.EventContentDelta, model.EventDone}
	if len(received) != len(want) {
		t.Fatalf("received %v, want %v", received, want)
	}
	for index := range want {
		if received[index] != want[index] {
			t.Fatalf("received %v, want %v", received, want)
		}
	}
}

func TestCustomLoopCanGenerateMultipleTurnsAndPublish(t *testing.T) {
	bus := event.NewBus()
	var turns []int
	_, err := event.Subscribe(bus, func(_ context.Context, notification customEvent) error {
		turns = append(turns, notification.turn)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	strategy := looper.LoopFunc(func(ctx context.Context, run looper.Run, input looper.Input) error {
		if specs := run.Tools().Specs(); len(specs) != 0 {
			return errors.New("expected empty tool service")
		}
		for turn := 1; turn <= 2; turn++ {
			stream, err := run.Generate(ctx, input.Request)
			if err != nil {
				return err
			}
			for _, streamErr := range stream {
				if streamErr != nil {
					return streamErr
				}
			}
			if err := run.Publish(ctx, customEvent{turn: turn}); err != nil {
				return err
			}
		}
		return nil
	})
	var calls int
	provider := &fakeProvider{generate: func(context.Context, model.Request) (model.Stream, error) {
		calls++
		return doneStream("turn"), nil
	}}
	runner, err := looper.New(strategy, newRuntime(t, bus, provider))
	if err != nil {
		t.Fatal(err)
	}

	if err := runner.Run(context.Background(), input()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if calls != 2 || len(turns) != 2 || turns[0] != 1 || turns[1] != 2 {
		t.Fatalf("calls = %d, turns = %v", calls, turns)
	}
}

func TestSingleTurnLoopPropagatesStreamAndHandlerErrors(t *testing.T) {
	streamFailure := errors.New("stream failed")
	handlerFailure := errors.New("handler failed")

	tests := []struct {
		name      string
		stream    model.Stream
		handler   error
		wantError error
	}{
		{
			name: "stream",
			stream: func(yield func(model.Event, error) bool) {
				yield(model.Event{}, streamFailure)
			},
			wantError: streamFailure,
		},
		{
			name:      "handler",
			stream:    doneStream("hello"),
			handler:   handlerFailure,
			wantError: handlerFailure,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			bus := event.NewBus()
			_, err := event.Subscribe(bus, func(context.Context, looper.ModelEvent) error {
				return test.handler
			})
			if err != nil {
				t.Fatal(err)
			}
			provider := &fakeProvider{generate: func(context.Context, model.Request) (model.Stream, error) {
				return test.stream, nil
			}}
			runner, err := looper.New(looper.SingleTurnLoop{}, newRuntime(t, bus, provider))
			if err != nil {
				t.Fatal(err)
			}

			err = runner.Run(context.Background(), input())
			if !errors.Is(err, test.wantError) {
				t.Fatalf("Run() error = %v, want %v", err, test.wantError)
			}
		})
	}
}

func TestRunPropagatesCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	provider := &fakeProvider{generate: func(context.Context, model.Request) (model.Stream, error) {
		t.Fatal("Generate should not be called after cancellation")
		return nil, nil
	}}
	runner, err := looper.New(looper.SingleTurnLoop{}, newRuntime(t, event.NewBus(), provider))
	if err != nil {
		t.Fatal(err)
	}
	if err := runner.Run(ctx, input()); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error = %v, want context.Canceled", err)
	}
}

func TestRunReturnsModelResolutionError(t *testing.T) {
	provider := &fakeProvider{generate: func(context.Context, model.Request) (model.Stream, error) {
		t.Fatal("Generate should not be called for an unknown model")
		return nil, nil
	}}
	runner, err := looper.New(looper.SingleTurnLoop{}, newRuntime(t, event.NewBus(), provider))
	if err != nil {
		t.Fatal(err)
	}
	request := input()
	request.ModelID = "missing"
	if err := runner.Run(context.Background(), request); err == nil {
		t.Fatal("Run() accepted an unknown model")
	}
}

func TestLooperSupportsConcurrentRuns(t *testing.T) {
	const workers = 32
	var calls atomic.Int64
	provider := &fakeProvider{generate: func(context.Context, model.Request) (model.Stream, error) {
		calls.Add(1)
		return doneStream("ok"), nil
	}}
	runner, err := looper.New(looper.SingleTurnLoop{}, newRuntime(t, event.NewBus(), provider))
	if err != nil {
		t.Fatal(err)
	}

	var waitGroup sync.WaitGroup
	errorsByRun := make(chan error, workers)
	for range workers {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			errorsByRun <- runner.Run(context.Background(), input())
		}()
	}
	waitGroup.Wait()
	close(errorsByRun)
	for err := range errorsByRun {
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	}
	if calls.Load() != workers {
		t.Fatalf("Generate calls = %d, want %d", calls.Load(), workers)
	}
}
