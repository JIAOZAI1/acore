package prompt_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/JIAOZAI1/acore/prompt"
)

func TestNewTemplateValidatesConfig(t *testing.T) {
	tests := []struct {
		name   string
		config prompt.TemplateConfig
	}{
		{name: "empty name", config: prompt.TemplateConfig{}},
		{name: "invalid text", config: prompt.TemplateConfig{Name: "invalid", Text: "{{"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := prompt.NewTemplate(test.config); !errors.Is(err, prompt.ErrInvalidTemplate) {
				t.Fatalf("NewTemplate() error = %v, want ErrInvalidTemplate", err)
			}
		})
	}

	empty, err := prompt.NewTemplate(prompt.TemplateConfig{Name: "empty"})
	if err != nil {
		t.Fatalf("NewTemplate(empty text) error = %v", err)
	}
	output, err := empty.Render(context.Background(), prompt.Input{})
	if err != nil {
		t.Fatalf("Render(empty text) error = %v", err)
	}
	if output != "" {
		t.Fatalf("Render(empty text) = %q, want empty", output)
	}
}

func TestTemplateRenderMergesValuesStrictly(t *testing.T) {
	defaults := prompt.Values{
		"language": "zh-CN",
		"region":   "default",
		"tone":     "friendly",
	}
	renderer, err := prompt.NewTemplate(prompt.TemplateConfig{
		Name:     "assistant",
		Text:     "{{.role}}|{{.language}}|{{.region}}|{{.tone}}|{{index . \"name-with-dash\"}}",
		Defaults: defaults,
	})
	if err != nil {
		t.Fatalf("NewTemplate() error = %v", err)
	}
	defaults["language"] = "mutated"
	defaults["tone"] = "mutated"

	values := prompt.Values{
		"role":           "support",
		"language":       "",
		"region":         "run",
		"name-with-dash": "strict-index",
		"extra":          "ignored",
	}
	got, err := renderer.Render(context.Background(), prompt.Input{Values: values})
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	if got != "support||run|friendly|strict-index" {
		t.Fatalf("Render() = %q, want merged values", got)
	}
	if values["language"] != "" || values["region"] != "run" {
		t.Fatal("Render() modified input values")
	}
}

func TestTemplateRenderReportsMissingVariables(t *testing.T) {
	tests := []struct {
		name string
		text string
	}{
		{name: "field", text: "before {{.missing}} after"},
		{name: "strict index", text: "before {{index . \"missing-key\"}} after"},
		{name: "invalid strict index input", text: "{{index .present 0}}"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			renderer, err := prompt.NewTemplate(prompt.TemplateConfig{Name: test.name, Text: test.text})
			if err != nil {
				t.Fatalf("NewTemplate() error = %v", err)
			}
			output, err := renderer.Render(context.Background(), prompt.Input{Values: prompt.Values{"present": "value"}})
			if !errors.Is(err, prompt.ErrRender) {
				t.Fatalf("Render() error = %v, want ErrRender", err)
			}
			if output != "" {
				t.Fatalf("Render() output = %q, want no partial output", output)
			}
		})
	}
}

func TestTemplateRenderPreservesTextAndValidatesContext(t *testing.T) {
	renderer, err := prompt.NewTemplate(prompt.TemplateConfig{
		Name: "exact",
		Text: "  <{{.value}}>\n",
	})
	if err != nil {
		t.Fatalf("NewTemplate() error = %v", err)
	}
	got, err := renderer.Render(context.Background(), prompt.Input{Values: prompt.Values{"value": "a&b"}})
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	if got != "  <a&b>\n" {
		t.Fatalf("Render() = %q, want unescaped exact text", got)
	}
	if _, err := renderer.Render(nil, prompt.Input{}); !errors.Is(err, prompt.ErrInvalidContext) {
		t.Fatalf("Render(nil) error = %v, want ErrInvalidContext", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := renderer.Render(ctx, prompt.Input{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Render(canceled) error = %v, want context.Canceled", err)
	}

	var nilRenderer *prompt.Template
	if _, err := nilRenderer.Render(context.Background(), prompt.Input{}); !errors.Is(err, prompt.ErrNilRenderer) {
		t.Fatalf("nil Render() error = %v, want ErrNilRenderer", err)
	}
	zeroRenderer := &prompt.Template{}
	if _, err := zeroRenderer.Render(context.Background(), prompt.Input{}); !errors.Is(err, prompt.ErrInvalidTemplate) {
		t.Fatalf("zero Render() error = %v, want ErrInvalidTemplate", err)
	}
}

func TestTemplateSupportsConcurrentRender(t *testing.T) {
	renderer, err := prompt.NewTemplate(prompt.TemplateConfig{Name: "concurrent", Text: "run={{.value}}"})
	if err != nil {
		t.Fatalf("NewTemplate() error = %v", err)
	}

	const runs = 32
	errorsByRun := make(chan error, runs)
	var wait sync.WaitGroup
	for index := 0; index < runs; index++ {
		index := index
		wait.Add(1)
		go func() {
			defer wait.Done()
			want := fmt.Sprintf("run=%d", index)
			got, err := renderer.Render(context.Background(), prompt.Input{Values: prompt.Values{"value": fmt.Sprint(index)}})
			if err != nil {
				errorsByRun <- fmt.Errorf("%s: %w", want, err)
				return
			}
			if got != want {
				errorsByRun <- fmt.Errorf("%s: output = %q", want, got)
			}
		}()
	}
	wait.Wait()
	close(errorsByRun)
	for err := range errorsByRun {
		t.Error(err)
	}
}

var _ prompt.Renderer = (*prompt.Template)(nil)
