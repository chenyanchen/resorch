# 07_kv_composition_matrix

Demonstrates composable KV dependency graphs with conditional `Deps` in `resorch`.

This example registers `userKV` and `articleKV` definitions where `Redis` and `Database` are optional.
`Memory` is an optional top-layer cache switch.

The built-in matrix covers 6 modes:

1. `mem-redis-db`
2. `mem-redis`
3. `mem-db`
4. `redis-db`
5. `redis`
6. `db`

## Run

From repository root:

```bash
cd examples
go run ./07_kv_composition_matrix
```

This prints each mode's direct dependencies from the compiled graph and verifies each mode with `Set/Get`.

## Infra-free graph preview

To only preview the dependency graph summary without runtime KV verification:

```bash
go run ./07_kv_composition_matrix -verify=false
```

## Prerequisites for full verification

```bash
docker compose -f ./docker-compose.yml up -d
```
