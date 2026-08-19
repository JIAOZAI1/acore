package looper

import (
	"context"
	"fmt"

	"github.com/JIAOZAI1/acore/model"
)

// ModelEvent exposes one provider-independent model stream event on the agent
// event bus. Subscribers receive deltas synchronously, which provides natural
// backpressure without an internal unbounded queue.
type ModelEvent struct {
	Event model.Event
}

// Name implements event.Event.
func (ModelEvent) Name() string { return "looper.model" }

// SingleTurnLoop is the minimal built-in policy. It performs one generation
// and publishes every model stream event. Multi-turn model/tool policies should
// implement Loop instead of adding unrelated policy to this type.
type SingleTurnLoop struct{}

// Run performs one complete model generation.
func (SingleTurnLoop) Run(ctx context.Context, run Run, input Input) error {
	stream, err := run.Generate(ctx, input.Request)
	if err != nil {
		return fmt.Errorf("generate: %w", err)
	}
	if stream == nil {
		return model.ErrUnexpectedStreamEnd
	}

	for modelEvent, streamErr := range stream {
		if streamErr != nil {
			return fmt.Errorf("consume model stream: %w", streamErr)
		}
		if err := run.Publish(ctx, ModelEvent{Event: modelEvent}); err != nil {
			return fmt.Errorf("publish model event: %w", err)
		}
		if modelEvent.Type != model.EventDone {
			continue
		}
		if modelEvent.Result == nil {
			return model.ErrInvalidDoneEvent
		}
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return model.ErrUnexpectedStreamEnd
}
