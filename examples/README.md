# examples

Each example is an independently runnable `main` package under the `examples` Go module.

## Usage

From repository root:

```bash
cd examples
```

## Learning Path (Simple to Complex)

1. `01_basic_dependency`: one direct dependency (`cache -> redis`).
2. `02_multiple_instances`: multiple instances under the same kind.
3. `03_close_order`: reverse-topological shutdown behavior.
4. `04_graph_export`: inspect graph and export DOT/Mermaid.
5. `05_yaml_to_specs`: parse YAML config into `[]resorch.NodeSpec`.
6. `06_hot_reload_http`: experimental hot reload flow.
7. `07_kv_composition_matrix`: optional dependency composition matrix.
8. `08_http_user_article_kv`: realistic HTTP service with layered KV.
9. `09_production_like_sanitized`: production-shaped pipeline wiring from sanitized YAML.

## Core Examples (No External Services)

```bash
go run ./01_basic_dependency
go run ./02_multiple_instances
go run ./03_close_order
go run ./04_graph_export
go run ./05_yaml_to_specs
go run ./06_hot_reload_http
go run ./07_kv_composition_matrix -verify=false
```

## Graph Export Outputs

`go run ./04_graph_export` writes:

- `graph.dot` (Graphviz DOT)
- `graph.mmd` (Mermaid)
- `graph.svg` (when Graphviz `dot` is available)

If Graphviz is not installed, render manually after installation:

```bash
dot -Tsvg graph.dot -o graph.svg
```

## Real-world Flavored Examples (Require Local Docker Services)

Start local dependencies:

```bash
docker compose -f ./docker-compose.yml up -d
```

Run:

```bash
go run ./07_kv_composition_matrix
go run ./08_http_user_article_kv
go run ./09_production_like_sanitized -config ./09_production_like_sanitized/config.sanitized.yaml
```

Details:

- `07_kv_composition_matrix`: [07_kv_composition_matrix/README.md](07_kv_composition_matrix/README.md)
- `05_yaml_to_specs`: [05_yaml_to_specs/README.md](05_yaml_to_specs/README.md)
- `08_http_user_article_kv`: [08_http_user_article_kv/README.md](08_http_user_article_kv/README.md)
- `09_production_like_sanitized`: [09_production_like_sanitized/README.md](09_production_like_sanitized/README.md)

Stop local dependencies:

```bash
docker compose -f ./docker-compose.yml down
```
