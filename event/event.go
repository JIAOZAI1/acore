// Package event provides an in-process, synchronous event bus.
//
// Events are routed by their exact Go type. Publishing T does not notify
// subscribers of *T or an interface implemented by T.
package event

import (
	"context"
	"reflect"
)

// Event is an in-process notification. Name is intended for observability;
// routing is based exclusively on the event's exact Go type.
type Event interface {
	Name() string
}

// Publisher is the minimal event publication capability shared across
// components. Implementations define delivery and error semantics; Bus provides
// synchronous in-process delivery.
type Publisher interface {
	Publish(context.Context, Event) error
}

// Handler consumes events of the exact type returned by EventType.
// Implementations should honor context cancellation and must be safe to call
// concurrently when the same Bus is published to concurrently.
type Handler interface {
	EventType() reflect.Type
	Handle(context.Context, Event) error
}

// HandlerFunc is a type-safe event handler function.
type HandlerFunc[E Event] func(context.Context, E) error

type typedHandler[E Event] struct {
	fn HandlerFunc[E]
}

func (h typedHandler[E]) EventType() reflect.Type {
	return reflect.TypeFor[E]()
}

func (h typedHandler[E]) Handle(ctx context.Context, event Event) error {
	// Bus dispatches by exact type, so this assertion can only fail when the
	// handler is invoked directly or implements an inconsistent EventType.
	return h.fn(ctx, event.(E))
}
