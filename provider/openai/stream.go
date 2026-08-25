package openai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/JIAOZAI1/acore/model"
)

const (
	maxSSEEventSize = 4 << 20
	sseBufferSize   = 64 << 10
)

var errConsumerStopped = errors.New("openai: stream consumer stopped")

type chatChunk struct {
	Model   string          `json:"model"`
	Choices []chatChoice    `json:"choices"`
	Usage   *chatUsage      `json:"usage"`
	Error   *chatChunkError `json:"error"`
}

type chatChoice struct {
	Index        int             `json:"index"`
	Delta        chatChoiceDelta `json:"delta"`
	FinishReason *string         `json:"finish_reason"`
}

type chatChoiceDelta struct {
	Content   string              `json:"content"`
	Refusal   string              `json:"refusal"`
	ToolCalls []chatToolCallDelta `json:"tool_calls"`
}

type chatToolCallDelta struct {
	Index    *int                  `json:"index"`
	ID       string                `json:"id"`
	Type     string                `json:"type"`
	Function chatToolFunctionDelta `json:"function"`
}

type chatToolFunctionDelta struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type chatUsage struct {
	PromptTokens           int64                      `json:"prompt_tokens"`
	CompletionTokens       int64                      `json:"completion_tokens"`
	TotalTokens            int64                      `json:"total_tokens"`
	PromptTokensDetails    chatPromptTokenDetails     `json:"prompt_tokens_details"`
	CompletionTokenDetails chatCompletionTokenDetails `json:"completion_tokens_details"`
}

type chatPromptTokenDetails struct {
	CachedTokens     int64 `json:"cached_tokens"`
	CacheWriteTokens int64 `json:"cache_write_tokens"`
}

type chatCompletionTokenDetails struct {
	ReasoningTokens int64 `json:"reasoning_tokens"`
}

type chatChunkError struct {
	Message string          `json:"message"`
	Type    string          `json:"type"`
	Code    json.RawMessage `json:"code"`
}

type outputBlock struct {
	index int
	kind  model.ContentKind
	text  strings.Builder
	tool  *toolOutput
}

type toolOutput struct {
	id        string
	name      string
	arguments strings.Builder
	emitted   int
	started   bool
}

type streamState struct {
	requestedModelID string
	actualModelID    string
	blocks           []*outputBlock
	textBlock        *outputBlock
	tools            map[int]*outputBlock
	usage            model.Usage
	finishReason     string
	finishSeen       bool
}

func consumeStream(ctx context.Context, source io.Reader, selected model.Model, yield func(model.Event, error) bool) {
	if err := ctx.Err(); err != nil {
		yield(model.Event{}, err)
		return
	}
	if !yield(model.Event{Type: model.EventStart}, nil) {
		return
	}

	reader := newSSEReader(source)
	state := &streamState{
		requestedModelID: selected.ID,
		tools:            make(map[int]*outputBlock),
	}
	for {
		if err := ctx.Err(); err != nil {
			yield(model.Event{}, err)
			return
		}

		data, err := reader.Next()
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				yield(model.Event{}, ctxErr)
				return
			}
			if errors.Is(err, io.EOF) {
				err = fmt.Errorf("%w: stream ended before [DONE]", ErrInvalidStream)
			} else if !errors.Is(err, ErrInvalidStream) {
				err = fmt.Errorf("openai: read stream: %w", err)
			}
			yield(model.Event{}, err)
			return
		}

		if strings.TrimSpace(data) == "[DONE]" {
			if err := state.finish(yield); err != nil && !errors.Is(err, errConsumerStopped) {
				yield(model.Event{}, err)
			}
			return
		}
		if strings.TrimSpace(data) == "" {
			continue
		}

		var chunk chatChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			yield(model.Event{}, fmt.Errorf("%w: decode chunk: %v", ErrInvalidStream, err))
			return
		}
		if err := state.consumeChunk(chunk, yield); err != nil {
			if !errors.Is(err, errConsumerStopped) {
				yield(model.Event{}, err)
			}
			return
		}
	}
}

