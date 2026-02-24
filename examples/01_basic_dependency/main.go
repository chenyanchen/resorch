package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/chenyanchen/resorch"
)

type redisOpt struct {
	Addr string `json:"addr"`
}

type cacheOpt struct {
	Redis string `json:"redis"`
}

type redisClient struct {
	Addr string
}

type cacheClient struct {
	Redis *redisClient
}

func main() {
	reg := resorch.NewRegistry()
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
			redis, err := resorch.ResolveAs[*redisClient](ctx, r, resorch.ID{
				Kind: "redis",
				Name: opt.Redis,
			})
			if err != nil {
				return nil, err
			}
			return &cacheClient{Redis: redis}, nil
		},
	})

	container, err := resorch.NewContainer(reg, []resorch.NodeSpec{
		{Kind: "redis", Name: "main", Driver: "mock", Options: rawJSON(redisOpt{Addr: "127.0.0.1:6379"})},
		{Kind: "cache", Name: "creative", Driver: "with-redis", Options: rawJSON(cacheOpt{Redis: "main"})},
	})
	must(err)
	defer container.Close(context.Background())

	cache, err := resorch.ResolveAs[*cacheClient](context.Background(), container, resorch.ID{
		Kind: "cache",
		Name: "creative",
	})
	must(err)
	fmt.Println("cache redis addr:", cache.Redis.Addr)
}

func rawJSON(v any) json.RawMessage {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return data
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
