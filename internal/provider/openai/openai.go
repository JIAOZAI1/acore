// Package openai implements the model.Provider contract with OpenAI's
// Chat Completions streaming API.
package openai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/JIAOZAI1/acore/model"
)

const (
	defaultProviderID = "openai"
	defaultBaseURL    = "https://api.openai.com/v1"
	chatPath          = "/chat/completions"
)

// Config configures an OpenAI or OpenAI-compatible provider.
type Config struct {
	// ID defaults to "openai". Give compatible providers their own ID.
	ID      string
	APIKey  string
	BaseURL string
	Headers map[string]string
	Client  *http.Client
	Models  []model.Model
}

// Provider implements model.Provider.
type Provider struct {
	id      string
	apiKey  string
	baseURL string
	headers map[string]string
	client  *http.Client
	models  []model.Model
}

// New creates a provider. Authentication is configured on the provider rather
// than included in model.Request, preventing credentials from entering message
// serialization.
func New(config Config) (*Provider, error) {
	if config.ID == "" {
		config.ID = defaultProviderID
	}
	if config.BaseURL == "" {
		config.BaseURL = defaultBaseURL
	}
	if config.Client == nil {
		config.Client = &http.Client{Timeout: 5 * time.Minute}
	}
	if len(config.Models) == 0 {
		config.Models = defaultModels(config.ID)
	}
	for i := range config.Models {
		if config.Models[i].ID == "" {
			return nil, fmt.Errorf("openai: model %d has an empty ID", i)
		}
		if config.Models[i].Provider == "" {
			config.Models[i].Provider = config.ID
		}
		if config.Models[i].Provider != config.ID {
			return nil, fmt.Errorf("openai: model %q belongs to provider %q, want %q", config.Models[i].ID, config.Models[i].Provider, config.ID)
		}
	}

	return &Provider{
		id:      config.ID,
		apiKey:  config.APIKey,
		baseURL: strings.TrimRight(config.BaseURL, "/"),
		headers: cloneMap(config.Headers),
		client:  config.Client,
		models:  cloneModels(config.Models),
	}, nil
}

func defaultModels(providerID string) []model.Model {
	return []model.Model{
		{
			ID:              "gpt-4o-mini",
			Name:            "GPT-4o mini",
			Provider:        providerID,
			API:             "openai-chat-completions",
			InputModalities: []string{"text", "image"},
			ContextWindow:   128_000,
			MaxOutputTokens: 16_384,
		},
		{
			ID:              "gpt-4o",
			Name:            "GPT-4o",
			Provider:        providerID,
			API:             "openai-chat-completions",
			InputModalities: []string{"text", "image"},
			ContextWindow:   128_000,
			MaxOutputTokens: 16_384,
		},
	}
}

func (p *Provider) ID() string { return p.id }

func (p *Provider) Models() []model.Model { return cloneModels(p.models) }

// Generate establishes a streaming Chat Completions request. The returned
// generator owns the response body and closes it when iteration ends, including
// when the consumer stops early.
func (p *Provider) Generate(ctx context.Context, selected model.Model, request model.Request) (model.Stream, error) {
	if selected.Provider != p.id {
		return nil, fmt.Errorf("openai: model provider %q does not match %q", selected.Provider, p.id)
	}

	body, err := buildRequest(selected, request)
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("openai: encode request: %w", err)
	}

	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+chatPath, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("openai: create request: %w", err)
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Accept", "text/event-stream")
	if p.apiKey != "" {
		httpRequest.Header.Set("Authorization", "Bearer "+p.apiKey)
	}
	for key, value := range p.headers {
		httpRequest.Header.Set(key, value)
	}

	response, err := p.client.Do(httpRequest)
	if err != nil {
		return nil, fmt.Errorf("openai: send request: %w", err)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		defer response.Body.Close()
		message, _ := io.ReadAll(io.LimitReader(response.Body, 64<<10))
		return nil, &APIError{StatusCode: response.StatusCode, Body: strings.TrimSpace(string(message))}
	}

	return func(yield func(model.Event, error) bool) {
		defer response.Body.Close()
		consumeSSE(ctx, response.Body, selected, yield)
	}, nil
}

// APIError is a non-success response returned by the OpenAI API.
type APIError struct {
	StatusCode int
	Body       string
}

func (e *APIError) Error() string {
	if e.Body == "" {
		return fmt.Sprintf("openai: HTTP %d", e.StatusCode)
	}
	return fmt.Sprintf("openai: HTTP %d: %s", e.StatusCode, e.Body)
}

