package runtime_test

import (
	"context"
	"errors"
	"testing"

	"github.com/JIAOZAI1/acore/event"
	"github.com/JIAOZAI1/acore/model"
	acruntime "github.com/JIAOZAI1/acore/runtime"
	"github.com/JIAOZAI1/acore/tool"
)

type fakeProvider struct {
	id string
}

func (p *fakeProvider) ID() string { return p.id }

func (p *fakeProvider) Models() []model.Model {
	return []model.Model{{ID: "test-model", Provider: p.id}}
}

func (p *fakeProvider) Generate(context.Context, model.Model, model.Request) (model.Stream, error) {
	return nil, nil
}

type fakeEvent struct{}

func (fakeEvent) Name() string { return "runtime.test" }

func validBuilder(t *testing.T) (*acruntime.Builder, *event.Bus) {
	t.Helper()
	builder := acruntime.NewBuilder()
	if err := builder.AddProvider(&fakeProvider{id: "test"}); err != nil {
		t.Fatalf("AddProvider() error = %v", err)
	}
	bus := event.NewBus()
	if err := builder.UseEvents(bus); err != nil {
		t.Fatalf("UseEvents() error = %v", err)
	}
	tools, err := tool.NewBuilder().Build()
	if err != nil {
		t.Fatalf("build tools: %v", err)
	}
	if err := builder.UseTools(tools); err != nil {
		t.Fatalf("UseTools() error = %v", err)
	}
	return builder, bus
}

func TestBuilderBuildsRuntimeCapabilities(t *testing.T) {
	builder, bus := validBuilder(t)
	var publications int
	_, err := event.Subscribe(bus, func(context.Context, fakeEvent) error {
		publications++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	runtime, err := builder.Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	llm, err := runtime.Models().LLM("test", "test-model")
	if err != nil {
		t.Fatalf("Models().LLM() error = %v", err)
	}
	if llm.Model().ID != "test-model" {
		t.Fatalf("resolved model = %q, want test-model", llm.Model().ID)
	}
	if specs := runtime.Tools().Specs(); len(specs) != 0 {
		t.Fatalf("Tools().Specs() = %v, want empty", specs)
	}
	if err := runtime.Events().Publish(context.Background(), fakeEvent{}); err != nil {
		t.Fatalf("Events().Publish() error = %v", err)
	}
	if publications != 1 {
		t.Fatalf("publications = %d, want 1", publications)
	}
}

func TestBuilderValidatesRequiredCapabilities(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(*acruntime.Builder) error
		want    error
	}{
		{
			name: "providers",
			prepare: func(builder *acruntime.Builder) error {
				return builder.UseEvents(event.NewBus())
			},
			want: acruntime.ErrNoProviders,
		},
		{
			name: "events",
			prepare: func(builder *acruntime.Builder) error {
				return builder.AddProvider(&fakeProvider{id: "test"})
			},
			want: acruntime.ErrNilEvents,
		},
		{
			name: "tools",
			prepare: func(builder *acruntime.Builder) error {
				if err := builder.AddProvider(&fakeProvider{id: "test"}); err != nil {
					return err
				}
				return builder.UseEvents(event.NewBus())
			},
			want: acruntime.ErrNilTools,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			builder := acruntime.NewBuilder()
			if err := test.prepare(builder); err != nil {
				t.Fatalf("prepare error = %v", err)
			}
			if _, err := builder.Build(); !errors.Is(err, test.want) {
				t.Fatalf("Build() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestBuilderRejectsNilAndDuplicateCapabilities(t *testing.T) {
	builder := acruntime.NewBuilder()
	var nilProvider *fakeProvider
	if err := builder.AddProvider(nilProvider); !errors.Is(err, acruntime.ErrNilProvider) {
		t.Fatalf("AddProvider(nil) error = %v", err)
	}
	var nilBus *event.Bus
	if err := builder.UseEvents(nilBus); !errors.Is(err, acruntime.ErrNilEvents) {
		t.Fatalf("UseEvents(nil) error = %v", err)
	}
	var nilTools *tool.System
	if err := builder.UseTools(nilTools); !errors.Is(err, acruntime.ErrNilTools) {
		t.Fatalf("UseTools(nil) error = %v", err)
	}
	if err := builder.AddProvider(&fakeProvider{id: "duplicate"}); err != nil {
		t.Fatal(err)
	}
	if err := builder.AddProvider(&fakeProvider{id: "duplicate"}); err == nil {
		t.Fatal("AddProvider() accepted a duplicate provider ID")
	}
	if err := builder.UseEvents(event.NewBus()); err != nil {
		t.Fatal(err)
	}
	if err := builder.UseEvents(event.NewBus()); !errors.Is(err, acruntime.ErrEventsAlreadySet) {
		t.Fatalf("second UseEvents() error = %v", err)
	}
	tools, err := tool.NewBuilder().Build()
	if err != nil {
		t.Fatal(err)
	}
	if err := builder.UseTools(tools); err != nil {
		t.Fatal(err)
	}
	otherTools, err := tool.NewBuilder().Build()
	if err != nil {
		t.Fatal(err)
	}
	if err := builder.UseTools(otherTools); !errors.Is(err, acruntime.ErrToolsAlreadySet) {
		t.Fatalf("second UseTools() error = %v", err)
	}
}

func TestBuilderFreezesOnlyAfterSuccessfulBuild(t *testing.T) {
	builder := acruntime.NewBuilder()
	if _, err := builder.Build(); !errors.Is(err, acruntime.ErrNoProviders) {
		t.Fatalf("first Build() error = %v", err)
	}
	if err := builder.AddProvider(&fakeProvider{id: "test"}); err != nil {
		t.Fatalf("AddProvider() after failed build error = %v", err)
	}
	if err := builder.UseEvents(event.NewBus()); err != nil {
		t.Fatalf("UseEvents() after failed build error = %v", err)
	}
	tools, err := tool.NewBuilder().Build()
	if err != nil {
		t.Fatal(err)
	}
	if err := builder.UseTools(tools); err != nil {
		t.Fatalf("UseTools() after failed build error = %v", err)
	}
	if _, err := builder.Build(); err != nil {
		t.Fatalf("second Build() error = %v", err)
	}
	if err := builder.AddProvider(&fakeProvider{id: "late"}); !errors.Is(err, acruntime.ErrBuilderBuilt) {
		t.Fatalf("AddProvider() after build error = %v", err)
	}
	if err := builder.UseEvents(event.NewBus()); !errors.Is(err, acruntime.ErrBuilderBuilt) {
		t.Fatalf("UseEvents() after build error = %v", err)
	}
	if err := builder.UseTools(tools); !errors.Is(err, acruntime.ErrBuilderBuilt) {
		t.Fatalf("UseTools() after build error = %v", err)
	}
	if _, err := builder.Build(); !errors.Is(err, acruntime.ErrBuilderBuilt) {
		t.Fatalf("repeated Build() error = %v", err)
	}
}
