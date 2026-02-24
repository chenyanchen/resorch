package reload

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"

	"github.com/chenyanchen/resorch"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type leafOpt struct {
	Value string `json:"value"`
}

type parentOpt struct {
	Leaf string `json:"leaf"`
}

type leafRes struct {
	Value string
}

type parentRes struct {
	Leaf *leafRes
}

func TestReconcile_ReuseAndRebuild(t *testing.T) {
	reg := resorch.NewRegistry()

	var leafBuildCount int32
	var parentBuildCount int32
	var leafCloseCount int32
	var parentCloseCount int32

	resorch.MustRegister(reg, "leaf", "mock", resorch.Definition[leafOpt, *leafRes]{
		Build: func(_ context.Context, _ resorch.Resolver, opt leafOpt) (*leafRes, error) {
			atomic.AddInt32(&leafBuildCount, 1)
			return &leafRes{Value: opt.Value}, nil
		},
		Close: func(_ context.Context, _ *leafRes) error {
			atomic.AddInt32(&leafCloseCount, 1)
			return nil
		},
	})
	resorch.MustRegister(reg, "parent", "mock", resorch.Definition[parentOpt, *parentRes]{
		Deps: func(opt parentOpt) ([]resorch.ID, error) {
			return []resorch.ID{{Kind: "leaf", Name: opt.Leaf}}, nil
		},
		Build: func(ctx context.Context, r resorch.Resolver, opt parentOpt) (*parentRes, error) {
			atomic.AddInt32(&parentBuildCount, 1)
			leaf, err := resorch.ResolveAs[*leafRes](ctx, r, resorch.ID{Kind: "leaf", Name: opt.Leaf})
			if err != nil {
				return nil, err
			}
			return &parentRes{Leaf: leaf}, nil
		},
		Close: func(_ context.Context, _ *parentRes) error {
			atomic.AddInt32(&parentCloseCount, 1)
			return nil
		},
	})

	initial := []resorch.NodeSpec{
		{Kind: "leaf", Name: "main", Driver: "mock", Options: rawJSON(t, leafOpt{Value: "v1"})},
		{Kind: "parent", Name: "svc", Driver: "mock", Options: rawJSON(t, parentOpt{Leaf: "main"})},
	}

	reconciler, err := New(reg, initial)
	require.NoError(t, err)

	current := reconciler.Current()
	require.NotNil(t, current)
	_, err = current.Resolve(context.Background(), resorch.ID{Kind: "parent", Name: "svc"})
	require.NoError(t, err)

	assert.Equal(t, int32(1), atomic.LoadInt32(&leafBuildCount))
	assert.Equal(t, int32(1), atomic.LoadInt32(&parentBuildCount))

	// Same config update: all nodes should be reused.
	sameResult, err := reconciler.Reconcile(context.Background(), initial)
	require.NoError(t, err)
	assert.Len(t, sameResult.Rebuilt, 0)
	assert.Len(t, sameResult.Reused, 2)
	assert.Equal(t, int32(1), atomic.LoadInt32(&leafBuildCount))
	assert.Equal(t, int32(1), atomic.LoadInt32(&parentBuildCount))

	// Leaf change should rebuild leaf and all dependents.
	updated := []resorch.NodeSpec{
		{Kind: "leaf", Name: "main", Driver: "mock", Options: rawJSON(t, leafOpt{Value: "v2"})},
		{Kind: "parent", Name: "svc", Driver: "mock", Options: rawJSON(t, parentOpt{Leaf: "main"})},
	}
	updateResult, err := reconciler.Reconcile(context.Background(), updated)
	require.NoError(t, err)
	assert.Len(t, updateResult.Rebuilt, 2)
	assert.Len(t, updateResult.Reused, 0)
	assert.Equal(t, int32(2), atomic.LoadInt32(&leafBuildCount))
	assert.Equal(t, int32(2), atomic.LoadInt32(&parentBuildCount))
	assert.Equal(t, int32(1), atomic.LoadInt32(&leafCloseCount))
	assert.Equal(t, int32(1), atomic.LoadInt32(&parentCloseCount))

	parent, err := resorch.ResolveAs[*parentRes](context.Background(), reconciler.Current(), resorch.ID{
		Kind: "parent",
		Name: "svc",
	})
	require.NoError(t, err)
	assert.Equal(t, "v2", parent.Leaf.Value)
}

func TestReconcile_RollbackOnPrewarmFailure(t *testing.T) {
	reg := resorch.NewRegistry()

	var leafBuildCount int32
	var leafCloseCount int32

	resorch.MustRegister(reg, "leaf", "mock", resorch.Definition[leafOpt, *leafRes]{
		Build: func(_ context.Context, _ resorch.Resolver, opt leafOpt) (*leafRes, error) {
			if opt.Value == "bad" {
				return nil, assert.AnError
			}
			atomic.AddInt32(&leafBuildCount, 1)
			return &leafRes{Value: opt.Value}, nil
		},
		Close: func(_ context.Context, _ *leafRes) error {
			atomic.AddInt32(&leafCloseCount, 1)
			return nil
		},
	})

	initial := []resorch.NodeSpec{
		{Kind: "leaf", Name: "main", Driver: "mock", Options: rawJSON(t, leafOpt{Value: "v1"})},
	}
	reconciler, err := New(reg, initial)
	require.NoError(t, err)

	oldContainer := reconciler.Current()
	_, err = oldContainer.Resolve(context.Background(), resorch.ID{Kind: "leaf", Name: "main"})
	require.NoError(t, err)
	assert.Equal(t, int32(1), atomic.LoadInt32(&leafBuildCount))

	_, err = reconciler.Reconcile(context.Background(), []resorch.NodeSpec{
		{Kind: "leaf", Name: "main", Driver: "mock", Options: rawJSON(t, leafOpt{Value: "bad"})},
	})
	require.Error(t, err)

	// Failed prewarm must roll back: current remains old, old instances stay alive.
	assert.Same(t, oldContainer, reconciler.Current())
	assert.Equal(t, int32(1), atomic.LoadInt32(&leafBuildCount))
	assert.Equal(t, int32(0), atomic.LoadInt32(&leafCloseCount))
}

func rawJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(v)
	require.NoError(t, err)
	return data
}