type chatRequest struct {
	Model               string        `json:"model"`
	Messages            []chatMessage `json:"messages"`
	Tools               []chatTool    `json:"tools,omitempty"`
	Temperature         *float64      `json:"temperature,omitempty"`
	MaxTokens           *int          `json:"max_tokens,omitempty"`
	MaxCompletionTokens *int          `json:"max_completion_tokens,omitempty"`
	ReasoningEffort     string        `json:"reasoning_effort,omitempty"`
	Stream              bool          `json:"stream"`
	StreamOptions       streamOptions `json:"stream_options"`
}

type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type chatMessage struct {
	Role             string     `json:"role"`
	Content          any        `json:"content,omitempty"`
	ToolCallID       string     `json:"tool_call_id,omitempty"`
	ToolCalls        []toolCall `json:"tool_calls,omitempty"`
	ReasoningContent string     `json:"reasoning_content,omitempty"`
}

type contentPart struct {
	Type     string    `json:"type"`
	Text     string    `json:"text,omitempty"`
	ImageURL *imageURL `json:"image_url,omitempty"`
}

type imageURL struct {
	URL string `json:"url"`
}

type toolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function toolFunction `json:"function"`
}

type toolFunction struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

type chatTool struct {
	Type     string       `json:"type"`
	Function toolSpecBody `json:"function"`
}

type toolSpecBody struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters"`
}

func buildRequest(selected model.Model, request model.Request) (chatRequest, error) {
	messages, err := buildMessages(request.Context)
	if err != nil {
		return chatRequest{}, err
	}
	body := chatRequest{
		Model:         selected.ID,
		Messages:      messages,
		Temperature:   request.Temperature,
		Stream:        true,
		StreamOptions: streamOptions{IncludeUsage: true},
	}
	if selected.Reasoning {
		body.MaxCompletionTokens = request.MaxTokens
	} else {
		body.MaxTokens = request.MaxTokens
	}
	if request.Reasoning != nil {
		switch *request.Reasoning {
		case model.ReasoningLow:
			body.ReasoningEffort = "low"
		case model.ReasoningMedium:
			body.ReasoningEffort = "medium"
		case model.ReasoningHigh:
			body.ReasoningEffort = "high"
		}
	}
	for _, tool := range request.Context.Tools {
		body.Tools = append(body.Tools, chatTool{
			Type: "function",
			Function: toolSpecBody{
				Name: tool.Name, Description: tool.Description, Parameters: tool.Parameters,
			},
		})
	}
	return body, nil
}

func buildMessages(input model.Context) ([]chatMessage, error) {
	messages := make([]chatMessage, 0, len(input.Messages)+1)
	if input.SystemPrompt != "" {
		messages = append(messages, chatMessage{Role: "system", Content: input.SystemPrompt})
	}
	for i, message := range input.Messages {
		converted, err := buildMessage(message)
		if err != nil {
			return nil, fmt.Errorf("openai: message %d: %w", i, err)
		}
		messages = append(messages, converted)
	}
	return messages, nil
}

func buildMessage(message model.Message) (chatMessage, error) {
	result := chatMessage{}
	switch message.Role {
	case model.RoleUser:
		result.Role = "user"
	case model.RoleAssistant:
		result.Role = "assistant"
	case model.RoleTool:
		if message.ToolCallID == "" {
			return chatMessage{}, errors.New("tool message has no tool call ID")
		}
		result.Role = "tool"
		result.ToolCallID = message.ToolCallID
	default:
		return chatMessage{}, fmt.Errorf("unsupported role %q", message.Role)
	}

	parts := make([]contentPart, 0, len(message.Content))
	var text strings.Builder
	for _, block := range message.Content {
		switch block.Kind {
		case model.ContentText:
			text.WriteString(block.Text)
			parts = append(parts, contentPart{Type: "text", Text: block.Text})
		case model.ContentThinking:
			if message.Role == model.RoleAssistant && !block.Redacted {
				result.ReasoningContent += block.Text
			}
		case model.ContentImage:
			if message.Role != model.RoleUser {
				return chatMessage{}, errors.New("images are only supported in user messages")
			}
			url, err := blockImageURL(block)
			if err != nil {
				return chatMessage{}, err
			}
			parts = append(parts, contentPart{Type: "image_url", ImageURL: &imageURL{URL: url}})
		case model.ContentToolCall:
			if message.Role != model.RoleAssistant || block.ToolCall == nil {
				return chatMessage{}, errors.New("tool calls are only supported in assistant messages")
			}
			arguments := block.ToolCall.Arguments
			if len(arguments) == 0 {
				arguments = json.RawMessage("{}")
			}
			if !json.Valid(arguments) {
				return chatMessage{}, fmt.Errorf("tool call %q has invalid arguments", block.ToolCall.Name)
			}
			result.ToolCalls = append(result.ToolCalls, toolCall{
				ID:   block.ToolCall.ID,
				Type: "function",
				Function: toolFunction{
					Name: block.ToolCall.Name, Arguments: arguments,
				},
			})
		default:
			return chatMessage{}, fmt.Errorf("unsupported content kind %q", block.Kind)
		}
	}

	if message.Role == model.RoleTool {
		result.Content = text.String()
	} else if len(parts) == 0 {
		result.Content = ""
	} else if onlyText(parts) {
		result.Content = text.String()
	} else {
		result.Content = parts
	}
	return result, nil
}

