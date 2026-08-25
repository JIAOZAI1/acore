package contextwindow

import (
	"context"

	"github.com/JIAOZAI1/acore/model"
)

// Estimator estimates all input tokens represented by value for selected.
// Implementations must support concurrent calls or synchronize their state.
type Estimator interface {
	Estimate(context.Context, model.Model, model.Context) (int64, error)
}

// EstimatorFunc adapts a function to Estimator.
type EstimatorFunc func(context.Context, model.Model, model.Context) (int64, error)

// Estimate calls f after validating ctx. A context error observed after f
// returns takes precedence over f's result.
func (f EstimatorFunc) Estimate(ctx context.Context, selected model.Model, value model.Context) (int64, error) {
	if f == nil {
		return 0, ErrNilEstimator
	}
	if ctx == nil {
		return 0, ErrInvalidContext
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}

	estimate, err := f(ctx, selected, value)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return 0, ctxErr
	}
	if err != nil {
		return 0, err
	}
	return estimate, nil
}
