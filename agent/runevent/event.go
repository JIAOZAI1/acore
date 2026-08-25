// Package runevent defines standard events for observing an Agent run.
//
// The package contains data contracts only. It does not publish events or
// depend on the Agent runtime implementation.
package runevent

import (
	"time"

	"github.com/JIAOZAI1/acore/event"
	"github.com/JIAOZAI1/acore/model"
)

const (
	// RunStartedEventName is the stable name of RunStartedEvent.
	RunStartedEventName = "agent.run.started"
	// ModelTurnStartedEventName is the stable name of ModelTurnStartedEvent.
	ModelTurnStartedEventName = "agent.model.turn.started"
	// ModelTurnCompletedEventName is the stable name of ModelTurnCompletedEvent.
	ModelTurnCompletedEventName = "agent.model.turn.completed"
	// ModelTurnFailedEventName is the stable name of ModelTurnFailedEvent.
	ModelTurnFailedEventName = "agent.model.turn.failed"
	// ToolCallStartedEventName is the stable name of ToolCallStartedEvent.
	ToolCallStartedEventName = "agent.tool.call.started"
	// ToolCallCompletedEventName is the stable name of ToolCallCompletedEvent.
	ToolCallCompletedEventName = "agent.tool.call.completed"
	// RunCompletedEventName is the stable name of RunCompletedEvent.
	RunCompletedEventName = "agent.run.completed"
	// RunFailedEventName is the stable name of RunFailedEvent.
	RunFailedEventName = "agent.run.failed"
	// RunCanceledEventName is the stable name of RunCanceledEvent.
	RunCanceledEventName = "agent.run.canceled"
)

// Metadata identifies an event within one run.
type Metadata struct {
	RunID      string    `json:"runId"`
	Sequence   uint64    `json:"sequence"`
	OccurredAt time.Time `json:"occurredAt"`
}

// Failure is a sanitized summary of a runtime failure.
type Failure struct {
	Stage     string `json:"stage"`
	Code      string `json:"code"`
	Retryable bool   `json:"retryable,omitempty"`
}

// ToolCallStatus describes the terminal state of a tool call.
type ToolCallStatus uint8

const (
	// ToolCallStatusUnknown means the terminal status is not known.
	ToolCallStatusUnknown ToolCallStatus = iota
	// ToolCallStatusSucceeded means the tool completed successfully.
	ToolCallStatusSucceeded
	// ToolCallStatusFailed means the tool completed with an error.
	ToolCallStatusFailed
	// ToolCallStatusCanceled means the tool was canceled.
	ToolCallStatusCanceled
)

// String returns the stable textual representation of the tool call status.
func (s ToolCallStatus) String() string {
	switch s {
	case ToolCallStatusSucceeded:
		return "succeeded"
	case ToolCallStatusFailed:
		return "failed"
	case ToolCallStatusCanceled:
		return "canceled"
	default:
		return "unknown"
	}
}

// RunStartedEvent marks the beginning of an Agent run.
type RunStartedEvent struct {
	Metadata   Metadata `json:"metadata"`
	ModelID    string   `json:"modelId,omitempty"`
	ProviderID string   `json:"providerId,omitempty"`
}

// Name implements event.Event.
func (RunStartedEvent) Name() string { return RunStartedEventName }

// ModelTurnStartedEvent marks the beginning of a model generation turn.
type ModelTurnStartedEvent struct {
	Metadata   Metadata `json:"metadata"`
	Turn       int      `json:"turn"`
	ModelID    string   `json:"modelId,omitempty"`
	ProviderID string   `json:"providerId,omitempty"`
}

// Name implements event.Event.
func (ModelTurnStartedEvent) Name() string { return ModelTurnStartedEventName }