func blockImageURL(block model.ContentBlock) (string, error) {
	if block.URL != "" && block.Data != "" {
		return "", errors.New("image cannot contain both URL and data")
	}
	if block.URL != "" {
		return block.URL, nil
	}
	if block.Data == "" || block.MIMEType == "" {
		return "", errors.New("base64 image requires data and MIME type")
	}
	return "data:" + block.MIMEType + ";base64," + block.Data, nil
}

func onlyText(parts []contentPart) bool {
	for _, part := range parts {
		if part.Type != "text" {
			return false
		}
	}
	return true
}

// The streaming wire types intentionally contain only fields needed by the
// provider-independent protocol.
type chunk struct {
	Model   string   `json:"model"`
	Choices []choice `json:"choices"`
	Usage   *usage   `json:"usage,omitempty"`
}

type choice struct {
	Index        int     `json:"index"`
	Delta        delta   `json:"delta"`
	FinishReason *string `json:"finish_reason"`
}

type delta struct {
	Content   *string         `json:"content,omitempty"`
	Reasoning *string         `json:"reasoning_content,omitempty"`
	ToolCalls []deltaToolCall `json:"tool_calls,omitempty"`
}

type deltaToolCall struct {
	Index    int    `json:"index"`
	ID       string `json:"id,omitempty"`
	Function struct {
		Name      string `json:"name,omitempty"`
		Arguments string `json:"arguments,omitempty"`
	} `json:"function"`
}

