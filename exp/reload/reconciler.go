package reload

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/chenyanchen/resorch"
)

type snapshotNode struct {
	id   resorch.ID
	hash string
	deps []resorch.ID
}

// Result describes the resource changes of one reconciliation.
type Result struct {
	Added     []resorch.ID // Node exists only in new specs.
	Removed   []resorch.ID // Node exists only in old specs.
	Reused    []resorch.ID // Node is reused (unchanged and deps not rebuilt).
	Rebuilt   []resorch.ID // Node is rebuilt (added/self changed/dependency changed).
	ClosedOld []resorch.ID // Old instances closed after switch (removed + rebuilt in old graph).
}

// Reconciler keeps the active container and applies incremental switches on new specs.
//
// Semantics:
// 1. build next container from new specs
// 2. inject reusable instances into next
// 3. prewarm rebuilt nodes before switching
// 4. atomically swap current
// 5. close expired nodes from old in reverse-topological order
type Reconciler struct {
	registry *resorch.Registry

	mu       sync.RWMutex
	current  *resorch.Container
	snapshot map[string]snapshotNode
}

func New(registry *resorch.Registry, initial []resorch.NodeSpec) (*Reconciler, error) {
	if registry == nil {
		return nil, fmt.Errorf("new reconciler: registry is nil")
	}
	r := &Reconciler{
		registry: registry,
	}
	if len(initial) == 0 {
		return r, nil
	}
	if _, err := r.Reconcile(context.Background(), initial); err != nil {
		return nil, err
	}
	return r, nil
}

// Current returns the active container snapshot.
func (r *Reconciler) Current() *resorch.Container {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.current
}

// Reconcile switches reconciler state to new specs.
func (r *Reconciler) Reconcile(ctx context.Context, specs []resorch.NodeSpec) (Result, error) {
	next, err := resorch.NewContainer(r.registry, specs)
	if err != nil {
		return Result{}, fmt.Errorf("build next container: %w", err)
	}
	nextSnapshot, err := buildSnapshot(specs, next.Graph())
	if err != nil {
		return Result{}, err
	}

	r.mu.Lock()
	if r.current == nil {
		r.current = next
		r.snapshot = nextSnapshot
		r.mu.Unlock()
		order := next.TopoOrder()
		return Result{
			Added:   append([]resorch.ID(nil), order...),
			Rebuilt: append([]resorch.ID(nil), order...),
		}, nil
	}

	old := r.current
	oldSnapshot := cloneSnapshot(r.snapshot)
	diff := diffSnapshots(oldSnapshot, nextSnapshot, old.TopoOrder(), next.TopoOrder())

	for _, id := range diff.Reused {
		instance, ok := old.Resolved(id)
		if !ok {
			continue
		}
		if err := next.InjectResolved(id, instance); err != nil {
			r.mu.Unlock()
			return Result{}, fmt.Errorf("inject reused instance %s: %w", id.String(), err)
		}
	}

	if err := next.ResolveIDs(ctx, diff.Rebuilt); err != nil {
		r.mu.Unlock()
		_ = next.CloseIDs(context.Background(), diff.Rebuilt)
		return Result{}, fmt.Errorf("prewarm rebuilt resources: %w", err)
	}

	r.current = next
	r.snapshot = nextSnapshot
	r.mu.Unlock()

	closeErr := old.CloseIDs(ctx, diff.ClosedOld)
	if closeErr != nil {
		return diff, fmt.Errorf("switch success but close old failed: %w", closeErr)
	}
	return diff, nil
}

func buildSnapshot(specs []resorch.NodeSpec, graph resorch.Graph) (map[string]snapshotNode, error) {
	specByKey := make(map[string]resorch.NodeSpec, len(specs))
	for _, spec := range specs {
		id := spec.ID()
		specByKey[id.String()] = spec
	}
	depsByKey := make(map[string][]resorch.ID, len(graph.Nodes))
	for _, edge := range graph.Edges {
		key := edge.From.String()
		depsByKey[key] = append(depsByKey[key], edge.To)
	}

	out := make(map[string]snapshotNode, len(graph.Nodes))
	for _, node := range graph.Nodes {
		key := node.ID.String()
		spec, ok := specByKey[key]
		if !ok {
			return nil, fmt.Errorf("build snapshot: missing spec for %s", key)
		}
		deps := append([]resorch.ID(nil), depsByKey[key]...)
		sort.Slice(deps, func(i, j int) bool {
			return deps[i].String() < deps[j].String()
		})
		hash, err := hashSpec(spec, deps)
		if err != nil {
			return nil, fmt.Errorf("build snapshot hash for %s: %w", key, err)
		}
		out[key] = snapshotNode{
			id:   node.ID,
			hash: hash,
			deps: deps,
		}
	}
	return out, nil
}

