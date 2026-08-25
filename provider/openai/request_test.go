package openai

import (
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"testing"

	"github.com/JIAOZAI1/acore/model"
)

func TestBuildRequestMapsPortableProtocol(t *testing.T) {
	temperature := 0.4
	maxTokens := 1024
	reasoning := model.ReasoningHigh
	request := model.Request{
		Temperature: &temperature,
		MaxTokens:   &maxTokens,
		Reasoning:   &reasoning,
		Context: model.Context{
			SystemPrompt: "be concise",
			Messages: []model.Message{
				{
					Role: model.RoleUser,
					Content: []model.ContentBlock{
						{Kind: model.ContentText, Text: "inspect "},
						{Kind: model.ContentImage, URL: "https://example.com/input.png"},
						{Kind: model.ContentImage, MIMEType: "image/png", Data: "aGVsbG8="},
					},
				},
				{
					Role: model.RoleAssistant,
					Content: []model.ContentBlock{
						{Kind: model.ContentText, Text: "calling"},
						{Kind: model.ContentToolCall, ToolCall: &model.ToolCall{
							ID: "call-1", Name: "weather", Arguments: json.RawMessage(`{"city":"Paris"}`),
						}},
					},
				},
				{
					Role:       model.RoleTool,
					ToolCallID: "call-1",
					IsError:    true,
					Content:    []model.ContentBlock{{Kind: model.ContentText, Text: "temporary failure"}},
				},
			},
			Tools: []model.ToolSpec{{
				Name:        "weather",
				Description: "gets weather",
				Parameters:  json.RawMessage(`{"type":"object","properties":{"city":{"type":"string"}}}`),
			}},
		},
	}

	got, err := buildRequest(model.Model{ID: "gpt-test"}, request)
	if err != nil {
		t.Fatalf("buildRequest() error = %v", err)
	}
	if got.Model != "gpt-test" || !got.Stream || !got.StreamOptions.IncludeUsage {
		t.Fatalf("request flags = %+v", got)
	}
	if got.Temperature != request.Temperature || got.MaxCompletionTokens != request.MaxTokens || got.ReasoningEffort != "high" {
		t.Fatalf("request options = %+v", got)
	}
	if len(got.Messages) != 4 || got.Messages[0].Role != "developer" || got.Messages[0].Content != "be concise" {
		t.Fatalf("messages = %#v", got.Messages)
	}

	userParts, ok := got.Messages[1].Content.([]chatContentPart)
	if !ok || len(userParts) != 3 {
		t.Fatalf("user content = %#v", got.Messages[1].Content)
	}
	if userParts[0].Type != "text" || userParts[0].Text != "inspect " {
		t.Fatalf("text part = %+v", userParts[0])
	}
	if userParts[1].ImageURL.URL != "https://example.com/input.png" {
		t.Fatalf("URL image = %+v", userParts[1])
	}
	if userParts[2].ImageURL.URL != "data:image/png;base64,aGVsbG8=" {
		t.Fatalf("data image = %+v", userParts[2])
	}

	assistant := got.Messages[2]
	if assistant.Content != "calling" || len(assistant.ToolCalls) != 1 {
		t.Fatalf("assistant = %#v", assistant)
	}
	call := assistant.ToolCalls[0]
	if call.ID != "call-1" || call.Type != "function" || call.Function.Name != "weather" || call.Function.Arguments != `{"city":"Paris"}` {
		t.Fatalf("tool call = %+v", call)
	}
	if got.Messages[3].Role != "tool" || got.Messages[3].ToolCallID != "call-1" || got.Messages[3].Content != "temporary failure" {
		t.Fatalf("tool result = %#v", got.Messages[3])
	}
	if len(got.Tools) != 1 || got.Tools[0].Type != "function" || got.Tools[0].Function.Name != "weather" {
		t.Fatalf("tools = %#v", got.Tools)
	}

	payload, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	var wire struct {
		Messages []struct {
			ToolCalls []struct {
				Function struct {
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(payload, &wire); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if wire.Messages[2].ToolCalls[0].Function.Arguments != `{"city":"Paris"}` {
		t.Fatalf("wire arguments = %q, want JSON string", wire.Messages[2].ToolCalls[0].Function.Arguments)
	}
}

func TestBuildRequestUsesStringForTextOnlyMessage(t *testing.T) {
	request := model.Request{Context: model.Context{Messages: []model.Message{{
		Role: model.RoleUser,
		Content: []model.ContentBlock{
			{Kind: model.ContentText, Text: "hello "},
			{Kind: model.ContentText, Text: "world"},
		},
	}}}}
	got, err := buildRequest(model.Model{ID: "gpt-test"}, request)
	if err != nil {
		t.Fatalf("buildRequest() error = %v", err)
	}
	if got.Messages[0].Content != "hello world" {
		t.Fatalf("content = %#v", got.Messages[0].Content)
	}
}

func TestBuildRequestMapsReasoning(t *testing.T) {
	tests := []struct {
		name  string
		level *model.ReasoningLevel
		want  string
	}{
		{name: "nil", level: nil, want: ""},
		{name: "default", level: reasoningLevel(model.ReasoningDefault), want: ""},
		{name: "off", level: reasoningLevel(model.ReasoningOff), want: "none"},
		{name: "low", level: reasoningLevel(model.ReasoningLow), want: "low"},
		{name: "medium", level: reasoningLevel(model.ReasoningMedium), want: "medium"},
		{name: "high", level: reasoningLevel(model.ReasoningHigh), want: "high"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := buildRequest(model.Model{ID: "gpt-test"}, model.Request{
				Reasoning: test.level,
				Context:   basicRequest().Context,
			})
			if err != nil {
				t.Fatalf("buildRequest() error = %v", err)
			}
			if got.ReasoningEffort != test.want {
				t.Fatalf("ReasoningEffort = %q, want %q", got.ReasoningEffort, test.want)
			}
		})
	}
}

func TestBuildRequestRejectsInvalidInput(t *testing.T) {
	nan := math.NaN()
	negativeTemperature := -0.1
	tooHighTemperature := 2.1
	zero := 0
	unknownReasoning := model.ReasoningLevel(99)

	tests := []struct {
		name    string
		request model.Request
	}{
		{name: "no messages", request: model.Request{}},
		{name: "NaN temperature", request: requestWithOptions(&nan, nil, nil)},
		{name: "negative temperature", request: requestWithOptions(&negativeTemperature, nil, nil)},
		{name: "high temperature", request: requestWithOptions(&tooHighTemperature, nil, nil)},
		{name: "zero max tokens", request: requestWithOptions(nil, &zero, nil)},
		{name: "unknown reasoning", request: requestWithOptions(nil, nil, &unknownReasoning)},
		{name: "unknown role", request: requestWithMessage(model.Message{Role: model.RoleUnknown})},
		{name: "user tool metadata", request: requestWithMessage(model.Message{Role: model.RoleUser, ToolCallID: "call-1"})},
		{name: "assistant tool metadata", request: requestWithMessage(model.Message{Role: model.RoleAssistant, IsError: true})},
		{name: "thinking", request: requestWithMessage(model.Message{Role: model.RoleAssistant, Content: []model.ContentBlock{{Kind: model.ContentThinking, Text: "secret"}}})},
		{name: "user tool call", request: requestWithMessage(model.Message{Role: model.RoleUser, Content: []model.ContentBlock{{Kind: model.ContentToolCall, ToolCall: &model.ToolCall{}}}})},
		{name: "assistant image", request: requestWithMessage(model.Message{Role: model.RoleAssistant, Content: []model.ContentBlock{{Kind: model.ContentImage, URL: "https://example.com/image.png"}}})},
		{name: "nil tool call", request: requestWithMessage(model.Message{Role: model.RoleAssistant, Content: []model.ContentBlock{{Kind: model.ContentToolCall}}})},
		{name: "tool call no ID", request: requestWithMessage(assistantToolCall(&model.ToolCall{Name: "tool", Arguments: json.RawMessage(`{}`)}))},
		{name: "tool call no name", request: requestWithMessage(assistantToolCall(&model.ToolCall{ID: "call-1", Arguments: json.RawMessage(`{}`)}))},
		{name: "invalid tool arguments", request: requestWithMessage(assistantToolCall(&model.ToolCall{ID: "call-1", Name: "tool", Arguments: json.RawMessage(`{`)}))},
		{name: "tool result no ID", request: requestWithMessage(model.Message{Role: model.RoleTool, Content: []model.ContentBlock{{Kind: model.ContentText, Text: "result"}}})},
		{name: "tool result image", request: requestWithMessage(model.Message{Role: model.RoleTool, ToolCallID: "call-1", Content: []model.ContentBlock{{Kind: model.ContentImage, URL: "https://example.com/image.png"}}})},
		{name: "image URL and data", request: requestWithImage(model.ContentBlock{Kind: model.ContentImage, URL: "https://example.com/image.png", MIMEType: "image/png", Data: "aA=="})},
		{name: "image no MIME", request: requestWithImage(model.ContentBlock{Kind: model.ContentImage, Data: "aA=="})},
		{name: "invalid MIME", request: requestWithImage(model.ContentBlock{Kind: model.ContentImage, MIMEType: "text/plain", Data: "aA=="})},
		{name: "invalid base64", request: requestWithImage(model.ContentBlock{Kind: model.ContentImage, MIMEType: "image/png", Data: "%%%"})},
		{name: "tool no name", request: requestWithTools([]model.ToolSpec{{Parameters: json.RawMessage(`{}`)}})},
		{name: "invalid tool schema", request: requestWithTools([]model.ToolSpec{{Name: "tool", Parameters: json.RawMessage(`[]`)}})},
		{name: "duplicate tools", request: requestWithTools([]model.ToolSpec{{Name: "tool", Parameters: json.RawMessage(`{}`)}, {Name: "tool", Parameters: json.RawMessage(`{}`)}})},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := buildRequest(model.Model{ID: "gpt-test"}, test.request)
			if !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("buildRequest() error = %v, want ErrInvalidRequest", err)
			}
		})
	}
}

