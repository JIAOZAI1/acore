package openai_test

import (
	"fmt"

	"github.com/JIAOZAI1/acore/model"
	"github.com/JIAOZAI1/acore/provider/openai"
)

func ExampleNew() {
	provider, err := openai.New(openai.Config{
		APIKey: "test-key",
		Models: []model.Model{{ID: "gpt-example"}},
	})
	if err != nil {
		panic(err)
	}

	configured := provider.Models()[0]
	fmt.Println(provider.ID(), configured.ID, configured.API)

	// Output: openai gpt-example openai-chat-completions
}
