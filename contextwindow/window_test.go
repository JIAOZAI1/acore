package contextwindow_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/JIAOZAI1/acore/contextwindow"
	"github.com/JIAOZAI1/acore/model"
)

type fakeReducer struct {
	reduce func(context.Context, contextwindow.Input) (contextwindow.Result, error)
}

func (f *fakeReducer) Reduce(ctx context.Context, input contextwindow.Input) (contextwindow.Result, error) {
	return f.reduce(ctx, input)
}

func TestApplyValidatesContextReducerAndInput(t *testing.T) {
	valid := contextwindow.Input{
		Context:           model.Context{Messages: textMessages(model.RoleUser, "current")},
		ProtectedMessages: 1,
	}
	reducer := contextwindow.ReducerFunc(func(context.Context, contextwindow.Input) (contextwindow.Result, error) {
		return contextwindow.Result{}, nil
	})

	if _, err := contextwindow.Apply(nil, reducer, valid); !errors.Is(err, contextwindow.ErrInvalidContext) {
		t.Fatalf("Apply(nil context) error = %v, want ErrInvalidContext", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := contextwindow.Apply(ctx, reducer, valid); !errors.Is(err, context.Canceled) {
		t.Fatalf("Apply(canceled) error = %v, want context.Canceled", err)
	}
	if _, err := contextwindow.Apply(context.Background(), nil, valid); !errors.Is(err, contextwindow.ErrNilReducer) {
		t.Fatalf("Apply(nil reducer) error = %v, want ErrNilReducer", err)
	}
	var typedNil *fakeReducer
	if _, err := contextwindow.Apply(context.Background(), typedNil, valid); !errors.Is(err, contextwindow.ErrNilReducer) {
		t.Fatalf("Apply(typed nil reducer) error = %v, want ErrNilReducer", err)
	}

	tests := []struct {
		name  string
		input contextwindow.Input
	}{
		{name: "empty messages", input: contextwindow.Input{ProtectedMessages: 1}},
		{name: "zero protected", input: contextwindow.Input{Context: valid.Context}},
		{name: "negative protected", input: contextwindow.Input{Context: valid.Context, ProtectedMessages: -1}},
		{name: "too many protected", input: contextwindow.Input{Context: valid.Context, ProtectedMessages: 2}},
		{name: "negative output", input: contextwindow.Input{Context: valid.Context, ProtectedMessages: 1, RequestedOutputTokens: -1}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			called := false
			checking := contextwindow.ReducerFunc(func(context.Context, contextwindow.Input) (contextwindow.Result, error) {
				called = true
				return contextwindow.Result{}, nil
			})
			if _, err := contextwindow.Apply(context.Background(), checking, test.input); !errors.Is(err, contextwindow.ErrInvalidInput) {
				t.Fatalf("Apply() error = %v, want ErrInvalidInput", err)
			}
			if called {
				t.Fatal("Reducer called for invalid input")
			}
		})
	}
}

func TestApplyRejectsUnsafeReducerResults(t *testing.T) {
	input := contextwindow.Input{
		Context: model.Context{Messages: []model.Message{
			textMessage(model.RoleUser, "old"),
			textMessage(model.RoleAssistant, "answer"),
			textMessage(model.RoleUser, "current"),
		}},
		ProtectedMessages: 1,
	}

	tests := []struct {
		name  string
		start int
	}{
		{name: "negative", start: -1},
		{name: "inside protected suffix", start: 3},
		{name: "non-user boundary", start: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			reducer := contextwindow.ReducerFunc(func(context.Context, contextwindow.Input) (contextwindow.Result, error) {
				return contextwindow.Result{MessageStart: test.start}, nil
			})
			if _, err := contextwindow.Apply(context.Background(), reducer, input); !errors.Is(err, contextwindow.ErrInvalidResult) {
				t.Fatalf("Apply() error = %v, want ErrInvalidResult", err)
			}
		})
	}
}