func TestBuildRequestDoesNotMutateInput(t *testing.T) {
	request := model.Request{Context: model.Context{
		Messages: []model.Message{{
			Role: model.RoleAssistant,
			Content: []model.ContentBlock{{Kind: model.ContentToolCall, ToolCall: &model.ToolCall{
				ID: "call-1", Name: "tool",
			}}},
		}},
		Tools: []model.ToolSpec{{Name: "tool", Parameters: json.RawMessage(`{"type":"object"}`)}},
	}}
	want := model.Request{Context: model.Context{
		Messages: []model.Message{{
			Role: model.RoleAssistant,
			Content: []model.ContentBlock{{Kind: model.ContentToolCall, ToolCall: &model.ToolCall{
				ID: "call-1", Name: "tool",
			}}},
		}},
		Tools: []model.ToolSpec{{Name: "tool", Parameters: json.RawMessage(`{"type":"object"}`)}},
	}}
	if _, err := buildRequest(model.Model{ID: "gpt-test"}, request); err != nil {
		t.Fatalf("buildRequest() error = %v", err)
	}
	if !reflect.DeepEqual(request, want) {
		t.Fatalf("request mutated:\n got: %#v\nwant: %#v", request, want)
	}
}

func reasoningLevel(level model.ReasoningLevel) *model.ReasoningLevel {
	return &level
}

func requestWithOptions(temperature *float64, maxTokens *int, reasoning *model.ReasoningLevel) model.Request {
	request := basicRequest()
	request.Temperature = temperature
	request.MaxTokens = maxTokens
	request.Reasoning = reasoning
	return request
}

func requestWithMessage(message model.Message) model.Request {
	return model.Request{Context: model.Context{Messages: []model.Message{message}}}
}

func assistantToolCall(call *model.ToolCall) model.Message {
	return model.Message{Role: model.RoleAssistant, Content: []model.ContentBlock{{Kind: model.ContentToolCall, ToolCall: call}}}
}

func requestWithImage(image model.ContentBlock) model.Request {
	return requestWithMessage(model.Message{Role: model.RoleUser, Content: []model.ContentBlock{image}})
}

func requestWithTools(tools []model.ToolSpec) model.Request {
	request := basicRequest()
	request.Context.Tools = tools
	return request
}
