// Package tool defines provider-independent tools and an execution system with
// an immutable proxy chain.
package tool

import (
	"context"
	"encoding/json"
)

// Spec describes a tool that can be presented to a model. Parameters contains
// the JSON Schema accepted by the tool.
type Spec struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters"`
}

// Result is the successful output of one tool invocation. Tool and proxy
// failures use Go errors rather than successful Result values.
type Result struct {
	Content string `json:"content"`
}

// Call identifies one tool invocation requested by a Loop. CallID normally
// corresponds to the model tool-call ID.
type Call struct {
	CallID    string          `json:"callId,omitempty"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// Tool implements one concrete tool. Implementations may be called
// concurrently and must honor context cancellation.
type Tool interface {
	Spec() Spec
	Execute(context.Context, json.RawMessage) (Result, error)
}

// Service is the complete capability visible to a Loop. Proxy registration,
// chain ordering, and concrete Tool values are intentionally hidden.
type Service interface {
	Specs() []Spec
	Execute(context.Context, Call) (Result, error)
}

// Invocation is the immutable, resolved invocation passed through proxies.
// Accessors return copies of slice-backed values. Use WithArguments when a
// proxy deliberately needs to pass transformed arguments to the next node.
type Invocation struct {
	callID    string
	name      string
	arguments json.RawMessage
	spec      Spec
	tool      Tool
}

// CallID returns the model tool-call ID, when available.
func (i Invocation) CallID() string { return i.callID }

// Name returns the resolved tool name.
func (i Invocation) Name() string { return i.name }

// Arguments returns a copy of the invocation JSON arguments.
func (i Invocation) Arguments() json.RawMessage {
	return cloneJSON(i.arguments)
}

// Spec returns a copy of the resolved tool descriptor.
func (i Invocation) Spec() Spec { return cloneSpec(i.spec) }

// WithArguments returns a copy of the invocation with replacement arguments.
// The System terminal validates the replacement before invoking the Tool.
func (i Invocation) WithArguments(arguments json.RawMessage) Invocation {
	i.arguments = cloneJSON(arguments)
	return i
}

// Next is the remainder of a proxy chain. A proxy may call it zero times to
// short-circuit, once for normal delegation, or multiple times for an explicit
// bounded retry policy.
type Next interface {
	Execute(context.Context, Invocation) (Result, error)
}

// NextFunc adapts a function to Next.
type NextFunc func(context.Context, Invocation) (Result, error)

// Execute calls f.
func (f NextFunc) Execute(ctx context.Context, invocation Invocation) (Result, error) {
	return f(ctx, invocation)
}

// Proxy wraps execution of every resolved tool invocation. Implementations
// reused by a System must be safe for concurrent calls.
type Proxy interface {
	ID() string
	Execute(context.Context, Invocation, Next) (Result, error)
}

func cloneJSON(value json.RawMessage) json.RawMessage {
	return append(json.RawMessage(nil), value...)
}

func cloneSpec(spec Spec) Spec {
	spec.Parameters = cloneJSON(spec.Parameters)
	return spec
}
