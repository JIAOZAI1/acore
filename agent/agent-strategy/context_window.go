package agentstrategy

import (
	"context"
	"fmt"

	"github.com/JIAOZAI1/acore/contextwindow"
	"github.com/JIAOZAI1/acore/model"
)

func applyContextWindow(ctx context.Context, reducer contextwindow.Reducer, selected model.Model, request model.Request, protectedMessages int) (model.Request, error) {
	if reducer == nil {
		return request, nil
	}

	var requestedOutputTokens int64
	if request.MaxTokens != nil {
		requestedOutputTokens = int64(*request.MaxTokens)
	}
	reduced, err := contextwindow.Apply(ctx, reducer, contextwindow.Input{
		Model:                 selected,
		Context:               request.Context,
		RequestedOutputTokens: requestedOutputTokens,
		ProtectedMessages:     protectedMessages,
	})
	if ctxErr := ctx.Err(); ctxErr != nil {
		return model.Request{}, ctxErr
	}
	if err != nil {
		return model.Request{}, fmt.Errorf("%w: %w", ErrReduceContextWindow, err)
	}

	request.Context = reduced
	return request, nil
}

func initialProtectedMessageCount(messages []model.Message, state *SessionRunState) int {
	if state != nil {
		return len(state.input)
	}
	for index := len(messages) - 1; index >= 0; index-- {
		if messages[index].Role == model.RoleUser {
			return len(messages) - index
		}
	}
	return len(messages)
}
