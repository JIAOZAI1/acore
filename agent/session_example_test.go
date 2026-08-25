package agent_test

import (
	"context"
	"fmt"

	"github.com/JIAOZAI1/acore/agent"
	"github.com/JIAOZAI1/acore/model"
	"github.com/JIAOZAI1/acore/session"
)

func ExampleNewSingleTurnBuilder_session() {
	history := session.NewMemoryService()
	strategyBuilder := agent.NewSingleTurnBuilder()
	if err := strategyBuilder.UseSession(history); err != nil {
		panic(err)
	}
	strategy, err := strategyBuilder.Build()
	if err != nil {
		panic(err)
	}

	agentBuilder := agent.NewBuilder()
	if err := agentBuilder.UseLLM(promptExampleLLM{}); err != nil {
		panic(err)
	}
	if err := agentBuilder.UseRunStrategy(strategy); err != nil {
		panic(err)
	}
	value, err := agentBuilder.Build()
	if err != nil {
		panic(err)
	}

	key := session.Key{Scope: "example", ID: "conversation-1"}
	_, err = agent.Complete(context.Background(), value, agent.Request{
		Session: &agent.SessionInput{
			Key: key,
			Messages: []model.Message{{
				Role:    model.RoleUser,
				Content: []model.ContentBlock{{Kind: model.ContentText, Text: "say hello"}},
			}},
		},
	})
	if err != nil {
		panic(err)
	}

	snapshot, err := history.Load(context.Background(), key)
	if err != nil {
		panic(err)
	}
	fmt.Println(snapshot.Revision, len(snapshot.Messages))

	// Output: 1 2
}
