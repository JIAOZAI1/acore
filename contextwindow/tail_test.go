package contextwindow_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/JIAOZAI1/acore/contextwindow"
	"github.com/JIAOZAI1/acore/model"
)

type fakeEstimator struct {
	estimate func(context.Context, model.Model, model.Context) (int64, error)
}

func (f *fakeEstimator) Estimate(ctx context.Context, selected model.Model, value model.Context) (int64, error) {
	return f.estimate(ctx, selected, value)
}

func TestNewTailReducerValidatesAndSnapshotsConfig(t *testing.T) {
	if _, err := contextwindow.NewTailReducer(contextwindow.TailConfig{}); !errors.Is(err, contextwindow.ErrNilEstimator) {
		t.Fatalf("NewTailReducer(nil estimator) error = %v, want ErrNilEstimator", err)
	}
	var typedNil *fakeEstimator
	if _, err := contextwindow.NewTailReducer(contextwindow.TailConfig{Estimator: typedNil}); !errors.Is(err, contextwindow.ErrNilEstimator) {
		t.Fatalf("NewTailReducer(typed nil estimator) error = %v, want ErrNilEstimator", err)
	}

	estimator := countMessagesEstimator()
	invalid := []contextwindow.TailConfig{
		{Estimator: estimator, SafetyMarginTokens: -1},
		{Estimator: estimator, FallbackOutputTokens: -1},
	}
	for _, config := range invalid {
		if _, err := contextwindow.NewTailReducer(config); !errors.Is(err, contextwindow.ErrInvalidConfig) {
			t.Fatalf("NewTailReducer(%+v) error = %v, want ErrInvalidConfig", config, err)
		}
	}

	if reducer, err := contextwindow.NewTailReducer(contextwindow.TailConfig{Estimator: estimator}); err != nil || reducer == nil {
		t.Fatalf("NewTailReducer(valid) = %v, %v", reducer, err)
	}
}

