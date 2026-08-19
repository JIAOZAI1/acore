package event

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
)

var (
	// ErrNilEvent indicates that Publish received a nil event, including a typed nil pointer.
	ErrNilEvent = errors.New("event: nil event")
	// ErrNilHandler indicates that SubscribeHandler received a nil handler.
	ErrNilHandler = errors.New("event: nil handler")
	// ErrNilHandlerFunc indicates that Subscribe received a nil handler function.
	ErrNilHandlerFunc = errors.New("event: nil handler function")
	// ErrInvalidEventType indicates that a handler did not declare a concrete Event type.
	ErrInvalidEventType = errors.New("event: event type must be concrete and implement Event")
)

type subscriptionEntry struct {
	id      uint64
	handler Handler
}

// Bus is a concurrency-safe, synchronous, in-process event bus.
//
// Handlers run sequentially in subscription order. Publish invokes every
// handler in its snapshot and joins their errors. A Bus must not be copied
// after first use. Its zero value is ready to use.
type Bus struct {
	mu       sync.RWMutex
	nextID   uint64
	handlers map[reflect.Type][]subscriptionEntry
}

// NewBus constructs an empty Bus.
func NewBus() *Bus {
	return &Bus{handlers: make(map[reflect.Type][]subscriptionEntry)}
}

// Publish synchronously delivers event to handlers subscribed to its exact Go
// type. Handler errors are joined so one failing handler does not prevent later
// handlers from running. If ctx is canceled, Publish stops before invoking the
// next handler and returns the cancellation error together with prior errors.
func (b *Bus) Publish(ctx context.Context, event Event) error {
	if isNil(event) {
		return ErrNilEvent
	}

	eventType := reflect.TypeOf(event)
	b.mu.RLock()
	entries := append([]subscriptionEntry(nil), b.handlers[eventType]...)
	b.mu.RUnlock()

	handlerErrors := make([]error, 0)
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			handlerErrors = append(handlerErrors, err)
			break
		}
		if err := entry.handler.Handle(ctx, event); err != nil {
			handlerErrors = append(handlerErrors, fmt.Errorf("event: handle %T with subscription %d: %w", event, entry.id, err))
		}
	}
	return errors.Join(handlerErrors...)
}

// Subscription controls the lifetime of a registered handler.
type Subscription interface {
	// Unsubscribe removes the handler from future publication snapshots.
	// It is idempotent. A publication that already captured the handler may
	// still invoke it.
	Unsubscribe()
}

type busSubscription struct {
	once      sync.Once
	bus       *Bus
	eventType reflect.Type
	id        uint64
}

func (s *busSubscription) Unsubscribe() {
	s.once.Do(func() {
		s.bus.unsubscribe(s.eventType, s.id)
	})
}

// Subscribe registers a type-safe handler for E.
func Subscribe[E Event](b *Bus, fn HandlerFunc[E]) (Subscription, error) {
	if fn == nil {
		return nil, ErrNilHandlerFunc
	}
	return b.SubscribeHandler(typedHandler[E]{fn: fn})
}

// SubscribeHandler registers a custom Handler. Most callers should use the
// generic Subscribe function instead.
func (b *Bus) SubscribeHandler(handler Handler) (Subscription, error) {
	if isNil(handler) {
		return nil, ErrNilHandler
	}

	eventType := handler.EventType()
	if eventType == nil || eventType.Kind() == reflect.Interface || !eventType.Implements(reflect.TypeFor[Event]()) {
		return nil, ErrInvalidEventType
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	if b.handlers == nil {
		b.handlers = make(map[reflect.Type][]subscriptionEntry)
	}
	b.nextID++
	entry := subscriptionEntry{id: b.nextID, handler: handler}
	b.handlers[eventType] = append(b.handlers[eventType], entry)

	return &busSubscription{bus: b, eventType: eventType, id: entry.id}, nil
}

func (b *Bus) unsubscribe(eventType reflect.Type, id uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()

	entries := b.handlers[eventType]
	for i, entry := range entries {
		if entry.id != id {
			continue
		}
		if len(entries) == 1 {
			delete(b.handlers, eventType)
		} else {
			b.handlers[eventType] = append(entries[:i], entries[i+1:]...)
		}
		return
	}
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