func (s *streamState) consumeChunk(chunk chatChunk, yield func(model.Event, error) bool) error {
	if chunk.Error != nil {
		detail := chunk.Error.Message
		if detail == "" {
			detail = chunk.Error.Type
		}
		if detail == "" {
			detail = "request failed"
		}
		if code := scalarString(chunk.Error.Code); code != "" {
			detail += " (code: " + code + ")"
		}
		return fmt.Errorf("%w: API stream error: %s", ErrInvalidStream, detail)
	}
	if chunk.Model != "" {
		if s.actualModelID != "" && s.actualModelID != chunk.Model {
			return fmt.Errorf("%w: model changed from %q to %q", ErrInvalidStream, s.actualModelID, chunk.Model)
		}
		s.actualModelID = chunk.Model
	}
	if chunk.Usage != nil {
		usage, err := mapUsage(*chunk.Usage)
		if err != nil {
			return err
		}
		s.usage = usage
	}
	if len(chunk.Choices) > 1 {
		return fmt.Errorf("%w: received %d choices", ErrInvalidStream, len(chunk.Choices))
	}
	if len(chunk.Choices) == 0 {
		return nil
	}

	choice := chunk.Choices[0]
	if choice.Index != 0 {
		return fmt.Errorf("%w: unexpected choice index %d", ErrInvalidStream, choice.Index)
	}
	if choice.Delta.Content != "" {
		if err := s.appendText(choice.Delta.Content, yield); err != nil {
			return err
		}
	}
	if choice.Delta.Refusal != "" {
		if err := s.appendText(choice.Delta.Refusal, yield); err != nil {
			return err
		}
	}
	for _, delta := range choice.Delta.ToolCalls {
		if err := s.appendTool(delta, yield); err != nil {
			return err
		}
	}
	if choice.FinishReason != nil {
		if *choice.FinishReason == "" {
			return fmt.Errorf("%w: empty finish reason", ErrInvalidStream)
		}
		if s.finishSeen && s.finishReason != *choice.FinishReason {
			return fmt.Errorf("%w: finish reason changed from %q to %q", ErrInvalidStream, s.finishReason, *choice.FinishReason)
		}
		s.finishSeen = true
		s.finishReason = *choice.FinishReason
	}
	return nil
}

func (s *streamState) appendText(delta string, yield func(model.Event, error) bool) error {
	if s.textBlock == nil {
		s.textBlock = &outputBlock{index: len(s.blocks), kind: model.ContentText}
		s.blocks = append(s.blocks, s.textBlock)
		if !yield(model.Event{
			Type:         model.EventContentStart,
			ContentIndex: s.textBlock.index,
			Block:        &model.ContentBlock{Kind: model.ContentText},
		}, nil) {
			return errConsumerStopped
		}
	}
	s.textBlock.text.WriteString(delta)
	if !yield(model.Event{Type: model.EventContentDelta, ContentIndex: s.textBlock.index, Delta: delta}, nil) {
		return errConsumerStopped
	}
	return nil
}

func (s *streamState) appendTool(delta chatToolCallDelta, yield func(model.Event, error) bool) error {
	if delta.Index == nil || *delta.Index < 0 {
		return fmt.Errorf("%w: tool call has an invalid index", ErrInvalidStream)
	}
	if delta.Type != "" && delta.Type != "function" {
		return fmt.Errorf("%w: unsupported tool call type %q", ErrInvalidStream, delta.Type)
	}

	block := s.tools[*delta.Index]
	if block == nil {
		block = &outputBlock{
			index: len(s.blocks),
			kind:  model.ContentToolCall,
			tool:  &toolOutput{},
		}
		s.tools[*delta.Index] = block
		s.blocks = append(s.blocks, block)
	}
	if err := setStableField(&block.tool.id, delta.ID, "tool call ID"); err != nil {
		return err
	}
	if err := setStableField(&block.tool.name, delta.Function.Name, "tool call name"); err != nil {
		return err
	}
	block.tool.arguments.WriteString(delta.Function.Arguments)

	if !block.tool.started && block.tool.id != "" && block.tool.name != "" {
		block.tool.started = true
		if !yield(model.Event{
			Type:         model.EventContentStart,
			ContentIndex: block.index,
			Block: &model.ContentBlock{
				Kind: model.ContentToolCall,
				ToolCall: &model.ToolCall{
					ID:   block.tool.id,
					Name: block.tool.name,
				},
			},
		}, nil) {
			return errConsumerStopped
		}
	}
	if block.tool.started && block.tool.emitted < block.tool.arguments.Len() {
		arguments := block.tool.arguments.String()
		fragment := arguments[block.tool.emitted:]
		block.tool.emitted = len(arguments)
		if !yield(model.Event{Type: model.EventContentDelta, ContentIndex: block.index, Delta: fragment}, nil) {
			return errConsumerStopped
		}
	}
	return nil
}

func setStableField(target *string, incoming, name string) error {
	if incoming == "" {
		return nil
	}
	if *target == "" {
		*target = incoming
		return nil
	}
	if *target != incoming {
		return fmt.Errorf("%w: %s changed from %q to %q", ErrInvalidStream, name, *target, incoming)
	}
	return nil
}

func (s *streamState) finish(yield func(model.Event, error) bool) error {
	if !s.finishSeen {
		return fmt.Errorf("%w: missing finish reason", ErrInvalidStream)
	}

	content := make([]model.ContentBlock, len(s.blocks))
	for i, block := range s.blocks {
		converted, err := finishBlock(block)
		if err != nil {
			return err
		}
		content[i] = converted
	}
	for i := range content {
		block := content[i]
		if !yield(model.Event{Type: model.EventContentEnd, ContentIndex: i, Block: cloneContentBlock(block)}, nil) {
			return errConsumerStopped
		}
	}

	modelID := s.actualModelID
	if modelID == "" {
		modelID = s.requestedModelID
	}
	result := &model.Result{
		Message: model.Message{
			Role:    model.RoleAssistant,
			Content: content,
		},
		Usage:      s.usage,
		StopReason: mapStopReason(s.finishReason),
		ModelID:    modelID,
		ProviderID: ProviderID,
	}
	if !yield(model.Event{Type: model.EventDone, Result: result}, nil) {
		return errConsumerStopped
	}
	return nil
}

