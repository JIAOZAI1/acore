package agent_test

import (
	"context"
	"errors"
	"math"
	"testing"

	"github.com/JIAOZAI1/acore/agent"
	"github.com/JIAOZAI1/acore/model"
)

func TestBuilderRequiresComponentsAndCanRecoverFromFailedBuild(t *testing.T) {
	builder := agent.NewBuilder()
	if _, err := builder.Build(); !errors.Is(err, agent.ErrMissingLLM) {
		t.Fatalf("Build() error = %v, want ErrMissingLLM", err)
	}
	if err := builder.UseLLM(nil); !errors.Is(err, agent.ErrNilLLM) {
		t.Fatalf("UseLLM(nil) error = %v, want ErrNilLLM", err)
	}
	var typedNil *fakeLLM
	if err := builder.UseLLM(typedNil); !errors.Is(err, agent.ErrNilLLM) {
		t.Fatalf("UseLLM(typed nil) error = %v, want ErrNilLLM", err)
	}

	llm := &fakeLLM{generate: func(context.Context, model.Request) (model.Stream, error) {
		return doneModelStream("ok"), nil
	}}
	if err := builder.UseLLM(llm); err != nil {
		t.Fatalf("UseLLM() after failed Build error = %v", err)
	}
	if err := builder.UseLLM(nil); !errors.Is(err, agent.ErrLLMAlreadySet) {
		t.Fatalf("second UseLLM() error = %v, want ErrLLMAlreadySet", err)
	}
	if _, err := builder.Build(); !errors.Is(err, agent.ErrMissingRunStrategy) {
		t.Fatalf("Build() without strategy error = %v, want ErrMissingRunStrategy", err)
	}
	if err := builder.UseRunStrategy(nil); !errors.Is(err, agent.ErrNilRunStrategy) {
		t.Fatalf("UseRunStrategy(nil) error = %v, want ErrNilRunStrategy", err)
	}
	var typedNilStrategy *fakeRunStrategy
	if err := builder.UseRunStrategy(typedNilStrategy); !errors.Is(err, agent.ErrNilRunStrategy) {
		t.Fatalf("UseRunStrategy(typed nil) error = %v, want ErrNilRunStrategy", err)
	}
	strategy := agent.NewSingleTurnStrategy()
	if err := builder.UseRunStrategy(strategy); err != nil {
		t.Fatalf("UseRunStrategy() after failed Build error = %v", err)
	}
	if err := builder.UseRunStrategy(nil); !errors.Is(err, agent.ErrRunStrategyAlreadySet) {
		t.Fatalf("second UseRunStrategy() error = %v, want ErrRunStrategyAlreadySet", err)
	}
	if _, err := builder.Build(); err != nil {
		t.Fatalf("Build() error = %v", err)
	}
}

func TestBuilderRejectsDuplicateConfiguration(t *testing.T) {
	builder := agent.NewBuilder()
	if err := builder.SetSystemPrompt("first"); err != nil {
		t.Fatalf("SetSystemPrompt() error = %v", err)
	}
	if err := builder.SetSystemPrompt("second"); !errors.Is(err, agent.ErrConfigAlreadySet) {
		t.Fatalf("second SetSystemPrompt() error = %v", err)
	}
	if err := builder.SetModelOptions(agent.ModelOptions{}); err != nil {
		t.Fatalf("SetModelOptions() error = %v", err)
	}
	if err := builder.SetModelOptions(agent.ModelOptions{}); !errors.Is(err, agent.ErrConfigAlreadySet) {
		t.Fatalf("second SetModelOptions() error = %v", err)
	}
}

func TestBuilderFreezesAfterSuccessfulBuild(t *testing.T) {
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
	if _, err := builder.Build(); err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	if err := builder.UseLLM(llm); !errors.Is(err, agent.ErrBuilderBuilt) {
		t.Fatalf("UseLLM() after Build error = %v", err)
	}
	if err := builder.UseRunStrategy(agent.NewSingleTurnStrategy()); !errors.Is(err, agent.ErrBuilderBuilt) {
		t.Fatalf("UseRunStrategy() after Build error = %v", err)
	}
	if err := builder.SetSystemPrompt("late"); !errors.Is(err, agent.ErrBuilderBuilt) {
		t.Fatalf("SetSystemPrompt() after Build error = %v", err)
	}
	if err := builder.SetModelOptions(agent.ModelOptions{}); !errors.Is(err, agent.ErrBuilderBuilt) {
		t.Fatalf("SetModelOptions() after Build error = %v", err)
	}
	if _, err := builder.Build(); !errors.Is(err, agent.ErrBuilderBuilt) {
		t.Fatalf("second Build() error = %v", err)
	}
}

