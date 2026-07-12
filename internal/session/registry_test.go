package session

import (
	"context"
	"errors"
	"testing"

	"github.com/carlotran4/kanbi/internal/multiplexer"
)

func TestRegistryResolvesAdapterByKind(t *testing.T) {
	tmuxAdapter := stubMultiplexer{kind: multiplexer.KindTmux}
	herdrAdapter := stubMultiplexer{kind: multiplexer.KindHerdr}
	registry, err := NewRegistry(tmuxAdapter, herdrAdapter)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	got, err := registry.For(multiplexer.KindHerdr)
	if err != nil {
		t.Fatalf("For() error = %v", err)
	}
	if got.Kind() != multiplexer.KindHerdr {
		t.Fatalf("For() kind = %q, want %q", got.Kind(), multiplexer.KindHerdr)
	}
}

func TestRegistryRejectsInvalidRegistrations(t *testing.T) {
	var typedNil *pointerStubMultiplexer
	tests := []struct {
		name     string
		adapters []multiplexer.Interface
		want     error
	}{
		{name: "missing adapter", adapters: []multiplexer.Interface{nil}, want: ErrMultiplexerRequired},
		{name: "typed nil adapter", adapters: []multiplexer.Interface{typedNil}, want: ErrMultiplexerRequired},
		{name: "missing kind", adapters: []multiplexer.Interface{stubMultiplexer{}}, want: ErrMultiplexerKindRequired},
		{name: "duplicate kind", adapters: []multiplexer.Interface{stubMultiplexer{kind: multiplexer.KindTmux}, stubMultiplexer{kind: multiplexer.KindTmux}}, want: ErrMultiplexerAlreadyPresent},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewRegistry(tt.adapters...)
			if !errors.Is(err, tt.want) {
				t.Fatalf("NewRegistry() error = %v, want errors.Is(%v)", err, tt.want)
			}
		})
	}
}

type pointerStubMultiplexer struct{ stubMultiplexer }

func TestRegistryReturnsExplicitMissingAndUnknownErrors(t *testing.T) {
	registry, err := NewRegistry(stubMultiplexer{kind: multiplexer.KindTmux})
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	if _, err := registry.For(""); !errors.Is(err, ErrMultiplexerKindRequired) {
		t.Fatalf("For(empty) error = %v, want ErrMultiplexerKindRequired", err)
	}
	_, err = registry.For(multiplexer.Kind("future"))
	if !errors.Is(err, ErrMultiplexerNotRegistered) {
		t.Fatalf("For(unknown) error = %v, want ErrMultiplexerNotRegistered", err)
	}
	var unknown UnknownMultiplexerError
	if !errors.As(err, &unknown) || unknown.Kind != "future" {
		t.Fatalf("For(unknown) error = %v, want typed error for future", err)
	}
}

type stubMultiplexer struct {
	kind multiplexer.Kind
}

func (s stubMultiplexer) Kind() multiplexer.Kind             { return s.kind }
func (stubMultiplexer) Ensure(context.Context, string) error { return nil }
func (stubMultiplexer) Launch(context.Context, multiplexer.LaunchSpec) (multiplexer.ContainerRef, error) {
	return multiplexer.ContainerRef{}, nil
}
func (stubMultiplexer) Validate(context.Context, multiplexer.ContainerRef) (bool, error) {
	return true, nil
}
func (stubMultiplexer) Focus(context.Context, multiplexer.ContainerRef) error { return nil }
func (stubMultiplexer) Read(context.Context, multiplexer.ContainerRef, multiplexer.ReadOptions) (string, error) {
	return "", nil
}
func (stubMultiplexer) SendText(context.Context, multiplexer.ContainerRef, string) error { return nil }
func (stubMultiplexer) SendKeys(context.Context, multiplexer.ContainerRef, ...string) error {
	return nil
}
func (stubMultiplexer) Close(context.Context, multiplexer.ContainerRef) error { return nil }
func (stubMultiplexer) Detect(context.Context, multiplexer.ContainerRef) (multiplexer.Detection, error) {
	return multiplexer.Detection{}, nil
}
