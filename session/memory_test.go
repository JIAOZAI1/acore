package session

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/JIAOZAI1/acore/model"
)

func TestMemoryServiceLoadAppendAndConflict(t *testing.T) {
	service := &MemoryService{}
	key := Key{Scope: "tenant-a", ID: "conversation-a"}

	snapshot, err := service.Load(context.Background(), key)
	if err != nil {
		t.Fatalf("Load(absent) error = %v", err)
	}
	if snapshot.Revision != 0 || len(snapshot.Messages) != 0 {
		t.Fatalf("Load(absent) = %+v, want empty revision zero snapshot", snapshot)
	}

	first := testMessage("first")
	revision, err := service.Append(context.Background(), key, 0, []model.Message{first})
	if err != nil {
		t.Fatalf("Append(first) error = %v", err)
	}
	if revision != 1 {
		t.Fatalf("Append(first) revision = %d, want 1", revision)
	}

	if _, err := service.Append(context.Background(), key, 0, []model.Message{testMessage("conflict")}); !errors.Is(err, ErrConflict) {
		t.Fatalf("Append(conflict) error = %v, want ErrConflict", err)
	}

	second := testMessage("second")
	revision, err = service.Append(context.Background(), key, 1, []model.Message{second})
	if err != nil {
		t.Fatalf("Append(second) error = %v", err)
	}
	if revision != 2 {
		t.Fatalf("Append(second) revision = %d, want 2", revision)
	}

	snapshot, err = service.Load(context.Background(), key)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if snapshot.Revision != 2 || len(snapshot.Messages) != 2 {
		t.Fatalf("Load() = %+v, want revision 2 with two messages", snapshot)
	}
	if got := snapshot.Messages[0].Content[0].Text; got != "first" {
		t.Fatalf("first message = %q, want first", got)
	}
	if got := snapshot.Messages[1].Content[0].Text; got != "second" {
		t.Fatalf("second message = %q, want second", got)
	}
}

func TestMemoryServiceCopiesMutableMessageData(t *testing.T) {
	service := NewMemoryService()
	key := Key{Scope: "tenant-a", ID: "copies"}
	signature := "signature"
	arguments := json.RawMessage(`{"value":1}`)
	messages := []model.Message{{
		Role: model.RoleAssistant,
		Content: []model.ContentBlock{
			{Kind: model.ContentThinking, Text: "thinking", Signature: &signature},
			{Kind: model.ContentToolCall, ToolCall: &model.ToolCall{ID: "call-1", Name: "lookup", Arguments: arguments}},
		},
	}}

	if _, err := service.Append(context.Background(), key, 0, messages); err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	messages[0].Content[0].Text = "changed"
	*messages[0].Content[0].Signature = "changed"
	messages[0].Content[1].ToolCall.Arguments[0] = '['

	first, err := service.Load(context.Background(), key)
	if err != nil {
		t.Fatalf("Load(first) error = %v", err)
	}
	if first.Messages[0].Content[0].Text != "thinking" || *first.Messages[0].Content[0].Signature != "signature" {
		t.Fatalf("Load(first) thinking block = %+v, want isolated original", first.Messages[0].Content[0])
	}
	if got := string(first.Messages[0].Content[1].ToolCall.Arguments); got != `{"value":1}` {
		t.Fatalf("Load(first) arguments = %q, want original", got)
	}

	first.Messages[0].Content[0].Text = "mutated load"
	first.Messages[0].Content[1].ToolCall.Arguments[0] = '['
	second, err := service.Load(context.Background(), key)
	if err != nil {
		t.Fatalf("Load(second) error = %v", err)
	}
	if second.Messages[0].Content[0].Text != "thinking" {
		t.Fatalf("Load(second) text = %q, want thinking", second.Messages[0].Content[0].Text)
	}
	if got := string(second.Messages[0].Content[1].ToolCall.Arguments); got != `{"value":1}` {
		t.Fatalf("Load(second) arguments = %q, want original", got)
	}
}

