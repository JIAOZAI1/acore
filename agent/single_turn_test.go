package agent_test

import (
	"context"
	"errors"
	"testing"

	"github.com/JIAOZAI1/acore/agent"
	"github.com/JIAOZAI1/acore/model"
)

func TestSingleTurnStrategyValidatesDirectInput(t *testing.T) {
	validLLM := &fakeLLM{generate: func(context.Context, model.Request) (model.Stream, error) {
		return doneModelStream("ok"), nil
	}}
	validInput := agent.RunInput{
		LLM:     validLLM,
		Request: agent.Request{Messages: userMessages("hello")},
	}

	var nilStrategy *agent.SingleTurnStrategy
	if _, err := nilStrategy.Run(context.Background(), validInput); !errors.Is(err, agent.ErrNilRunStrategy) {
		t.Fatalf("nil SingleTurnStrategy.Run() error = %v, want ErrNilRunStrategy", err)
	}

	strategy := agent.NewSingleTurnStrategy()
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

func TestSingleTurnStrategyPrefersContextErrorAfterGenerate(t *testing.T) {
	wantGenerateError := errors.New("late model failure")
	ctx, cancel := context.WithCancel(context.Background())
	llm := &fakeLLM{generate: func(context.Context, model.Request) (model.Stream, error) {
		cancel()
		return nil, wantGenerateError
	}}
	_, err := agent.NewSingleTurnStrategy().Run(ctx, agent.RunInput{
		LLM:     llm,
		Request: agent.Request{Messages: userMessages("hello")},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error = %v, want context.Canceled", err)
	}
}
