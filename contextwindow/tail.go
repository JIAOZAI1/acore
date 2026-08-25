package contextwindow

import (
	"context"
	"fmt"

	"github.com/JIAOZAI1/acore/internal/nilcheck"
	"github.com/JIAOZAI1/acore/model"
)

// TailConfig configures a TailReducer.
type TailConfig struct {
	// Estimator is required and must account for the complete model context.
	Estimator Estimator
	// SafetyMarginTokens reserves additional input capacity for known estimator
	// uncertainty. Zero disables the margin.
	SafetyMarginTokens int64
	// FallbackOutputTokens is used only when neither the request nor the model
	// descriptor provides an output token limit. Zero disables the fallback.
	FallbackOutputTokens int64
}

// TailReducer keeps the largest recent suffix that fits its estimated input
// budget. It removes messages only at user-message boundaries.
type TailReducer struct {
	estimator            Estimator
	safetyMarginTokens   int64
	fallbackOutputTokens int64
}

// NewTailReducer validates config and constructs an immutable TailReducer.
func NewTailReducer(config TailConfig) (*TailReducer, error) {
	if nilcheck.IsNil(config.Estimator) {
		return nil, ErrNilEstimator
	}
	if config.SafetyMarginTokens < 0 {
		return nil, fmt.Errorf("%w: safety margin tokens must not be negative", ErrInvalidConfig)
	}
	if config.FallbackOutputTokens < 0 {
		return nil, fmt.Errorf("%w: fallback output tokens must not be negative", ErrInvalidConfig)
	}

	return &TailReducer{
		estimator:            config.Estimator,
		safetyMarginTokens:   config.SafetyMarginTokens,
		fallbackOutputTokens: config.FallbackOutputTokens,
	}, nil
}

// Reduce selects the earliest safe message boundary whose complete model
// context fits the estimated input budget.
func (r *TailReducer) Reduce(ctx context.Context, input Input) (Result, error) {
	if r == nil {
		return Result{}, ErrNilReducer
	}
	if ctx == nil {
		return Result{}, ErrInvalidContext
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if err := validateInput(input); err != nil {
		return Result{}, err
	}
	if nilcheck.IsNil(r.estimator) {
		return Result{}, ErrNilEstimator
	}

	budget, err := r.inputBudget(input)
	if err != nil {
		return Result{}, err
	}
	estimated, err := r.estimate(ctx, input.Model, input.Context)
	if err != nil {
		return Result{}, err
	}
	if estimated <= budget {
		return Result{}, nil
	}

	lastEstimate := estimated
	maxStart := len(input.Context.Messages) - input.ProtectedMessages
	for messageStart := 1; messageStart <= maxStart; messageStart++ {
		if input.Context.Messages[messageStart].Role != model.RoleUser {
			continue
		}

		candidate := cloneContext(input.Context)
		candidate.Messages = candidate.Messages[messageStart:]
		lastEstimate, err = r.estimate(ctx, input.Model, candidate)
		if err != nil {
			return Result{}, err
		}
		if lastEstimate <= budget {
			return Result{MessageStart: messageStart}, nil
		}
	}

	return Result{}, fmt.Errorf(
		"%w: estimated input tokens %d exceed budget %d",
		ErrCannotFit,
		lastEstimate,
		budget,
	)
}

func (r *TailReducer) inputBudget(input Input) (int64, error) {
	contextWindow := int64(input.Model.ContextWindow)
	if contextWindow <= 0 {
		return 0, fmt.Errorf("%w: model %q has no positive context window", ErrBudgetUnavailable, input.Model.ID)
	}
	if input.Model.MaxOutputTokens < 0 {
		return 0, fmt.Errorf("%w: model %q has negative max output tokens", ErrInvalidInput, input.Model.ID)
	}

	reservedOutputTokens := input.RequestedOutputTokens
	if reservedOutputTokens == 0 {
		reservedOutputTokens = int64(input.Model.MaxOutputTokens)
	}
	if reservedOutputTokens == 0 {
		reservedOutputTokens = r.fallbackOutputTokens
	}
	if reservedOutputTokens <= 0 {
		return 0, fmt.Errorf("%w: model %q has no output token reserve", ErrBudgetUnavailable, input.Model.ID)
	}
	if reservedOutputTokens >= contextWindow {
		return 0, fmt.Errorf(
			"%w: output reserve %d leaves no input budget in context window %d",
			ErrCannotFit,
			reservedOutputTokens,
			contextWindow,
		)
	}

	budget := contextWindow - reservedOutputTokens
	if r.safetyMarginTokens >= budget {
		return 0, fmt.Errorf(
			"%w: safety margin %d leaves no input budget after output reserve %d",
			ErrCannotFit,
			r.safetyMarginTokens,
			reservedOutputTokens,
		)
	}
	return budget - r.safetyMarginTokens, nil
}

func (r *TailReducer) estimate(ctx context.Context, selected model.Model, value model.Context) (int64, error) {
	estimated, err := r.estimator.Estimate(ctx, cloneModel(selected), cloneContext(value))
	if ctxErr := ctx.Err(); ctxErr != nil {
		return 0, ctxErr
	}
	if err != nil {
		return 0, fmt.Errorf("%w: %w", ErrEstimate, err)
	}
	if estimated < 0 {
		return 0, fmt.Errorf("%w: token count %d", ErrInvalidEstimate, estimated)
	}
	return estimated, nil
}

var _ Reducer = (*TailReducer)(nil)
