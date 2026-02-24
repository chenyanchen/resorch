package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/chenyanchen/resorch"
	"gopkg.in/yaml.v3"
)

type resourceEntry[T any] struct {
	Name    string `yaml:"name"`
	Driver  string `yaml:"driver"`
	Options T      `yaml:"options"`
}

type configFile struct {
	Redis []resourceEntry[redisOpt] `yaml:"redis"`
	Cache []resourceEntry[cacheOpt] `yaml:"cache"`
}

type redisOpt struct {
	Addr string `json:"addr" yaml:"addr"`
}

type cacheOpt struct {
	Redis string `json:"redis" yaml:"redis"`
}

type redisClient struct {
	Addr string
}

type cacheClient struct {
	Redis *redisClient
}

func main() {
	configPath := flag.String("config", "./05_yaml_to_specs/config.yaml", "path to yaml config")
	flag.Parse()

	cfg, err := loadConfig(*configPath)
	must(err)

	specs, err := buildNodeSpecs(cfg)
	must(err)

	reg := resorch.NewRegistry()
	registerDefinitions(reg)
	container, err := resorch.NewContainer(reg, specs)
	must(err)
	defer container.Close(context.Background())

	cache, err := resorch.ResolveAs[*cacheClient](context.Background(), container, resorch.ID{Kind: "cache", Name: "creative"})
	must(err)

	graph := container.Graph()
	fmt.Printf("loaded %d nodes and %d edges from YAML\n", len(graph.Nodes), len(graph.Edges))
	fmt.Println("cache redis addr:", cache.Redis.Addr)
}

func registerDefinitions(reg *resorch.Registry) {
	resorch.MustRegister(reg, "redis", "mock", resorch.Definition[redisOpt, *redisClient]{
		Build: func(_ context.Context, _ resorch.Resolver, opt redisOpt) (*redisClient, error) {
			return &redisClient{Addr: opt.Addr}, nil
		},
	})

	resorch.MustRegister(reg, "cache", "with-redis", resorch.Definition[cacheOpt, *cacheClient]{
		Deps: func(opt cacheOpt) ([]resorch.ID, error) {
			return []resorch.ID{{Kind: "redis", Name: opt.Redis}}, nil
		},
		Build: func(ctx context.Context, r resorch.Resolver, opt cacheOpt) (*cacheClient, error) {
			redis, err := resorch.ResolveAs[*redisClient](ctx, r, resorch.ID{Kind: "redis", Name: opt.Redis})
			if err != nil {
				return nil, err
			}
			return &cacheClient{Redis: redis}, nil
		},
	})
}

func loadConfig(path string) (configFile, error) {
	payload, err := os.ReadFile(path)
	if err != nil {
		return configFile{}, err
	}

	var cfg configFile
	if err := yaml.Unmarshal(payload, &cfg); err != nil {
		return configFile{}, err
	}
	return cfg, nil
}

func buildNodeSpecs(cfg configFile) ([]resorch.NodeSpec, error) {
	specs := make([]resorch.NodeSpec, 0, len(cfg.Redis)+len(cfg.Cache))
	if err := appendSpecs(&specs, "redis", cfg.Redis); err != nil {
		return nil, err
	}
	if err := appendSpecs(&specs, "cache", cfg.Cache); err != nil {
		return nil, err
	}
	return specs, nil
}

func appendSpecs[T any](specs *[]resorch.NodeSpec, kind string, entries []resourceEntry[T]) error {
	for _, entry := range entries {
		raw, err := json.Marshal(entry.Options)
		if err != nil {
			return fmt.Errorf("marshal options for %s/%s: %w", kind, entry.Name, err)
		}
		*specs = append(*specs, resorch.NodeSpec{
			Kind:    kind,
			Name:    entry.Name,
			Driver:  entry.Driver,
			Options: raw,
		})
	}
	return nil
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
