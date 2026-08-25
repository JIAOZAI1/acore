// Package tool defines provider-independent tools and their execution service.
package tool

import (
	"context"
	"encoding/json"

	"github.com/JIAOZAI1/acore/internal/clone"
)

// Spec describes a tool that can be presented to a model. Parameters contains
// the JSON Schema accepted by the tool.
type Spec struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters"`
}

// Call is one requested tool invocation. ID may be empty for callers that do
// not need to correlate the invocation with a model tool call.
type Call struct {
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// Result is the successful text output of one tool invocation. Execution
// failures are returned as Go errors.
type Result struct {
	Content string `json:"content"`
}

// Tool implements one concrete tool. Implementations may be called
// concurrently and must honor context cancellation.
type Tool interface {
	Spec() Spec
	Execute(context.Context, json.RawMessage) (Result, error)
}

// Service is the minimal tool catalog and execution capability exposed to an
// agent loop.
type Service interface {
	Specs() []Spec
	Execute(context.Context, Call) (Result, error)
}

func cloneSpec(spec Spec) Spec {
	spec.Parameters = clone.Slice(spec.Parameters)
	return spec
}
