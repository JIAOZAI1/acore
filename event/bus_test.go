package event_test

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/JIAOZAI1/acore/event"
)

type userCreated struct{ id string }

func (userCreated) Name() string { return "user.created" }

type orderCreated struct{ id string }

func (orderCreated) Name() string { return "order.created" }

type invalidHandler struct{}

func (invalidHandler) EventType() reflect.Type { return reflect.TypeFor[int]() }
func (invalidHandler) Handle(context.Context, event.Event) error {
	return nil
}

func TestBusPublishInSubscriptionOrder(t *testing.T) {
	bus := event.NewBus()
	var calls []string

	first, err := event.Subscribe(bus, func(_ context.Context, e userCreated) error {
		calls = append(calls, "first:"+e.id)
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe first handler: %v", err)
	}
	defer first.Unsubscribe()
	second, err := event.Subscribe(bus, func(_ context.Context, e userCreated) error {
		calls = append(calls, "second:"+e.id)
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe second handler: %v", err)
	}
	defer second.Unsubscribe()

	if err := bus.Publish(context.Background(), userCreated{id: "42"}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	want := []string{"first:42", "second:42"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
}

func TestBusRoutesByExactType(t *testing.T) {
	bus := event.NewBus()
	var valueCalls, pointerCalls, otherCalls int
	_, err := event.Subscribe(bus, func(context.Context, userCreated) error {
		valueCalls++
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe value handler: %v", err)
	}
	_, err = event.Subscribe(bus, func(context.Context, *userCreated) error {
		pointerCalls++
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe pointer handler: %v", err)
	}
	_, err = event.Subscribe(bus, func(context.Context, orderCreated) error {
		otherCalls++
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe other handler: %v", err)
	}

	if err := bus.Publish(context.Background(), userCreated{}); err != nil {
		t.Fatalf("publish value: %v", err)
	}
	if err := bus.Publish(context.Background(), &userCreated{}); err != nil {
		t.Fatalf("publish pointer: %v", err)
	}
	if valueCalls != 1 || pointerCalls != 1 || otherCalls != 0 {
		t.Fatalf("calls = value:%d pointer:%d other:%d", valueCalls, pointerCalls, otherCalls)
	}
}

func TestBusJoinsHandlerErrorsAndContinues(t *testing.T) {
	bus := event.NewBus()
	firstErr := errors.New("first failed")
	secondErr := errors.New("second failed")
	var calls int
	_, _ = event.Subscribe(bus, func(context.Context, userCreated) error {
		calls++
		return firstErr
	})
	_, _ = event.Subscribe(bus, func(context.Context, userCreated) error {
		calls++
		return secondErr
	})

	err := bus.Publish(context.Background(), userCreated{})
	if !errors.Is(err, firstErr) || !errors.Is(err, secondErr) {
		t.Fatalf("publish error %v does not contain both handler errors", err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
}

func TestBusStopsAfterContextCancellation(t *testing.T) {
	bus := event.NewBus()
	ctx, cancel := context.WithCancel(context.Background())
	var calls int
	_, _ = event.Subscribe(bus, func(context.Context, userCreated) error {
		calls++
		cancel()
		return nil
	})
	_, _ = event.Subscribe(bus, func(context.Context, userCreated) error {
		calls++
		return nil
	})

	err := bus.Publish(ctx, userCreated{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("publish error = %v, want context.Canceled", err)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
}

func TestSubscriptionIsIdempotent(t *testing.T) {
	bus := event.NewBus()
	var calls int
	subscription, err := event.Subscribe(bus, func(context.Context, userCreated) error {
		calls++
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	subscription.Unsubscribe()
	subscription.Unsubscribe()

	if err := bus.Publish(context.Background(), userCreated{}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if calls != 0 {
		t.Fatalf("calls = %d, want 0", calls)
	}
}

func TestHandlerCanUnsubscribeDuringPublish(t *testing.T) {
	bus := event.NewBus()
	var calls int
	var subscription event.Subscription
	subscription, err := event.Subscribe(bus, func(context.Context, userCreated) error {
		calls++
		subscription.Unsubscribe()
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	if err := bus.Publish(context.Background(), userCreated{}); err != nil {
		t.Fatalf("first publish: %v", err)
	}
	if err := bus.Publish(context.Background(), userCreated{}); err != nil {
		t.Fatalf("second publish: %v", err)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
}

func TestBusRejectsInvalidInputs(t *testing.T) {
	bus := event.NewBus()

	if err := bus.Publish(context.Background(), nil); !errors.Is(err, event.ErrNilEvent) {
		t.Fatalf("nil event error = %v", err)
	}
	var typedNil *userCreated
	if err := bus.Publish(context.Background(), typedNil); !errors.Is(err, event.ErrNilEvent) {
		t.Fatalf("typed nil event error = %v", err)
	}
	if _, err := event.Subscribe[userCreated](bus, nil); !errors.Is(err, event.ErrNilHandlerFunc) {
		t.Fatalf("nil handler function error = %v", err)
	}
	if _, err := event.Subscribe[event.Event](bus, func(context.Context, event.Event) error { return nil }); !errors.Is(err, event.ErrInvalidEventType) {
		t.Fatalf("interface event type error = %v", err)
	}
	if _, err := bus.SubscribeHandler(nil); !errors.Is(err, event.ErrNilHandler) {
		t.Fatalf("nil handler error = %v", err)
	}
	if _, err := bus.SubscribeHandler(invalidHandler{}); !errors.Is(err, event.ErrInvalidEventType) {
		t.Fatalf("non-event handler type error = %v", err)
	}
}

func TestZeroBusAndConcurrentUse(t *testing.T) {
	var bus event.Bus
	var calls atomic.Int64
	const workers = 20
	const publishes = 50

	var subscriptionsMu sync.Mutex
	subscriptions := make([]event.Subscription, 0, workers)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			subscription, err := event.Subscribe(&bus, func(context.Context, userCreated) error {
				calls.Add(1)
				return nil
			})
			if err != nil {
				t.Errorf("subscribe: %v", err)
				return
			}
			subscriptionsMu.Lock()
			subscriptions = append(subscriptions, subscription)
			subscriptionsMu.Unlock()
		}()
	}
	wg.Wait()

	for range publishes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := bus.Publish(context.Background(), userCreated{}); err != nil {
				t.Errorf("publish: %v", err)
			}
		}()
	}
	wg.Wait()

	want := int64(workers * publishes)
	if calls.Load() != want {
		t.Fatalf("calls = %d, want %d", calls.Load(), want)
	}
	for _, subscription := range subscriptions {
		subscription.Unsubscribe()
	}
}
