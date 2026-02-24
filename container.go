package resorch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"
	"sync"

	"golang.org/x/sync/singleflight"
)

type compiledNode struct {
	id     ID
	driver string
	opt    any
	deps   []ID
	def    compiledDefinition
}

// Container is the runtime holder for resource instances.
// It provides:
// 1) startup dependency graph compilation and validation
// 2) runtime lazy build, caching, and singleflight deduplication
// 3) reverse-topological shutdown
type Container struct {
	registry *Registry

	nodes map[string]*compiledNode
	topo  []ID
	graph Graph

	mu        sync.RWMutex
	instances map[string]any

	sf singleflight.Group
}

func NewContainer(registry *Registry, specs []NodeSpec) (*Container, error) {
	if registry == nil {
		return nil, fmt.Errorf("new container: registry is nil")
	}

	c := &Container{
		registry:  registry,
		nodes:     make(map[string]*compiledNode, len(specs)),
		instances: make(map[string]any, len(specs)),
	}
	if err := c.compile(specs); err != nil {
		return nil, err
	}
	return c, nil
}

// Graph returns a compiled dependency graph snapshot for this container.
func (c *Container) Graph() Graph {
	return c.graph.clone()
}

// TopoOrder returns a topological order snapshot (dependencies first).
func (c *Container) TopoOrder() []ID {
	order := make([]ID, len(c.topo))
	copy(order, c.topo)
	return order
}

// Resolve builds or returns a cached resource instance by ID.
func (c *Container) Resolve(ctx context.Context, id ID) (any, error) {
	if err := validateID(id); err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}

	withStack, err := pushResolveStack(ctx, id)
	if err != nil {
		return nil, err
	}
	key := id.String()

	c.mu.RLock()
	cached, ok := c.instances[key]
	c.mu.RUnlock()
	if ok {
		return cached, nil
	}

	v, err, _ := c.sf.Do(key, func() (any, error) {
		c.mu.RLock()
		cachedAgain, ok := c.instances[key]
		c.mu.RUnlock()
		if ok {
			return cachedAgain, nil
		}

		node, ok := c.nodes[key]
		if !ok {
			return nil, NodeNotFoundError{ID: id}
		}

		for _, dep := range node.deps {
			if _, err := c.Resolve(withStack, dep); err != nil {
				return nil, fmt.Errorf("resolve dependency %s for %s: %w", dep.String(), key, err)
			}
		}

		instance, err := node.def.build(withStack, c, node.opt)
		if err != nil {
			return nil, fmt.Errorf("build resource %s: %w", key, err)
		}

		c.mu.Lock()
		c.instances[key] = instance
		c.mu.Unlock()
		return instance, nil
	})
	if err != nil {
		return nil, err
	}
	return v, nil
}

// ResolveIDs resolves instances in batch.
func (c *Container) ResolveIDs(ctx context.Context, ids []ID) error {
	for _, id := range ids {
		if _, err := c.Resolve(ctx, id); err != nil {
			return fmt.Errorf("resolve %s: %w", id.String(), err)
		}
	}
	return nil
}

// Resolved returns a cached instance without triggering a build.
func (c *Container) Resolved(id ID) (any, bool) {
	if validateID(id) != nil {
		return nil, false
	}
	key := id.String()
	c.mu.RLock()
	defer c.mu.RUnlock()
	v, ok := c.instances[key]
	return v, ok
}

// InjectResolved injects an external instance into cache (commonly used in hot reload reuse).
func (c *Container) InjectResolved(id ID, instance any) error {
	if err := validateID(id); err != nil {
		return err
	}
	key := id.String()
	if _, ok := c.nodes[key]; !ok {
		return NodeNotFoundError{ID: id}
	}
	c.mu.Lock()
	c.instances[key] = instance
	c.mu.Unlock()
	return nil
}

// ResolveAs is a typed wrapper around Resolve.
func ResolveAs[T any](ctx context.Context, r Resolver, id ID) (T, error) {
	var zero T
	v, err := r.Resolve(ctx, id)
	if err != nil {
		return zero, err
	}
	typed, ok := v.(T)
	if !ok {
		return zero, TypeMismatchError{
			ID:       id,
			Expected: reflect.TypeOf((*T)(nil)).Elem().String(),
			Actual:   fmt.Sprintf("%T", v),
		}
	}
	return typed, nil
}

// Close shuts down all built resources in reverse topological order.
func (c *Container) Close(ctx context.Context) error {
	return c.closeWithKeys(ctx, nil)
}

// CloseIDs shuts down selected built resources in reverse topological order.
func (c *Container) CloseIDs(ctx context.Context, ids []ID) error {
	if len(ids) == 0 {
		return nil
	}
	keys := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if err := validateID(id); err != nil {
			return err
		}
		keys[id.String()] = struct{}{}
	}
	return c.closeWithKeys(ctx, keys)
}

