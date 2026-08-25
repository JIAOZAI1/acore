package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/JIAOZAI1/acore/agent"
	"github.com/JIAOZAI1/acore/model"
	"github.com/JIAOZAI1/acore/tool"
)

type fakeToolService struct {
	specs    []tool.Spec
	execute  func(context.Context, tool.Call) (tool.Result, error)
	specCall int
}

func (f *fakeToolService) Specs() []tool.Spec {
	f.specCall++
	return f.specs
}

func (f *fakeToolService) Execute(ctx context.Context, call tool.Call) (tool.Result, error) {
	if f.execute == nil {
		return tool.Result{}, nil
	}
	return f.execute(ctx, call)
}

var _ tool.Service = (*fakeToolService)(nil)

func TestToolLoopBuilderRequiresToolServiceAndRecovers(t *testing.T) {
	builder := agent.NewToolLoopBuilder()
	if _, err := builder.Build(); !errors.Is(err, agent.ErrMissingToolService) {
		t.Fatalf("Build() error = %v, want ErrMissingToolService", err)
	}
	if err := builder.UseTools(nil); !errors.Is(err, agent.ErrNilToolService) {
		t.Fatalf("UseTools(nil) error = %v, want ErrNilToolService", err)
	}
	var typedNil *fakeToolService
	if err := builder.UseTools(typedNil); !errors.Is(err, agent.ErrNilToolService) {
		t.Fatalf("UseTools(typed nil) error = %v, want ErrNilToolService", err)
	}

	service := &fakeToolService{}
	if err := builder.UseTools(service); err != nil {
		t.Fatalf("UseTools() error = %v", err)
	}
	if err := builder.UseTools(service); !errors.Is(err, agent.ErrToolServiceAlreadySet) {
		t.Fatalf("second UseTools() error = %v, want ErrToolServiceAlreadySet", err)
	}
	strategy, err := builder.Build()
	if err != nil {
		t.Fatalf("Build() after failed Build error = %v", err)
	}
	if strategy == nil {
		t.Fatal("Build() strategy is nil")
	}
	if service.specCall != 1 {
		t.Fatalf("Specs() calls = %d, want 1", service.specCall)
	}
}

func TestToolLoopBuilderValidatesConfiguration(t *testing.T) {
	limitsTests := []struct {
		name   string
		limits agent.ToolLoopLimits
	}{
		{name: "zero turns", limits: agent.ToolLoopLimits{MaxToolCalls: 1, MaxToolResultBytes: 1}},
		{name: "zero calls", limits: agent.ToolLoopLimits{MaxModelTurns: 1, MaxToolResultBytes: 1}},
		{name: "zero result bytes", limits: agent.ToolLoopLimits{MaxModelTurns: 1, MaxToolCalls: 1}},
		{name: "negative", limits: agent.ToolLoopLimits{MaxModelTurns: -1, MaxToolCalls: 1, MaxToolResultBytes: 1}},
	}
	for _, test := range limitsTests {
		t.Run(test.name, func(t *testing.T) {
			builder := agent.NewToolLoopBuilder()
			if err := builder.SetLimits(test.limits); !errors.Is(err, agent.ErrInvalidToolLoopLimits) {
				t.Fatalf("SetLimits() error = %v, want ErrInvalidToolLoopLimits", err)
			}
			if err := builder.SetLimits(agent.DefaultToolLoopLimits()); err != nil {
				t.Fatalf("valid SetLimits() after failure error = %v", err)
			}
		})
	}

	builder := agent.NewToolLoopBuilder()
	if err := builder.SetLimits(agent.DefaultToolLoopLimits()); err != nil {
		t.Fatalf("SetLimits() error = %v", err)
	}
	if err := builder.SetLimits(agent.DefaultToolLoopLimits()); !errors.Is(err, agent.ErrConfigAlreadySet) {
		t.Fatalf("second SetLimits() error = %v, want ErrConfigAlreadySet", err)
	}
	if err := builder.SetToolErrorMode(agent.ToolErrorMode(255)); !errors.Is(err, agent.ErrInvalidToolErrorMode) {
		t.Fatalf("SetToolErrorMode(invalid) error = %v, want ErrInvalidToolErrorMode", err)
	}
	if err := builder.SetToolErrorMode(agent.ToolErrorModeFailFast); err != nil {
		t.Fatalf("SetToolErrorMode() after failure error = %v", err)
	}
	if err := builder.SetToolErrorMode(agent.ToolErrorModeFeedback); !errors.Is(err, agent.ErrConfigAlreadySet) {
		t.Fatalf("second SetToolErrorMode() error = %v, want ErrConfigAlreadySet", err)
	}

	if got := agent.DefaultToolLoopLimits(); got != (agent.ToolLoopLimits{MaxModelTurns: 8, MaxToolCalls: 32, MaxToolResultBytes: 64 * 1024}) {
		t.Fatalf("DefaultToolLoopLimits() = %+v", got)
	}
	modeNames := map[agent.ToolErrorMode]string{
		agent.ToolErrorModeFeedback: "feedback",
		agent.ToolErrorModeFailFast: "failFast",
		agent.ToolErrorMode(255):    "unknown",
	}
	for mode, want := range modeNames {
		if got := mode.String(); got != want {
			t.Errorf("ToolErrorMode(%d).String() = %q, want %q", mode, got, want)
		}
	}
}

