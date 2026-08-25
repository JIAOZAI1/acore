package tool

import (
	"errors"
	"fmt"

	"github.com/JIAOZAI1/acore/internal/jsoncheck"
	"github.com/JIAOZAI1/acore/internal/nilcheck"
)

var (
	// ErrBuilderBuilt indicates that a successful Build froze the Builder.
	ErrBuilderBuilt = errors.New("tool: builder already built")
	// ErrNilTool indicates that AddTool received nil, including a typed nil.
	ErrNilTool = errors.New("tool: nil tool")
	// ErrEmptyToolName indicates that a Tool or Call has no name.
	ErrEmptyToolName = errors.New("tool: empty tool name")
	// ErrInvalidSchema indicates that a Tool parameter schema is not a JSON object.
	ErrInvalidSchema = errors.New("tool: invalid parameter schema")
	// ErrDuplicateTool indicates that a tool name is already registered.
	ErrDuplicateTool = errors.New("tool: duplicate tool")
	// ErrNilProxy indicates that UseProxy received nil, including a typed nil.
	ErrNilProxy = errors.New("tool: nil proxy")
	// ErrInvalidArguments indicates that invocation arguments are not a JSON object.
	ErrInvalidArguments = errors.New("tool: invalid arguments")
	// ErrToolNotFound indicates that a Call names no registered Tool.
	ErrToolNotFound = errors.New("tool: tool not found")
	// ErrInvalidInvocation indicates that a Proxy passed an invalid Invocation to Next.
	ErrInvalidInvocation = errors.New("tool: invalid invocation")
)

type registeredTool struct {
	spec Spec
	tool Tool
}

// Builder registers tools and execution proxies during application startup. It
// is intended for single-goroutine setup. A successful Build freezes it.
type Builder struct {
	tools   map[string]registeredTool
	order   []string
	proxies []Proxy
	built   bool
}

// NewBuilder creates an empty Builder. An empty tool system is valid.
func NewBuilder() *Builder {
	return &Builder{tools: make(map[string]registeredTool)}
}

// AddTool registers a Tool by its unique Spec name and snapshots its Spec.
func (b *Builder) AddTool(value Tool) error {
	if b.built {
		return ErrBuilderBuilt
	}
	if nilcheck.IsNil(value) {
		return ErrNilTool
	}

	spec := cloneSpec(value.Spec())
	if spec.Name == "" {
		return ErrEmptyToolName
	}
	if !jsoncheck.IsObject(spec.Parameters) {
		return fmt.Errorf("%w: %s", ErrInvalidSchema, spec.Name)
	}
	if b.tools == nil {
		b.tools = make(map[string]registeredTool)
	}
	if _, exists := b.tools[spec.Name]; exists {
		return fmt.Errorf("%w: %s", ErrDuplicateTool, spec.Name)
	}

	b.tools[spec.Name] = registeredTool{spec: spec, tool: value}
	b.order = append(b.order, spec.Name)
	return nil
}

// UseProxy appends a Proxy. Proxies run in registration order on the request
// path and reverse order on the result path.
func (b *Builder) UseProxy(proxy Proxy) error {
	if b.built {
		return ErrBuilderBuilt
	}
	if nilcheck.IsNil(proxy) {
		return ErrNilProxy
	}
	b.proxies = append(b.proxies, proxy)
	return nil
}

// Build snapshots the catalog and proxy list into an immutable System.
func (b *Builder) Build() (*System, error) {
	if b.built {
		return nil, ErrBuilderBuilt
	}

	tools := make(map[string]registeredTool, len(b.tools))
	specs := make([]Spec, 0, len(b.order))
	for _, name := range b.order {
		registered := b.tools[name]
		registered.spec = cloneSpec(registered.spec)
		tools[name] = registered
		specs = append(specs, cloneSpec(registered.spec))
	}
	proxies := append([]Proxy(nil), b.proxies...)

	b.built = true
	return &System{tools: tools, specs: specs, proxies: proxies}, nil
}
