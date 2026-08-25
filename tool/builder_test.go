package tool_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/JIAOZAI1/acore/tool"
)

type fakeTool struct {
	spec    tool.Spec
	execute func(context.Context, json.RawMessage) (tool.Result, error)
}

func (f *fakeTool) Spec() tool.Spec { return f.spec }

func (f *fakeTool) Execute(ctx context.Context, arguments json.RawMessage) (tool.Result, error) {
	return f.execute(ctx, arguments)
}

type fakeProxy struct {
	execute func(context.Context, tool.Invocation, tool.Next) (tool.Result, error)
}

func (p *fakeProxy) Execute(ctx context.Context, invocation tool.Invocation, next tool.Next) (tool.Result, error) {
	return p.execute(ctx, invocation, next)
}

func newFakeTool(name string, execute func(context.Context, json.RawMessage) (tool.Result, error)) *fakeTool {
	return &fakeTool{
		spec: tool.Spec{
			Name:       name,
			Parameters: json.RawMessage(`{"type":"object"}`),
		},
		execute: execute,
	}
}

func TestBuilderBuildsEmptySystem(t *testing.T) {
	system, err := tool.NewBuilder().Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if specs := system.Specs(); len(specs) != 0 {
		t.Fatalf("Specs() length = %d, want 0", len(specs))
	}
}

func TestBuilderPreservesOrderAndSnapshotsSpecs(t *testing.T) {
	builder := tool.NewBuilder()
	first := newFakeTool("zeta", func(context.Context, json.RawMessage) (tool.Result, error) {
		return tool.Result{}, nil
	})
	second := newFakeTool("alpha", func(context.Context, json.RawMessage) (tool.Result, error) {
		return tool.Result{}, nil
	})
	if err := builder.AddTool(first); err != nil {
		t.Fatalf("AddTool(first) error = %v", err)
	}
	if err := builder.AddTool(second); err != nil {
		t.Fatalf("AddTool(second) error = %v", err)
	}

	first.spec.Name = "changed"
	first.spec.Parameters[0] = '['
	system, err := builder.Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	specs := system.Specs()
	if len(specs) != 2 || specs[0].Name != "zeta" || specs[1].Name != "alpha" {
		t.Fatalf("Specs() = %+v, want zeta then alpha", specs)
	}
	if string(specs[0].Parameters) != `{"type":"object"}` {
		t.Fatalf("snapshotted schema = %s", specs[0].Parameters)
	}
	specs[0].Name = "mutated"
	specs[0].Parameters[0] = '['
	got := system.Specs()[0]
	if got.Name != "zeta" || string(got.Parameters) != `{"type":"object"}` {
		t.Fatalf("Specs() exposed internal state: %+v", got)
	}
}

func TestBuilderRejectsInvalidTools(t *testing.T) {
	execute := func(context.Context, json.RawMessage) (tool.Result, error) {
		return tool.Result{}, nil
	}

	tests := []struct {
		name string
		tool tool.Tool
		want error
	}{
		{name: "nil", tool: nil, want: tool.ErrNilTool},
		{name: "typed nil", tool: (*fakeTool)(nil), want: tool.ErrNilTool},
		{name: "empty name", tool: newFakeTool("", execute), want: tool.ErrEmptyToolName},
		{name: "empty schema", tool: &fakeTool{spec: tool.Spec{Name: "empty"}, execute: execute}, want: tool.ErrInvalidSchema},
		{name: "invalid JSON", tool: &fakeTool{spec: tool.Spec{Name: "invalid", Parameters: json.RawMessage(`{`)}, execute: execute}, want: tool.ErrInvalidSchema},
		{name: "array schema", tool: &fakeTool{spec: tool.Spec{Name: "array", Parameters: json.RawMessage(`[]`)}, execute: execute}, want: tool.ErrInvalidSchema},
		{name: "null schema", tool: &fakeTool{spec: tool.Spec{Name: "null", Parameters: json.RawMessage(`null`)}, execute: execute}, want: tool.ErrInvalidSchema},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := tool.NewBuilder().AddTool(test.tool); !errors.Is(err, test.want) {
				t.Fatalf("AddTool() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestBuilderRejectsDuplicatesAndNilProxy(t *testing.T) {
	builder := tool.NewBuilder()
	value := newFakeTool("duplicate", func(context.Context, json.RawMessage) (tool.Result, error) {
		return tool.Result{}, nil
	})
	if err := builder.AddTool(value); err != nil {
		t.Fatalf("AddTool() error = %v", err)
	}
	if err := builder.AddTool(value); !errors.Is(err, tool.ErrDuplicateTool) {
		t.Fatalf("duplicate AddTool() error = %v", err)
	}

	var nilProxy *fakeProxy
	if err := builder.UseProxy(nilProxy); !errors.Is(err, tool.ErrNilProxy) {
		t.Fatalf("UseProxy(nil) error = %v", err)
	}

	proxy := &fakeProxy{execute: func(ctx context.Context, invocation tool.Invocation, next tool.Next) (tool.Result, error) {
		return next.Execute(ctx, invocation)
	}}
	if err := builder.UseProxy(proxy); err != nil {
		t.Fatalf("first UseProxy() error = %v", err)
	}
	if err := builder.UseProxy(proxy); err != nil {
		t.Fatalf("second UseProxy() error = %v, duplicate proxy registration should be allowed", err)
	}
}

func TestBuilderFreezesAfterSuccessfulBuild(t *testing.T) {
	builder := tool.NewBuilder()
	if _, err := builder.Build(); err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	value := newFakeTool("late", func(context.Context, json.RawMessage) (tool.Result, error) {
		return tool.Result{}, nil
	})
	proxy := &fakeProxy{execute: func(ctx context.Context, invocation tool.Invocation, next tool.Next) (tool.Result, error) {
		return next.Execute(ctx, invocation)
	}}
	if err := builder.AddTool(value); !errors.Is(err, tool.ErrBuilderBuilt) {
		t.Fatalf("AddTool() after Build error = %v", err)
	}
	if err := builder.UseProxy(proxy); !errors.Is(err, tool.ErrBuilderBuilt) {
		t.Fatalf("UseProxy() after Build error = %v", err)
	}
	if _, err := builder.Build(); !errors.Is(err, tool.ErrBuilderBuilt) {
		t.Fatalf("second Build() error = %v", err)
	}
}