func TestApplyUsesIsolatedOriginalSnapshot(t *testing.T) {
	signature := "original-signature"
	arguments := json.RawMessage(`{"value":1}`)
	schema := json.RawMessage(`{"type":"object"}`)
	input := contextwindow.Input{
		Model: model.Model{
			ID:              "original-model",
			InputModalities: []string{"text"},
		},
		Context: model.Context{
			SystemPrompt: "original prompt",
			Messages: []model.Message{
				textMessage(model.RoleUser, "old"),
				textMessage(model.RoleAssistant, "answer"),
				{
					Role: model.RoleUser,
					Content: []model.ContentBlock{
						{Kind: model.ContentThinking, Text: "thinking", Signature: &signature},
						{Kind: model.ContentToolCall, ToolCall: &model.ToolCall{ID: "call", Name: "tool", Arguments: arguments}},
					},
				},
			},
			Tools: []model.ToolSpec{{Name: "tool", Parameters: schema}},
		},
		ProtectedMessages: 1,
	}

	reducer := contextwindow.ReducerFunc(func(_ context.Context, got contextwindow.Input) (contextwindow.Result, error) {
		got.Model.ID = "mutated model"
		got.Model.InputModalities[0] = "image"
		got.Context.SystemPrompt = "mutated prompt"
		got.Context.Messages[2].Content[0].Text = "mutated thinking"
		*got.Context.Messages[2].Content[0].Signature = "mutated signature"
		got.Context.Messages[2].Content[1].ToolCall.Arguments[0] = '['
		got.Context.Tools[0].Name = "mutated tool"
		got.Context.Tools[0].Parameters[0] = '['
		return contextwindow.Result{MessageStart: 2}, nil
	})

	got, err := contextwindow.Apply(context.Background(), reducer, input)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if got.SystemPrompt != "original prompt" || len(got.Messages) != 1 || got.Messages[0].Role != model.RoleUser {
		t.Fatalf("Apply() context = %+v", got)
	}
	if got.Messages[0].Content[0].Text != "thinking" || *got.Messages[0].Content[0].Signature != "original-signature" {
		t.Fatalf("Apply() thinking block = %+v", got.Messages[0].Content[0])
	}
	if value := string(got.Messages[0].Content[1].ToolCall.Arguments); value != `{"value":1}` {
		t.Fatalf("Apply() arguments = %s", value)
	}
	if len(got.Tools) != 1 || got.Tools[0].Name != "tool" || string(got.Tools[0].Parameters) != `{"type":"object"}` {
		t.Fatalf("Apply() tools = %+v", got.Tools)
	}
	if input.Model.ID != "original-model" || input.Model.InputModalities[0] != "text" {
		t.Fatalf("caller model was modified: %+v", input.Model)
	}
	if input.Context.SystemPrompt != "original prompt" || input.Context.Messages[2].Content[0].Text != "thinking" {
		t.Fatal("caller context was modified")
	}
	if string(arguments) != `{"value":1}` || string(schema) != `{"type":"object"}` || signature != "original-signature" {
		t.Fatal("caller nested data was modified")
	}

	got.Messages[0].Content[0].Text = "output mutation"
	got.Tools[0].Parameters[0] = '['
	if input.Context.Messages[2].Content[0].Text != "thinking" || string(schema) != `{"type":"object"}` {
		t.Fatal("Apply() output shares mutable data with caller")
	}
}

func TestReducerFuncContract(t *testing.T) {
	var nilReducer contextwindow.ReducerFunc
	if _, err := nilReducer.Reduce(context.Background(), contextwindow.Input{}); !errors.Is(err, contextwindow.ErrNilReducer) {
		t.Fatalf("nil Reduce() error = %v, want ErrNilReducer", err)
	}

	reducer := contextwindow.ReducerFunc(func(context.Context, contextwindow.Input) (contextwindow.Result, error) {
		return contextwindow.Result{MessageStart: 2}, nil
	})
	result, err := reducer.Reduce(context.Background(), contextwindow.Input{})
	if err != nil || result.MessageStart != 2 {
		t.Fatalf("Reduce() = %+v, %v", result, err)
	}
	if _, err := reducer.Reduce(nil, contextwindow.Input{}); !errors.Is(err, contextwindow.ErrInvalidContext) {
		t.Fatalf("Reduce(nil) error = %v, want ErrInvalidContext", err)
	}

	want := errors.New("late reducer error")
	ctx, cancel := context.WithCancel(context.Background())
	canceling := contextwindow.ReducerFunc(func(context.Context, contextwindow.Input) (contextwindow.Result, error) {
		cancel()
		return contextwindow.Result{MessageStart: 1}, want
	})
	result, err = canceling.Reduce(ctx, contextwindow.Input{})
	if !errors.Is(err, context.Canceled) || result != (contextwindow.Result{}) {
		t.Fatalf("canceling Reduce() = %+v, %v, want zero/context.Canceled", result, err)
	}
}

func textMessages(role model.Role, texts ...string) []model.Message {
	messages := make([]model.Message, len(texts))
	for index, text := range texts {
		messages[index] = textMessage(role, text)
	}
	return messages
}

func textMessage(role model.Role, text string) model.Message {
	return model.Message{
		Role:    role,
		Content: []model.ContentBlock{{Kind: model.ContentText, Text: text}},
	}
}

var _ contextwindow.Reducer = (*fakeReducer)(nil)
var _ contextwindow.Reducer = contextwindow.ReducerFunc(nil)
