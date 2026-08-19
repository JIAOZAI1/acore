package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
)

var (
	// ErrNilTool indicates that AddTool received nil, including a typed nil.
	ErrNilTool = errors.New("tool: nil tool")
	// ErrEmptyToolName indicates that a Tool has no stable name.
	ErrEmptyToolName = errors.New("tool: empty tool name")
	// ErrInvalidSchema indicates that a Tool parameter schema is not valid JSON.
	ErrInvalidSchema = errors.New("tool: invalid parameter schema")
	// ErrDuplicateTool indicates that a tool name is already registered.
	ErrDuplicateTool = errors.New("tool: duplicate tool")
	// ErrToolNotFound indicates that a Call names no registered Tool.
	ErrToolNotFound = errors.New("tool: tool not found")
	// ErrInvalidArguments indicates that invocation arguments are not valid JSON.
	ErrInvalidArguments = errors.New("tool: invalid arguments")
	// ErrNilProxy indicates that UseProxy received nil, including a typed nil.
	ErrNilProxy = errors.New("tool: nil proxy")
	// ErrEmptyProxyID indicates that a Proxy has no stable ID.
	ErrEmptyProxyID = errors.New("tool: empty proxy ID")
	// ErrDuplicateProxy indicates that a Proxy ID is already registered.
	ErrDuplicateProxy = errors.New("tool: duplicate proxy")
	// ErrInvalidInvocation indicates that a Proxy passed an invalid value to Next.
	ErrInvalidInvocation = errors.New("tool: invalid invocation")
	// ErrBuilderBuilt indicates that a successful Build froze the Builder.
	ErrBuilderBuilt = errors.New("tool: builder already built")
)

type registeredTool struct {
	spec Spec
	tool Tool
}

// Builder registers tools and execution proxies during application startup.
// A successful Build freezes the Builder. It is intended for single-goroutine
// setup; the resulting System is safe for concurrent use when its Tools and
// Proxies are also safe for concurrent calls.
type Builder struct {
	tools    map[string]registeredTool
	proxies  []Proxy
	proxyIDs map[string]struct{}
	built    bool
}

// NewBuilder creates an empty tool-system builder. Building with no tools or
// proxies is valid and produces an empty Service.
func NewBuilder() *Builder {
	return &Builder{
		tools:    make(map[string]registeredTool),
		proxyIDs: make(map[string]struct{}),
	}
}

// AddTool registers one tool by its unique Spec name.
func (b *Builder) AddTool(value Tool) error {
	if b.built {
		return ErrBuilderBuilt
	}
	if isNil(value) {
		return ErrNilTool
	}
	spec := value.Spec()
	if spec.Name == "" {
		return ErrEmptyToolName
	}
	if len(spec.Parameters) == 0 || !json.Valid(spec.Parameters) {
		return fmt.Errorf("%w: %s", ErrInvalidSchema, spec.Name)
	}
	if _, exists := b.tools[spec.Name]; exists {
		return fmt.Errorf("%w: %s", ErrDuplicateTool, spec.Name)
	}
	b.tools[spec.Name] = registeredTool{spec: cloneSpec(spec), tool: value}
	return nil
}

// UseProxy appends one custom proxy. Proxies execute in registration order on
// the request path and reverse order on the response path.
func (b *Builder) UseProxy(proxy Proxy) error {
	if b.built {
		return ErrBuilderBuilt
	}
	if isNil(proxy) {
		return ErrNilProxy
	}
	id := proxy.ID()
	if id == "" {
		return ErrEmptyProxyID
	}
	if _, exists := b.proxyIDs[id]; exists {
		return fmt.Errorf("%w: %s", ErrDuplicateProxy, id)
	}
	b.proxyIDs[id] = struct{}{}
	b.proxies = append(b.proxies, proxy)
	return nil
}

// Build snapshots all descriptors, constructs an immutable proxy chain, and
// freezes the Builder.
func (b *Builder) Build() (*System, error) {
	if b.built {
		return nil, ErrBuilderBuilt
	}

	tools := make(map[string]registeredTool, len(b.tools))
	for name, registered := range b.tools {
		tools[name] = registeredTool{spec: cloneSpec(registered.spec), tool: registered.tool}
	}

	var chain Next = terminalExecutor{}
	for index := len(b.proxies) - 1; index >= 0; index-- {
		chain = &proxyNode{proxy: b.proxies[index], next: chain}
	}

	b.built = true
	return &System{tools: tools, chain: chain}, nil
}

// System is an immutable tool catalog and execution service. Every successfully
// resolved invocation traverses the complete configured proxy chain unless a
// proxy deliberately short-circuits it.
type System struct {
	tools map[string]registeredTool
	chain Next
}

// Specs returns tool descriptors ordered by name. Returned schemas are copies.
func (s *System) Specs() []Spec {
	names := make([]string, 0, len(s.tools))
	for name := range s.tools {
		names = append(names, name)
	}
	sort.Strings(names)

	specs := make([]Spec, 0, len(names))
	for _, name := range names {
		specs = append(specs, cloneSpec(s.tools[name].spec))
	}
	return specs
}

// Execute resolves a call and sends it through the immutable proxy chain.
func (s *System) Execute(ctx context.Context, call Call) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if call.Name == "" {
		return Result{}, ErrEmptyToolName
	}
	if len(call.Arguments) == 0 || !json.Valid(call.Arguments) {
		return Result{}, fmt.Errorf("%w: %s", ErrInvalidArguments, call.Name)
	}
	registered, exists := s.tools[call.Name]
	if !exists {
		return Result{}, fmt.Errorf("%w: %s", ErrToolNotFound, call.Name)
	}

	invocation := Invocation{
		callID:    call.CallID,
		name:      call.Name,
		arguments: cloneJSON(call.Arguments),
		spec:      cloneSpec(registered.spec),
		tool:      registered.tool,
	}
	return s.chain.Execute(ctx, invocation)
}

type proxyNode struct {
	proxy Proxy
	next  Next
}

func (n *proxyNode) Execute(ctx context.Context, invocation Invocation) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	return n.proxy.Execute(ctx, invocation, n.next)
}

type terminalExecutor struct{}

func (terminalExecutor) Execute(ctx context.Context, invocation Invocation) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if isNil(invocation.tool) || invocation.name == "" {
		return Result{}, ErrInvalidInvocation
	}
	if len(invocation.arguments) == 0 || !json.Valid(invocation.arguments) {
		return Result{}, fmt.Errorf("%w: %s", ErrInvalidArguments, invocation.name)
	}
	result, err := invocation.tool.Execute(ctx, cloneJSON(invocation.arguments))
	if err != nil {
		return Result{}, fmt.Errorf("tool: execute %q: %w", invocation.name, err)
	}
	return result, nil
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

var _ Service = (*System)(nil)