func hashSpec(spec resorch.NodeSpec, deps []resorch.ID) (string, error) {
	options, err := normalizeJSON(spec.Options)
	if err != nil {
		return "", err
	}

	var b strings.Builder
	b.WriteString(spec.Kind)
	b.WriteByte('\n')
	b.WriteString(spec.Name)
	b.WriteByte('\n')
	b.WriteString(spec.Driver)
	b.WriteByte('\n')
	b.Write(options)
	b.WriteByte('\n')
	for _, dep := range deps {
		b.WriteString(dep.String())
		b.WriteByte('\n')
	}

	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:]), nil
}

func normalizeJSON(raw json.RawMessage) ([]byte, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return []byte("null"), nil
	}
	var v any
	if err := json.Unmarshal(trimmed, &v); err != nil {
		// Options are not required to be JSON; fall back to raw bytes on decode failure.
		return trimmed, nil
	}
	normalized, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return normalized, nil
}

func cloneSnapshot(in map[string]snapshotNode) map[string]snapshotNode {
	out := make(map[string]snapshotNode, len(in))
	for k, v := range in {
		cp := snapshotNode{
			id:   v.id,
			hash: v.hash,
			deps: append([]resorch.ID(nil), v.deps...),
		}
		out[k] = cp
	}
	return out
}

func diffSnapshots(
	oldSnap map[string]snapshotNode,
	newSnap map[string]snapshotNode,
	oldTopo []resorch.ID,
	newTopo []resorch.ID,
) Result {
	addedSet := make(map[string]struct{})
	removedSet := make(map[string]struct{})
	changedSet := make(map[string]struct{})

	for key, newNode := range newSnap {
		oldNode, ok := oldSnap[key]
		if !ok {
			addedSet[key] = struct{}{}
			continue
		}
		if newNode.hash != oldNode.hash {
			changedSet[key] = struct{}{}
		}
	}
	for key := range oldSnap {
		if _, ok := newSnap[key]; !ok {
			removedSet[key] = struct{}{}
		}
	}

	// Propagate dependency changes upward: if a dependency is rebuilt, rebuild this node too.
	rebuildSet := make(map[string]struct{}, len(newSnap))
	for key := range addedSet {
		rebuildSet[key] = struct{}{}
	}
	for key := range changedSet {
		rebuildSet[key] = struct{}{}
	}
	for _, id := range newTopo {
		key := id.String()
		if _, already := rebuildSet[key]; already {
			continue
		}
		node := newSnap[key]
		for _, dep := range node.deps {
			if _, changed := rebuildSet[dep.String()]; changed {
				rebuildSet[key] = struct{}{}
				break
			}
		}
	}

	result := Result{
		Added:     make([]resorch.ID, 0, len(addedSet)),
		Removed:   make([]resorch.ID, 0, len(removedSet)),
		Reused:    make([]resorch.ID, 0, len(newSnap)),
		Rebuilt:   make([]resorch.ID, 0, len(rebuildSet)),
		ClosedOld: make([]resorch.ID, 0, len(removedSet)+len(rebuildSet)),
	}

	for _, id := range newTopo {
		key := id.String()
		if _, ok := addedSet[key]; ok {
			result.Added = append(result.Added, id)
		}
		if _, ok := rebuildSet[key]; ok {
			result.Rebuilt = append(result.Rebuilt, id)
		} else {
			result.Reused = append(result.Reused, id)
		}
	}

	for _, id := range oldTopo {
		key := id.String()
		if _, ok := removedSet[key]; ok {
			result.Removed = append(result.Removed, id)
			result.ClosedOld = append(result.ClosedOld, id)
			continue
		}
		if _, existsInNew := newSnap[key]; !existsInNew {
			continue
		}
		if _, rebuild := rebuildSet[key]; rebuild {
			result.ClosedOld = append(result.ClosedOld, id)
		}
	}

	return result
}
