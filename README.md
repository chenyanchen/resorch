# resorch

Declarative resource orchestration and lifecycle runtime for Go.

`resorch` is a framework-agnostic core for managing multi-type, multi-instance resources with explicit dependencies.

## Features

- Resource definition registry by `(kind, driver)`
- Declarative instance specs via `NodeSpec`
- Startup validation: missing definition, duplicate nodes, missing dependencies, cycle detection
- Runtime lazy initialization, caching, and singleflight deduplication
- Reverse-topological shutdown lifecycle
- Graph export support (`DOT` and `Mermaid`)

## Installation

```bash
go get github.com/chenyanchen/resorch
```

## Quick Example

```go
reg := resorch.NewRegistry()

resorch.MustRegister(reg, "redis", "mock", resorch.Definition[RedisOpt, *RedisClient]{
	Build: func(_ context.Context, _ resorch.Resolver, opt RedisOpt) (*RedisClient, error) {
		return NewRedisClient(opt.Addr), nil
	},
})

resorch.MustRegister(reg, "cache", "with-redis", resorch.Definition[CacheOpt, *Cache]{
	Deps: func(opt CacheOpt) ([]resorch.ID, error) {
		return []resorch.ID{{Kind: "redis", Name: opt.Redis}}, nil
	},
	Build: func(ctx context.Context, r resorch.Resolver, opt CacheOpt) (*Cache, error) {
		cli, err := resorch.ResolveAs[*RedisClient](ctx, r, resorch.ID{Kind: "redis", Name: opt.Redis})
		if err != nil {
			return nil, err
		}
		return NewCache(cli), nil
	},
})

container, err := resorch.NewContainer(reg, []resorch.NodeSpec{
	{Kind: "redis", Name: "main", Driver: "mock", Options: []byte(`{"addr":"127.0.0.1:6379"}`)},
	{Kind: "cache", Name: "creative", Driver: "with-redis", Options: []byte(`{"redis":"main"}`)},
})
if err != nil {
	panic(err)
}
defer container.Close(context.Background())
```

## Examples

- `go run ./examples/basic_dependency`
- `go run ./examples/multiple_instances`
- `go run ./examples/close_order`
- `go run ./examples/graph_export`
