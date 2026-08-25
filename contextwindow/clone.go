package contextwindow

import (
	"github.com/JIAOZAI1/acore/internal/clone"
	"github.com/JIAOZAI1/acore/model"
)

func cloneInput(input Input) Input {
	input.Model = cloneModel(input.Model)
	input.Context = cloneContext(input.Context)
	return input
}

func cloneModel(value model.Model) model.Model {
	value.InputModalities = clone.Slice(value.InputModalities)
	return value
}

func cloneContext(value model.Context) model.Context {
	value.Messages = cloneMessages(value.Messages)
	value.Tools = clone.SliceWith(value.Tools, cloneToolSpec)
	return value
}

func cloneMessages(messages []model.Message) []model.Message {
	return clone.SliceWith(messages, cloneMessage)
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

func cloneToolSpec(spec model.ToolSpec) model.ToolSpec {
	spec.Parameters = clone.Slice(spec.Parameters)
	return spec
}
