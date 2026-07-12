package session

import (
	"errors"
	"fmt"
	"reflect"
	"sync"

	"github.com/carlotran4/kanbi/internal/multiplexer"
)

var (
	ErrMultiplexerRequired       = errors.New("multiplexer adapter is required")
	ErrMultiplexerKindRequired   = errors.New("multiplexer kind is required")
	ErrMultiplexerNotRegistered  = errors.New("multiplexer is not registered")
	ErrMultiplexerAlreadyPresent = errors.New("multiplexer is already registered")
)

type UnknownMultiplexerError struct {
	Kind multiplexer.Kind
}

func (e UnknownMultiplexerError) Error() string {
	return fmt.Sprintf("multiplexer %q is not registered", e.Kind)
}

func (e UnknownMultiplexerError) Unwrap() error { return ErrMultiplexerNotRegistered }

type DuplicateMultiplexerError struct {
	Kind multiplexer.Kind
}

func (e DuplicateMultiplexerError) Error() string {
	return fmt.Sprintf("multiplexer %q is already registered", e.Kind)
}

func (e DuplicateMultiplexerError) Unwrap() error { return ErrMultiplexerAlreadyPresent }

// Registry resolves compiled-in multiplexer adapters explicitly. It never
// falls back to a default for an empty or unknown durable provider kind.
type Registry struct {
	mu       sync.RWMutex
	adapters map[multiplexer.Kind]multiplexer.Interface
}

func NewRegistry(adapters ...multiplexer.Interface) (*Registry, error) {
	r := &Registry{adapters: make(map[multiplexer.Kind]multiplexer.Interface, len(adapters))}
	for _, adapter := range adapters {
		if err := r.Register(adapter); err != nil {
			return nil, err
		}
	}
	return r, nil
}

func (r *Registry) Register(adapter multiplexer.Interface) error {
	if adapter == nil || isNilInterface(adapter) {
		return ErrMultiplexerRequired
	}
	kind := adapter.Kind()
	if kind == "" {
		return ErrMultiplexerKindRequired
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.adapters == nil {
		r.adapters = make(map[multiplexer.Kind]multiplexer.Interface)
	}
	if _, exists := r.adapters[kind]; exists {
		return DuplicateMultiplexerError{Kind: kind}
	}
	r.adapters[kind] = adapter
	return nil
}

func isNilInterface(value any) bool {
	rv := reflect.ValueOf(value)
	switch rv.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return rv.IsNil()
	default:
		return false
	}
}

func (r *Registry) For(kind multiplexer.Kind) (multiplexer.Interface, error) {
	if kind == "" {
		return nil, ErrMultiplexerKindRequired
	}
	if r == nil {
		return nil, UnknownMultiplexerError{Kind: kind}
	}
	r.mu.RLock()
	adapter := r.adapters[kind]
	r.mu.RUnlock()
	if adapter == nil {
		return nil, UnknownMultiplexerError{Kind: kind}
	}
	return adapter, nil
}