func TestBuilderValidatesModelOptions(t *testing.T) {
	nan := math.NaN()
	positiveInfinity := math.Inf(1)
	zero := 0
	negative := -1
	unknownReasoning := model.ReasoningLevel(255)

	tests := []struct {
		name    string
		options agent.ModelOptions
	}{
		{name: "NaN temperature", options: agent.ModelOptions{Temperature: &nan}},
		{name: "infinite temperature", options: agent.ModelOptions{Temperature: &positiveInfinity}},
		{name: "zero max tokens", options: agent.ModelOptions{MaxTokens: &zero}},
		{name: "negative max tokens", options: agent.ModelOptions{MaxTokens: &negative}},
		{name: "unknown reasoning", options: agent.ModelOptions{Reasoning: &unknownReasoning}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			builder := agent.NewBuilder()
			if err := builder.SetModelOptions(test.options); !errors.Is(err, agent.ErrInvalidOptions) {
				t.Fatalf("SetModelOptions() error = %v, want ErrInvalidOptions", err)
			}
			if err := builder.SetModelOptions(agent.ModelOptions{}); err != nil {
				t.Fatalf("valid SetModelOptions() after failure error = %v", err)
			}
		})
	}

	negativeTemperature := -100.0
	if err := agent.NewBuilder().SetModelOptions(agent.ModelOptions{Temperature: &negativeTemperature}); err != nil {
		t.Fatalf("provider-independent finite temperature rejected: %v", err)
	}
}

func TestBuilderSnapshotsDefaultsAndRunInput(t *testing.T) {
	defaultTemperature := 0.2
	defaultMaxTokens := 100
	defaultReasoning := model.ReasoningLow
	var captured model.Request
	llm := &fakeLLM{generate: func(_ context.Context, req model.Request) (model.Stream, error) {
		captured = req
		return doneModelStream("ok"), nil
	}}

	builder := agent.NewBuilder()
	if err := builder.UseLLM(llm); err != nil {
		t.Fatalf("UseLLM() error = %v", err)
	}
	if err := builder.UseRunStrategy(agent.NewSingleTurnStrategy()); err != nil {
		t.Fatalf("UseRunStrategy() error = %v", err)
	}
	if err := builder.SetSystemPrompt("system instruction"); err != nil {
		t.Fatalf("SetSystemPrompt() error = %v", err)
	}
	if err := builder.SetModelOptions(agent.ModelOptions{
		Temperature: &defaultTemperature,
		MaxTokens:   &defaultMaxTokens,
		Reasoning:   &defaultReasoning,
	}); err != nil {
		t.Fatalf("SetModelOptions() error = %v", err)
	}
	value, err := builder.Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	defaultTemperature = 9
	defaultMaxTokens = 9
	defaultReasoning = model.ReasoningHigh
	overrideTemperature := 0.8
	signature := "request-signature"
	arguments := []byte(`{"city":"shanghai"}`)
	req := agent.Request{
		Messages: []model.Message{{
			Role: model.RoleUser,
			Content: []model.ContentBlock{
				{Kind: model.ContentThinking, Text: "private", Signature: &signature},
				{Kind: model.ContentToolCall, ToolCall: &model.ToolCall{ID: "call", Name: "weather", Arguments: arguments}},
			},
		}},
		Options: agent.ModelOptions{Temperature: &overrideTemperature},
	}
	if _, err := value.Run(context.Background(), req); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	overrideTemperature = 4
	signature = "mutated"
	arguments[0] = '['
	req.Messages[0].Content[0].Text = "mutated"
	req.Messages[0].Content[1].ToolCall.Name = "mutated"

	if captured.Context.SystemPrompt != "system instruction" || len(captured.Context.Tools) != 0 {
		t.Fatalf("model context = %+v", captured.Context)
	}
	if captured.Temperature == nil || *captured.Temperature != 0.8 {
		t.Fatalf("temperature = %v, want 0.8", captured.Temperature)
	}
	if captured.MaxTokens == nil || *captured.MaxTokens != 100 {
		t.Fatalf("max tokens = %v, want inherited 100", captured.MaxTokens)
	}
	if captured.Reasoning == nil || *captured.Reasoning != model.ReasoningLow {
		t.Fatalf("reasoning = %v, want inherited low", captured.Reasoning)
	}
	if captured.Context.Messages[0].Content[0].Text != "private" || *captured.Context.Messages[0].Content[0].Signature != "request-signature" {
		t.Fatalf("thinking block was not snapshotted: %+v", captured.Context.Messages[0].Content[0])
	}
	call := captured.Context.Messages[0].Content[1].ToolCall
	if call.Name != "weather" || string(call.Arguments) != `{"city":"shanghai"}` {
		t.Fatalf("tool call was not snapshotted: %+v", call)
	}
}

