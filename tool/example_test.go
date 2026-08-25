package tool_test

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/JIAOZAI1/acore/tool"
)

type greetingTool struct{}

func (greetingTool) Spec() tool.Spec {
	return tool.Spec{
		Name:        "greet",
		Description: "greets a user",
		Parameters:  json.RawMessage(`{"type":"object"}`),
	}
}

func (greetingTool) Execute(_ context.Context, arguments json.RawMessage) (tool.Result, error) {
	var input struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(arguments, &input); err != nil {
		return tool.Result{}, fmt.Errorf("decode greeting arguments: %w", err)
	}
	return tool.Result{Content: "hello, " + input.Name}, nil
}

func ExampleSystem() {
	builder := tool.NewBuilder()
	if err := builder.AddTool(greetingTool{}); err != nil {
		panic(err)
	}
	system, err := builder.Build()
	if err != nil {
		panic(err)
	}

	result, err := system.Execute(context.Background(), tool.Call{
		ID:        "call-1",
		Name:      "greet",
		Arguments: json.RawMessage(`{"name":"acore"}`),
	})
	if err != nil {
		panic(err)
	}
	fmt.Println(result.Content)

	// Output: hello, acore
}
