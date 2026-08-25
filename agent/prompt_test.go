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
	"github.com/JIAOZAI1/acore/prompt"
	"github.com/JIAOZAI1/acore/session"
	"github.com/JIAOZAI1/acore/tool"
)

type fakePromptRenderer struct {
	render func(context.Context, prompt.Input) (string, error)
}

func (f *fakePromptRenderer) Render(ctx context.Context, input prompt.Input) (string, error) {
	if f.render == nil {
		return "", nil
	}
	return f.render(ctx, input)
}

func TestBuilderValidatesPromptConfiguration(t *testing.T) {
	builder := agent.NewBuilder()
	if err := builder.UsePrompt(nil); !errors.Is(err, agent.ErrNilPromptRenderer) {
		t.Fatalf("UsePrompt(nil) error = %v, want ErrNilPromptRenderer", err)
	}
	var typedNil *fakePromptRenderer
	if err := builder.UsePrompt(typedNil); !errors.Is(err, agent.ErrNilPromptRenderer) {
		t.Fatalf("UsePrompt(typed nil) error = %v, want ErrNilPromptRenderer", err)
	}
	renderer := prompt.NewStatic("system")
	if err := builder.UsePrompt(renderer); err != nil {
		t.Fatalf("UsePrompt() after invalid values error = %v", err)
	}
	if err := builder.UsePrompt(renderer); !errors.Is(err, agent.ErrConfigAlreadySet) {
		t.Fatalf("second UsePrompt() error = %v, want ErrConfigAlreadySet", err)
	}
	if err := builder.SetSystemPrompt("duplicate"); !errors.Is(err, agent.ErrConfigAlreadySet) {
		t.Fatalf("SetSystemPrompt() after UsePrompt error = %v, want ErrConfigAlreadySet", err)
	}

	staticBuilder := agent.NewBuilder()
	if err := staticBuilder.SetSystemPrompt("static"); err != nil {
		t.Fatalf("SetSystemPrompt() error = %v", err)
	}
	if err := staticBuilder.UsePrompt(renderer); !errors.Is(err, agent.ErrConfigAlreadySet) {
		t.Fatalf("UsePrompt() after SetSystemPrompt error = %v, want ErrConfigAlreadySet", err)
	}
}

func TestBuilderFreezesPromptAfterBuild(t *testing.T) {
	builder := agent.NewBuilder()
	llm := &fakeLLM{generate: func(context.Context, model.Request) (model.Stream, error) {
		return doneModelStream("ok"), nil
	}}
	if err := builder.UseLLM(llm); err != nil {
		t.Fatalf("UseLLM() error = %v", err)
	}
	if err := builder.UseRunStrategy(agent.NewSingleTurnStrategy()); err != nil {
		t.Fatalf("UseRunStrategy() error = %v", err)
	}
	if err := builder.UsePrompt(prompt.NewStatic("system")); err != nil {
		t.Fatalf("UsePrompt() error = %v", err)
	}
	if _, err := builder.Build(); err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if err := builder.UsePrompt(prompt.NewStatic("late")); !errors.Is(err, agent.ErrBuilderBuilt) {
		t.Fatalf("UsePrompt() after Build error = %v, want ErrBuilderBuilt", err)
	}
}

func TestConfiguredAgentRendersPromptWithIsolatedValues(t *testing.T) {
	var renderCalls int
	var renderedRole string
	renderer := &fakePromptRenderer{render: func(_ context.Context, input prompt.Input) (string, error) {
		renderCalls++
		renderedRole = input.Values["role"]
		input.Values["role"] = "renderer-mutated"
		input.Values["added"] = "renderer-only"
		return "system:" + renderedRole, nil
	}}
	var captured agent.RunInput
	strategy := &fakeRunStrategy{run: func(_ context.Context, input agent.RunInput) (agent.Stream, error) {
		captured = input
		return successfulAgentStream("done"), nil
	}}
	value := buildAgentWithPrompt(t, strategy, renderer)

	req := agent.Request{
		Messages:     userMessages("hello"),
		PromptValues: prompt.Values{"role": "support"},
	}
	stream, err := value.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if _, err := collect(stream); err != nil {
		t.Fatalf("stream error = %v", err)
	}

	if renderCalls != 1 {
		t.Fatalf("Render() calls = %d, want 1", renderCalls)
	}
	if renderedRole != "support" || captured.SystemPrompt != "system:support" {
		t.Fatalf("rendered role = %q, system prompt = %q", renderedRole, captured.SystemPrompt)
	}
	if req.PromptValues["role"] != "support" {
		t.Fatalf("caller PromptValues = %+v, want unchanged", req.PromptValues)
	}
	if captured.Request.PromptValues["role"] != "support" {
		t.Fatalf("strategy PromptValues = %+v, want original snapshot", captured.Request.PromptValues)
	}
	if _, exists := captured.Request.PromptValues["added"]; exists {
		t.Fatalf("strategy PromptValues include renderer mutation: %+v", captured.Request.PromptValues)
	}

	req.PromptValues["role"] = "caller-mutated"
	if captured.Request.PromptValues["role"] != "support" {
		t.Fatal("strategy PromptValues share caller state")
	}
}

