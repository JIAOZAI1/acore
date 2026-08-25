package agent_test

import (
	"context"
	"fmt"

	"github.com/JIAOZAI1/acore/agent"
	"github.com/JIAOZAI1/acore/model"
	"github.com/JIAOZAI1/acore/tool"
)

func ExampleNewToolLoopBuilder() {
	toolBuilder := tool.NewBuilder()
	toolService, err := toolBuilder.Build()
	if err != nil {
		panic(err)
	}

	strategyBuilder := agent.NewToolLoopBuilder()
	if err := strategyBuilder.UseTools(toolService); err != nil {
		panic(err)
	}
	strategy, err := strategyBuilder.Build()
	if err != nil {
		panic(err)
	}

	llm := &fakeLLM{generate: func(context.Context, model.Request) (model.Stream, error) {
		return doneModelStream("done"), nil
	}}
	agentBuilder := agent.NewBuilder()
	if err := agentBuilder.UseLLM(llm); err != nil {
		panic(err)
	}
	if err := agentBuilder.UseRunStrategy(strategy); err != nil {
		panic(err)
	}
	value, err := agentBuilder.Build()
	if err != nil {
		panic(err)
	}

	result, err := agent.Complete(context.Background(), value, agent.Request{Messages: userMessages("hello")})
	if err != nil {
		panic(err)
	}
	fmt.Println(result.Output.Content[0].Text)

	// Output: done
}
