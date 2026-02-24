package resorch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sync"
)

type registryKey struct {
	kind   string
	driver string
}

type compiledDefinition struct {
	decode  func(raw json.RawMessage) (any, error)
	deps    func(opt any) ([]ID, error)
	build   func(ctx context.Context, r Resolver, opt any) (any, error)
	closeFn func(ctx context.Context, out any) error
}

// Registry stores all registered resource definitions by (kind, driver).
type Registry struct {
	mu   sync.RWMutex
	defs map[registryKey]compiledDefinition
}

func NewRegistry() *Registry {
	return &Registry{
		defs: make(map[registryKey]compiledDefinition),
	}
}

// Register registers one resource definition with generics.
func Register[Opt any, Out any](r *Registry, kind string, driver string, def Definition[Opt, Out]) error {
	if r == nil {
		return fmt.Errorf("register resource definition: registry is nil")
	}
	if kind == "" {
		return fmt.Errorf("register resource definition: kind is empty")
	}
	if driver == "" {
		return fmt.Errorf("register resource definition: driver is empty")
	}
	if def.Build == nil {
		return fmt.Errorf("register resource definition: build func is nil for %s:%s", kind, driver)
	}

	decodeFn := def.Decode
	if decodeFn == nil {
		decodeFn = defaultDecode[Opt]
	}
	depsFn := def.Deps
	if depsFn == nil {
		depsFn = func(Opt) ([]ID, error) { return nil, nil }
	}

	compiled := compiledDefinition{
		decode: func(raw json.RawMessage) (any, error) {
			opt, err := decodeFn(raw)
			if err != nil {
				return nil, err
			}
			return opt, nil
		},
		deps: func(opt any) ([]ID, error) {
			typed, ok := opt.(Opt)
			if !ok {
				return nil, fmt.Errorf("deps option type mismatch: want=%T got=%T", *new(Opt), opt)
			}
			return depsFn(typed)
		},
		build: func(ctx context.Context, resolver Resolver, opt any) (any, error) {
			typed, ok := opt.(Opt)
			if !ok {
				return nil, fmt.Errorf("build option type mismatch: want=%T got=%T", *new(Opt), opt)
			}
			return def.Build(ctx, resolver, typed)
		},
	}
	if def.Close != nil {
		compiled.closeFn = func(ctx context.Context, out any) error {
			typed, ok := out.(Out)
			if !ok {
				return fmt.Errorf("close output type mismatch: want=%T got=%T", *new(Out), out)
			}
			return def.Close(ctx, typed)
		}
	}

	k := registryKey{kind: kind, driver: driver}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.defs[k]; exists {
		return fmt.Errorf("register resource definition: duplicate definition for %s:%s", kind, driver)
	}
	r.defs[k] = compiled
	return nil
}

// MustRegister panics on registration error; intended for bootstrap code paths.
func MustRegister[Opt any, Out any](r *Registry, kind string, driver string, def Definition[Opt, Out]) {
	if err := Register(r, kind, driver, def); err != nil {
		panic(err)
	}
}

func (r *Registry) get(kind string, driver string) (compiledDefinition, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	def, ok := r.defs[registryKey{kind: kind, driver: driver}]
	return def, ok
}

func defaultDecode[Opt any](raw json.RawMessage) (Opt, error) {
	var opt Opt
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return opt, nil
	}
	if err := json.Unmarshal(raw, &opt); err != nil {
		return opt, err
	}
	return opt, nil
}
