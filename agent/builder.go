package agent

import (
	"fmt"
	"math"

	"github.com/JIAOZAI1/acore/internal/nilcheck"
	"github.com/JIAOZAI1/acore/model"
	"github.com/JIAOZAI1/acore/prompt"
)

// Builder assembles an Agent during application startup. It is intended for
// single-goroutine setup. A successful Build freezes it.
type Builder struct {
	llm             model.LLM
	runStrategy     RunStrategy
	promptRenderer  prompt.Renderer
	modelOptions    ModelOptions
	llmSet          bool
	runStrategySet  bool
	promptSet       bool
	modelOptionsSet bool
	built           bool
}

// NewBuilder creates an empty Builder. An LLM and RunStrategy must be
// configured before Build.
func NewBuilder() *Builder {
	return &Builder{}
}

// UseLLM configures the model used by the built Agent.
func (b *Builder) UseLLM(llm model.LLM) error {
	if b.built {
		return ErrBuilderBuilt
	}
	if b.llmSet {
		return ErrLLMAlreadySet
	}
	if nilcheck.IsNil(llm) {
		return ErrNilLLM
	}

	b.llm = llm
	b.llmSet = true
	return nil
}

// UseRunStrategy configures the execution algorithm used by the built Agent.
func (b *Builder) UseRunStrategy(strategy RunStrategy) error {
	if b.built {
		return ErrBuilderBuilt
	}
	if b.runStrategySet {
		return ErrRunStrategyAlreadySet
	}
	if nilcheck.IsNil(strategy) {
		return ErrNilRunStrategy
	}

	b.runStrategy = strategy
	b.runStrategySet = true
	return nil
}

// UsePrompt configures the system-prompt renderer used by the built Agent.
func (b *Builder) UsePrompt(renderer prompt.Renderer) error {
	if b.built {
		return ErrBuilderBuilt
	}
	if b.promptSet {
		return fmt.Errorf("%w: prompt", ErrConfigAlreadySet)
	}
	if nilcheck.IsNil(renderer) {
		return ErrNilPromptRenderer
	}

	b.promptRenderer = renderer
	b.promptSet = true
	return nil
}

// SetSystemPrompt configures the fixed system prompt for every run.
func (b *Builder) SetSystemPrompt(text string) error {
	if b.built {
		return ErrBuilderBuilt
	}
	if b.promptSet {
		return fmt.Errorf("%w: system prompt", ErrConfigAlreadySet)
	}

	b.promptRenderer = prompt.NewStatic(text)
	b.promptSet = true
	return nil
}

// SetModelOptions configures the default portable model options.
func (b *Builder) SetModelOptions(options ModelOptions) error {
	if b.built {
		return ErrBuilderBuilt
	}
	if b.modelOptionsSet {
		return fmt.Errorf("%w: model options", ErrConfigAlreadySet)
	}
	if err := validateModelOptions(options); err != nil {
		return err
	}

	b.modelOptions = cloneModelOptions(options)
	b.modelOptionsSet = true
	return nil
}

// Build snapshots the configuration into an immutable Agent.
func (b *Builder) Build() (Agent, error) {
	if b.built {
		return nil, ErrBuilderBuilt
	}
	if !b.llmSet || nilcheck.IsNil(b.llm) {
		return nil, ErrMissingLLM
	}
	if !b.runStrategySet || nilcheck.IsNil(b.runStrategy) {
		return nil, ErrMissingRunStrategy
	}
	if err := validateModelOptions(b.modelOptions); err != nil {
		return nil, err
	}

	value := &configuredAgent{
		llm:            b.llm,
		runStrategy:    b.runStrategy,
		promptRenderer: b.promptRenderer,
		options:        cloneModelOptions(b.modelOptions),
	}
	b.built = true
	return value, nil
}

func validateModelOptions(options ModelOptions) error {
	if options.Temperature != nil && (math.IsNaN(*options.Temperature) || math.IsInf(*options.Temperature, 0)) {
		return fmt.Errorf("%w: temperature must be finite", ErrInvalidOptions)
	}
	if options.MaxTokens != nil && *options.MaxTokens <= 0 {
		return fmt.Errorf("%w: max tokens must be positive", ErrInvalidOptions)
	}
	if options.Reasoning != nil && !validReasoningLevel(*options.Reasoning) {
		return fmt.Errorf("%w: unknown reasoning level %d", ErrInvalidOptions, *options.Reasoning)
	}
	return nil
}

func validReasoningLevel(level model.ReasoningLevel) bool {
	switch level {
	case model.ReasoningDefault,
		model.ReasoningOff,
		model.ReasoningLow,
		model.ReasoningMedium,
		model.ReasoningHigh:
		return true
	default:
		return false
	}
}

func cloneModelOptions(options ModelOptions) ModelOptions {
	cloned := ModelOptions{}
	if options.Temperature != nil {
		value := *options.Temperature
		cloned.Temperature = &value
	}
	if options.MaxTokens != nil {
		value := *options.MaxTokens
		cloned.MaxTokens = &value
	}
	if options.Reasoning != nil {
		value := *options.Reasoning
		cloned.Reasoning = &value
	}
	return cloned
}

func mergeModelOptions(defaults, overrides ModelOptions) ModelOptions {
	merged := cloneModelOptions(defaults)
	if overrides.Temperature != nil {
		value := *overrides.Temperature
		merged.Temperature = &value
	}
	if overrides.MaxTokens != nil {
		value := *overrides.MaxTokens
		merged.MaxTokens = &value
	}
	if overrides.Reasoning != nil {
		value := *overrides.Reasoning
		merged.Reasoning = &value
	}
	return merged
}
