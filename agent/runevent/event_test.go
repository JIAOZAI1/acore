package runevent

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/JIAOZAI1/acore/event"
)

func TestEventNamesAreStableAndUnique(t *testing.T) {
	events := []event.Event{
		RunStartedEvent{},
		ModelTurnStartedEvent{},
		ModelTurnCompletedEvent{},
		ModelTurnFailedEvent{},
		ToolCallStartedEvent{},
		ToolCallCompletedEvent{},
		RunCompletedEvent{},
		RunFailedEvent{},
		RunCanceledEvent{},
	}

	seen := make(map[string]struct{}, len(events))
	for _, got := range events {
		if got.Name() == "" {
			t.Fatal("event name must not be empty")
		}
		if _, ok := seen[got.Name()]; ok {
			t.Fatalf("duplicate event name %q", got.Name())
		}
		seen[got.Name()] = struct{}{}
	}
}

func TestToolCallStatusString(t *testing.T) {
	tests := []struct {
		status ToolCallStatus
		want   string
	}{
		{ToolCallStatusUnknown, "unknown"},
		{ToolCallStatusSucceeded, "succeeded"},
		{ToolCallStatusFailed, "failed"},
		{ToolCallStatusCanceled, "canceled"},
		{ToolCallStatus(99), "unknown"},
	}

	for _, tt := range tests {
		if got := tt.status.String(); got != tt.want {
			t.Errorf("ToolCallStatus(%d).String() = %q, want %q", tt.status, got, tt.want)
		}
	}
}

func TestRunStartedEventJSON(t *testing.T) {
	input := RunStartedEvent{
		Metadata: Metadata{
			RunID:      "run-1",
			Sequence:   1,
			OccurredAt: time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC),
		},
		ModelID:    "model-1",
		ProviderID: "provider-1",
	}

	data, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	var fields map[string]any
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	for _, field := range []string{"metadata", "modelId", "providerId"} {
		if _, ok := fields[field]; !ok {
			t.Errorf("JSON field %q is missing: %s", field, data)
		}
	}
}

func TestEventsCanBePublishedThroughGenericPublisher(t *testing.T) {
	var published event.Event
	publisher := publisherFunc(func(_ context.Context, got event.Event) error {
		published = got
		return nil
	})

	want := RunCompletedEvent{}
	if err := publisher.Publish(context.Background(), want); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	if _, ok := published.(RunCompletedEvent); !ok {
		t.Fatalf("published event type = %T, want runevent.RunCompletedEvent", published)
	}
}

type publisherFunc func(context.Context, event.Event) error

func (f publisherFunc) Publish(ctx context.Context, e event.Event) error {
	return f(ctx, e)
}
