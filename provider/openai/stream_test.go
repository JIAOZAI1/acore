package openai

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/JIAOZAI1/acore/model"
)

func TestConsumeStreamMapsTextUsageAndRefusal(t *testing.T) {
	source := strings.Join([]string{
		": keep-alive\n",
		"event: message\n",
		"data: {\"model\":\"gpt-test-2026\",\"choices\":[\n",
		"data: {\"index\":0,\"delta\":{\"content\":\"hel\"},\"finish_reason\":null}]}\n\n",
		"data: {\"model\":\"gpt-test-2026\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"lo\",\"refusal\":\"!\"},\"finish_reason\":null}]}\n\n",
		"data: {\"model\":\"gpt-test-2026\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n",
		"data: {\"model\":\"gpt-test-2026\",\"choices\":[],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":4,\"total_tokens\":14,\"prompt_tokens_details\":{\"cached_tokens\":3,\"cache_write_tokens\":2},\"completion_tokens_details\":{\"reasoning_tokens\":1}}}\n\n",
		"data: [DONE]\n\n",
	}, "")

	events, err := collectStream(context.Background(), source)
	if err != nil {
		t.Fatalf("consumeStream() error = %v", err)
	}
	wantTypes := []model.EventType{
		model.EventStart,
		model.EventContentStart,
		model.EventContentDelta,
		model.EventContentDelta,
		model.EventContentDelta,
		model.EventContentEnd,
		model.EventDone,
	}
	assertEventTypes(t, events, wantTypes)
	if events[2].Delta != "hel" || events[3].Delta != "lo" || events[4].Delta != "!" {
		t.Fatalf("deltas = %q, %q, %q", events[2].Delta, events[3].Delta, events[4].Delta)
	}
	if events[5].Block == nil || events[5].Block.Text != "hello!" {
		t.Fatalf("content end = %+v", events[5])
	}
	result := events[6].Result
	if result == nil {
		t.Fatal("Done has no result")
	}
	if result.Message.Role != model.RoleAssistant || len(result.Message.Content) != 1 || result.Message.Content[0].Text != "hello!" {
		t.Fatalf("result message = %+v", result.Message)
	}
	if result.StopReason != model.ReasonStop || result.ModelID != "gpt-test-2026" || result.ProviderID != ProviderID {
		t.Fatalf("result metadata = %+v", result)
	}
	wantUsage := model.Usage{InputTokens: 10, OutputTokens: 4, CacheRead: 3, CacheWrite: 2, ReasoningTokens: 1, TotalTokens: 14}
	if result.Usage != wantUsage {
		t.Fatalf("usage = %+v, want %+v", result.Usage, wantUsage)
	}
}

func TestConsumeStreamMapsParallelToolCalls(t *testing.T) {
	source := strings.Join([]string{
		"data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call-1\",\"type\":\"function\",\"function\":{\"name\":\"weather\",\"arguments\":\"{\\\"city\\\":\"}},{\"index\":1,\"id\":\"call-2\",\"type\":\"function\",\"function\":{\"name\":\"time\",\"arguments\":\"{\"}}]},\"finish_reason\":null}]}\n\n",
		"data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"\\\"Paris\\\"}\"}},{\"index\":1,\"function\":{\"arguments\":\"\\\"zone\\\":\\\"UTC\\\"}\"}}]},\"finish_reason\":null}]}\n\n",
		"data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\n",
		"data: [DONE]\n\n",
	}, "")

	events, err := collectStream(context.Background(), source)
	if err != nil {
		t.Fatalf("consumeStream() error = %v", err)
	}
	wantTypes := []model.EventType{
		model.EventStart,
		model.EventContentStart,
		model.EventContentDelta,
		model.EventContentStart,
		model.EventContentDelta,
		model.EventContentDelta,
		model.EventContentDelta,
		model.EventContentEnd,
		model.EventContentEnd,
		model.EventDone,
	}
	assertEventTypes(t, events, wantTypes)
	result := events[len(events)-1].Result
	if result == nil || result.StopReason != model.ReasonToolUse || len(result.Message.Content) != 2 {
		t.Fatalf("result = %+v", result)
	}
	first := result.Message.Content[0].ToolCall
	second := result.Message.Content[1].ToolCall
	if first == nil || first.ID != "call-1" || first.Name != "weather" || string(first.Arguments) != `{"city":"Paris"}` {
		t.Fatalf("first tool call = %+v", first)
	}
	if second == nil || second.ID != "call-2" || second.Name != "time" || string(second.Arguments) != `{"zone":"UTC"}` {
		t.Fatalf("second tool call = %+v", second)
	}
}

func TestConsumeStreamDefaultsEmptyToolArguments(t *testing.T) {
	source := "data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call-1\",\"type\":\"function\",\"function\":{\"name\":\"ping\",\"arguments\":\"\"}}]},\"finish_reason\":null}]}\n\n" +
		"data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\n" +
		"data: [DONE]\n\n"
	events, err := collectStream(context.Background(), source)
	if err != nil {
		t.Fatalf("consumeStream() error = %v", err)
	}
	call := events[len(events)-1].Result.Message.Content[0].ToolCall
	if call == nil || string(call.Arguments) != `{}` {
		t.Fatalf("tool call = %+v", call)
	}
}

