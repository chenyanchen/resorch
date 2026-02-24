# 05_yaml_to_specs

A minimal example focused on one thing: load a YAML config file and convert it into `[]resorch.NodeSpec`.

It keeps the runtime tiny (`redis -> cache`) so you can focus on the config-to-spec pipeline used in larger setups.

## Run

```bash
cd examples
go run ./05_yaml_to_specs
```

With custom config path:

```bash
go run ./05_yaml_to_specs -config ./05_yaml_to_specs/config.yaml
```