func finishBlock(block *outputBlock) (model.ContentBlock, error) {
	switch block.kind {
	case model.ContentText:
		return model.ContentBlock{Kind: model.ContentText, Text: block.text.String()}, nil
	case model.ContentToolCall:
		if block.tool == nil || !block.tool.started || strings.TrimSpace(block.tool.id) == "" || strings.TrimSpace(block.tool.name) == "" {
			return model.ContentBlock{}, fmt.Errorf("%w: incomplete tool call", ErrInvalidStream)
		}
		arguments := block.tool.arguments.String()
		if arguments == "" {
			arguments = `{}`
		}
		if !json.Valid([]byte(arguments)) {
			return model.ContentBlock{}, fmt.Errorf("%w: tool call %q has invalid arguments", ErrInvalidStream, block.tool.name)
		}
		return model.ContentBlock{
			Kind: model.ContentToolCall,
			ToolCall: &model.ToolCall{
				ID:        block.tool.id,
				Name:      block.tool.name,
				Arguments: json.RawMessage(arguments),
			},
		}, nil
	default:
		return model.ContentBlock{}, fmt.Errorf("%w: unknown output block", ErrInvalidStream)
	}
}

func cloneContentBlock(block model.ContentBlock) *model.ContentBlock {
	if block.ToolCall != nil {
		call := *block.ToolCall
		call.Arguments = append(json.RawMessage(nil), call.Arguments...)
		block.ToolCall = &call
	}
	return &block
}

func mapUsage(usage chatUsage) (model.Usage, error) {
	values := []int64{
		usage.PromptTokens,
		usage.CompletionTokens,
		usage.TotalTokens,
		usage.PromptTokensDetails.CachedTokens,
		usage.PromptTokensDetails.CacheWriteTokens,
		usage.CompletionTokenDetails.ReasoningTokens,
	}
	for _, value := range values {
		if value < 0 {
			return model.Usage{}, fmt.Errorf("%w: usage contains a negative token count", ErrInvalidStream)
		}
	}
	return model.Usage{
		InputTokens:     usage.PromptTokens,
		OutputTokens:    usage.CompletionTokens,
		CacheRead:       usage.PromptTokensDetails.CachedTokens,
		CacheWrite:      usage.PromptTokensDetails.CacheWriteTokens,
		ReasoningTokens: usage.CompletionTokenDetails.ReasoningTokens,
		TotalTokens:     usage.TotalTokens,
	}, nil
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

type sseReader struct {
	reader     *bufio.Reader
	eventBytes int
	data       strings.Builder
	dataLines  int
}

func newSSEReader(source io.Reader) *sseReader {
	return &sseReader{reader: bufio.NewReaderSize(source, sseBufferSize)}
}

func (r *sseReader) Next() (string, error) {
	for {
		line, err := r.readLine()
		if err != nil {
			return "", err
		}
		r.eventBytes += len(line)
		if r.eventBytes > maxSSEEventSize {
			return "", fmt.Errorf("%w: SSE event exceeds %d bytes", ErrInvalidStream, maxSSEEventSize)
		}

		if len(line) == 0 {
			r.eventBytes = 0
			if r.dataLines == 0 {
				continue
			}
			data := r.data.String()
			r.data.Reset()
			r.dataLines = 0
			return data, nil
		}
		if line[0] == ':' {
			continue
		}

		field, value, found := bytes.Cut(line, []byte{':'})
		if !found || string(field) != "data" {
			continue
		}
		if len(value) > 0 && value[0] == ' ' {
			value = value[1:]
		}
		if r.dataLines > 0 {
			r.data.WriteByte('\n')
		}
		r.data.Write(value)
		r.dataLines++
	}
}

func (r *sseReader) readLine() ([]byte, error) {
	line := make([]byte, 0, 256)
	for {
		fragment, err := r.reader.ReadSlice('\n')
		if len(line)+len(fragment)+r.eventBytes > maxSSEEventSize {
			return nil, fmt.Errorf("%w: SSE event exceeds %d bytes", ErrInvalidStream, maxSSEEventSize)
		}
		line = append(line, fragment...)
		switch {
		case err == nil:
			line = line[:len(line)-1]
			if len(line) > 0 && line[len(line)-1] == '\r' {
				line = line[:len(line)-1]
			}
			return line, nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF) && len(line) == 0:
			return nil, io.EOF
		case errors.Is(err, io.EOF):
			return nil, fmt.Errorf("%w: unterminated SSE line", ErrInvalidStream)
		default:
			return nil, err
		}
	}
}