func TestConsumeStreamMapsStopReasons(t *testing.T) {
	tests := []struct {
		wire string
		want model.StopReason
	}{
		{wire: "stop", want: model.ReasonStop},
		{wire: "length", want: model.ReasonLength},
		{wire: "tool_calls", want: model.ReasonToolUse},
		{wire: "function_call", want: model.ReasonToolUse},
		{wire: "content_filter", want: model.ReasonContentFilter},
		{wire: "future_reason", want: model.ReasonUnknown},
	}
	for _, test := range tests {
		t.Run(test.wire, func(t *testing.T) {
			source := fmt.Sprintf("data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":%q}]}\n\ndata: [DONE]\n\n", test.wire)
			events, err := collectStream(context.Background(), source)
			if err != nil {
				t.Fatalf("consumeStream() error = %v", err)
			}
			if got := events[len(events)-1].Result.StopReason; got != test.want {
				t.Fatalf("StopReason = %v, want %v", got, test.want)
			}
		})
	}
}

func TestConsumeStreamRejectsInvalidProtocol(t *testing.T) {
	oversized := "data: " + strings.Repeat("x", maxSSEEventSize) + "\n\n"
	tests := []struct {
		name   string
		source string
	}{
		{name: "malformed JSON", source: "data: {\n\n"},
		{name: "missing done", source: "data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"},
		{name: "missing finish reason", source: "data: {\"choices\":[]}\n\ndata: [DONE]\n\n"},
		{name: "invalid tool arguments", source: toolStream("{", "tool_calls")},
		{name: "incomplete tool", source: "data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call-1\",\"function\":{\"arguments\":\"{}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n"},
		{name: "missing tool index", source: "data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"id\":\"call-1\",\"function\":{\"name\":\"tool\"}}]},\"finish_reason\":null}]}\n\n"},
		{name: "unsupported tool type", source: "data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"type\":\"custom\"}]},\"finish_reason\":null}]}\n\n"},
		{name: "tool ID changes", source: "data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call-1\",\"function\":{\"name\":\"tool\"}}]},\"finish_reason\":null}]}\n\ndata: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call-2\"}]},\"finish_reason\":null}]}\n\n"},
		{name: "stream API error", source: "data: {\"error\":{\"message\":\"failed\",\"type\":\"server_error\",\"code\":\"bad_stream\"}}\n\n"},
		{name: "choice index", source: "data: {\"choices\":[{\"index\":1,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"},
		{name: "multiple choices", source: "data: {\"choices\":[{\"index\":0,\"delta\":{}},{\"index\":0,\"delta\":{}}]}\n\n"},
		{name: "negative usage", source: "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":-1}}\n\n"},
		{name: "model changes", source: "data: {\"model\":\"a\",\"choices\":[]}\n\ndata: {\"model\":\"b\",\"choices\":[]}\n\n"},
		{name: "empty finish reason", source: "data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"\"}]}\n\n"},
		{name: "unterminated line", source: "data: [DONE]"},
		{name: "oversized event", source: oversized},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := collectStream(context.Background(), test.source)
			if !errors.Is(err, ErrInvalidStream) {
				t.Fatalf("consumeStream() error = %v, want ErrInvalidStream", err)
			}
		})
	}
}

func TestConsumeStreamPreservesReadError(t *testing.T) {
	want := errors.New("connection reset")
	reader := io.MultiReader(
		strings.NewReader("data: {\"choices\":[]}\n\n"),
		errorReader{err: want},
	)
	var got error
	consumeStream(context.Background(), reader, model.Model{ID: "gpt-test"}, func(_ model.Event, err error) bool {
		if err != nil {
			got = err
		}
		return true
	})
	if !errors.Is(got, want) {
		t.Fatalf("consumeStream() error = %v, want errors.Is(%v)", got, want)
	}
}

func TestConsumeStreamHonorsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := collectStream(ctx, "")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("consumeStream() error = %v, want context.Canceled", err)
	}
}

func collectStream(ctx context.Context, source string) ([]model.Event, error) {
	var events []model.Event
	var streamErr error
	consumeStream(ctx, strings.NewReader(source), model.Model{ID: "gpt-test"}, func(event model.Event, err error) bool {
		if err != nil {
			streamErr = err
			return false
		}
		events = append(events, event)
		return true
	})
	return events, streamErr
}

func assertEventTypes(t *testing.T, events []model.Event, want []model.EventType) {
	t.Helper()
	if len(events) != len(want) {
		t.Fatalf("event count = %d, want %d: %+v", len(events), len(want), events)
	}
	for i := range want {
		if events[i].Type != want[i] {
			t.Fatalf("event %d type = %v, want %v", i, events[i].Type, want[i])
		}
	}
}

func toolStream(arguments, finishReason string) string {
	return fmt.Sprintf("data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call-1\",\"type\":\"function\",\"function\":{\"name\":\"tool\",\"arguments\":%q}}]},\"finish_reason\":null}]}\n\ndata: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":%q}]}\n\ndata: [DONE]\n\n", arguments, finishReason)
}

type errorReader struct {
	err error
}

func (r errorReader) Read([]byte) (int, error) {
	return 0, r.err
}
