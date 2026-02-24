package resorch

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type testCloseRecorder struct {
	name  string
	order *[]string
	mu    *sync.Mutex
}

func (r *testCloseRecorder) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	*r.order = append(*r.order, r.name)
	return nil
}

func TestContainerResolveAndGraph(t *testing.T) {
	type dbOpt struct {
		DSN string `json:"dsn"`
	}
	type svcOpt struct {
		DB string `json:"db"`
	}
	type dbResource struct {
		dsn string
	}
	type svcResource struct {
		db *dbResource
	}

	reg := NewRegistry()
	var dbBuildCount int32
	var svcBuildCount int32

	require.NoError(t, Register(reg, "db", "mock", Definition[dbOpt, *dbResource]{
		Build: func(_ context.Context, _ Resolver, opt dbOpt) (*dbResource, error) {
			atomic.AddInt32(&dbBuildCount, 1)
			return &dbResource{dsn: opt.DSN}, nil
		},
	}))
	require.NoError(t, Register(reg, "svc", "v1", Definition[svcOpt, *svcResource]{
		Deps: func(opt svcOpt) ([]ID, error) {
			return []ID{{Kind: "db", Name: opt.DB}}, nil
		},
		Build: func(ctx context.Context, r Resolver, opt svcOpt) (*svcResource, error) {
			atomic.AddInt32(&svcBuildCount, 1)
			db, err := ResolveAs[*dbResource](ctx, r, ID{Kind: "db", Name: opt.DB})
			if err != nil {
				return nil, err
			}
			return &svcResource{db: db}, nil
		},
	}))

	specs := []NodeSpec{
		{Kind: "db", Name: "main", Driver: "mock", Options: mustRawJSON(t, dbOpt{DSN: "dsn://main"})},
		{Kind: "svc", Name: "api", Driver: "v1", Options: mustRawJSON(t, svcOpt{DB: "main"})},
	}
	c, err := NewContainer(reg, specs)
	require.NoError(t, err)

	graph := c.Graph()
	require.Len(t, graph.Nodes, 2)
	require.Len(t, graph.Edges, 1)
	assert.Equal(t, ID{Kind: "svc", Name: "api"}, graph.Edges[0].From)
	assert.Equal(t, ID{Kind: "db", Name: "main"}, graph.Edges[0].To)
	assert.Contains(t, graph.DOT(), "digraph resorch")
	assert.Contains(t, graph.Mermaid(), "graph TD")

	ctx := context.Background()
	svc1, err := ResolveAs[*svcResource](ctx, c, ID{Kind: "svc", Name: "api"})
	require.NoError(t, err)
	require.NotNil(t, svc1)
	assert.Equal(t, "dsn://main", svc1.db.dsn)

	svc2, err := ResolveAs[*svcResource](ctx, c, ID{Kind: "svc", Name: "api"})
	require.NoError(t, err)
	assert.True(t, svc1 == svc2, "resolved service instance should be cached singleton")
	assert.Equal(t, int32(1), atomic.LoadInt32(&svcBuildCount))
	assert.Equal(t, int32(1), atomic.LoadInt32(&dbBuildCount))
}

