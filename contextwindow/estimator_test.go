package contextwindow_test

import (
	"context"
	"errors"
	"testing"

	"github.com/JIAOZAI1/acore/contextwindow"
	"github.com/JIAOZAI1/acore/model"
)

func TestEstimatorFuncContract(t *testing.T) {
	var nilEstimator contextwindow.EstimatorFunc
	if _, err := nilEstimator.Estimate(context.Background(), model.Model{}, model.Context{}); !errors.Is(err, contextwindow.ErrNilEstimator) {
		t.Fatalf("nil Estimate() error = %v, want ErrNilEstimator", err)
	}

	estimator := contextwindow.EstimatorFunc(func(_ context.Context, selected model.Model, value model.Context) (int64, error) {
		return int64(len(selected.ID) + len(value.SystemPrompt)), nil
	})
	got, err := estimator.Estimate(context.Background(), model.Model{ID: "model"}, model.Context{SystemPrompt: "prompt"})
	if err != nil || got != 11 {
		t.Fatalf("Estimate() = %d, %v, want 11", got, err)
	}
	if _, err := estimator.Estimate(nil, model.Model{}, model.Context{}); !errors.Is(err, contextwindow.ErrInvalidContext) {
		t.Fatalf("Estimate(nil) error = %v, want ErrInvalidContext", err)
	}

	want := errors.New("late estimator error")
	ctx, cancel := context.WithCancel(context.Background())
	canceling := contextwindow.EstimatorFunc(func(context.Context, model.Model, model.Context) (int64, error) {
		cancel()
		return 99, want
	})
	got, err = canceling.Estimate(ctx, model.Model{}, model.Context{})
	if !errors.Is(err, context.Canceled) || got != 0 {
		t.Fatalf("canceling Estimate() = %d, %v, want 0/context.Canceled", got, err)
	}
}

var _ contextwindow.Estimator = contextwindow.EstimatorFunc(nil)