func TestMemoryServiceValidationAndContext(t *testing.T) {
	service := NewMemoryService()
	validKey := Key{Scope: "tenant-a", ID: "conversation-a"}

	if _, err := service.Load(nil, validKey); !errors.Is(err, ErrInvalidContext) {
		t.Fatalf("Load(nil context) error = %v, want ErrInvalidContext", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := service.Load(ctx, validKey); !errors.Is(err, context.Canceled) {
		t.Fatalf("Load(canceled) error = %v, want context.Canceled", err)
	}

	invalidKeys := []Key{{ID: "id"}, {Scope: "scope"}}
	for _, key := range invalidKeys {
		if _, err := service.Load(context.Background(), key); !errors.Is(err, ErrInvalidKey) {
			t.Fatalf("Load(%+v) error = %v, want ErrInvalidKey", key, err)
		}
	}
	if _, err := service.Append(context.Background(), validKey, 0, nil); !errors.Is(err, ErrInvalidMessages) {
		t.Fatalf("Append(empty) error = %v, want ErrInvalidMessages", err)
	}
}

func TestMemoryServiceConcurrentAppendHasSingleWinner(t *testing.T) {
	service := NewMemoryService()
	key := Key{Scope: "tenant-a", ID: "concurrent"}
	const workers = 16

	var successes atomic.Int64
	var conflicts atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := service.Append(context.Background(), key, 0, []model.Message{testMessage("message")})
			switch {
			case err == nil:
				successes.Add(1)
			case errors.Is(err, ErrConflict):
				conflicts.Add(1)
			default:
				t.Errorf("Append() error = %v", err)
			}
		}()
	}
	wg.Wait()

	if got := successes.Load(); got != 1 {
		t.Fatalf("successful appends = %d, want 1", got)
	}
	if got := conflicts.Load(); got != workers-1 {
		t.Fatalf("conflicting appends = %d, want %d", got, workers-1)
	}
}

func TestMemoryServiceIsolatesKeys(t *testing.T) {
	service := NewMemoryService()
	firstKey := Key{Scope: "tenant-a", ID: "shared-id"}
	secondKey := Key{Scope: "tenant-b", ID: "shared-id"}

	if _, err := service.Append(context.Background(), firstKey, 0, []model.Message{testMessage("first")}); err != nil {
		t.Fatalf("Append(first key) error = %v", err)
	}
	if _, err := service.Append(context.Background(), secondKey, 0, []model.Message{testMessage("second")}); err != nil {
		t.Fatalf("Append(second key) error = %v", err)
	}

	first, err := service.Load(context.Background(), firstKey)
	if err != nil {
		t.Fatalf("Load(first key) error = %v", err)
	}
	second, err := service.Load(context.Background(), secondKey)
	if err != nil {
		t.Fatalf("Load(second key) error = %v", err)
	}
	if first.Revision != 1 || len(first.Messages) != 1 || first.Messages[0].Content[0].Text != "first" {
		t.Fatalf("first snapshot = %+v", first)
	}
	if second.Revision != 1 || len(second.Messages) != 1 || second.Messages[0].Content[0].Text != "second" {
		t.Fatalf("second snapshot = %+v", second)
	}
}

func TestMemoryServiceRejectsRevisionOverflow(t *testing.T) {
	service := NewMemoryService()
	key := Key{Scope: "tenant-a", ID: "overflow"}
	service.byKey[key] = Snapshot{Revision: Revision(math.MaxUint64), Messages: []model.Message{testMessage("existing")}}

	if _, err := service.Append(context.Background(), key, Revision(math.MaxUint64), []model.Message{testMessage("new")}); !errors.Is(err, ErrRevisionExhausted) {
		t.Fatalf("Append() error = %v, want ErrRevisionExhausted", err)
	}
}

func testMessage(text string) model.Message {
	return model.Message{Role: model.RoleUser, Content: []model.ContentBlock{{Kind: model.ContentText, Text: text}}}
}