func TestToolLoopBuilderValidatesAndSnapshotsCatalog(t *testing.T) {
	tests := []struct {
		name  string
		specs []tool.Spec
	}{
		{name: "empty name", specs: []tool.Spec{{Parameters: json.RawMessage(`{}`)}}},
		{name: "duplicate name", specs: []tool.Spec{{Name: "same", Parameters: json.RawMessage(`{}`)}, {Name: "same", Parameters: json.RawMessage(`{}`)}}},
		{name: "empty schema", specs: []tool.Spec{{Name: "bad"}}},
		{name: "invalid schema", specs: []tool.Spec{{Name: "bad", Parameters: json.RawMessage(`{`)}}},
		{name: "array schema", specs: []tool.Spec{{Name: "bad", Parameters: json.RawMessage(`[]`)}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			builder := agent.NewToolLoopBuilder()
			if err := builder.UseTools(&fakeToolService{specs: test.specs}); err != nil {
				t.Fatalf("UseTools() error = %v", err)
			}
			if _, err := builder.Build(); !errors.Is(err, agent.ErrInvalidToolCatalog) {
				t.Fatalf("Build() error = %v, want ErrInvalidToolCatalog", err)
			}
		})
	}

	schema := json.RawMessage(`{"type":"object"}`)
	service := &fakeToolService{specs: []tool.Spec{
		{Name: "first", Description: "one", Parameters: schema},
		{Name: "second", Description: "two", Parameters: json.RawMessage(`{"type":"object","properties":{}}`)},
	}}
	builder := agent.NewToolLoopBuilder()
	if err := builder.UseTools(service); err != nil {
		t.Fatalf("UseTools() error = %v", err)
	}
	strategy, err := builder.Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	schema[0] = '['
	service.specs[0].Name = "mutated"

	var request model.Request
	llm := &fakeLLM{generate: func(_ context.Context, req model.Request) (model.Stream, error) {
		request = req
		return doneModelStream("done"), nil
	}}
	stream, err := strategy.Run(context.Background(), agent.RunInput{
		LLM:     llm,
		Request: agent.Request{Messages: userMessages("hello")},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if _, err := collect(stream); err != nil {
		t.Fatalf("stream error = %v", err)
	}
	if len(request.Context.Tools) != 2 || request.Context.Tools[0].Name != "first" || request.Context.Tools[1].Name != "second" {
		t.Fatalf("request tools = %+v", request.Context.Tools)
	}
	if got := string(request.Context.Tools[0].Parameters); got != `{"type":"object"}` {
		t.Fatalf("snapshotted schema = %s", got)
	}
}

func TestToolLoopBuilderFreezesAfterSuccessfulBuild(t *testing.T) {
	builder := agent.NewToolLoopBuilder()
	service := &fakeToolService{}
	if err := builder.UseTools(service); err != nil {
		t.Fatalf("UseTools() error = %v", err)
	}
	if _, err := builder.Build(); err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	if err := builder.UseTools(service); !errors.Is(err, agent.ErrToolLoopBuilderBuilt) {
		t.Fatalf("UseTools() after Build error = %v", err)
	}
	if err := builder.SetLimits(agent.DefaultToolLoopLimits()); !errors.Is(err, agent.ErrToolLoopBuilderBuilt) {
		t.Fatalf("SetLimits() after Build error = %v", err)
	}
	if err := builder.SetToolErrorMode(agent.ToolErrorModeFeedback); !errors.Is(err, agent.ErrToolLoopBuilderBuilt) {
		t.Fatalf("SetToolErrorMode() after Build error = %v", err)
	}
	if _, err := builder.Build(); !errors.Is(err, agent.ErrToolLoopBuilderBuilt) {
		t.Fatalf("second Build() error = %v", err)
	}
}
