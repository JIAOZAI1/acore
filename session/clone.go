package session

import (
	"github.com/JIAOZAI1/acore/internal/clone"
	"github.com/JIAOZAI1/acore/model"
)

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
