// Package event provides type-safe, synchronous, in-process event delivery.
//
// Events are routed by their exact Go type. Publishing T does not notify
// subscribers of *T or an interface implemented by T.
package event

import "context"

// Event is an in-process notification. Name is intended for observability;
// routing is based exclusively on the event's exact Go type.
type Event interface {
	Name() string
}

// Publisher is the minimal event publication capability shared across
// components.
type Publisher interface {
	Publish(context.Context, Event) error
}

// HandlerFunc consumes events of type E. A handler may be called concurrently
// when the same Bus is published to concurrently.
type HandlerFunc[E Event] func(context.Context, E) error

// Subscription controls the lifetime of a registered handler.
type Subscription interface {
	// Unsubscribe removes the handler from future publication snapshots. It is
	// idempotent. A publication that already captured the handler may still
	// invoke it.
	Unsubscribe()
}
