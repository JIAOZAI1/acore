package agentstrategy

import (
	"context"

	"github.com/JIAOZAI1/acore/contextwindow"
	"github.com/JIAOZAI1/acore/model"
	"github.com/JIAOZAI1/acore/session"
	"github.com/JIAOZAI1/acore/tool"
)

// PrepareSessionRun prepares the request and optional session state for a
// strategy implementation.
func PrepareSessionRun(ctx context.Context, service session.Service, req Request) (Request, *SessionRunState, error) {
	return prepareSessionRun(ctx, service, req)
}

// CommitSessionRun commits generated messages for a stateful run.
func CommitSessionRun(ctx context.Context, state *SessionRunState, result *Result) error {
	return commitSessionRun(ctx, state, result)
}

// ApplyContextWindow applies an optional context-window reducer.
func ApplyContextWindow(ctx context.Context, reducer contextwindow.Reducer, modelInfo model.Model, request model.Request, protectedMessages int) (model.Request, error) {
	return applyContextWindow(ctx, reducer, modelInfo, request, protectedMessages)
}

// InitialProtectedMessageCount returns the number of messages protected by a
// stateful run.
func InitialProtectedMessageCount(messages []model.Message, state *SessionRunState) int {
	return initialProtectedMessageCount(messages, state)
}

// CloneMessages returns a deep copy of model messages.
func CloneMessages(messages []model.Message) []model.Message { return cloneMessages(messages) }

// CloneMessage returns a deep copy of one model message.
func CloneMessage(message model.Message) model.Message { return cloneMessage(message) }

// CloneModelOptions returns a copy of model options.
func CloneModelOptions(options ModelOptions) ModelOptions { return cloneModelOptions(options) }

// CloneModelEvent returns a deep copy of one model event.
func CloneModelEvent(value model.Event) model.Event { return cloneModelEvent(value) }

// CloneModelResult returns a deep copy of one model result.
func CloneModelResult(result *model.Result) *model.Result { return cloneModelResult(result) }

// CloneToolCall returns a deep copy of one tool call.
func CloneToolCall(call tool.Call) tool.Call { return cloneToolCall(call) }

// CloneToolSpecs returns a deep copy of tool specifications.
func CloneToolSpecs(specs []model.ToolSpec) []model.ToolSpec { return cloneToolSpecs(specs) }

// CloneResult returns a deep copy of one Agent result.
func CloneResult(result *Result) *Result { return cloneResult(result) }
