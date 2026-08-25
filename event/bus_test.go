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

func TestBusPublishesCustomEventInSubscriptionOrder(t *testing.T) {
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

	if _, err := event.Subscribe(bus, func(context.Context, userCreated) error {
		valueCalls++
		return nil
	}); err != nil {
		t.Fatalf("subscribe value handler: %v", err)
	}
	if _, err := event.Subscribe(bus, func(context.Context, *userCreated) error {
		pointerCalls++
		return nil
	}); err != nil {
		t.Fatalf("subscribe pointer handler: %v", err)
	}
	if _, err := event.Subscribe(bus, func(context.Context, orderCreated) error {
		otherCalls++
		return nil
	}); err != nil {
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

func TestBusPublishWithoutSubscribers(t *testing.T) {
	if err := event.NewBus().Publish(context.Background(), userCreated{}); err != nil {
		t.Fatalf("publish without subscribers: %v", err)
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

func TestBusHonorsContextCancellation(t *testing.T) {
	t.Run("before publish", func(t *testing.T) {
		bus := event.NewBus()
		var calls int
		_, _ = event.Subscribe(bus, func(context.Context, userCreated) error {
			calls++
			return nil
		})
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		err := bus.Publish(ctx, userCreated{})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("publish error = %v, want context.Canceled", err)
		}
		if calls != 0 {
			t.Fatalf("calls = %d, want 0", calls)
		}
	})

	t.Run("during consumption", func(t *testing.T) {
		bus := event.NewBus()
		ctx, cancel := context.WithCancel(context.Background())
		handlerErr := errors.New("handler failed")
		var calls int
		_, _ = event.Subscribe(bus, func(context.Context, userCreated) error {
			calls++
			cancel()
			return handlerErr
		})
		_, _ = event.Subscribe(bus, func(context.Context, userCreated) error {
			calls++
			return nil
		})

		err := bus.Publish(ctx, userCreated{})
		if !errors.Is(err, context.Canceled) || !errors.Is(err, handlerErr) {
			t.Fatalf("publish error = %v, want context.Canceled and handler error", err)
		}
		if calls != 1 {
			t.Fatalf("calls = %d, want 1", calls)
		}
	})
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

func TestHandlerCanUnsubscribeItself(t *testing.T) {
	bus := event.NewBus()
	var calls int
	var subscription event.Subscription
	var err error
	subscription, err = event.Subscribe(bus, func(context.Context, userCreated) error {
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

func TestUnsubscribeOnlyAffectsFutureSnapshots(t *testing.T) {
	bus := event.NewBus()
	var firstCalls, secondCalls int
	var second event.Subscription

	_, err := event.Subscribe(bus, func(context.Context, userCreated) error {
		firstCalls++
		second.Unsubscribe()
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe first handler: %v", err)
	}
	second, err = event.Subscribe(bus, func(context.Context, userCreated) error {
		secondCalls++
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe second handler: %v", err)
	}

	if err := bus.Publish(context.Background(), userCreated{}); err != nil {
		t.Fatalf("first publish: %v", err)
	}
	if err := bus.Publish(context.Background(), userCreated{}); err != nil {
		t.Fatalf("second publish: %v", err)
	}
	if firstCalls != 2 || secondCalls != 1 {
		t.Fatalf("calls = first:%d second:%d, want first:2 second:1", firstCalls, secondCalls)
	}
}

func TestBusRejectsInvalidInputs(t *testing.T) {
	bus := event.NewBus()
	var nilBus *event.Bus

	if err := nilBus.Publish(context.Background(), userCreated{}); !errors.Is(err, event.ErrNilBus) {
		t.Fatalf("nil bus publish error = %v", err)
	}
	if _, err := event.Subscribe(nilBus, func(context.Context, userCreated) error { return nil }); !errors.Is(err, event.ErrNilBus) {
		t.Fatalf("nil bus subscribe error = %v", err)
	}
	if err := bus.Publish(context.Background(), nil); !errors.Is(err, event.ErrNilEvent) {
		t.Fatalf("nil event error = %v", err)
	}
	var typedNil *userCreated
	if err := bus.Publish(context.Background(), typedNil); !errors.Is(err, event.ErrNilEvent) {
		t.Fatalf("typed nil event error = %v", err)
	}
	if _, err := event.Subscribe[userCreated](bus, nil); !errors.Is(err, event.ErrNilHandler) {
		t.Fatalf("nil handler error = %v", err)
	}
	if _, err := event.Subscribe[event.Event](bus, func(context.Context, event.Event) error { return nil }); !errors.Is(err, event.ErrInvalidEventType) {
		t.Fatalf("interface event type error = %v", err)
	}
}

func TestZeroBusAndConcurrentUse(t *testing.T) {
	var bus event.Bus
	var calls atomic.Int64

	base, err := event.Subscribe(&bus, func(context.Context, userCreated) error {
		calls.Add(1)
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe base handler: %v", err)
	}
	defer base.Unsubscribe()

	const workers = 12
	const iterations = 80
	var wg sync.WaitGroup
	for range workers {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for range iterations {
				if err := bus.Publish(context.Background(), userCreated{}); err != nil {
					t.Errorf("publish: %v", err)
					return
				}
			}
		}()
		go func() {
			defer wg.Done()
			for range iterations {
				subscription, err := event.Subscribe(&bus, func(context.Context, userCreated) error {
					return nil
				})
				if err != nil {
					t.Errorf("subscribe: %v", err)
					return
				}
				subscription.Unsubscribe()
			}
		}()
	}
	wg.Wait()

	want := int64(workers * iterations)
	if calls.Load() != want {
		t.Fatalf("base handler calls = %d, want %d", calls.Load(), want)
	}
}
