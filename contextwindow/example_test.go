package contextwindow_test

import (
	"context"
	"fmt"

	"github.com/JIAOZAI1/acore/contextwindow"
	"github.com/JIAOZAI1/acore/model"
)

func ExampleTailReducer() {
	estimator := contextwindow.EstimatorFunc(func(_ context.Context, _ model.Model, value model.Context) (int64, error) {
		return int64(len(value.Messages)), nil
	})
	reducer, err := contextwindow.NewTailReducer(contextwindow.TailConfig{
		Estimator: estimator,
	})
	if err != nil {
		panic(err)
	}

	fitted, err := contextwindow.Apply(context.Background(), reducer, contextwindow.Input{
		Model: model.Model{ContextWindow: 3},
		Context: model.Context{Messages: []model.Message{
			textMessage(model.RoleUser, "old question"),
			textMessage(model.RoleAssistant, "old answer"),
			textMessage(model.RoleUser, "current question"),
		}},
		RequestedOutputTokens: 2,
		ProtectedMessages:     1,
	})
	if err != nil {
		panic(err)
	}

	fmt.Println(len(fitted.Messages))
	fmt.Println(fitted.Messages[0].Content[0].Text)

	// Output:
	// 1
	// current question
}