// ModelTurnCompletedEvent contains the summary of a successful model turn.
type ModelTurnCompletedEvent struct {
	Metadata   Metadata         `json:"metadata"`
	Turn       int              `json:"turn"`
	ModelID    string           `json:"modelId,omitempty"`
	ProviderID string           `json:"providerId,omitempty"`
	Usage      model.Usage      `json:"usage"`
	StopReason model.StopReason `json:"stopReason"`
	DurationMS int64            `json:"durationMs,omitempty"`
}

// Name implements event.Event.
func (ModelTurnCompletedEvent) Name() string { return ModelTurnCompletedEventName }

// ModelTurnFailedEvent reports a model turn that did not complete.
type ModelTurnFailedEvent struct {
	Metadata Metadata `json:"metadata"`
	Turn     int      `json:"turn"`
	Failure  Failure  `json:"failure"`
}

// Name implements event.Event.
func (ModelTurnFailedEvent) Name() string { return ModelTurnFailedEventName }

// ToolCallStartedEvent marks the beginning of a tool invocation.
type ToolCallStartedEvent struct {
	Metadata Metadata `json:"metadata"`
	Turn     int      `json:"turn"`
	CallID   string   `json:"callId"`
	ToolName string   `json:"toolName"`
}

// Name implements event.Event.
func (ToolCallStartedEvent) Name() string { return ToolCallStartedEventName }

// ToolCallCompletedEvent contains the summary of a completed tool invocation.
type ToolCallCompletedEvent struct {
	Metadata    Metadata       `json:"metadata"`
	Turn        int            `json:"turn"`
	CallID      string         `json:"callId"`
	ToolName    string         `json:"toolName"`
	Status      ToolCallStatus `json:"status"`
	ResultBytes int            `json:"resultBytes,omitempty"`
	ErrorCode   string         `json:"errorCode,omitempty"`
	DurationMS  int64          `json:"durationMs,omitempty"`
}

// Name implements event.Event.
func (ToolCallCompletedEvent) Name() string { return ToolCallCompletedEventName }

// RunCompletedEvent marks a successful terminal state for an Agent run.
type RunCompletedEvent struct {
	Metadata              Metadata         `json:"metadata"`
	Usage                 model.Usage      `json:"usage"`
	StopReason            model.StopReason `json:"stopReason"`
	ModelTurns            int              `json:"modelTurns"`
	ToolCalls             int              `json:"toolCalls"`
	ToolErrors            int              `json:"toolErrors"`
	GeneratedMessageCount int              `json:"generatedMessageCount"`
}

// Name implements event.Event.
func (RunCompletedEvent) Name() string { return RunCompletedEventName }

// RunFailedEvent marks a failed terminal state for an Agent run.
type RunFailedEvent struct {
	Metadata  Metadata `json:"metadata"`
	Failure   Failure  `json:"failure"`
	ModelTurn int      `json:"modelTurn,omitempty"`
	CallID    string   `json:"callId,omitempty"`
	ToolName  string   `json:"toolName,omitempty"`
}

// Name implements event.Event.
func (RunFailedEvent) Name() string { return RunFailedEventName }

// RunCanceledEvent marks a canceled terminal state for an Agent run.
type RunCanceledEvent struct {
	Metadata  Metadata `json:"metadata"`
	Stage     string   `json:"stage"`
	ModelTurn int      `json:"modelTurn,omitempty"`
	CallID    string   `json:"callId,omitempty"`
	ToolName  string   `json:"toolName,omitempty"`
}

// Name implements event.Event.
func (RunCanceledEvent) Name() string { return RunCanceledEventName }

var (
	_ event.Event = RunStartedEvent{}
	_ event.Event = ModelTurnStartedEvent{}
	_ event.Event = ModelTurnCompletedEvent{}
	_ event.Event = ModelTurnFailedEvent{}
	_ event.Event = ToolCallStartedEvent{}
	_ event.Event = ToolCallCompletedEvent{}
	_ event.Event = RunCompletedEvent{}
	_ event.Event = RunFailedEvent{}
	_ event.Event = RunCanceledEvent{}
)
