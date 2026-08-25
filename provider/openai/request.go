package openai

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"mime"
	"strings"

	"github.com/JIAOZAI1/acore/model"
)

type chatRequest struct {
	Model               string            `json:"model"`
	Messages            []chatMessage     `json:"messages"`
	Tools               []chatTool        `json:"tools,omitempty"`
	Temperature         *float64          `json:"temperature,omitempty"`
	MaxCompletionTokens *int              `json:"max_completion_tokens,omitempty"`
	ReasoningEffort     string            `json:"reasoning_effort,omitempty"`
	Stream              bool              `json:"stream"`
	StreamOptions       chatStreamOptions `json:"stream_options"`
}

type chatStreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type chatMessage struct {
	Role       string         `json:"role"`
	Content    any            `json:"content,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
	ToolCalls  []chatToolCall `json:"tool_calls,omitempty"`
}

type chatContentPart struct {
	Type     string        `json:"type"`
	Text     string        `json:"text,omitempty"`
	ImageURL *chatImageURL `json:"image_url,omitempty"`
}

type chatImageURL struct {
	URL string `json:"url"`
}

type chatToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function chatToolFunction `json:"function"`
}

type chatToolFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type chatTool struct {
	Type     string               `json:"type"`
	Function chatToolFunctionSpec `json:"function"`
}

type chatToolFunctionSpec struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters"`
}

func buildRequest(selected model.Model, request model.Request) (chatRequest, error) {
	if err := validateOptions(request); err != nil {
		return chatRequest{}, err
	}
	messages, err := buildMessages(request.Context)
	if err != nil {
		return chatRequest{}, err
	}
	tools, err := buildTools(request.Context.Tools)
	if err != nil {
		return chatRequest{}, err
	}

	reasoningEffort, err := mapReasoning(request.Reasoning)
	if err != nil {
		return chatRequest{}, err
	}
	return chatRequest{
		Model:               selected.ID,
		Messages:            messages,
		Tools:               tools,
		Temperature:         request.Temperature,
		MaxCompletionTokens: request.MaxTokens,
		ReasoningEffort:     reasoningEffort,
		Stream:              true,
		StreamOptions:       chatStreamOptions{IncludeUsage: true},
	}, nil
}

func validateOptions(request model.Request) error {
	if request.Temperature != nil {
		temperature := *request.Temperature
		if math.IsNaN(temperature) || math.IsInf(temperature, 0) || temperature < 0 || temperature > 2 {
			return invalidRequest("temperature must be between 0 and 2")
		}
	}
	if request.MaxTokens != nil && *request.MaxTokens <= 0 {
		return invalidRequest("max tokens must be positive")
	}
	return nil
}

func mapReasoning(level *model.ReasoningLevel) (string, error) {
	if level == nil || *level == model.ReasoningDefault {
		return "", nil
	}
	switch *level {
	case model.ReasoningOff:
		return "none", nil
	case model.ReasoningLow:
		return "low", nil
	case model.ReasoningMedium:
		return "medium", nil
	case model.ReasoningHigh:
		return "high", nil
	default:
		return "", invalidRequest("unknown reasoning level %d", *level)
	}
}

func buildMessages(input model.Context) ([]chatMessage, error) {
	messages := make([]chatMessage, 0, len(input.Messages)+1)
	if input.SystemPrompt != "" {
		messages = append(messages, chatMessage{Role: "developer", Content: input.SystemPrompt})
	}
	for i, message := range input.Messages {
		converted, err := buildMessage(message)
		if err != nil {
			return nil, fmt.Errorf("%w: message %d: %v", ErrInvalidRequest, i, err)
		}
		messages = append(messages, converted)
	}
	if len(messages) == 0 {
		return nil, invalidRequest("at least one message is required")
	}
	return messages, nil
}

func buildMessage(message model.Message) (chatMessage, error) {
	switch message.Role {
	case model.RoleUser:
		if message.ToolCallID != "" || message.IsError {
			return chatMessage{}, fmt.Errorf("user message contains tool metadata")
		}
		return buildUserMessage(message)
	case model.RoleAssistant:
		if message.ToolCallID != "" || message.IsError {
			return chatMessage{}, fmt.Errorf("assistant message contains tool metadata")
		}
		return buildAssistantMessage(message)
	case model.RoleTool:
		return buildToolMessage(message)
	default:
		return chatMessage{}, fmt.Errorf("unsupported role %q", message.Role)
	}
}

func buildUserMessage(message model.Message) (chatMessage, error) {
	parts := make([]chatContentPart, 0, len(message.Content))
	var text strings.Builder
	hasImage := false
	for i, block := range message.Content {
		switch block.Kind {
		case model.ContentText:
			text.WriteString(block.Text)
			if block.Text != "" {
				parts = append(parts, chatContentPart{Type: "text", Text: block.Text})
			}
		case model.ContentImage:
			imageURL, err := buildImageURL(block)
			if err != nil {
				return chatMessage{}, fmt.Errorf("content %d: %w", i, err)
			}
			hasImage = true
			parts = append(parts, chatContentPart{Type: "image_url", ImageURL: &chatImageURL{URL: imageURL}})
		case model.ContentThinking:
			return chatMessage{}, fmt.Errorf("content %d: thinking content is unsupported", i)
		case model.ContentToolCall:
			return chatMessage{}, fmt.Errorf("content %d: tool call is only valid in assistant messages", i)
		default:
			return chatMessage{}, fmt.Errorf("content %d: unsupported content kind %q", i, block.Kind)
		}
	}

	content := any(text.String())
	if hasImage {
		content = parts
	}
	return chatMessage{Role: "user", Content: content}, nil
}

