package prompt_test

import (
	"context"
	"errors"
	"testing"

	"github.com/JIAOZAI1/acore/prompt"
)

func TestStaticRender(t *testing.T) {
	renderer := prompt.NewStatic("  fixed prompt\n")
	values := prompt.Values{"ignored": "value"}
	got, err := renderer.Render(context.Background(), prompt.Input{Values: values})
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	if got != "  fixed prompt\n" {
		t.Fatalf("Render() = %q, want exact static text", got)
	}
	if values["ignored"] != "value" {
		t.Fatal("Render() modified input values")
	}

	empty, err := prompt.NewStatic("").Render(context.Background(), prompt.Input{})
	if err != nil {
		t.Fatalf("empty Render() error = %v", err)
	}
	if empty != "" {
		t.Fatalf("empty Render() = %q, want empty", empty)
	}
}

func TestStaticRenderValidatesReceiverAndContext(t *testing.T) {
	var nilRenderer *prompt.Static
	if _, err := nilRenderer.Render(context.Background(), prompt.Input{}); !errors.Is(err, prompt.ErrNilRenderer) {
		t.Fatalf("nil Render() error = %v, want ErrNilRenderer", err)
	}

	renderer := prompt.NewStatic("fixed")
	if _, err := renderer.Render(nil, prompt.Input{}); !errors.Is(err, prompt.ErrInvalidContext) {
		t.Fatalf("Render(nil) error = %v, want ErrInvalidContext", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := renderer.Render(ctx, prompt.Input{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Render(canceled) error = %v, want context.Canceled", err)
	}
}

func TestRendererFunc(t *testing.T) {
	values := prompt.Values{"name": "acore"}
	renderer := prompt.RendererFunc(func(_ context.Context, input prompt.Input) (string, error) {
		return input.Values["name"], nil
	})
	got, err := renderer.Render(context.Background(), prompt.Input{Values: values})
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	if got != "acore" {
		t.Fatalf("Render() = %q, want acore", got)
	}

	var nilRenderer prompt.RendererFunc
	if _, err := nilRenderer.Render(context.Background(), prompt.Input{}); !errors.Is(err, prompt.ErrNilRenderer) {
		t.Fatalf("nil Render() error = %v, want ErrNilRenderer", err)
	}
	if _, err := renderer.Render(nil, prompt.Input{}); !errors.Is(err, prompt.ErrInvalidContext) {
		t.Fatalf("Render(nil) error = %v, want ErrInvalidContext", err)
	}

	want := errors.New("render failed")
	failing := prompt.RendererFunc(func(context.Context, prompt.Input) (string, error) {
		return "partial", want
	})
	output, err := failing.Render(context.Background(), prompt.Input{})
	if !errors.Is(err, want) {
		t.Fatalf("failing Render() error = %v, want %v", err, want)
	}
	if output != "" {
		t.Fatalf("failing Render() output = %q, want empty", output)
	}
}

func TestRendererFuncPrefersContextError(t *testing.T) {
	want := errors.New("late renderer error")
	ctx, cancel := context.WithCancel(context.Background())
	renderer := prompt.RendererFunc(func(context.Context, prompt.Input) (string, error) {
		cancel()
		return "late output", want
	})
	output, err := renderer.Render(ctx, prompt.Input{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Render() error = %v, want context.Canceled", err)
	}
	if output != "" {
		t.Fatalf("Render() output = %q, want empty", output)
	}
}

var _ prompt.Renderer = prompt.RendererFunc(nil)
var _ prompt.Renderer = (*prompt.Static)(nil)
