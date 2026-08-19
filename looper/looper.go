// Package looper drives one agent run while leaving the loop policy replaceable.
//
// The package deliberately does not define retry, persistence, approval, or
// recovery semantics. Those capabilities can be composed around a Loop after
// their contracts are known.
package looper

import (
	"context"
	"errors"
	"fmt"
	"reflect"

	"github.com/JIAOZAI1/acore/event"
	"github.com/JIAOZAI1/acore/model"
	acruntime "github.com/JIAOZAI1/acore/runtime"
	"github.com/JIAOZAI1/acore/tool"
)

var (
	// ErrNilLoop indicates that New received no loop strategy.
	ErrNilLoop = errors.New("looper: nil loop")
	// ErrNilRuntime indicates that New received no runtime.
	ErrNilRuntime = errors.New("looper: nil runtime")
)

// Input selects a provider/model pair and contains the initial request for one
// run. Slices reachable through Request belong to the caller; a Loop must copy
// them before mutation when it needs to advance conversation state.
type Input struct {
	ProviderID string
	ModelID    string
	Request    model.Request
}

// Run provides the model, tool, and event operations available to a Loop.
// Keeping these operations behind a run-scoped interface allows custom
// strategies to perform model turns, call the opaque tool service, and publish
// their own event types without receiving the complete process Runtime.
type Run interface {
	Generate(context.Context, model.Request) (model.Stream, error)
	Tools() tool.Service
	Publish(context.Context, event.Event) error
}

// Loop is the replaceable policy for an agent run.
//
// A Loop owns conversation advancement and decides when the run is complete.
// Implementations reused by a Looper must be safe for concurrent calls. They
// should not start detached goroutines and must return context cancellation and
// model, tool, or publication failures to the caller.
type Loop interface {
	Run(context.Context, Run, Input) error
}

// LoopFunc adapts a function to Loop.
type LoopFunc func(context.Context, Run, Input) error

// Run executes f.
func (f LoopFunc) Run(ctx context.Context, run Run, input Input) error {
	return f(ctx, run, input)
}

// Looper binds a replaceable Loop policy to a process Runtime. It has no
// mutable per-run state, so Run may be called concurrently when the configured
// Loop and Runtime services also support concurrent use.
type Looper struct {
	loop    Loop
	runtime *acruntime.Runtime
}

// New constructs a Looper.
func New(loop Loop, runtime *acruntime.Runtime) (*Looper, error) {
	if isNil(loop) {
		return nil, ErrNilLoop
	}
	if runtime == nil {
		return nil, ErrNilRuntime
	}
	return &Looper{loop: loop, runtime: runtime}, nil
}

// Run executes one agent run and synchronously streams events through the
// Runtime event service. Handler, model, strategy, and context errors are
// returned without being converted into successful events.
func (l *Looper) Run(ctx context.Context, input Input) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	llm, err := l.runtime.Models().LLM(input.ProviderID, input.ModelID)
	if err != nil {
		return fmt.Errorf("looper: resolve model %q/%q: %w", input.ProviderID, input.ModelID, err)
	}

	run := &runContext{llm: llm, tools: l.runtime.Tools(), events: l.runtime.Events()}
	if err := l.loop.Run(ctx, run, input); err != nil {
		return fmt.Errorf("looper: run: %w", err)
	}
	return nil
}

type runContext struct {
	llm    model.LLM
	tools  tool.Service
	events event.Publisher
}

func (r *runContext) Generate(ctx context.Context, request model.Request) (model.Stream, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return r.llm.Generate(ctx, request)
}

func (r *runContext) Tools() tool.Service { return r.tools }

func (r *runContext) Publish(ctx context.Context, notification event.Event) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return r.events.Publish(ctx, notification)
}

func isNil(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}
