package openai

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/JIAOZAI1/acore/model"
)

func TestCompleteTextStream(t *testing.T) {
	var received chatRequest
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != chatPath {
			t.Errorf("path = %q, want %q", request.URL.Path, chatPath)
		}
		if got := request.Header.Get("Authorization"); got != "Bearer secret" {
			t.Errorf("Authorization = %q", got)
		}
		if got := request.Header.Get("X-Test"); got != "yes" {
			t.Errorf("X-Test = %q", got)
		}
		if err := json.NewDecoder(request.Body).Decode(&received); err != nil {
			t.Errorf("decode request: %v", err)
		}

		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = writer.Write([]byte("data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hel\"}}]}\n\n"))
		_, _ = writer.Write([]byte("data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"lo\"},\"finish_reason\":\"stop\"}]}\n\n"))
		_, _ = writer.Write([]byte("data: {\"choices\":[],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":2,\"total_tokens\":5,\"prompt_tokens_details\":{\"cached_tokens\":1}}}\n\n"))
		_, _ = writer.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	provider, err := New(Config{APIKey: "secret", BaseURL: server.URL, Headers: map[string]string{"X-Test": "yes"}})
	if err != nil {
		t.Fatal(err)
	}
	selected := provider.Models()[0]
	llm, err := model.Bind(provider, selected)
	if err != nil {
		t.Fatal(err)
	}
	maxTokens := 100
	result, err := model.Complete(context.Background(), llm, model.Request{
		Context: model.Context{
			SystemPrompt: "be concise",
			Messages: []model.Message{{
				Role:    model.RoleUser,
				Content: []model.ContentBlock{{Kind: model.ContentText, Text: "hi"}},
			}},
		},
		MaxTokens: &maxTokens,
	})
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if received.Model != selected.ID || len(received.Messages) != 2 {
		t.Fatalf("unexpected request: %+v", received)
	}
	if received.Messages[0].Role != "system" || received.Messages[0].Content != "be concise" {
		t.Fatalf("system message = %+v", received.Messages[0])
	}
	if received.MaxTokens == nil || *received.MaxTokens != maxTokens {
		t.Fatalf("max_tokens = %v", received.MaxTokens)
	}
	if len(result.Message.Content) != 1 || result.Message.Content[0].Text != "hello" {
		t.Fatalf("content = %+v", result.Message.Content)
	}
	if result.StopReason != model.ReasonStop || result.Usage.TotalTokens != 5 || result.Usage.CacheRead != 1 {
		t.Fatalf("result metadata = %+v", result)
	}
}

func TestCompleteToolCallStream(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = writer.Write([]byte("data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call-1\",\"function\":{\"name\":\"weather\",\"arguments\":\"{\\\"city\\\":\"}}]}}]}\n\n"))
		_, _ = writer.Write([]byte("data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"\\\"Paris\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\n"))
		_, _ = writer.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	provider, err := New(Config{BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	llm, err := model.Bind(provider, provider.Models()[0])
	if err != nil {
		t.Fatal(err)
	}
	result, err := model.Complete(context.Background(), llm, model.Request{})
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if result.StopReason != model.ReasonToolUse || len(result.Message.Content) != 1 {
		t.Fatalf("result = %+v", result)
	}
	call := result.Message.Content[0].ToolCall
	if call == nil || call.ID != "call-1" || call.Name != "weather" || string(call.Arguments) != `{"city":"Paris"}` {
		t.Fatalf("tool call = %+v", call)
	}
}

func TestHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		http.Error(writer, "bad key", http.StatusUnauthorized)
	}))
	defer server.Close()

	provider, err := New(Config{BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	_, err = provider.Generate(context.Background(), provider.Models()[0], model.Request{})
	var apiError *APIError
	if !errors.As(err, &apiError) || apiError.StatusCode != http.StatusUnauthorized {
		t.Fatalf("Generate() error = %v", err)
	}
}

func TestStreamRejectsPrematureEOF(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = writer.Write([]byte("data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"partial\"}}]}\n\n"))
	}))
	defer server.Close()

	provider, err := New(Config{BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	llm, err := model.Bind(provider, provider.Models()[0])
	if err != nil {
		t.Fatal(err)
	}
	_, err = model.Complete(context.Background(), llm, model.Request{})
	if err == nil {
		t.Fatal("Complete() should reject a stream without [DONE]")
	}
}

func TestBuildMessageImageData(t *testing.T) {
	message, err := buildMessage(model.Message{
		Role: model.RoleUser,
		Content: []model.ContentBlock{{
			Kind: model.ContentImage, MIMEType: "image/png", Data: "YWJj",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	parts, ok := message.Content.([]contentPart)
	if !ok || len(parts) != 1 || parts[0].ImageURL.URL != "data:image/png;base64,YWJj" {
		t.Fatalf("content = %#v", message.Content)
	}
}