type usage struct {
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
	TotalTokens      int64 `json:"total_tokens"`
	PromptDetails    struct {
		CachedTokens int64 `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
	CompletionDetails struct {
		ReasoningTokens int64 `json:"reasoning_tokens"`
	} `json:"completion_tokens_details"`
}

type blockAccumulator struct {
	kind      model.ContentKind
	text      strings.Builder
	toolID    string
	toolName  string
	toolArgs  strings.Builder
	toolIndex int
}

type streamState struct {
	blocks         []*blockAccumulator
	textIndex      int
	reasoningIndex int
	toolIndexes    map[int]int
	usage          model.Usage
	finishReason   *string
}

func newStreamState() *streamState {
	return &streamState{textIndex: -1, reasoningIndex: -1, toolIndexes: make(map[int]int)}
}

func consumeSSE(ctx context.Context, reader io.Reader, selected model.Model, yield func(model.Event, error) bool) {
	if !yield(model.Event{Type: model.EventStart}, nil) {
		return
	}

	state := newStreamState()
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64<<10), 4<<20)
	sawDone := false

	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			yield(model.Event{}, err)
			return
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			sawDone = true
			break
		}

		var value chunk
		if err := json.Unmarshal([]byte(data), &value); err != nil {
			yield(model.Event{}, fmt.Errorf("openai: decode stream chunk: %w", err))
			return
		}
		if value.Usage != nil {
			state.usage = model.Usage{
				InputTokens:     value.Usage.PromptTokens,
				OutputTokens:    value.Usage.CompletionTokens,
				CacheRead:       value.Usage.PromptDetails.CachedTokens,
				ReasoningTokens: value.Usage.CompletionDetails.ReasoningTokens,
				TotalTokens:     value.Usage.TotalTokens,
			}
		}
		for _, choice := range value.Choices {
			if choice.Index != 0 {
				continue // The portable Result represents one assistant candidate.
			}
			if choice.FinishReason != nil {
				state.finishReason = choice.FinishReason
			}
			if !state.consumeDelta(choice.Delta, yield) {
				return
			}
		}
	}
	if err := scanner.Err(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			yield(model.Event{}, ctxErr)
		} else {
			yield(model.Event{}, fmt.Errorf("openai: read stream: %w", err))
		}
		return
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		yield(model.Event{}, ctxErr)
		return
	}
	if !sawDone {
		yield(model.Event{}, errors.New("openai: stream ended before [DONE]"))
		return
	}
	if state.finishReason == nil {
		yield(model.Event{}, errors.New("openai: stream has no finish reason"))
		return
	}

	content := make([]model.ContentBlock, 0, len(state.blocks))
	for index, accumulator := range state.blocks {
		block, err := accumulator.finalBlock()
		if err != nil {
			yield(model.Event{}, err)
			return
		}
		content = append(content, block)
		if !yield(model.Event{Type: model.EventContentEnd, ContentIndex: index, Block: &block}, nil) {
			return
		}
	}
	stopReason := mapStopReason(*state.finishReason)
	result := &model.Result{
		Message:    model.Message{Role: model.RoleAssistant, Content: content},
		Usage:      state.usage,
		StopReason: stopReason,
		ModelID:    selected.ID,
		ProviderID: selected.Provider,
	}
	yield(model.Event{Type: model.EventDone, Result: result}, nil)
}

func (s *streamState) consumeDelta(value delta, yield func(model.Event, error) bool) bool {
	if value.Reasoning != nil && *value.Reasoning != "" {
		index, ok := s.ensureBlock(model.ContentThinking, -1, yield)
		if !ok {
			return false
		}
		s.blocks[index].text.WriteString(*value.Reasoning)
		if !yield(model.Event{Type: model.EventContentDelta, ContentIndex: index, Delta: *value.Reasoning}, nil) {
			return false
		}
	}
	if value.Content != nil && *value.Content != "" {
		index, ok := s.ensureBlock(model.ContentText, -1, yield)
		if !ok {
			return false
		}
		s.blocks[index].text.WriteString(*value.Content)
		if !yield(model.Event{Type: model.EventContentDelta, ContentIndex: index, Delta: *value.Content}, nil) {
			return false
		}
	}
	for _, call := range value.ToolCalls {
		index, ok := s.ensureBlock(model.ContentToolCall, call.Index, yield)
		if !ok {
			return false
		}
		accumulator := s.blocks[index]
		if call.ID != "" {
			accumulator.toolID = call.ID
		}
		if call.Function.Name != "" {
			accumulator.toolName += call.Function.Name
		}
		if call.Function.Arguments != "" {
			accumulator.toolArgs.WriteString(call.Function.Arguments)
			if !yield(model.Event{Type: model.EventContentDelta, ContentIndex: index, Delta: call.Function.Arguments}, nil) {
				return false
			}
		}
	}
	return true
}

func (s *streamState) ensureBlock(kind model.ContentKind, toolIndex int, yield func(model.Event, error) bool) (int, bool) {
	var index int
	switch kind {
	case model.ContentText:
		index = s.textIndex
	case model.ContentThinking:
		index = s.reasoningIndex
	case model.ContentToolCall:
		index = -1
		if existing, ok := s.toolIndexes[toolIndex]; ok {
			index = existing
		}
	}
	if index >= 0 {
		return index, true
	}

	index = len(s.blocks)
	accumulator := &blockAccumulator{kind: kind, toolIndex: toolIndex}
	s.blocks = append(s.blocks, accumulator)
	switch kind {
	case model.ContentText:
		s.textIndex = index
	case model.ContentThinking:
		s.reasoningIndex = index
	case model.ContentToolCall:
		s.toolIndexes[toolIndex] = index
	}
	startBlock := model.ContentBlock{Kind: kind}
	if !yield(model.Event{Type: model.EventContentStart, ContentIndex: index, Block: &startBlock}, nil) {
		return index, false
	}
	return index, true
}

func (a *blockAccumulator) finalBlock() (model.ContentBlock, error) {
	switch a.kind {
	case model.ContentText, model.ContentThinking:
		return model.ContentBlock{Kind: a.kind, Text: a.text.String()}, nil
	case model.ContentToolCall:
		arguments := json.RawMessage(a.toolArgs.String())
		if len(arguments) == 0 {
			arguments = json.RawMessage("{}")
		}
		if !json.Valid(arguments) {
			return model.ContentBlock{}, fmt.Errorf("openai: tool call %q returned invalid arguments", a.toolName)
		}
		return model.ContentBlock{Kind: model.ContentToolCall, ToolCall: &model.ToolCall{
			ID: a.toolID, Name: a.toolName, Arguments: arguments,
		}}, nil
	default:
		return model.ContentBlock{}, errors.New("openai: unsupported streamed content block")
	}
}

func mapStopReason(reason string) model.StopReason {
	switch reason {
	case "stop":
		return model.ReasonStop
	case "length":
		return model.ReasonLength
	case "tool_calls", "function_call":
		return model.ReasonToolUse
	case "content_filter":
		return model.ReasonContentFilter
	default:
		return model.ReasonUnknown
	}
}

func cloneMap(input map[string]string) map[string]string {
	if input == nil {
		return nil
	}
	output := make(map[string]string, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}

func cloneModels(input []model.Model) []model.Model {
	output := make([]model.Model, len(input))
	copy(output, input)
	for i := range output {
		output[i].InputModalities = append([]string(nil), input[i].InputModalities...)
	}
	return output
}

var _ model.Provider = (*Provider)(nil)
