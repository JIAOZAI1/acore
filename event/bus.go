package event

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"

	"github.com/JIAOZAI1/acore/internal/nilcheck"
)

var (
	// ErrNilBus indicates that an operation received a nil Bus.
	ErrNilBus = errors.New("event: nil bus")
	// ErrNilEvent indicates that Publish received a nil event, including a typed nil.
	ErrNilEvent = errors.New("event: nil event")
	// ErrNilHandler indicates that Subscribe received a nil handler function.
	ErrNilHandler = errors.New("event: nil handler")
	// ErrInvalidEventType indicates that a subscription did not specify a concrete event type.
	ErrInvalidEventType = errors.New("event: event type must be concrete")
)

type subscriptionEntry struct {
	id      uint64
	handler func(context.Context, Event) error
}

// Bus is a concurrency-safe, synchronous, in-process event bus.
//
// Handlers run sequentially in subscription order. Publish invokes every
// handler in its snapshot and joins their errors. The zero value is ready to
// use. A Bus must not be copied after first use.
type Bus struct {
	mu       sync.RWMutex
	nextID   uint64
	handlers map[reflect.Type][]subscriptionEntry
}

// NewBus constructs an empty Bus.
func NewBus() *Bus {
	return &Bus{handlers: make(map[reflect.Type][]subscriptionEntry)}
}

// Publish synchronously delivers an event to handlers subscribed to its exact
// Go type. A failing handler does not prevent later handlers from running. If
// ctx is canceled, Publish stops before invoking the next handler.
func (b *Bus) Publish(ctx context.Context, published Event) error {
	if b == nil {
		return ErrNilBus
	}
	if nilcheck.IsNil(published) {
		return ErrNilEvent
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	eventType := reflect.TypeOf(published)
	b.mu.RLock()
	entries := append([]subscriptionEntry(nil), b.handlers[eventType]...)
	b.mu.RUnlock()

	handlerErrors := make([]error, 0)
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			handlerErrors = append(handlerErrors, err)
			break
		}
		if err := entry.handler(ctx, published); err != nil {
			handlerErrors = append(handlerErrors, fmt.Errorf(
				"event: handle %T with subscription %d: %w",
				published,
				entry.id,
				err,
			))
		}
	}
	return errors.Join(handlerErrors...)
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

// Subscribe registers a type-safe handler for events of the exact type E.
func Subscribe[E Event](b *Bus, handler HandlerFunc[E]) (Subscription, error) {
	if b == nil {
		return nil, ErrNilBus
	}
	if handler == nil {
		return nil, ErrNilHandler
	}

	eventType := reflect.TypeFor[E]()
	if eventType == nil || eventType.Kind() == reflect.Interface || !eventType.Implements(reflect.TypeFor[Event]()) {
		return nil, ErrInvalidEventType
	}

	entry := subscriptionEntry{
		handler: func(ctx context.Context, published Event) error {
			return handler(ctx, published.(E))
		},
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	if b.handlers == nil {
		b.handlers = make(map[reflect.Type][]subscriptionEntry)
	}
	b.nextID++
	entry.id = b.nextID
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

var _ Publisher = (*Bus)(nil)
