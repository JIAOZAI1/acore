package agent

import (
	"github.com/JIAOZAI1/acore/internal/clone"
	"github.com/JIAOZAI1/acore/model"
	"github.com/JIAOZAI1/acore/prompt"
	"github.com/JIAOZAI1/acore/tool"
)

func cloneMessages(messages []model.Message) []model.Message {
	return clone.SliceWith(messages, cloneMessage)
}

func cloneSessionInput(input *SessionInput) *SessionInput {
	if input == nil {
		return nil
	}
	cloned := *input
	cloned.Messages = cloneMessages(input.Messages)
	return &cloned
}

func clonePromptValues(values prompt.Values) prompt.Values {
	if values == nil {
		return nil
	}
	cloned := make(prompt.Values, len(values))
	for name, value := range values {
		cloned[name] = value
	}
	return cloned
}

func cloneMessage(message model.Message) model.Message {
	message.Content = clone.SliceWith(message.Content, cloneContentBlock)
	return message
}

func cloneContentBlock(block model.ContentBlock) model.ContentBlock {
	if block.Signature != nil {
		value := *block.Signature
		block.Signature = &value
	}
	if block.ToolCall != nil {
		call := *block.ToolCall
		call.Arguments = clone.Slice(call.Arguments)
		block.ToolCall = &call
	}
	return block
}

func cloneModelResult(result *model.Result) *model.Result {
	if result == nil {
		return nil
	}
	cloned := *result
	cloned.Message = cloneMessage(result.Message)
	return &cloned
}

func cloneModelEvent(event model.Event) model.Event {
	if event.Block != nil {
		block := cloneContentBlock(*event.Block)
		event.Block = &block
	}
	event.Result = cloneModelResult(event.Result)
	return event
}

func cloneEvent(event Event) Event {
	if event.ModelEvent != nil {
		modelEvent := cloneModelEvent(*event.ModelEvent)
		event.ModelEvent = &modelEvent
	}
	event.Tool = cloneToolEvent(event.Tool)
	event.Result = cloneResult(event.Result)
	return event
}

func cloneToolEvent(event *ToolEvent) *ToolEvent {
	if event == nil {
		return nil
	}
	cloned := *event
	cloned.Call = cloneToolCall(event.Call)
	if event.Result != nil {
		result := *event.Result
		cloned.Result = &result
	}
	return &cloned
}

func cloneToolCall(call tool.Call) tool.Call {
	call.Arguments = clone.Slice(call.Arguments)
	return call
}

func cloneResult(result *Result) *Result {
	if result == nil {
		return nil
	}
	cloned := *result
	cloned.Output = cloneMessage(result.Output)
	cloned.GeneratedMessages = cloneMessages(result.GeneratedMessages)
	return &cloned
}