func buildAssistantMessage(message model.Message) (chatMessage, error) {
	result := chatMessage{Role: "assistant"}
	var text strings.Builder
	for i, block := range message.Content {
		switch block.Kind {
		case model.ContentText:
			text.WriteString(block.Text)
		case model.ContentToolCall:
			call, err := buildToolCall(block.ToolCall)
			if err != nil {
				return chatMessage{}, fmt.Errorf("content %d: %w", i, err)
			}
			result.ToolCalls = append(result.ToolCalls, call)
		case model.ContentThinking:
			return chatMessage{}, fmt.Errorf("content %d: thinking content is unsupported", i)
		case model.ContentImage:
			return chatMessage{}, fmt.Errorf("content %d: image is only valid in user messages", i)
		default:
			return chatMessage{}, fmt.Errorf("content %d: unsupported content kind %q", i, block.Kind)
		}
	}
	if text.Len() > 0 || len(result.ToolCalls) == 0 {
		result.Content = text.String()
	}
	return result, nil
}

func buildToolMessage(message model.Message) (chatMessage, error) {
	if strings.TrimSpace(message.ToolCallID) == "" {
		return chatMessage{}, fmt.Errorf("tool message has no tool call ID")
	}
	var text strings.Builder
	for i, block := range message.Content {
		if block.Kind != model.ContentText {
			return chatMessage{}, fmt.Errorf("content %d: tool messages only support text", i)
		}
		text.WriteString(block.Text)
	}
	return chatMessage{Role: "tool", Content: text.String(), ToolCallID: message.ToolCallID}, nil
}

func buildToolCall(call *model.ToolCall) (chatToolCall, error) {
	if call == nil {
		return chatToolCall{}, fmt.Errorf("tool call is nil")
	}
	if strings.TrimSpace(call.ID) == "" {
		return chatToolCall{}, fmt.Errorf("tool call has no ID")
	}
	if strings.TrimSpace(call.Name) == "" {
		return chatToolCall{}, fmt.Errorf("tool call has no name")
	}
	arguments := call.Arguments
	if len(arguments) == 0 {
		arguments = json.RawMessage(`{}`)
	}
	if !json.Valid(arguments) {
		return chatToolCall{}, fmt.Errorf("tool call %q has invalid arguments", call.Name)
	}
	return chatToolCall{
		ID:   call.ID,
		Type: "function",
		Function: chatToolFunction{
			Name:      call.Name,
			Arguments: string(arguments),
		},
	}, nil
}

func buildImageURL(block model.ContentBlock) (string, error) {
	if block.URL != "" && block.Data != "" {
		return "", fmt.Errorf("image cannot contain both URL and data")
	}
	if block.URL != "" {
		return block.URL, nil
	}
	if block.Data == "" || strings.TrimSpace(block.MIMEType) == "" {
		return "", fmt.Errorf("base64 image requires data and MIME type")
	}
	mediaType, _, err := mime.ParseMediaType(block.MIMEType)
	if err != nil || !strings.HasPrefix(strings.ToLower(mediaType), "image/") {
		return "", fmt.Errorf("invalid image MIME type %q", block.MIMEType)
	}
	if _, err := base64.StdEncoding.DecodeString(block.Data); err != nil {
		return "", fmt.Errorf("invalid base64 image data: %w", err)
	}
	return "data:" + strings.TrimSpace(block.MIMEType) + ";base64," + block.Data, nil
}

func buildTools(specs []model.ToolSpec) ([]chatTool, error) {
	tools := make([]chatTool, 0, len(specs))
	names := make(map[string]struct{}, len(specs))
	for i, spec := range specs {
		if strings.TrimSpace(spec.Name) == "" {
			return nil, invalidRequest("tool %d has no name", i)
		}
		if _, exists := names[spec.Name]; exists {
			return nil, invalidRequest("duplicate tool %q", spec.Name)
		}
		if !isJSONObject(spec.Parameters) {
			return nil, invalidRequest("tool %q has an invalid parameter schema", spec.Name)
		}
		names[spec.Name] = struct{}{}
		tools = append(tools, chatTool{
			Type: "function",
			Function: chatToolFunctionSpec{
				Name:        spec.Name,
				Description: spec.Description,
				Parameters:  spec.Parameters,
			},
		})
	}
	return tools, nil
}

func isJSONObject(value json.RawMessage) bool {
	if len(value) == 0 || !json.Valid(value) {
		return false
	}
	var object map[string]json.RawMessage
	return json.Unmarshal(value, &object) == nil && object != nil
}

func invalidRequest(format string, arguments ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidRequest, fmt.Sprintf(format, arguments...))
}
