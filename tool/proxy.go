package tool

import (
	"context"
	"encoding/json"

	"github.com/JIAOZAI1/acore/internal/clone"
)

type invocationToken struct {
	marker byte
}

// Invocation is an immutable, resolved invocation passed through proxies.
// Accessors return copies of slice-backed values. WithArguments is the only
// supported way to replace arguments before calling Next.
type Invocation struct {
	id        string
	name      string
	arguments json.RawMessage
	spec      Spec
	tool      Tool
	token     *invocationToken
}

// ID returns the caller-provided invocation identifier, when available.
func (i Invocation) ID() string { return i.id }

// Name returns the resolved tool name.
func (i Invocation) Name() string { return i.name }

// Arguments returns a copy of the invocation arguments.
func (i Invocation) Arguments() json.RawMessage {
	return clone.Slice(i.arguments)
}

// Spec returns a copy of the resolved tool descriptor.
func (i Invocation) Spec() Spec { return cloneSpec(i.spec) }

// WithArguments returns an invocation with replacement arguments. The
// execution terminal validates the replacement before invoking the Tool.
func (i Invocation) WithArguments(arguments json.RawMessage) Invocation {
	i.arguments = clone.Slice(arguments)
	return i
}

// Next is the remainder of a proxy chain. A proxy may skip it to short-circuit
// execution or invoke it multiple times as part of an explicit bounded policy.
type Next interface {
	Execute(context.Context, Invocation) (Result, error)
}

// NextFunc adapts a function to Next.
type NextFunc func(context.Context, Invocation) (Result, error)

// Execute calls f.
func (f NextFunc) Execute(ctx context.Context, invocation Invocation) (Result, error) {
	return f(ctx, invocation)
}

// Proxy wraps every successfully resolved tool invocation. Implementations
// reused by a System must be safe for concurrent calls.
type Proxy interface {
	Execute(context.Context, Invocation, Next) (Result, error)
}
