# resorch

Framework-agnostic declarative resource orchestration and lifecycle runtime for Go.
It manages multi-type, multi-instance resources with explicit dependencies.

## Why `resorch` Exists

`resorch` exists to turn repeated, high-risk runtime wiring work into a reusable infrastructure capability.

- Unify semantics across projects: dependency declaration, build, close, and reconcile follow one contract.
- Reduce systemic risk: graph validation, reverse-topological close, and rollback behavior are implemented once and hardened centrally.
- Improve change velocity: onboarding a new resource is usually definition registration + spec declaration, not rewriting another container framework.
- Enable control-plane evolution: config-driven editing, validation, DAG visualization, and hot apply become sustainable only with a shared runtime kernel.

## When `resorch` Is Probably Not Needed

- You only have one service with very stable dependencies.
- Dependency wiring is simple and rarely changes.
- You do not need runtime config apply/hot switch semantics.

## Value Signals

You can evaluate whether `resorch` is paying off by tracking:

- time to onboard a new resource kind
- cross-project reuse ratio of resource definitions
- incident rate caused by config/wiring changes
- hot-reload rollback success rate

## How It Works

1. Register resource definitions by `(kind, driver)` with `Definition[Opt, Out]`.
2. Declare instances with `NodeSpec` (`kind/name/driver/options`).
3. Build a `Container` to compile and validate the dependency graph before runtime.
4. Resolve resources lazily at runtime and close them in reverse-topological order.

## Features

- Resource definition registry by `(kind, driver)`
- Declarative instance specs via `NodeSpec`
- Startup validation: missing definition, duplicate nodes, missing dependencies, cycle detection
- Runtime lazy initialization, caching, and singleflight deduplication
- Reverse-topological shutdown lifecycle
- Graph export support (`DOT` and `Mermaid`)

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

## Installation

```bash
go get github.com/chenyanchen/resorch
```

## Examples

Examples are maintained in the dedicated `./examples` module.

For run instructions of each example, see: [examples/README.md](examples/README.md)

## Experimental: `exp/reload`

`exp/reload` provides experimental reconcile-based hot reload:

1. Build a next container from new specs
2. Reuse unchanged instances
3. Prewarm rebuilt instances
4. Atomically switch current container
5. Close expired instances from the old container

API under `exp/` is experimental and may change before `v1.0.0`.

## Project Story and Handover

For project origin, design decisions, and continuation guidance, see:

- `docs/ORIGIN_AND_HANDOVER.md`