func TestConfiguredAgentValidatesBeforeRenderingPrompt(t *testing.T) {
	renderCalls := 0
	renderer := prompt.RendererFunc(func(context.Context, prompt.Input) (string, error) {
		renderCalls++
		return "unused", nil
	})
	strategy := &fakeRunStrategy{run: func(context.Context, agent.RunInput) (agent.Stream, error) {
		t.Fatal("strategy should not be called")
		return nil, nil
	}}
	value := buildAgentWithPrompt(t, strategy, renderer)
	if _, err := value.Run(context.Background(), agent.Request{}); !errors.Is(err, agent.ErrInvalidRequest) {
		t.Fatalf("Run() error = %v, want ErrInvalidRequest", err)
	}
	if renderCalls != 0 {
		t.Fatalf("Render() calls = %d, want 0", renderCalls)
	}
}

func TestConfiguredAgentReturnsPromptErrorsBeforeStrategy(t *testing.T) {
	want := errors.New("template failed")
	renderer := &fakePromptRenderer{render: func(context.Context, prompt.Input) (string, error) {
		return "partial", fmt.Errorf("%w: %w", prompt.ErrRender, want)
	}}
	strategyCalled := false
	strategy := &fakeRunStrategy{run: func(context.Context, agent.RunInput) (agent.Stream, error) {
		strategyCalled = true
		return successfulAgentStream("unused"), nil
	}}
	value := buildAgentWithPrompt(t, strategy, renderer)

	_, err := value.Run(context.Background(), agent.Request{Messages: userMessages("hello")})
	for _, expected := range []error{agent.ErrRenderPrompt, prompt.ErrRender, want} {
		if !errors.Is(err, expected) {
			t.Errorf("Run() error = %v, want errors.Is(_, %v)", err, expected)
		}
	}
	if strategyCalled {
		t.Fatal("strategy was called after prompt failure")
	}
}

func TestConfiguredAgentPrefersContextErrorAfterPromptRender(t *testing.T) {
	want := errors.New("late prompt failure")
	ctx, cancel := context.WithCancel(context.Background())
	renderer := &fakePromptRenderer{render: func(context.Context, prompt.Input) (string, error) {
		cancel()
		return "late", want
	}}
	strategyCalled := false
	strategy := &fakeRunStrategy{run: func(context.Context, agent.RunInput) (agent.Stream, error) {
		strategyCalled = true
		return successfulAgentStream("unused"), nil
	}}
	value := buildAgentWithPrompt(t, strategy, renderer)

	_, err := value.Run(ctx, agent.Request{Messages: userMessages("hello")})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error = %v, want context.Canceled", err)
	}
	if strategyCalled {
		t.Fatal("strategy was called after context cancellation")
	}
}

func TestConfiguredAgentRendersPromptOnceForToolLoop(t *testing.T) {
	service := &fakeToolService{
		specs: []tool.Spec{{Name: "lookup", Parameters: json.RawMessage(`{"type":"object"}`)}},
		execute: func(context.Context, tool.Call) (tool.Result, error) {
			return tool.Result{Content: "found"}, nil
		},
	}
	strategy := buildToolLoopStrategy(t, service, agent.DefaultToolLoopLimits(), agent.ToolErrorModeFeedback)
	modelCalls := 0
	var systemPrompts []string
	llm := &fakeLLM{generate: func(_ context.Context, req model.Request) (model.Stream, error) {
		modelCalls++
		systemPrompts = append(systemPrompts, req.Context.SystemPrompt)
		if modelCalls == 1 {
			return doneResultStream(&model.Result{
				Message: assistantToolCalls(model.ToolCall{
					ID: "call-1", Name: "lookup", Arguments: json.RawMessage(`{}`),
				}),
				StopReason: model.ReasonToolUse,
			}), nil
		}
		return doneResultStream(&model.Result{
			Message: model.Message{
				Role:    model.RoleAssistant,
				Content: []model.ContentBlock{{Kind: model.ContentText, Text: "done"}},
			},
			StopReason: model.ReasonStop,
		}), nil
	}}
	renderCalls := 0
	renderer := prompt.RendererFunc(func(_ context.Context, input prompt.Input) (string, error) {
		renderCalls++
		return "role=" + input.Values["role"], nil
	})

	builder := agent.NewBuilder()
	if err := builder.UseLLM(llm); err != nil {
		t.Fatalf("UseLLM() error = %v", err)
	}
	if err := builder.UseRunStrategy(strategy); err != nil {
		t.Fatalf("UseRunStrategy() error = %v", err)
	}
	if err := builder.UsePrompt(renderer); err != nil {
		t.Fatalf("UsePrompt() error = %v", err)
	}
	value, err := builder.Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if _, err := agent.Complete(context.Background(), value, agent.Request{
		Messages:     userMessages("hello"),
		PromptValues: prompt.Values{"role": "support"},
	}); err != nil {
		t.Fatalf("Complete() error = %v", err)
	}

	if renderCalls != 1 {
		t.Fatalf("Render() calls = %d, want 1", renderCalls)
	}
	if modelCalls != 2 {
		t.Fatalf("model calls = %d, want 2", modelCalls)
	}
	for index, systemPrompt := range systemPrompts {
		if systemPrompt != "role=support" {
			t.Errorf("model call %d system prompt = %q", index+1, systemPrompt)
		}
	}
}

