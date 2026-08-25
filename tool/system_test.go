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

func TestSystemExecutesToolAndIsolatesArguments(t *testing.T) {
	arguments := json.RawMessage(`{"query":"acore"}`)
	var received json.RawMessage
	search := newFakeTool("search", func(_ context.Context, input json.RawMessage) (tool.Result, error) {
		received = append(json.RawMessage(nil), input...)
		input[0] = '['
		return tool.Result{Content: "found"}, nil
	})
	system := buildSystem(t, []tool.Tool{search}, nil)

	result, err := system.Execute(context.Background(), tool.Call{
		ID: "call-1", Name: "search", Arguments: arguments,
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
	proxy := func(name string) tool.Proxy {
		return &fakeProxy{execute: func(ctx context.Context, invocation tool.Invocation, next tool.Next) (tool.Result, error) {
			order = append(order, name+":before")
			result, err := next.Execute(ctx, invocation)
			order = append(order, name+":after")
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
	transform := &fakeProxy{execute: func(ctx context.Context, invocation tool.Invocation, next tool.Next) (tool.Result, error) {
		if invocation.ID() != "call-2" || invocation.Name() != "transform" || invocation.Spec().Name != "transform" {
			t.Fatalf("unexpected invocation: id=%q name=%q spec=%q", invocation.ID(), invocation.Name(), invocation.Spec().Name)
		}
		arguments := invocation.Arguments()
		arguments[0] = '['
		if string(invocation.Arguments()) != `{"raw":true}` {
			t.Fatal("Arguments() exposed invocation state")
		}
		spec := invocation.Spec()
		spec.Parameters[0] = '['
		if string(invocation.Spec().Parameters) != `{"type":"object"}` {
			t.Fatal("Spec() exposed invocation state")
		}
		return next.Execute(ctx, invocation.WithArguments(json.RawMessage(`{"normalized":true}`)))
	}}
	system := buildSystem(t, []tool.Tool{value}, []tool.Proxy{transform})
	result, err := system.Execute(context.Background(), tool.Call{
		ID: "call-2", Name: "transform", Arguments: json.RawMessage(`{"raw":true}`),
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Content != `{"normalized":true}` || calls != 1 {
		t.Fatalf("result = %q, calls = %d", result.Content, calls)
	}

	shortCircuit := &fakeProxy{execute: func(context.Context, tool.Invocation, tool.Next) (tool.Result, error) {
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
	retry := &fakeProxy{execute: func(ctx context.Context, invocation tool.Invocation, next tool.Next) (tool.Result, error) {
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

func TestSystemRejectsInvalidInvocationFromProxy(t *testing.T) {
	var calls int
	value := newFakeTool("protected", func(context.Context, json.RawMessage) (tool.Result, error) {
		calls++
		return tool.Result{}, nil
	})
	invalid := &fakeProxy{execute: func(ctx context.Context, _ tool.Invocation, next tool.Next) (tool.Result, error) {
		return next.Execute(ctx, tool.Invocation{})
	}}
	system := buildSystem(t, []tool.Tool{value}, []tool.Proxy{invalid})

	_, err := system.Execute(context.Background(), tool.Call{Name: "protected", Arguments: json.RawMessage(`{}`)})
	if !errors.Is(err, tool.ErrInvalidInvocation) {
		t.Fatalf("Execute() error = %v, want ErrInvalidInvocation", err)
	}
	if calls != 0 {
		t.Fatalf("tool calls = %d, want 0", calls)
	}
}

func TestSystemRevalidatesTransformedArguments(t *testing.T) {
	var calls int
	value := newFakeTool("validated", func(context.Context, json.RawMessage) (tool.Result, error) {
		calls++
		return tool.Result{}, nil
	})
	invalid := &fakeProxy{execute: func(ctx context.Context, invocation tool.Invocation, next tool.Next) (tool.Result, error) {
		return next.Execute(ctx, invocation.WithArguments(json.RawMessage(`[]`)))
	}}
	system := buildSystem(t, []tool.Tool{value}, []tool.Proxy{invalid})

	_, err := system.Execute(context.Background(), tool.Call{Name: "validated", Arguments: json.RawMessage(`{}`)})
	if !errors.Is(err, tool.ErrInvalidArguments) {
		t.Fatalf("Execute() error = %v, want ErrInvalidArguments", err)
	}
	if calls != 0 {
		t.Fatalf("tool calls = %d, want 0", calls)
	}
}

func TestSystemRejectsInvalidCallsBeforeProxy(t *testing.T) {
	var proxyCalls int
	proxy := &fakeProxy{execute: func(ctx context.Context, invocation tool.Invocation, next tool.Next) (tool.Result, error) {
		proxyCalls++
		return next.Execute(ctx, invocation)
	}}
	value := newFakeTool("known", func(context.Context, json.RawMessage) (tool.Result, error) {
		return tool.Result{}, nil
	})
	system := buildSystem(t, []tool.Tool{value}, []tool.Proxy{proxy})

	tests := []struct {
		name string
		call tool.Call
		want error
	}{
		{name: "empty name", call: tool.Call{Arguments: json.RawMessage(`{}`)}, want: tool.ErrEmptyToolName},
		{name: "empty arguments", call: tool.Call{Name: "known"}, want: tool.ErrInvalidArguments},
		{name: "invalid JSON", call: tool.Call{Name: "known", Arguments: json.RawMessage(`{`)}, want: tool.ErrInvalidArguments},
		{name: "array arguments", call: tool.Call{Name: "known", Arguments: json.RawMessage(`[]`)}, want: tool.ErrInvalidArguments},
		{name: "null arguments", call: tool.Call{Name: "known", Arguments: json.RawMessage(`null`)}, want: tool.ErrInvalidArguments},
		{name: "not found", call: tool.Call{Name: "missing", Arguments: json.RawMessage(`{}`)}, want: tool.ErrToolNotFound},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := system.Execute(context.Background(), test.call); !errors.Is(err, test.want) {
				t.Fatalf("Execute() error = %v, want %v", err, test.want)
			}
		})
	}
	if proxyCalls != 0 {
		t.Fatalf("proxy calls = %d, want 0", proxyCalls)
	}
}

func TestSystemPreservesToolAndProxyErrors(t *testing.T) {
	toolFailure := errors.New("tool failed")
	proxyFailure := errors.New("proxy failed")
	value := newFakeTool("failure", func(context.Context, json.RawMessage) (tool.Result, error) {
		return tool.Result{Content: "discarded"}, toolFailure
	})

	system := buildSystem(t, []tool.Tool{value}, nil)
	result, err := system.Execute(context.Background(), tool.Call{Name: "failure", Arguments: json.RawMessage(`{}`)})
	if !errors.Is(err, toolFailure) || result != (tool.Result{}) {
		t.Fatalf("tool result = %+v, error = %v", result, err)
	}

	failingProxy := &fakeProxy{execute: func(context.Context, tool.Invocation, tool.Next) (tool.Result, error) {
		return tool.Result{Content: "discarded"}, proxyFailure
	}}
	system = buildSystem(t, []tool.Tool{value}, []tool.Proxy{failingProxy})
	result, err = system.Execute(context.Background(), tool.Call{Name: "failure", Arguments: json.RawMessage(`{}`)})
	if !errors.Is(err, proxyFailure) || result != (tool.Result{}) {
		t.Fatalf("proxy result = %+v, error = %v", result, err)
	}
}

func TestSystemHonorsContextCancellation(t *testing.T) {
	var toolCalls int
	value := newFakeTool("cancel", func(context.Context, json.RawMessage) (tool.Result, error) {
		toolCalls++
		return tool.Result{}, nil
	})

	t.Run("before execution", func(t *testing.T) {
		var proxyCalls int
		proxy := &fakeProxy{execute: func(ctx context.Context, invocation tool.Invocation, next tool.Next) (tool.Result, error) {
			proxyCalls++
			return next.Execute(ctx, invocation)
		}}
		system := buildSystem(t, []tool.Tool{value}, []tool.Proxy{proxy})
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		_, err := system.Execute(ctx, tool.Call{Name: "cancel", Arguments: json.RawMessage(`{}`)})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Execute() error = %v, want context.Canceled", err)
		}
		if proxyCalls != 0 {
			t.Fatalf("proxy calls = %d, want 0", proxyCalls)
		}
	})

	t.Run("between proxies", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancelingProxy := &fakeProxy{execute: func(nextCtx context.Context, invocation tool.Invocation, next tool.Next) (tool.Result, error) {
			cancel()
			return next.Execute(nextCtx, invocation)
		}}
		system := buildSystem(t, []tool.Tool{value}, []tool.Proxy{cancelingProxy})

		_, err := system.Execute(ctx, tool.Call{Name: "cancel", Arguments: json.RawMessage(`{}`)})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Execute() error = %v, want context.Canceled", err)
		}
	})

	if toolCalls != 0 {
		t.Fatalf("tool calls = %d, want 0", toolCalls)
	}
}

func TestSystemSupportsConcurrentExecution(t *testing.T) {
	var calls atomic.Int64
	value := newFakeTool("concurrent", func(context.Context, json.RawMessage) (tool.Result, error) {
		calls.Add(1)
		return tool.Result{Content: "ok"}, nil
	})
	proxy := &fakeProxy{execute: func(ctx context.Context, invocation tool.Invocation, next tool.Next) (tool.Result, error) {
		return next.Execute(ctx, invocation)
	}}
	system := buildSystem(t, []tool.Tool{value}, []tool.Proxy{proxy})

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