func (c *Container) closeWithKeys(ctx context.Context, keys map[string]struct{}) error {
	if ctx == nil {
		ctx = context.Background()
	}

	c.mu.RLock()
	built := make(map[string]any, len(c.instances))
	for k, v := range c.instances {
		if len(keys) > 0 {
			if _, selected := keys[k]; !selected {
				continue
			}
		}
		built[k] = v
	}
	c.mu.RUnlock()

	var errs []error
	closed := make([]string, 0, len(built))
	for i := len(c.topo) - 1; i >= 0; i-- {
		id := c.topo[i]
		key := id.String()
		instance, ok := built[key]
		if !ok {
			continue
		}

		node := c.nodes[key]
		if node == nil {
			continue
		}

		if node.def.closeFn != nil {
			if err := node.def.closeFn(ctx, instance); err != nil {
				errs = append(errs, fmt.Errorf("close resource %s: %w", key, err))
			}
			closed = append(closed, key)
			continue
		}
		if closer, ok := instance.(io.Closer); ok {
			if err := closer.Close(); err != nil {
				errs = append(errs, fmt.Errorf("close resource %s: %w", key, err))
			}
		}
		closed = append(closed, key)
	}

	c.mu.Lock()
	if len(keys) == 0 {
		c.instances = make(map[string]any, len(c.instances))
	} else {
		for _, key := range closed {
			delete(c.instances, key)
		}
	}
	c.mu.Unlock()

	if len(errs) == 0 {
		return nil
	}
	return errors.Join(errs...)
}

func (c *Container) compile(specs []NodeSpec) error {
	order := make([]ID, 0, len(specs))
	for _, spec := range specs {
		id := spec.ID()
		if err := validateID(id); err != nil {
			return fmt.Errorf("compile resource %s/%s: %w", spec.Kind, spec.Name, err)
		}
		if spec.Driver == "" {
			return fmt.Errorf("compile resource %s: driver is empty", id.String())
		}

		key := id.String()
		if _, exists := c.nodes[key]; exists {
			return DuplicateNodeError{ID: id}
		}

		def, ok := c.registry.get(spec.Kind, spec.Driver)
		if !ok {
			return DefinitionNotFoundError{
				Kind:   spec.Kind,
				Driver: spec.Driver,
			}
		}

		opt, err := def.decode(spec.Options)
		if err != nil {
			return fmt.Errorf("decode options for %s: %w", key, err)
		}
		deps, err := def.deps(opt)
		if err != nil {
			return fmt.Errorf("list deps for %s: %w", key, err)
		}
		for _, dep := range deps {
			if err := validateID(dep); err != nil {
				return fmt.Errorf("invalid dependency for %s: %w", key, err)
			}
		}

		c.nodes[key] = &compiledNode{
			id:     id,
			driver: spec.Driver,
			opt:    opt,
			deps:   append([]ID(nil), deps...),
			def:    def,
		}
		order = append(order, id)
	}

	edges := make([]GraphEdge, 0, len(specs))
	for _, id := range order {
		node := c.nodes[id.String()]
		for _, dep := range node.deps {
			if _, ok := c.nodes[dep.String()]; !ok {
				return DependencyNotFoundError{
					From: id,
					To:   dep,
				}
			}
			edges = append(edges, GraphEdge{From: id, To: dep})
		}
	}

	topo, err := c.topoSort(order)
	if err != nil {
		return err
	}
	c.topo = topo

	nodes := make([]GraphNode, 0, len(order))
	for _, id := range order {
		node := c.nodes[id.String()]
		nodes = append(nodes, GraphNode{
			ID:     id,
			Driver: node.driver,
		})
	}
	c.graph = Graph{
		Nodes:     nodes,
		Edges:     edges,
		TopoOrder: append([]ID(nil), topo...),
	}
	return nil
}

func (c *Container) topoSort(order []ID) ([]ID, error) {
	const (
		stateNew uint8 = iota
		stateVisiting
		stateDone
	)

	state := make(map[string]uint8, len(order))
	stack := make([]ID, 0, len(order))
	stackPos := make(map[string]int, len(order))
	topo := make([]ID, 0, len(order))

	var dfs func(id ID) error
	dfs = func(id ID) error {
		key := id.String()
		switch state[key] {
		case stateDone:
			return nil
		case stateVisiting:
			// This branch is usually caught in dependency traversal above; keep as a safety net.
			pos := stackPos[key]
			cycle := append([]ID(nil), stack[pos:]...)
			cycle = append(cycle, id)
			return CycleDetectedError{Path: cycle}
		}

		state[key] = stateVisiting
		stackPos[key] = len(stack)
		stack = append(stack, id)

		node := c.nodes[key]
		for _, dep := range node.deps {
			depKey := dep.String()
			if state[depKey] == stateVisiting {
				pos := stackPos[depKey]
				cycle := append([]ID(nil), stack[pos:]...)
				cycle = append(cycle, dep)
				return CycleDetectedError{Path: cycle}
			}
			if err := dfs(dep); err != nil {
				return err
			}
		}

		stack = stack[:len(stack)-1]
		delete(stackPos, key)
		state[key] = stateDone
		topo = append(topo, id)
		return nil
	}

	for _, id := range order {
		if state[id.String()] == stateDone {
			continue
		}
		if err := dfs(id); err != nil {
			return nil, err
		}
	}
	return topo, nil
}

func validateID(id ID) error {
	if id.Kind == "" {
		return fmt.Errorf("id.kind is empty")
	}
	if id.Name == "" {
		return fmt.Errorf("id.name is empty")
	}
	return nil
}

type resolveStackContextKey struct{}

func pushResolveStack(ctx context.Context, current ID) (context.Context, error) {
	stack, _ := ctx.Value(resolveStackContextKey{}).([]ID)
	for i := range stack {
		if stack[i] == current {
			cycle := append([]ID(nil), stack[i:]...)
			cycle = append(cycle, current)
			return nil, CycleDetectedError{Path: cycle}
		}
	}
	next := make([]ID, 0, len(stack)+1)
	next = append(next, stack...)
	next = append(next, current)
	return context.WithValue(ctx, resolveStackContextKey{}, next), nil
}
