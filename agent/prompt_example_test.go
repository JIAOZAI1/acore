package agent_test

import (
	"context"
	"fmt"

	"github.com/JIAOZAI1/acore/agent"
	"github.com/JIAOZAI1/acore/model"
	"github.com/JIAOZAI1/acore/prompt"
)

type promptExampleLLM struct{}

func (promptExampleLLM) Model() model.Model {
	return model.Model{ID: "example-model", Provider: "example"}
}

func (promptExampleLLM) Generate(_ context.Context, request model.Request) (model.Stream, error) {
	return doneModelStream(request.Context.SystemPrompt), nil
}

func ExampleBuilder_UsePrompt() {
	renderer, err := prompt.NewTemplate(prompt.TemplateConfig{
		Name: "assistant",
		Text: "You are a {{.role}} assistant. Reply in {{.language}}.",
		Defaults: prompt.Values{
			"language": "English",
		},
	})
	if err != nil {
		panic(err)
	}

	builder := agent.NewBuilder()
	if err := builder.UseLLM(promptExampleLLM{}); err != nil {
		panic(err)
	}
	if err := builder.UseRunStrategy(agent.NewSingleTurnStrategy()); err != nil {
		panic(err)
	}
	if err := builder.UsePrompt(renderer); err != nil {
		panic(err)
	}
	value, err := builder.Build()
	if err != nil {
		panic(err)
	}

	result, err := agent.Complete(context.Background(), value, agent.Request{
		Messages: []model.Message{{
			Role:    model.RoleUser,
			Content: []model.ContentBlock{{Kind: model.ContentText, Text: "help"}},
		}},
		PromptValues: prompt.Values{"role": "support"},
	})
	if err != nil {
		panic(err)
	}
	fmt.Println(result.Output.Content[0].Text)
	// Output: You are a support assistant. Reply in English.
}
