package agent

import (
	"context"
	"fmt"

	"github.com/JIAOZAI1/acore/model"
	"github.com/JIAOZAI1/acore/prompt"
)

type configuredAgent struct {
	llm            model.LLM
	runStrategy    RunStrategy
	promptRenderer prompt.Renderer
	options        ModelOptions
}

func (a *configuredAgent) Run(ctx context.Context, req Request) (Stream, error) {
	if ctx == nil {
		return nil, fmt.Errorf("%w: nil context", ErrInvalidRequest)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateRequestInput(req); err != nil {
		return nil, err
	}

	options := mergeModelOptions(a.options, req.Options)
	if err := validateModelOptions(options); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidRequest, err)
	}

	normalizedRequest := Request{
		Messages:     cloneMessages(req.Messages),
		Session:      cloneSessionInput(req.Session),
		Options:      cloneModelOptions(options),
		PromptValues: clonePromptValues(req.PromptValues),
	}
	systemPrompt := ""
	if a.promptRenderer != nil {
		rendered, err := a.promptRenderer.Render(ctx, prompt.Input{
			Values: clonePromptValues(normalizedRequest.PromptValues),
		})
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrRenderPrompt, err)
		}
		systemPrompt = rendered
	}

	input := RunInput{
		LLM:          a.llm,
		SystemPrompt: systemPrompt,
		Request:      normalizedRequest,
	}
	stream, err := a.runStrategy.Run(ctx, input)
	if ctxErr := ctx.Err(); ctxErr != nil {
		if stream != nil {
			releaseUnstartedStrategyStream(stream)
		}
		return nil, ctxErr
	}
	if err != nil {
		return nil, fmt.Errorf("agent: run strategy: %w", err)
	}
	if stream == nil {
		return nil, ErrUnexpectedStreamEnd
	}

	return protectStrategyStream(ctx, stream), nil
}

func protectStrategyStream(ctx context.Context, stream Stream) Stream {
	return func(yield func(Event, error) bool) {
		if err := ctx.Err(); err != nil {
			releaseUnstartedStrategyStream(stream)
			yield(Event{}, err)
			return
		}

		for event, streamErr := range stream {
			if err := ctx.Err(); err != nil {
				yield(Event{}, err)
				return
			}
			if streamErr != nil {
				yield(Event{}, streamErr)
				return
			}
			if event.Type == EventRunDone && event.Result == nil {
				yield(Event{}, ErrInvalidDoneEvent)
				return
			}

			if !yield(cloneEvent(event), nil) {
				return
			}
			if event.Type == EventRunDone {
				return
			}
		}

		if err := ctx.Err(); err != nil {
			yield(Event{}, err)
			return
		}
		yield(Event{}, ErrUnexpectedStreamEnd)
	}
}

func releaseUnstartedStrategyStream(stream Stream) {
	stream(func(Event, error) bool { return false })
}
