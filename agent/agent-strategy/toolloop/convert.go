package toolloop

import (
	"errors"
	"fmt"

	"github.com/JIAOZAI1/acore/internal/clone"
	"github.com/JIAOZAI1/acore/internal/jsoncheck"
	"github.com/JIAOZAI1/acore/model"
	"github.com/JIAOZAI1/acore/tool"
)

func convertToolSpecs(specs []tool.Spec) ([]model.ToolSpec, error) {
	converted := make([]model.ToolSpec, 0, len(specs))
	seen := make(map[string]struct{}, len(specs))
	for index, spec := range specs {
		if spec.Name == "" {
			return nil, fmt.Errorf("%w: tool %d has empty name", ErrInvalidToolCatalog, index)
		}
		if _, exists := seen[spec.Name]; exists {
			return nil, fmt.Errorf("%w: duplicate tool %q", ErrInvalidToolCatalog, spec.Name)
		}
		if !jsoncheck.IsObject(spec.Parameters) {
			return nil, fmt.Errorf("%w: invalid schema for tool %q", ErrInvalidToolCatalog, spec.Name)
		}

		seen[spec.Name] = struct{}{}
		converted = append(converted, model.ToolSpec{
			Name:        spec.Name,
			Description: spec.Description,
			Parameters:  clone.Slice(spec.Parameters),
		})
	}
	return converted, nil
}

func cloneToolSpecs(specs []model.ToolSpec) []model.ToolSpec {
	return clone.SliceWith(specs, func(spec model.ToolSpec) model.ToolSpec {
		spec.Parameters = clone.Slice(spec.Parameters)
		return spec
	})
}

func extractToolCalls(message model.Message, seen map[string]struct{}) ([]tool.Call, error) {
	calls := make([]tool.Call, 0)
	batchIDs := make(map[string]struct{})
	for index, block := range message.Content {
		if block.Kind != model.ContentToolCall {
			continue
		}
		if block.ToolCall == nil {
			return nil, fmt.Errorf("%w: content block %d has no call", ErrInvalidToolCall, index)
		}

		call := block.ToolCall
		if call.ID == "" {
			return nil, fmt.Errorf("%w: content block %d has empty ID", ErrInvalidToolCall, index)
		}
		if call.Name == "" {
			return nil, fmt.Errorf("%w: content block %d has empty name", ErrInvalidToolCall, index)
		}
		if !jsoncheck.IsObject(call.Arguments) {
			return nil, fmt.Errorf("%w: invalid arguments for call %q", ErrInvalidToolCall, call.ID)
		}
		if _, exists := seen[call.ID]; exists {
			return nil, fmt.Errorf("%w: duplicate call ID %q", ErrInvalidToolCall, call.ID)
		}
		if _, exists := batchIDs[call.ID]; exists {
			return nil, fmt.Errorf("%w: duplicate call ID %q", ErrInvalidToolCall, call.ID)
		}

		batchIDs[call.ID] = struct{}{}
		calls = append(calls, tool.Call{
			ID:        call.ID,
			Name:      call.Name,
			Arguments: clone.Slice(call.Arguments),
		})
	}
	return calls, nil
}

func toolMessage(call tool.Call, content string, isError bool) model.Message {
	return model.Message{
		Role:       model.RoleTool,
		ToolCallID: call.ID,
		IsError:    isError,
		Content: []model.ContentBlock{{
			Kind: model.ContentText,
			Text: content,
		}},
	}
}

func safeToolErrorMessage(err error) string {
	switch {
	case errors.Is(err, tool.ErrToolNotFound):
		return "tool not found"
	case errors.Is(err, tool.ErrInvalidArguments):
		return "invalid tool arguments"
	default:
		return "tool execution failed"
	}
}