func TestContainerResolveSingleflight(t *testing.T) {
	type opt struct{}
	type singleton struct{}

	reg := NewRegistry()
	var buildCount int32
	require.NoError(t, Register(reg, "singleton", "mock", Definition[opt, *singleton]{
		Build: func(_ context.Context, _ Resolver, _ opt) (*singleton, error) {
			atomic.AddInt32(&buildCount, 1)
			time.Sleep(30 * time.Millisecond)
			return &singleton{}, nil
		},
	}))

	c, err := NewContainer(reg, []NodeSpec{
		{Kind: "singleton", Name: "s1", Driver: "mock"},
	})
	require.NoError(t, err)

	const n = 32
	var wg sync.WaitGroup
	wg.Add(n)
	results := make([]*singleton, n)
	errCh := make(chan error, n)
	for i := 0; i < n; i++ {
		i := i
		go func() {
			defer wg.Done()
			v, e := ResolveAs[*singleton](context.Background(), c, ID{Kind: "singleton", Name: "s1"})
			if e != nil {
				errCh <- e
				return
			}
			results[i] = v
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		require.NoError(t, err)
	}

	assert.Equal(t, int32(1), atomic.LoadInt32(&buildCount))
	first := results[0]
	for i := 1; i < n; i++ {
		assert.True(t, first == results[i], "all resolves should share one instance")
	}
}

func TestContainerCloseReverseTopo(t *testing.T) {
	type opt struct {
		Name string `json:"name"`
		Dep  string `json:"dep"`
	}

	order := make([]string, 0, 2)
	var mu sync.Mutex

	reg := NewRegistry()
	require.NoError(t, Register(reg, "node", "dep", Definition[opt, *testCloseRecorder]{
		Deps: func(o opt) ([]ID, error) {
			if o.Dep == "" {
				return nil, nil
			}
			return []ID{{Kind: "node", Name: o.Dep}}, nil
		},
		Build: func(ctx context.Context, r Resolver, o opt) (*testCloseRecorder, error) {
			if o.Dep != "" {
				_, err := ResolveAs[*testCloseRecorder](ctx, r, ID{Kind: "node", Name: o.Dep})
				if err != nil {
					return nil, err
				}
			}
			return &testCloseRecorder{name: o.Name, order: &order, mu: &mu}, nil
		},
	}))

	c, err := NewContainer(reg, []NodeSpec{
		{Kind: "node", Name: "a", Driver: "dep", Options: mustRawJSON(t, opt{Name: "a"})},
		{Kind: "node", Name: "b", Driver: "dep", Options: mustRawJSON(t, opt{Name: "b", Dep: "a"})},
	})
	require.NoError(t, err)

	_, err = c.Resolve(context.Background(), ID{Kind: "node", Name: "b"})
	require.NoError(t, err)

	require.NoError(t, c.Close(context.Background()))
	assert.Equal(t, []string{"b", "a"}, order)
}

func TestContainerValidationErrors(t *testing.T) {
	type depOpt struct {
		Dep string `json:"dep"`
	}

	reg := NewRegistry()
	require.NoError(t, Register(reg, "node", "dep", Definition[depOpt, struct{}]{
		Deps: func(opt depOpt) ([]ID, error) {
			if opt.Dep == "" {
				return nil, nil
			}
			return []ID{{Kind: "node", Name: opt.Dep}}, nil
		},
		Build: func(_ context.Context, _ Resolver, _ depOpt) (struct{}, error) {
			return struct{}{}, nil
		},
	}))

	_, err := NewContainer(reg, []NodeSpec{
		{Kind: "node", Name: "a", Driver: "missing"},
	})
	require.Error(t, err)
	var defErr DefinitionNotFoundError
	assert.True(t, errors.As(err, &defErr))

	_, err = NewContainer(reg, []NodeSpec{
		{Kind: "node", Name: "a", Driver: "dep", Options: mustRawJSON(t, depOpt{Dep: "b"})},
	})
	require.Error(t, err)
	var missingErr DependencyNotFoundError
	assert.True(t, errors.As(err, &missingErr))

	_, err = NewContainer(reg, []NodeSpec{
		{Kind: "node", Name: "a", Driver: "dep", Options: mustRawJSON(t, depOpt{Dep: "b"})},
		{Kind: "node", Name: "b", Driver: "dep", Options: mustRawJSON(t, depOpt{Dep: "a"})},
	})
	require.Error(t, err)
	var cycleErr CycleDetectedError
	assert.True(t, errors.As(err, &cycleErr))
	assert.GreaterOrEqual(t, len(cycleErr.Path), 2)
}

func TestResolveAsTypeMismatch(t *testing.T) {
	type opt struct{}
	reg := NewRegistry()
	require.NoError(t, Register(reg, "value", "str", Definition[opt, string]{
		Build: func(_ context.Context, _ Resolver, _ opt) (string, error) {
			return "ok", nil
		},
	}))
	c, err := NewContainer(reg, []NodeSpec{
		{Kind: "value", Name: "v1", Driver: "str"},
	})
	require.NoError(t, err)

	_, err = ResolveAs[int](context.Background(), c, ID{Kind: "value", Name: "v1"})
	require.Error(t, err)
	var typeErr TypeMismatchError
	assert.True(t, errors.As(err, &typeErr))
}

func mustRawJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(v)
	require.NoError(t, err)
	return data
}
