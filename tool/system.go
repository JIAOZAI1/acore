package tool

import (
	"context"
	"fmt"

	"github.com/JIAOZAI1/acore/internal/clone"
	"github.com/JIAOZAI1/acore/internal/jsoncheck"
	"github.com/JIAOZAI1/acore/internal/nilcheck"
)

// System is an immutable tool catalog and execution service. Every
// successfully resolved invocation traverses its configured proxy chain unless
// a proxy deliberately short-circuits execution.
type System struct {
	tools   map[string]registeredTool
	specs   []Spec
	proxies []Proxy
}

// Specs returns tool descriptors in registration order. Returned schemas are
// copies and may be modified by the caller.
func (s *System) Specs() []Spec {
	specs := make([]Spec, 0, len(s.specs))
	for _, spec := range s.specs {
		specs = append(specs, cloneSpec(spec))
	}
	return specs
}

// Execute validates and resolves a Call before sending it through the proxy
// chain.
func (s *System) Execute(ctx context.Context, call Call) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if call.Name == "" {
		return Result{}, ErrEmptyToolName
	}
	if !jsoncheck.IsObject(call.Arguments) {
		return Result{}, fmt.Errorf("%w: %s", ErrInvalidArguments, call.Name)
	}

	registered, exists := s.tools[call.Name]
	if !exists {
		return Result{}, fmt.Errorf("%w: %s", ErrToolNotFound, call.Name)
	}

	invocation := Invocation{
		id:        call.ID,
		name:      call.Name,
		arguments: clone.Slice(call.Arguments),
		spec:      cloneSpec(registered.spec),
		tool:      registered.tool,
		token:     &invocationToken{marker: 1},
	}
	return s.executeAt(ctx, 0, invocation)
}

func (s *System) executeAt(ctx context.Context, index int, invocation Invocation) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if index == len(s.proxies) {
		return executeTool(ctx, invocation)
	}

	expectedToken := invocation.token
	next := NextFunc(func(nextCtx context.Context, nextInvocation Invocation) (Result, error) {
		if expectedToken == nil || nextInvocation.token != expectedToken {
			return Result{}, ErrInvalidInvocation
		}
		return s.executeAt(nextCtx, index+1, nextInvocation)
	})

	result, err := s.proxies[index].Execute(ctx, invocation, next)
	if err != nil {
		return Result{}, fmt.Errorf("tool: proxy %d execute %q: %w", index, invocation.name, err)
	}
	return result, nil
}

func executeTool(ctx context.Context, invocation Invocation) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if invocation.token == nil || invocation.name == "" || invocation.spec.Name != invocation.name || nilcheck.IsNil(invocation.tool) {
		return Result{}, ErrInvalidInvocation
	}
	if !jsoncheck.IsObject(invocation.arguments) {
		return Result{}, fmt.Errorf("%w: %s", ErrInvalidArguments, invocation.name)
	}

	result, err := invocation.tool.Execute(ctx, clone.Slice(invocation.arguments))
	if err != nil {
		return Result{}, fmt.Errorf("tool: execute %q: %w", invocation.name, err)
	}
	return result, nil
}

var _ Service = (*System)(nil)
