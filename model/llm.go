// Package model defines the provider-independent LLM protocol used by the
// agent loop. It contains protocol data and orchestration helpers only; vendor
// request/response types belong in provider implementations.
package model

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
)

// ContentKind identifies the payload represented by a ContentBlock.
type ContentKind uint8

const (
	ContentUnknown ContentKind = iota
	ContentText
	ContentThinking
	ContentImage
	ContentToolCall
)

func (k ContentKind) String() string {
	switch k {
	case ContentText:
		return "text"
	case ContentThinking:
		return "thinking"
	case ContentImage:
		return "image"
	case ContentToolCall:
		return "toolCall"
	default:
		return "unknown"
	}
}

// ContentBlock is the serializable union used for message content.
// Only fields belonging to Kind are meaningful.
type ContentBlock struct {
	Kind ContentKind `json:"kind"`

	// Text and Thinking.
	Text      string  `json:"text,omitempty"`
	Signature *string `json:"signature,omitempty"`
	Redacted  bool    `json:"redacted,omitempty"`

	// Image. Exactly one of URL and Data should be set. Data is raw base64.
	MIMEType string `json:"mimeType,omitempty"`
	URL      string `json:"url,omitempty"`
	Data     string `json:"data,omitempty"`

	// Tool call.
	ToolCall *ToolCall `json:"toolCall,omitempty"`
}

// ToolCall is a model-requested function invocation. Arguments is kept as raw
// JSON so replay does not lose number precision or provider signatures.
type ToolCall struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// Role identifies the author of a conversation message.
type Role uint8

const (
	RoleUnknown Role = iota
	RoleUser
	RoleAssistant
	RoleTool
)

func (r Role) String() string {
	switch r {
	case RoleUser:
		return "user"
	case RoleAssistant:
		return "assistant"
	case RoleTool:
		return "tool"
	default:
		return "unknown"
	}
}

// Message is replayable conversation data. Generation metadata such as usage
// and stop reason belongs to Result rather than the conversation message.
type Message struct {
	Role    Role           `json:"role"`
	Content []ContentBlock `json:"content"`

	// ToolCallID is required for RoleTool and links a result to its call.
	ToolCallID string `json:"toolCallId,omitempty"`
	IsError    bool   `json:"isError,omitempty"`
}

// ToolSpec describes a function available to the model.
type ToolSpec struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters"` // JSON Schema
}

// Context is the complete, immutable conversation input to one generation.
type Context struct {
	SystemPrompt string     `json:"systemPrompt,omitempty"`
	Messages     []Message  `json:"messages"`
	Tools        []ToolSpec `json:"tools,omitempty"`
}

// Model is a pure data descriptor. Provider contains behavior.
type Model struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	Provider        string   `json:"provider"`
	API             string   `json:"api,omitempty"`
	Reasoning       bool     `json:"reasoning,omitempty"`
	InputModalities []string `json:"inputModalities,omitempty"`
	// ContextWindow is the maximum combined input and requested output token
	// count for one generation. Zero means unknown.
	ContextWindow int `json:"contextWindow,omitempty"`
	// MaxOutputTokens is the model's maximum output token count. Zero means
	// unknown.
	MaxOutputTokens int `json:"maxOutputTokens,omitempty"`
}

// ReasoningLevel requests a provider-independent reasoning intensity.
type ReasoningLevel uint8

const (
	ReasoningDefault ReasoningLevel = iota
	ReasoningOff
	ReasoningLow
	ReasoningMedium
	ReasoningHigh
)

// Request contains portable generation options. Provider-specific options are
// intentionally excluded from the core contract.
type Request struct {
	Context     Context         `json:"context"`
	Temperature *float64        `json:"temperature,omitempty"`
	MaxTokens   *int            `json:"maxTokens,omitempty"`
	Reasoning   *ReasoningLevel `json:"reasoning,omitempty"`
}

// StopReason tells the agent loop why generation ended.
type StopReason uint8

const (
	ReasonUnknown StopReason = iota
	ReasonStop
	ReasonLength
	ReasonToolUse
	ReasonContentFilter
)

func (r StopReason) String() string {
	switch r {
	case ReasonStop:
		return "stop"
	case ReasonLength:
		return "length"
	case ReasonToolUse:
		return "toolUse"
	case ReasonContentFilter:
		return "contentFilter"
	default:
		return "unknown"
	}
}