func TestConfiguredAgentDoesNotPersistPromptInSession(t *testing.T) {
	history := session.NewMemoryService()
	strategyBuilder := agent.NewSingleTurnBuilder()
	if err := strategyBuilder.UseSession(history); err != nil {
		t.Fatalf("UseSession() error = %v", err)
	}
	strategy, err := strategyBuilder.Build()
	if err != nil {
		t.Fatalf("Build strategy error = %v", err)
	}
	renderer, err := prompt.NewTemplate(prompt.TemplateConfig{Name: "session", Text: "system-only={{.private}}"})
	if err != nil {
		t.Fatalf("NewTemplate() error = %v", err)
	}
	llm := &fakeLLM{generate: func(_ context.Context, req model.Request) (model.Stream, error) {
		if req.Context.SystemPrompt != "system-only=private-value" {
			t.Fatalf("system prompt = %q", req.Context.SystemPrompt)
		}
		return doneModelStream("assistant answer"), nil
	}}

	builder := agent.NewBuilder()
	if err := builder.UseLLM(llm); err != nil {
		t.Fatalf("UseLLM() error = %v", err)
	}
	if err := builder.UseRunStrategy(strategy); err != nil {
		t.Fatalf("UseRunStrategy() error = %v", err)
	}
	if err := builder.UsePrompt(renderer); err != nil {
		t.Fatalf("UsePrompt() error = %v", err)
	}
	value, err := builder.Build()
	if err != nil {
		t.Fatalf("Build agent error = %v", err)
	}

	key := session.Key{Scope: "prompt-test", ID: "conversation"}
	if _, err := agent.Complete(context.Background(), value, agent.Request{
		Session: &agent.SessionInput{
			Key:      key,
			Messages: userMessages("user input"),
		},
		PromptValues: prompt.Values{"private": "private-value"},
	}); err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	snapshot, err := history.Load(context.Background(), key)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(snapshot.Messages) != 2 {
		t.Fatalf("stored messages = %d, want 2", len(snapshot.Messages))
	}
	if snapshot.Messages[0].Content[0].Text != "user input" || snapshot.Messages[1].Content[0].Text != "assistant answer" {
		t.Fatalf("stored messages = %+v", snapshot.Messages)
	}
}

func TestConfiguredAgentSupportsConcurrentPromptRuns(t *testing.T) {
	renderer, err := prompt.NewTemplate(prompt.TemplateConfig{Name: "concurrent-agent", Text: "system={{.value}}"})
	if err != nil {
		t.Fatalf("NewTemplate() error = %v", err)
	}
	strategy := &fakeRunStrategy{run: func(_ context.Context, input agent.RunInput) (agent.Stream, error) {
		return successfulAgentStream(input.SystemPrompt), nil
	}}
	value := buildAgentWithPrompt(t, strategy, renderer)

	const runs = 32
	errorsByRun := make(chan error, runs)
	var wait sync.WaitGroup
	for index := 0; index < runs; index++ {
		index := index
		wait.Add(1)
		go func() {
			defer wait.Done()
			want := fmt.Sprintf("system=run-%d", index)
			result, err := agent.Complete(context.Background(), value, agent.Request{
				Messages:     userMessages("hello"),
				PromptValues: prompt.Values{"value": fmt.Sprintf("run-%d", index)},
			})
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

func buildAgentWithPrompt(t *testing.T, strategy agent.RunStrategy, renderer prompt.Renderer) agent.Agent {
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
	if err := builder.UsePrompt(renderer); err != nil {
		t.Fatalf("UsePrompt() error = %v", err)
	}
	value, err := builder.Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	return value
}

var _ prompt.Renderer = (*fakePromptRenderer)(nil)
