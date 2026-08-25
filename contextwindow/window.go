// Package contextwindow defines provider-independent context-window reducers.
package contextwindow

import (
	"context"
	"errors"
	"fmt"

	"github.com/JIAOZAI1/acore/internal/nilcheck"
	"github.com/JIAOZAI1/acore/model"
)

// Input is one immutable snapshot used to fit a model context. Implementations
// must not retain or modify its reference-backed fields. Code constructing
// Input should use keyed fields.
type Input struct {
	// Model identifies the selected model and carries its window metadata.
	Model model.Model
	// Context is the complete context before message reduction.
	Context model.Context
	// RequestedOutputTokens is the run's explicit output limit. Zero means the
	// request did not set one.
	RequestedOutputTokens int64
	// ProtectedMessages is the number of trailing messages that cannot be
	// removed by a Reducer.
	ProtectedMessages int
}

// Result selects the suffix Input.Context.Messages[MessageStart:].
type Result struct {
	// MessageStart is the index of the first retained input message.
	MessageStart int
}

// Reducer selects a context message suffix for one model request.
// Implementations must support concurrent calls or synchronize their state.
type Reducer interface {
	Reduce(context.Context, Input) (Result, error)
}

// ReducerFunc adapts a function to Reducer.
type ReducerFunc func(context.Context, Input) (Result, error)

// Reduce calls f after validating ctx. A context error observed after f
// returns takes precedence over f's result.
func (f ReducerFunc) Reduce(ctx context.Context, input Input) (Result, error) {
	if f == nil {
		return Result{}, ErrNilReducer
	}
	if ctx == nil {
		return Result{}, ErrInvalidContext
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}

	result, err := f(ctx, input)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return Result{}, ctxErr
	}
	if err != nil {
		return Result{}, err
	}
	return result, nil
}

// Apply invokes reducer with an isolated input, validates its result, and
// returns a complete context built only from the original input snapshot.
func Apply(ctx context.Context, reducer Reducer, input Input) (model.Context, error) {
	if ctx == nil {
		return model.Context{}, ErrInvalidContext
	}
	if err := ctx.Err(); err != nil {
		return model.Context{}, err
	}
	if nilcheck.IsNil(reducer) {
		return model.Context{}, ErrNilReducer
	}
	if err := validateInput(input); err != nil {
		return model.Context{}, err
	}

	snapshot := cloneInput(input)
	result, err := reducer.Reduce(ctx, cloneInput(snapshot))
	if ctxErr := ctx.Err(); ctxErr != nil {
		return model.Context{}, ctxErr
	}
	if err != nil {
		return model.Context{}, err
	}
	if err := validateResult(snapshot, result); err != nil {
		return model.Context{}, err
	}

	output := cloneContext(snapshot.Context)
	output.Messages = cloneMessages(snapshot.Context.Messages[result.MessageStart:])
	return output, nil
}

func validateInput(input Input) error {
	messageCount := len(input.Context.Messages)
	if messageCount == 0 {
		return fmt.Errorf("%w: messages must not be empty", ErrInvalidInput)
	}
	if input.ProtectedMessages <= 0 || input.ProtectedMessages > messageCount {
		return fmt.Errorf(
			"%w: protected messages %d outside range [1,%d]",
			ErrInvalidInput,
			input.ProtectedMessages,
			messageCount,
		)
	}
	if input.RequestedOutputTokens < 0 {
		return fmt.Errorf("%w: requested output tokens must not be negative", ErrInvalidInput)
	}
	return nil
}

func validateResult(input Input, result Result) error {
	maxStart := len(input.Context.Messages) - input.ProtectedMessages
	if result.MessageStart < 0 || result.MessageStart > maxStart {
		return fmt.Errorf(
			"%w: message start %d outside range [0,%d]",
			ErrInvalidResult,
			result.MessageStart,
			maxStart,
		)
	}
	if result.MessageStart > 0 && input.Context.Messages[result.MessageStart].Role != model.RoleUser {
		return fmt.Errorf(
			"%w: message start %d does not select a user message",
			ErrInvalidResult,
			result.MessageStart,
		)
	}
	return nil
}

var (
	// ErrInvalidContext indicates that an operation received a nil context.
	ErrInvalidContext = errors.New("contextwindow: invalid context")
	// ErrNilReducer indicates that a Reducer is nil, including a typed nil.
	ErrNilReducer = errors.New("contextwindow: nil reducer")
	// ErrNilEstimator indicates that an Estimator is nil, including a typed nil.
	ErrNilEstimator = errors.New("contextwindow: nil estimator")
	// ErrInvalidConfig indicates that a Reducer configuration is invalid.
	ErrInvalidConfig = errors.New("contextwindow: invalid config")
	// ErrInvalidInput indicates that a reduction input is structurally invalid.
	ErrInvalidInput = errors.New("contextwindow: invalid input")
	// ErrBudgetUnavailable indicates that a safe input budget cannot be derived.
	ErrBudgetUnavailable = errors.New("contextwindow: budget unavailable")
	// ErrEstimate indicates that an Estimator failed.
	ErrEstimate = errors.New("contextwindow: estimate input")
	// ErrInvalidEstimate indicates that an Estimator returned an invalid count.
	ErrInvalidEstimate = errors.New("contextwindow: invalid estimate")
	// ErrCannotFit indicates that fixed content and protected messages exceed the
	// available input budget.
	ErrCannotFit = errors.New("contextwindow: context cannot fit")
	// ErrInvalidResult indicates that a Reducer selected an unsafe message suffix.
	ErrInvalidResult = errors.New("contextwindow: invalid reducer result")
)
