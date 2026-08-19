package model

import (
	"context"
	"errors"
	"testing"
)

type fakeProvider struct {
	id       string
	models   []Model
	generate func(context.Context, Model, Request) (Stream, error)
}

func (f *fakeProvider) ID() string { return f.id }

func (f *fakeProvider) Models() []Model {
	return append([]Model(nil), f.models...)
}

func (f *fakeProvider) Generate(ctx context.Context, model Model, req Request) (Stream, error) {
	return f.generate(ctx, model, req)
}

func TestBindAndComplete(t *testing.T) {
	model := Model{ID: "test-model", Provider: "fake"}
	provider := &fakeProvider{
		id:     "fake",
		models: []Model{model},
		generate: func(_ context.Context, gotModel Model, _ Request) (Stream, error) {
			if gotModel.ID != model.ID {
				t.Fatalf("model ID = %q, want %q", gotModel.ID, model.ID)
			}
			return func(yield func(Event, error) bool) {
				yield(Event{Type: EventStart}, nil)
				yield(Event{Type: EventContentStart, Block: &ContentBlock{Kind: ContentText}}, nil)
				yield(Event{Type: EventContentDelta, Delta: "hello"}, nil)
				yield(Event{Type: EventContentEnd, Block: &ContentBlock{Kind: ContentText, Text: "hello"}}, nil)
				yield(Event{Type: EventDone, Result: &Result{
					Message:    Message{Role: RoleAssistant, Content: []ContentBlock{{Kind: ContentText, Text: "hello"}}},
					StopReason: ReasonStop,
				}}, nil)
			}, nil
		},
	}

	llm, err := Bind(provider, model)
	if err != nil {
		t.Fatalf("Bind() error = %v", err)
	}
	result, err := Complete(context.Background(), llm, Request{})
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if result.StopReason != ReasonStop || result.Message.Content[0].Text != "hello" {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestBindRejectsProviderMismatch(t *testing.T) {
	provider := &fakeProvider{id: "a"}
	_, err := Bind(provider, Model{ID: "m", Provider: "b"})
	if err == nil {
		t.Fatal("Bind() should reject a provider mismatch")
	}
}

func TestCompleteReturnsStreamError(t *testing.T) {
	want := errors.New("connection reset")
	provider := &fakeProvider{
		id: "fake",
		generate: func(context.Context, Model, Request) (Stream, error) {
			return func(yield func(Event, error) bool) {
				yield(Event{}, want)
			}, nil
		},
	}
	llm, err := Bind(provider, Model{ID: "m", Provider: "fake"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = Complete(context.Background(), llm, Request{})
	if !errors.Is(err, want) {
		t.Fatalf("Complete() error = %v, want %v", err, want)
	}
}

func TestCompleteRejectsSilentStreamEnd(t *testing.T) {
	provider := &fakeProvider{
		id: "fake",
		generate: func(context.Context, Model, Request) (Stream, error) {
			return func(func(Event, error) bool) {}, nil
		},
	}
	llm, err := Bind(provider, Model{ID: "m", Provider: "fake"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = Complete(context.Background(), llm, Request{})
	if !errors.Is(err, ErrUnexpectedStreamEnd) {
		t.Fatalf("Complete() error = %v, want %v", err, ErrUnexpectedStreamEnd)
	}
}

func TestGenerateValidatesPortableOptions(t *testing.T) {
	called := false
	provider := &fakeProvider{
		id: "fake",
		generate: func(context.Context, Model, Request) (Stream, error) {
			called = true
			return nil, nil
		},
	}
	llm, err := Bind(provider, Model{ID: "m", Provider: "fake"})
	if err != nil {
		t.Fatal(err)
	}
	zero := 0
	_, err = llm.Generate(context.Background(), Request{MaxTokens: &zero})
	if err == nil {
		t.Fatal("Generate() should reject non-positive max tokens")
	}
	if called {
		t.Fatal("provider should not be called for an invalid request")
	}
}