func TestBuilderUsesInjectedStrategyWithNormalizedRunInput(t *testing.T) {
	llm := &fakeLLM{descriptor: model.Model{ID: "injected-model"}, generate: func(context.Context, model.Request) (model.Stream, error) {
		t.Fatal("LLM should be called by the strategy, not configuredAgent")
		return nil, nil
	}}
	defaultMaxTokens := 200
	overrideTemperature := 0.6
	signature := "signature"
	arguments := []byte(`{"name":"acore"}`)
	var captured agent.RunInput
	strategy := &fakeRunStrategy{run: func(_ context.Context, input agent.RunInput) (agent.Stream, error) {
		captured = input
		return successfulAgentStream("strategy-result"), nil
	}}

	builder := agent.NewBuilder()
	if err := builder.UseLLM(llm); err != nil {
		t.Fatalf("UseLLM() error = %v", err)
	}
	if err := builder.UseRunStrategy(strategy); err != nil {
		t.Fatalf("UseRunStrategy() error = %v", err)
	}
	if err := builder.SetSystemPrompt("shared system prompt"); err != nil {
		t.Fatalf("SetSystemPrompt() error = %v", err)
	}
	if err := builder.SetModelOptions(agent.ModelOptions{MaxTokens: &defaultMaxTokens}); err != nil {
		t.Fatalf("SetModelOptions() error = %v", err)
	}
	value, err := builder.Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	req := agent.Request{
		Messages: []model.Message{{
			Role: model.RoleUser,
			Content: []model.ContentBlock{
				{Kind: model.ContentThinking, Text: "think", Signature: &signature},
				{Kind: model.ContentToolCall, ToolCall: &model.ToolCall{Name: "tool", Arguments: arguments}},
			},
		}},
		Options: agent.ModelOptions{Temperature: &overrideTemperature},
	}
	stream, err := value.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if _, err := collect(stream); err != nil {
		t.Fatalf("stream error = %v", err)
	}

	defaultMaxTokens = 1
	overrideTemperature = 9
	signature = "mutated"
	arguments[0] = '['
	req.Messages[0].Content[0].Text = "mutated"

	if captured.LLM != llm {
		t.Fatal("RunInput LLM is not the configured component")
	}
	if captured.SystemPrompt != "shared system prompt" {
		t.Fatalf("RunInput system prompt = %q", captured.SystemPrompt)
	}
	if captured.Request.Options.MaxTokens == nil || *captured.Request.Options.MaxTokens != 200 {
		t.Fatalf("RunInput max tokens = %v, want inherited 200", captured.Request.Options.MaxTokens)
	}
	if captured.Request.Options.Temperature == nil || *captured.Request.Options.Temperature != 0.6 {
		t.Fatalf("RunInput temperature = %v, want override 0.6", captured.Request.Options.Temperature)
	}
	thinking := captured.Request.Messages[0].Content[0]
	if thinking.Text != "think" || thinking.Signature == nil || *thinking.Signature != "signature" {
		t.Fatalf("RunInput thinking block was not snapshotted: %+v", thinking)
	}
	call := captured.Request.Messages[0].Content[1].ToolCall
	if call == nil || call.Name != "tool" || string(call.Arguments) != `{"name":"acore"}` {
		t.Fatalf("RunInput tool call was not snapshotted: %+v", call)
	}
}
