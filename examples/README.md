# examples

Each example is an independently runnable `main` package under the `examples` Go module.

## Usage

From repository root:

```bash
cd examples
```

## Core examples (no external services)

```bash
go run ./basic_dependency
go run ./multiple_instances
go run ./close_order
go run ./graph_export
go run ./hot_reload_http
```

## Real-world flavored examples (require local Docker services)

Start local dependencies:

```bash
docker compose -f ./docker-compose.yml up -d
```

Run:

```bash
go run ./http_user_article_kv
go run ./production_like_sanitized -config ./production_like_sanitized/config.sanitized.yaml
```

Stop local dependencies:

```bash
docker compose -f ./docker-compose.yml down
```
