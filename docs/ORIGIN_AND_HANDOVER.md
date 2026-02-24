# resorch: Origin and Handover Guide

This document records how `resorch` was born, why key decisions were made, and how to continue the project safely in future sessions.

## 1. Project Snapshot

- Repository: `github.com/chenyanchen/resorch`
- Module: `github.com/chenyanchen/resorch`
- Language baseline: Go `1.26.0`
- Core package: `resorch` (resource orchestration runtime)
- Experimental package: `exp/reload` (hot reload reconciler)

Primary goals:

1. Declarative resource management
2. Dependency DAG compilation and validation
3. Runtime lifecycle management (build/cache/close)
4. Incremental hot reload (currently experimental)
5. Multi-project reuse (not tied to one business service)

## 2. Why This Project Exists

The original implementation lived inside another business repository and worked well for:

- many dependency types,
- multiple instances per type,
- declarative config-driven wiring.

However, equivalent logic had been manually re-implemented in multiple projects. The goal of `resorch` is to extract that shared kernel into an independent, reusable package.

## 3. Naming Decision

Final repository name: `resorch`

Reasoning:

- Emphasizes **resource orchestration** instead of only DI.
- Future-proof for lifecycle/reconcile/hot-reload semantics.
- Does not overfit to one graph term (for example, hard-coding “dag” or “topo” in the name).

## 4. Timeline (Condensed)

All dates below refer to 2026-02-24 unless noted.

1. Extracted core resource runtime from prior implementation.
2. Created new repository `chenyanchen/resorch`.
3. Migrated in two phases:
   - Phase 1: stable core (`resorch` root package)
   - Phase 2: `exp/reload` marked experimental
4. Converted public comments/docs/examples to English.
5. Added CI and branch governance.
6. Added real-world runnable examples backed by local Docker infra.

## 5. Core Architecture

## 5.1 Registry + Definition

`(kind, driver)` maps to a typed `Definition[Opt, Out]`.

A definition can provide:

- `Decode` (optional)
- `Deps` (optional)
- `Build` (required)
- `Close` (optional)

## 5.2 NodeSpec-Driven Runtime

Instances are declared with `NodeSpec`:

- `kind`
- `name`
- `driver`
- `options`

The container compiles all specs into:

- dependency graph,
- topological order,
- compiled nodes.

## 5.3 Runtime Behavior

- Lazy initialization on `Resolve`
- Singleflight deduplication for concurrent resolves
- In-memory instance cache
- Reverse-topological shutdown (`Close`, `CloseIDs`)

## 5.4 Graph Export

Graph can be exported as:

- DOT
- Mermaid

This enables future visualization tooling while keeping rendering out of core runtime.

## 6. Experimental Hot Reload (`exp/reload`)

`Reconciler` handles incremental state transitions:

1. Build next container from new specs
2. Build snapshots and compute diff
3. Reuse unchanged instances
4. Prewarm rebuilt nodes
5. Atomically switch current
6. Close old expired nodes in reverse-topological order

`exp/reload` is intentionally unstable before `v1`.

## 7. CI and Governance Decisions

## 7.1 Single Go Workflow

All Go checks are consolidated in:

- `.github/workflows/go.yml`

Jobs include:

- build + test + vet
- race test
- golangci-lint
- examples build + runnable smoke

## 7.2 Lint Strategy

`golangci-lint` migrated to v2 config and action upgraded to latest major.

- Config file: `.golangci.yml`
- Action: `golangci/golangci-lint-action@v9`
- Binary version: `latest`

The config uses explicit enabled linters and avoids accidental drift from implicit defaults.

## 7.3 Branch Protection

`main` is protected with:

- required status checks
- linear history
- conversation resolution
- no force push/delete
- admin enforcement
- merges via PR

`delete_branch_on_merge=true` is enabled.

## 8. Example Strategy

Examples live inside `./examples` as an independent Go sub-module.

Decision: **separate examples from the root module**.

Why:

1. Keep root `go.mod` focused on core runtime dependencies.
2. Allow real-world examples to use heavier third-party packages without polluting library consumers.
3. Keep examples runnable and version-aligned via local `replace` from examples module to root module.

Current shape:

- Root module: `github.com/chenyanchen/resorch`
- Examples module: `github.com/chenyanchen/resorch/examples`

## 9. New Runnable Real-World Examples Added

## 9.1 `examples/08_http_user_article_kv`

A real HTTP service without hot reload.

- Endpoint: `/v1/summary`
- Uses two domains: `user` and `article`
- Uses `github.com/chenyanchen/kv` for 3-layer KV composition:
  1. in-memory cache
  2. redis
  3. postgres (pgx)

## 9.2 `examples/09_production_like_sanitized`

A production-like sanitized setup inspired by a complex real pipeline config.

- Keeps `logic -> stage -> pipeline -> api` shape
- All service dependencies are local or hard-coded mocks
- Endpoint simplified to `/v1/recommend`
- External online endpoints and sensitive values removed

## 9.3 Shared Local Infra

- `examples/docker-compose.yml`
- Services:
  - Postgres 16
  - Redis (profile/user cache)
  - Redis (article cache)

## 10. Local Runbook

Start infra:

```bash
cd examples
docker compose -f ./docker-compose.yml up -d
```

Run example 1:

```bash
go run ./08_http_user_article_kv
curl "http://127.0.0.1:18080/v1/summary?user_id=1&article_id=101"
```

Run example 2:

```bash
go run ./09_production_like_sanitized -config ./09_production_like_sanitized/config.sanitized.yaml
curl "http://127.0.0.1:18081/v1/recommend?user_id=1"
```

Stop infra:

```bash
docker compose -f ./docker-compose.yml down
```

## 11. What To Do Next (Recommended)

1. Add a control-plane demo for config edit + validate + apply (`Reconcile`) workflows.
2. Add a dependency metadata protocol (for UI drag/edit support) instead of relying on implicit `Deps` parsing.
3. Add benchmark and profiling suites for large graphs.
4. Add compatibility tests for hot-reload rollback semantics under failure injection.
5. Define graduation criteria from `exp/reload` to stable package.

## 12. Non-Goals (Current)

- Built-in visualization UI in core repository.
- Tight coupling with any DI or web framework.
- Premature API freeze for experimental reload features.

## 13. Session Handover Notes

If you start a new session and want fast context transfer, provide this document and ask the agent to:

1. read `README.md` and this file,
2. inspect `exp/reload` API boundary,
3. inspect `examples/08_http_user_article_kv` and `examples/09_production_like_sanitized`,
4. keep `main` branch protection constraints in mind (PR-based merge).

That is enough context to continue work without replaying old conversation history.
