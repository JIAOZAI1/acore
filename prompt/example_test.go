package prompt_test

import (
	"context"
	"fmt"

	"github.com/JIAOZAI1/acore/prompt"
)

func ExampleTemplate() {
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

	output, err := renderer.Render(context.Background(), prompt.Input{
		Values: prompt.Values{"role": "support"},
	})
	if err != nil {
		panic(err)
	}
	fmt.Println(output)
	// Output: You are a support assistant. Reply in English.
}
