// Package runtime composes the process-wide capabilities used by agent runs.
//
// A Runtime is immutable after construction and safe to share between
// concurrent runs. Request cancellation and deadlines remain explicit through
// context.Context parameters; they are never stored in Runtime.
package runtime

import (
	"errors"
	"reflect"

	"github.com/JIAOZAI1/acore/event"
	"github.com/JIAOZAI1/acore/model"
	"github.com/JIAOZAI1/acore/tool"
)

var (
	// ErrBuilderBuilt indicates that a successful Build has frozen the Builder.
	ErrBuilderBuilt = errors.New("runtime: builder already built")
	// ErrNilProvider indicates that AddProvider received a nil provider,
	// including a typed nil pointer.
	ErrNilProvider = errors.New("runtime: nil model provider")
	// ErrNoProviders indicates that Build has no model provider to expose.
	ErrNoProviders = errors.New("runtime: no model providers registered")
	// ErrNilEvents indicates that an event publisher was not configured or was nil.
	ErrNilEvents = errors.New("runtime: nil event publisher")
	// ErrEventsAlreadySet indicates that UseEvents was called more than once.
	ErrEventsAlreadySet = errors.New("runtime: event publisher already configured")
	// ErrNilTools indicates that a tool service was not configured or was nil.
	ErrNilTools = errors.New("runtime: nil tool service")
	// ErrToolsAlreadySet indicates that UseTools was called more than once.
	ErrToolsAlreadySet = errors.New("runtime: tool service already configured")
)

// ModelService is the model capability exposed by Runtime. Implementations
// resolve a provider/model address into an immutable LLM binding.
type ModelService interface {
	LLM(providerID, modelID string) (model.LLM, error)
}

// Runtime is an immutable process-wide capability container. It exposes only
// narrow service interfaces so callers cannot mutate the registries used to
// build it.
type Runtime struct {
	models ModelService
	tools  tool.Service
	events event.Publisher
}

// Models returns the configured model lookup capability.
func (r *Runtime) Models() ModelService { return r.models }

// Tools returns the configured tool service. Tool and proxy registration and
// proxy-chain execution remain encapsulated by the service implementation.
func (r *Runtime) Tools() tool.Service { return r.tools }

// Events returns the configured event publication capability.
func (r *Runtime) Events() event.Publisher { return r.events }

// Builder assembles a Runtime during application startup. A successful Build
// freezes the Builder. A failed Build leaves it open so missing capabilities
// can be supplied before retrying.
//
// Builder is intended for single-goroutine application setup. The Runtime
// returned by Build may be used concurrently.
type Builder struct {
	providers *model.ProviderRegistry
	tools     tool.Service
	events    event.Publisher
	built     bool
}

// NewBuilder creates an empty Runtime builder.
func NewBuilder() *Builder {
	return &Builder{providers: model.NewProviderRegistry()}
}

// AddProvider adds one model provider. Provider IDs must be non-empty and
// unique. Providers remain owned by the caller; Runtime does not close them.
func (b *Builder) AddProvider(provider model.Provider) error {
	if b.built {
		return ErrBuilderBuilt
	}
	if isNil(provider) {
		return ErrNilProvider
	}
	return b.providers.Add(provider)
}

// UseEvents configures the single event publication service.
func (b *Builder) UseEvents(events event.Publisher) error {
	if b.built {
		return ErrBuilderBuilt
	}
	if isNil(events) {
		return ErrNilEvents
	}
	if b.events != nil {
		return ErrEventsAlreadySet
	}
	b.events = events
	return nil
}

// UseTools configures the single tool service. The service must already own
// its tool registry and immutable proxy chain; Runtime does not inspect them.
func (b *Builder) UseTools(tools tool.Service) error {
	if b.built {
		return ErrBuilderBuilt
	}
	if isNil(tools) {
		return ErrNilTools
	}
	if b.tools != nil {
		return ErrToolsAlreadySet
	}
	b.tools = tools
	return nil
}

// Build validates the configured capabilities and returns an immutable
// Runtime. The Builder is frozen only after a successful build.
func (b *Builder) Build() (*Runtime, error) {
	if b.built {
		return nil, ErrBuilderBuilt
	}
	if len(b.providers.Providers()) == 0 {
		return nil, ErrNoProviders
	}
	if isNil(b.events) {
		return nil, ErrNilEvents
	}
	if isNil(b.tools) {
		return nil, ErrNilTools
	}

	b.built = true
	return &Runtime{models: b.providers, tools: b.tools, events: b.events}, nil
}

func isNil(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}