func TestTailReducerValidatesReceiverContextAndInput(t *testing.T) {
	validInput := contextwindow.Input{
		Model:                 model.Model{ContextWindow: 10},
		Context:               model.Context{Messages: textMessages(model.RoleUser, "current")},
		RequestedOutputTokens: 2,
		ProtectedMessages:     1,
	}
	var nilReducer *contextwindow.TailReducer
	if _, err := nilReducer.Reduce(context.Background(), validInput); !errors.Is(err, contextwindow.ErrNilReducer) {
		t.Fatalf("nil Reduce() error = %v, want ErrNilReducer", err)
	}

	reducer := mustTailReducer(t, contextwindow.TailConfig{Estimator: countMessagesEstimator()})
	if _, err := reducer.Reduce(nil, validInput); !errors.Is(err, contextwindow.ErrInvalidContext) {
		t.Fatalf("Reduce(nil context) error = %v, want ErrInvalidContext", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := reducer.Reduce(ctx, validInput); !errors.Is(err, context.Canceled) {
		t.Fatalf("Reduce(canceled) error = %v, want context.Canceled", err)
	}
	invalidInput := validInput
	invalidInput.ProtectedMessages = 0
	if _, err := reducer.Reduce(context.Background(), invalidInput); !errors.Is(err, contextwindow.ErrInvalidInput) {
		t.Fatalf("Reduce(invalid input) error = %v, want ErrInvalidInput", err)
	}

	zeroReducer := &contextwindow.TailReducer{}
	if _, err := zeroReducer.Reduce(context.Background(), validInput); !errors.Is(err, contextwindow.ErrNilEstimator) {
		t.Fatalf("zero Reduce() error = %v, want ErrNilEstimator", err)
	}
}

func TestTailReducerResolvesOutputReserveAndBudget(t *testing.T) {
	tests := []struct {
		name     string
		config   contextwindow.TailConfig
		input    contextwindow.Input
		want     error
		wantFits bool
	}{
		{
			name:   "request takes priority",
			config: contextwindow.TailConfig{},
			input: contextwindow.Input{
				Model:                 model.Model{ID: "m", ContextWindow: 10, MaxOutputTokens: 9},
				RequestedOutputTokens: 4,
			},
			wantFits: true,
		},
		{
			name:     "model output reserve",
			config:   contextwindow.TailConfig{},
			input:    contextwindow.Input{Model: model.Model{ID: "m", ContextWindow: 10, MaxOutputTokens: 4}},
			wantFits: true,
		},
		{
			name:     "fallback output reserve",
			config:   contextwindow.TailConfig{FallbackOutputTokens: 4},
			input:    contextwindow.Input{Model: model.Model{ID: "m", ContextWindow: 10}},
			wantFits: true,
		},
		{
			name:   "unknown context window",
			config: contextwindow.TailConfig{FallbackOutputTokens: 1},
			input:  contextwindow.Input{Model: model.Model{ID: "m"}},
			want:   contextwindow.ErrBudgetUnavailable,
		},
		{
			name:   "unknown output reserve",
			config: contextwindow.TailConfig{},
			input:  contextwindow.Input{Model: model.Model{ID: "m", ContextWindow: 10}},
			want:   contextwindow.ErrBudgetUnavailable,
		},
		{
			name:   "negative model output",
			config: contextwindow.TailConfig{},
			input:  contextwindow.Input{Model: model.Model{ID: "m", ContextWindow: 10, MaxOutputTokens: -1}},
			want:   contextwindow.ErrInvalidInput,
		},
		{
			name:   "output consumes window",
			config: contextwindow.TailConfig{},
			input: contextwindow.Input{
				Model:                 model.Model{ID: "m", ContextWindow: 10},
				RequestedOutputTokens: 10,
			},
			want: contextwindow.ErrCannotFit,
		},
		{
			name:   "margin consumes input budget",
			config: contextwindow.TailConfig{SafetyMarginTokens: 6},
			input: contextwindow.Input{
				Model:                 model.Model{ID: "m", ContextWindow: 10},
				RequestedOutputTokens: 4,
			},
			want: contextwindow.ErrCannotFit,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			test.config.Estimator = contextwindow.EstimatorFunc(func(context.Context, model.Model, model.Context) (int64, error) {
				return 6, nil
			})
			test.input.Context.Messages = textMessages(model.RoleUser, "current")
			test.input.ProtectedMessages = 1
			reducer, err := contextwindow.NewTailReducer(test.config)
			if err != nil {
				t.Fatalf("NewTailReducer() error = %v", err)
			}
			result, err := reducer.Reduce(context.Background(), test.input)
			if test.want != nil {
				if !errors.Is(err, test.want) {
					t.Fatalf("Reduce() error = %v, want %v", err, test.want)
				}
				return
			}
			if err != nil {
				t.Fatalf("Reduce() error = %v", err)
			}
			if test.wantFits && result.MessageStart != 0 {
				t.Fatalf("Reduce() result = %+v, want full context", result)
			}
		})
	}
}

func TestTailReducerKeepsLargestSafeSuffix(t *testing.T) {
	messages := []model.Message{
		textMessage(model.RoleUser, "old question"),
		textMessage(model.RoleAssistant, "old answer"),
		textMessage(model.RoleUser, "middle question"),
		textMessage(model.RoleAssistant, "middle answer"),
		textMessage(model.RoleUser, "current question"),
	}
	var contexts []model.Context
	estimator := contextwindow.EstimatorFunc(func(_ context.Context, selected model.Model, value model.Context) (int64, error) {
		if selected.ID != "model" || value.SystemPrompt != "system" || len(value.Tools) != 1 {
			t.Fatalf("Estimate() input model/context = %+v/%+v", selected, value)
		}
		contexts = append(contexts, value)
		return int64(2 + len(value.Messages)*2), nil
	})
	reducer := mustTailReducer(t, contextwindow.TailConfig{Estimator: estimator})
	result, err := reducer.Reduce(context.Background(), contextwindow.Input{
		Model: model.Model{ID: "model", ContextWindow: 12},
		Context: model.Context{
			SystemPrompt: "system",
			Messages:     messages,
			Tools:        []model.ToolSpec{{Name: "tool"}},
		},
		RequestedOutputTokens: 2,
		ProtectedMessages:     1,
	})
	if err != nil {
		t.Fatalf("Reduce() error = %v", err)
	}
	if result.MessageStart != 2 {
		t.Fatalf("Reduce() MessageStart = %d, want 2", result.MessageStart)
	}
	if len(contexts) != 2 || len(contexts[0].Messages) != 5 || len(contexts[1].Messages) != 3 {
		t.Fatalf("estimated contexts = %v", messageCounts(contexts))
	}
}

func TestTailReducerUsesOnlyUserBoundariesAndProtectsCurrentRun(t *testing.T) {
	t.Run("skips assistant and tool boundaries", func(t *testing.T) {
		messages := []model.Message{
			textMessage(model.RoleUser, "old"),
			textMessage(model.RoleAssistant, "tool call"),
			textMessage(model.RoleTool, "tool result"),
			textMessage(model.RoleUser, "current"),
		}
		reducer := mustTailReducer(t, contextwindow.TailConfig{Estimator: countMessagesEstimator()})
		result, err := reducer.Reduce(context.Background(), contextwindow.Input{
			Model:                 model.Model{ContextWindow: 3},
			Context:               model.Context{Messages: messages},
			RequestedOutputTokens: 2,
			ProtectedMessages:     1,
		})
		if err != nil {
			t.Fatalf("Reduce() error = %v", err)
		}
		if result.MessageStart != 3 {
			t.Fatalf("Reduce() MessageStart = %d, want 3", result.MessageStart)
		}
	})

	t.Run("protected messages cannot fit", func(t *testing.T) {
		messages := []model.Message{
			textMessage(model.RoleUser, "old"),
			textMessage(model.RoleAssistant, "old answer"),
			textMessage(model.RoleUser, "current"),
			textMessage(model.RoleAssistant, "tool call"),
			textMessage(model.RoleTool, "tool result"),
		}
		reducer := mustTailReducer(t, contextwindow.TailConfig{Estimator: countMessagesEstimator()})
		_, err := reducer.Reduce(context.Background(), contextwindow.Input{
			Model:                 model.Model{ContextWindow: 4},
			Context:               model.Context{Messages: messages},
			RequestedOutputTokens: 2,
			ProtectedMessages:     3,
		})
		if !errors.Is(err, contextwindow.ErrCannotFit) {
			t.Fatalf("Reduce() error = %v, want ErrCannotFit", err)
		}
	})

	t.Run("no user boundary", func(t *testing.T) {
		reducer := mustTailReducer(t, contextwindow.TailConfig{Estimator: countMessagesEstimator()})
		_, err := reducer.Reduce(context.Background(), contextwindow.Input{
			Model: model.Model{ContextWindow: 3},
			Context: model.Context{Messages: []model.Message{
				textMessage(model.RoleAssistant, "old"),
				textMessage(model.RoleTool, "current"),
			}},
			RequestedOutputTokens: 2,
			ProtectedMessages:     1,
		})
		if !errors.Is(err, contextwindow.ErrCannotFit) {
			t.Fatalf("Reduce() error = %v, want ErrCannotFit", err)
		}
	})
}

func TestTailReducerEstimatorErrorsCancellationAndIsolation(t *testing.T) {
	want := errors.New("tokenizer unavailable")
	failing := mustTailReducer(t, contextwindow.TailConfig{Estimator: contextwindow.EstimatorFunc(func(context.Context, model.Model, model.Context) (int64, error) {
		return 0, want
	})})
	input := contextwindow.Input{
		Model:                 model.Model{ID: "m", ContextWindow: 10},
		Context:               model.Context{Messages: textMessages(model.RoleUser, "current")},
		RequestedOutputTokens: 2,
		ProtectedMessages:     1,
	}
	if _, err := failing.Reduce(context.Background(), input); !errors.Is(err, contextwindow.ErrEstimate) || !errors.Is(err, want) {
		t.Fatalf("Reduce(estimator error) = %v, want ErrEstimate and original", err)
	}

	negative := mustTailReducer(t, contextwindow.TailConfig{Estimator: contextwindow.EstimatorFunc(func(context.Context, model.Model, model.Context) (int64, error) {
		return -1, nil
	})})
	if _, err := negative.Reduce(context.Background(), input); !errors.Is(err, contextwindow.ErrInvalidEstimate) {
		t.Fatalf("Reduce(negative estimate) = %v, want ErrInvalidEstimate", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	canceling := mustTailReducer(t, contextwindow.TailConfig{Estimator: contextwindow.EstimatorFunc(func(context.Context, model.Model, model.Context) (int64, error) {
		cancel()
		return 1, want
	})})
	if _, err := canceling.Reduce(ctx, input); !errors.Is(err, context.Canceled) {
		t.Fatalf("Reduce(canceling estimator) = %v, want context.Canceled", err)
	}

	modality := []string{"text"}
	mutating := mustTailReducer(t, contextwindow.TailConfig{Estimator: contextwindow.EstimatorFunc(func(_ context.Context, selected model.Model, value model.Context) (int64, error) {
		selected.InputModalities[0] = "image"
		value.Messages[0].Content[0].Text = "mutated"
		return 1, nil
	})})
	input.Model.InputModalities = modality
	if _, err := mutating.Reduce(context.Background(), input); err != nil {
		t.Fatalf("Reduce(mutating estimator) error = %v", err)
	}
	if modality[0] != "text" || input.Context.Messages[0].Content[0].Text != "current" {
		t.Fatal("Estimator modified caller-owned input")
	}
}

func TestTailReducerSupportsConcurrentCalls(t *testing.T) {
	reducer := mustTailReducer(t, contextwindow.TailConfig{Estimator: countMessagesEstimator()})
	input := contextwindow.Input{
		Model: model.Model{ContextWindow: 3},
		Context: model.Context{Messages: []model.Message{
			textMessage(model.RoleUser, "old"),
			textMessage(model.RoleAssistant, "answer"),
			textMessage(model.RoleUser, "current"),
		}},
		RequestedOutputTokens: 2,
		ProtectedMessages:     1,
	}

	const calls = 16
	errorsByCall := make(chan error, calls)
	var wait sync.WaitGroup
	for range calls {
		wait.Add(1)
		go func() {
			defer wait.Done()
			result, err := reducer.Reduce(context.Background(), input)
			if err != nil {
				errorsByCall <- err
				return
			}
			if result.MessageStart != 2 {
				errorsByCall <- fmt.Errorf("message start = %d, want 2", result.MessageStart)
			}
		}()
	}
	wait.Wait()
	close(errorsByCall)
	for err := range errorsByCall {
		t.Error(err)
	}
}

func countMessagesEstimator() contextwindow.Estimator {
	return contextwindow.EstimatorFunc(func(_ context.Context, _ model.Model, value model.Context) (int64, error) {
		return int64(len(value.Messages)), nil
	})
}

func mustTailReducer(t *testing.T, config contextwindow.TailConfig) *contextwindow.TailReducer {
	t.Helper()
	reducer, err := contextwindow.NewTailReducer(config)
	if err != nil {
		t.Fatalf("NewTailReducer() error = %v", err)
	}
	return reducer
}

func messageCounts(contexts []model.Context) string {
	counts := make([]int, len(contexts))
	for index, value := range contexts {
		counts[index] = len(value.Messages)
	}
	return fmt.Sprint(counts)
}

var _ contextwindow.Estimator = (*fakeEstimator)(nil)