// Usage records token accounting reported by a provider. Zero means unknown or
// unused; providers should fill every field they can report reliably.
type Usage struct {
	InputTokens     int64 `json:"inputTokens"`
	OutputTokens    int64 `json:"outputTokens"`
	CacheRead       int64 `json:"cacheRead,omitempty"`
	CacheWrite      int64 `json:"cacheWrite,omitempty"`
	ReasoningTokens int64 `json:"reasoningTokens,omitempty"`
	TotalTokens     int64 `json:"totalTokens"`
}

// Result is the successful terminal value of a generation.
type Result struct {
	Message    Message    `json:"message"`
	Usage      Usage      `json:"usage"`
	StopReason StopReason `json:"stopReason"`
	ModelID    string     `json:"modelId,omitempty"`
	ProviderID string     `json:"providerId,omitempty"`
}

// EventType identifies a successful stream event. Runtime failures are carried
// by Stream's error value, not encoded as events.
type EventType uint8

const (
	EventUnknown EventType = iota
	EventStart
	EventContentStart
	EventContentDelta
	EventContentEnd
	EventDone
)

// Event describes generation progress.
//
// ContentStart carries Block metadata; ContentDelta carries a text or tool-
// argument fragment; ContentEnd carries the final Block; Done carries Result.
// ContentIndex is stable across all events for the same block.
type Event struct {
	Type         EventType     `json:"type"`
	ContentIndex int           `json:"contentIndex,omitempty"`
	Block        *ContentBlock `json:"block,omitempty"`
	Delta        string        `json:"delta,omitempty"`
	Result       *Result       `json:"result,omitempty"`
}

// Stream is a pull-based event stream. A provider yields (event, nil) for
// progress, yields one error for a generation failure, and then returns. Returning
// from iteration early must release provider resources via generator defers.
type Stream = iter.Seq2[Event, error]

var (
	ErrUnexpectedStreamEnd = errors.New("model: stream ended without a done event")
	ErrInvalidDoneEvent    = errors.New("model: done event has no result")
)

// LLM binds one provider to one model and is the API normally consumed by an
// agent. Provider remains the extension point implemented by vendors.
type LLM interface {
	Model() Model
	Generate(ctx context.Context, req Request) (Stream, error)
}

type boundLLM struct {
	provider Provider
	model    Model
}

// Bind creates an LLM from a provider and model descriptor.
func Bind(provider Provider, model Model) (LLM, error) {
	if provider == nil {
		return nil, errors.New("model: nil provider")
	}
	if model.ID == "" {
		return nil, errors.New("model: empty model ID")
	}
	if model.Provider != provider.ID() {
		return nil, fmt.Errorf("model: model provider %q does not match provider %q", model.Provider, provider.ID())
	}
	return &boundLLM{provider: provider, model: model}, nil
}

func (l *boundLLM) Model() Model { return l.model }

func (l *boundLLM) Generate(ctx context.Context, req Request) (Stream, error) {
	if err := validateRequest(req); err != nil {
		return nil, err
	}
	return l.provider.Generate(ctx, l.model, req)
}

// Complete consumes an LLM stream and returns its terminal result.
func Complete(ctx context.Context, llm LLM, req Request) (*Result, error) {
	if llm == nil {
		return nil, errors.New("model: nil LLM")
	}
	stream, err := llm.Generate(ctx, req)
	if err != nil {
		return nil, err
	}
	if stream == nil {
		return nil, ErrUnexpectedStreamEnd
	}

	for event, streamErr := range stream {
		if streamErr != nil {
			return nil, streamErr
		}
		if event.Type != EventDone {
			continue
		}
		if event.Result == nil {
			return nil, ErrInvalidDoneEvent
		}
		return event.Result, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return nil, ErrUnexpectedStreamEnd
}

func validateRequest(req Request) error {
	if req.MaxTokens != nil && *req.MaxTokens <= 0 {
		return errors.New("model: max tokens must be positive")
	}
	for i, tool := range req.Context.Tools {
		if tool.Name == "" {
			return fmt.Errorf("model: tool %d has an empty name", i)
		}
		if len(tool.Parameters) == 0 || !json.Valid(tool.Parameters) {
			return fmt.Errorf("model: tool %q has an invalid parameter schema", tool.Name)
		}
	}
	return nil
}
