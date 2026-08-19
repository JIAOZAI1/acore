package tool_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
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
	id      string
	execute func(context.Context, tool.Invocation, tool.Next) (tool.Result, error)
}

func (p *fakeProxy) ID() string { return p.id }

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

func buildSystem(t *testing.T, tools []tool.Tool, proxies []tool.Proxy) *tool.System {
	t.Helper()
	builder := tool.NewBuilder()
	for _, value := range tools {
		if err := builder.AddTool(value); err != nil {
			t.Fatalf("AddTool() error = %v", err)
		}
	}
	for _, proxy := range proxies {
		if err := builder.UseProxy(proxy); err != nil {
			t.Fatalf("UseProxy() error = %v", err)
		}
	}
	system, err := builder.Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	return system
}

func TestSystemExecutesTool(t *testing.T) {
	arguments := json.RawMessage(`{"query":"acore"}`)
	var received json.RawMessage
	search := newFakeTool("search", func(_ context.Context, input json.RawMessage) (tool.Result, error) {
		received = append(json.RawMessage(nil), input...)
		input[0] = '['
		return tool.Result{Content: "found"}, nil
	})
	system := buildSystem(t, []tool.Tool{search}, nil)

	result, err := system.Execute(context.Background(), tool.Call{
		CallID: "call-1", Name: "search", Arguments: arguments,
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Content != "found" || string(received) != `{"query":"acore"}` {
		t.Fatalf("result = %+v, arguments = %s", result, received)
	}
	if string(arguments) != `{"query":"acore"}` {
		t.Fatalf("Execute() allowed Tool to mutate caller arguments: %s", arguments)
	}
}

func TestSystemExecutesProxiesInRegistrationOrder(t *testing.T) {
	var order []string
	proxy := func(id string) tool.Proxy {
		return &fakeProxy{id: id, execute: func(ctx context.Context, invocation tool.Invocation, next tool.Next) (tool.Result, error) {
			order = append(order, id+":before")
			result, err := next.Execute(ctx, invocation)
			order = append(order, id+":after")
			return result, err
		}}
	}
	value := newFakeTool("ordered", func(context.Context, json.RawMessage) (tool.Result, error) {
		order = append(order, "tool")
		return tool.Result{Content: "ok"}, nil
	})
	system := buildSystem(t, []tool.Tool{value}, []tool.Proxy{proxy("a"), proxy("b"), proxy("c")})

	_, err := system.Execute(context.Background(), tool.Call{Name: "ordered", Arguments: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	want := []string{"a:before", "b:before", "c:before", "tool", "c:after", "b:after", "a:after"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
}

func TestProxyCanInspectTransformAndShortCircuit(t *testing.T) {
	var calls int
	value := newFakeTool("transform", func(_ context.Context, arguments json.RawMessage) (tool.Result, error) {
		calls++
		return tool.Result{Content: string(arguments)}, nil
	})
	transform := &fakeProxy{id: "transform", execute: func(ctx context.Context, invocation tool.Invocation, next tool.Next) (tool.Result, error) {
		if invocation.CallID() != "call-2" || invocation.Name() != "transform" || invocation.Spec().Name != "transform" {
			t.Fatalf("unexpected invocation: call=%q name=%q spec=%q", invocation.CallID(), invocation.Name(), invocation.Spec().Name)
		}
		return next.Execute(ctx, invocation.WithArguments(json.RawMessage(`{"normalized":true}`)))
	}}
	system := buildSystem(t, []tool.Tool{value}, []tool.Proxy{transform})
	result, err := system.Execute(context.Background(), tool.Call{
		CallID: "call-2", Name: "transform", Arguments: json.RawMessage(`{"raw":true}`),
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Content != `{"normalized":true}` || calls != 1 {
		t.Fatalf("result = %q, calls = %d", result.Content, calls)
	}

	shortCircuit := &fakeProxy{id: "cache", execute: func(context.Context, tool.Invocation, tool.Next) (tool.Result, error) {
		return tool.Result{Content: "cached"}, nil
	}}
	system = buildSystem(t, []tool.Tool{value}, []tool.Proxy{shortCircuit})
	result, err = system.Execute(context.Background(), tool.Call{Name: "transform", Arguments: json.RawMessage(`{}`)})
	if err != nil || result.Content != "cached" || calls != 1 {
		t.Fatalf("short circuit result = %+v, error = %v, calls = %d", result, err, calls)
	}
}

func TestProxyCanImplementBoundedRetry(t *testing.T) {
	failure := errors.New("temporary")
	var calls int
	value := newFakeTool("retry", func(context.Context, json.RawMessage) (tool.Result, error) {
		calls++
		if calls == 1 {
			return tool.Result{}, failure
		}
		return tool.Result{Content: "ok"}, nil
	})
	retry := &fakeProxy{id: "retry", execute: func(ctx context.Context, invocation tool.Invocation, next tool.Next) (tool.Result, error) {
		result, err := next.Execute(ctx, invocation)
		if err == nil {
			return result, nil
		}
		return next.Execute(ctx, invocation)
	}}
	system := buildSystem(t, []tool.Tool{value}, []tool.Proxy{retry})

	result, err := system.Execute(context.Background(), tool.Call{Name: "retry", Arguments: json.RawMessage(`{}`)})
	if err != nil || result.Content != "ok" || calls != 2 {
		t.Fatalf("result = %+v, error = %v, calls = %d", result, err, calls)
	}
}

func TestSystemSpecsAreSortedCopies(t *testing.T) {
	first := newFakeTool("zeta", func(context.Context, json.RawMessage) (tool.Result, error) { return tool.Result{}, nil })
	second := newFakeTool("alpha", func(context.Context, json.RawMessage) (tool.Result, error) { return tool.Result{}, nil })
	system := buildSystem(t, []tool.Tool{first, second}, nil)

	specs := system.Specs()
	if len(specs) != 2 || specs[0].Name != "alpha" || specs[1].Name != "zeta" {
		t.Fatalf("Specs() = %+v", specs)
	}
	specs[0].Parameters[0] = '['
	if string(system.Specs()[0].Parameters) != `{"type":"object"}` {
		t.Fatal("Specs() exposed internal parameter schema")
	}
}

func TestBuilderRejectsInvalidRegistrationsAndFreezes(t *testing.T) {
	builder := tool.NewBuilder()
	var nilTool *fakeTool
	if err := builder.AddTool(nilTool); !errors.Is(err, tool.ErrNilTool) {
		t.Fatalf("AddTool(nil) error = %v", err)
	}
	if err := builder.AddTool(newFakeTool("", func(context.Context, json.RawMessage) (tool.Result, error) { return tool.Result{}, nil })); !errors.Is(err, tool.ErrEmptyToolName) {
		t.Fatalf("AddTool(empty name) error = %v", err)
	}
	invalidSchema := newFakeTool("invalid", func(context.Context, json.RawMessage) (tool.Result, error) { return tool.Result{}, nil })
	invalidSchema.spec.Parameters = json.RawMessage(`{`)
	if err := builder.AddTool(invalidSchema); !errors.Is(err, tool.ErrInvalidSchema) {
		t.Fatalf("AddTool(invalid schema) error = %v", err)
	}
	valid := newFakeTool("valid", func(context.Context, json.RawMessage) (tool.Result, error) { return tool.Result{}, nil })
	if err := builder.AddTool(valid); err != nil {
		t.Fatal(err)
	}
	if err := builder.AddTool(valid); !errors.Is(err, tool.ErrDuplicateTool) {
		t.Fatalf("AddTool(duplicate) error = %v", err)
	}

	var nilProxy *fakeProxy
	if err := builder.UseProxy(nilProxy); !errors.Is(err, tool.ErrNilProxy) {
		t.Fatalf("UseProxy(nil) error = %v", err)
	}
	emptyID := &fakeProxy{execute: func(ctx context.Context, invocation tool.Invocation, next tool.Next) (tool.Result, error) {
		return next.Execute(ctx, invocation)
	}}
	if err := builder.UseProxy(emptyID); !errors.Is(err, tool.ErrEmptyProxyID) {
		t.Fatalf("UseProxy(empty ID) error = %v", err)
	}
	proxy := &fakeProxy{id: "proxy", execute: emptyID.execute}
	if err := builder.UseProxy(proxy); err != nil {
		t.Fatal(err)
	}
	if err := builder.UseProxy(proxy); !errors.Is(err, tool.ErrDuplicateProxy) {
		t.Fatalf("UseProxy(duplicate) error = %v", err)
	}

	if _, err := builder.Build(); err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if err := builder.AddTool(newFakeTool("late", valid.execute)); !errors.Is(err, tool.ErrBuilderBuilt) {
		t.Fatalf("AddTool() after Build error = %v", err)
	}
	if err := builder.UseProxy(&fakeProxy{id: "late", execute: emptyID.execute}); !errors.Is(err, tool.ErrBuilderBuilt) {
		t.Fatalf("UseProxy() after Build error = %v", err)
	}
	if _, err := builder.Build(); !errors.Is(err, tool.ErrBuilderBuilt) {
		t.Fatalf("second Build() error = %v", err)
	}
}

func TestSystemRejectsInvalidCallsAndPropagatesErrors(t *testing.T) {
	toolFailure := errors.New("failed")
	value := newFakeTool("failure", func(context.Context, json.RawMessage) (tool.Result, error) {
		return tool.Result{}, toolFailure
	})
	system := buildSystem(t, []tool.Tool{value}, nil)

	tests := []struct {
		name string
		call tool.Call
		want error
	}{
		{name: "empty name", call: tool.Call{Arguments: json.RawMessage(`{}`)}, want: tool.ErrEmptyToolName},
		{name: "invalid arguments", call: tool.Call{Name: "failure", Arguments: json.RawMessage(`{`)}, want: tool.ErrInvalidArguments},
		{name: "not found", call: tool.Call{Name: "missing", Arguments: json.RawMessage(`{}`)}, want: tool.ErrToolNotFound},
		{name: "tool error", call: tool.Call{Name: "failure", Arguments: json.RawMessage(`{}`)}, want: toolFailure},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := system.Execute(context.Background(), test.call); !errors.Is(err, test.want) {
				t.Fatalf("Execute() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestSystemHonorsCancellationAndConcurrentExecution(t *testing.T) {
	var calls atomic.Int64
	value := newFakeTool("concurrent", func(context.Context, json.RawMessage) (tool.Result, error) {
		calls.Add(1)
		return tool.Result{Content: "ok"}, nil
	})
	system := buildSystem(t, []tool.Tool{value}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := system.Execute(ctx, tool.Call{Name: "concurrent", Arguments: json.RawMessage(`{}`)}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Execute() error = %v", err)
	}

	const workers = 32
	var waitGroup sync.WaitGroup
	errorsByCall := make(chan error, workers)
	for range workers {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			_, err := system.Execute(context.Background(), tool.Call{Name: "concurrent", Arguments: json.RawMessage(`{}`)})
			errorsByCall <- err
		}()
	}
	waitGroup.Wait()
	close(errorsByCall)
	for err := range errorsByCall {
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
	}
	if calls.Load() != workers {
		t.Fatalf("calls = %d, want %d", calls.Load(), workers)
	}
}
