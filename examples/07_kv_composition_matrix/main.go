package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"sort"
	"time"

	kvpkg "github.com/chenyanchen/kv"
	"github.com/chenyanchen/kv/cachekv"
	"github.com/chenyanchen/kv/layerkv"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/chenyanchen/resorch"
	"github.com/chenyanchen/resorch/examples/internal/demo"
)

type postgresOpt struct {
	DSN string `json:"dsn"`
}

type redisOpt struct {
	Addr string `json:"addr"`
	DB   int    `json:"db"`
}

type composedKVOpt struct {
	Redis    string `json:"redis,omitempty"`
	Database string `json:"database,omitempty"`
	Memory   bool   `json:"memory"`
}

type preset struct {
	Name      string
	Memory    bool
	UseRedis  bool
	UseDB     bool
	UserRedis string
	ArtRedis  string
	Database  string
}

func main() {
	verify := flag.Bool("verify", true, "verify each composition with Set/Get round-trips (requires local infra)")
	flag.Parse()

	reg := resorch.NewRegistry()
	registerDefinitions(reg)

	presets := []preset{
		{Name: "mem-redis-db", Memory: true, UseRedis: true, UseDB: true, UserRedis: "user-cache", ArtRedis: "article-cache", Database: "main"},
		{Name: "mem-redis", Memory: true, UseRedis: true, UseDB: false, UserRedis: "user-cache", ArtRedis: "article-cache"},
		{Name: "mem-db", Memory: true, UseRedis: false, UseDB: true, Database: "main"},
		{Name: "redis-db", Memory: false, UseRedis: true, UseDB: true, UserRedis: "user-cache", ArtRedis: "article-cache", Database: "main"},
		{Name: "redis", Memory: false, UseRedis: true, UseDB: false, UserRedis: "user-cache", ArtRedis: "article-cache"},
		{Name: "db", Memory: false, UseRedis: false, UseDB: true, Database: "main"},
	}

	specs := []resorch.NodeSpec{
		{Kind: "postgres", Name: "main", Driver: "pgx", Options: rawJSON(postgresOpt{
			DSN: "postgres://resorch:resorch@127.0.0.1:55432/resorch?sslmode=disable",
		})},
		{Kind: "redis", Name: "user-cache", Driver: "single", Options: rawJSON(redisOpt{
			Addr: "127.0.0.1:6381",
			DB:   0,
		})},
		{Kind: "redis", Name: "article-cache", Driver: "single", Options: rawJSON(redisOpt{
			Addr: "127.0.0.1:6382",
			DB:   0,
		})},
	}
	for _, p := range presets {
		specs = append(specs,
			resorch.NodeSpec{
				Kind:    "userKV",
				Name:    p.Name,
				Driver:  "composed",
				Options: rawJSON(p.userOpt()),
			},
			resorch.NodeSpec{
				Kind:    "articleKV",
				Name:    p.Name,
				Driver:  "composed",
				Options: rawJSON(p.articleOpt()),
			},
		)
	}

	container, err := resorch.NewContainer(reg, specs)
	must(err)
	defer container.Close(context.Background())

	graph := container.Graph()
	fmt.Printf("nodes=%d edges=%d\n", len(graph.Nodes), len(graph.Edges))
	fmt.Println("composition dependency summary:")
	for _, p := range presets {
		userDeps := depsFor(graph, resorch.ID{Kind: "userKV", Name: p.Name})
		articleDeps := depsFor(graph, resorch.ID{Kind: "articleKV", Name: p.Name})
		fmt.Printf("- mode=%s userKV deps=%v articleKV deps=%v\n", p.Name, userDeps, articleDeps)
	}

	if !*verify {
		fmt.Println("verify=false: skip runtime Set/Get checks")
		return
	}

	ctx := context.Background()
	for i, p := range presets {
		userKV, err := resorch.ResolveAs[kvpkg.KV[int64, demo.User]](ctx, container, resorch.ID{
			Kind: "userKV",
			Name: p.Name,
		})
		must(err)
		articleKV, err := resorch.ResolveAs[kvpkg.KV[int64, demo.Article]](ctx, container, resorch.ID{
			Kind: "articleKV",
			Name: p.Name,
		})
		must(err)

		userID := int64(9001 + i)
		articleID := int64(9101 + i)
		user := demo.User{ID: userID, Name: "mode-" + p.Name, Tier: "demo"}
		article := demo.Article{ID: articleID, Title: "mode-" + p.Name, Category: "demo", Score: 0.99}

		must(userKV.Set(ctx, userID, user))
		must(articleKV.Set(ctx, articleID, article))

		gotUser, err := userKV.Get(ctx, userID)
		must(err)
		gotArticle, err := articleKV.Get(ctx, articleID)
		must(err)

		fmt.Printf("verified mode=%s user=%s article=%s\n", p.Name, gotUser.Name, gotArticle.Title)
	}
}

func registerDefinitions(reg *resorch.Registry) {
	resorch.MustRegister(reg, "postgres", "pgx", resorch.Definition[postgresOpt, *pgxpool.Pool]{
		Build: func(ctx context.Context, _ resorch.Resolver, opt postgresOpt) (*pgxpool.Pool, error) {
			pool, err := pgxpool.New(ctx, opt.DSN)
			if err != nil {
				return nil, err
			}
			if err := pool.Ping(ctx); err != nil {
				pool.Close()
				return nil, err
			}
			if err := demo.EnsureSchema(ctx, pool); err != nil {
				pool.Close()
				return nil, err
			}
			if err := demo.SeedDemoData(ctx, pool); err != nil {
				pool.Close()
				return nil, err
			}
			return pool, nil
		},
		Close: func(_ context.Context, pool *pgxpool.Pool) error {
			pool.Close()
			return nil
		},
	})

	resorch.MustRegister(reg, "redis", "single", resorch.Definition[redisOpt, *redis.Client]{
		Build: func(ctx context.Context, _ resorch.Resolver, opt redisOpt) (*redis.Client, error) {
			cli := redis.NewClient(&redis.Options{Addr: opt.Addr, DB: opt.DB})
			if err := cli.Ping(ctx).Err(); err != nil {
				_ = cli.Close()
				return nil, err
			}
			if err := cli.FlushDB(ctx).Err(); err != nil {
				_ = cli.Close()
				return nil, err
			}
			return cli, nil
		},
		Close: func(_ context.Context, cli *redis.Client) error {
			return cli.Close()
		},
	})

	resorch.MustRegister(reg, "userKV", "composed", resorch.Definition[composedKVOpt, kvpkg.KV[int64, demo.User]]{
		Deps: func(opt composedKVOpt) ([]resorch.ID, error) {
			return depsFromOpt(opt)
		},
		Build: buildUserKV,
	})

	resorch.MustRegister(reg, "articleKV", "composed", resorch.Definition[composedKVOpt, kvpkg.KV[int64, demo.Article]]{
		Deps: func(opt composedKVOpt) ([]resorch.ID, error) {
			return depsFromOpt(opt)
		},
		Build: buildArticleKV,
	})
}

func buildUserKV(ctx context.Context, r resorch.Resolver, opt composedKVOpt) (kvpkg.KV[int64, demo.User], error) {
	var backend kvpkg.KV[int64, demo.User]
	if opt.Database != "" {
		pool, err := resorch.ResolveAs[*pgxpool.Pool](ctx, r, resorch.ID{Kind: "postgres", Name: opt.Database})
		if err != nil {
			return nil, err
		}
		backend = &demo.UserDBStore{Pool: pool}
	}
	if opt.Redis != "" {
		redisClient, err := resorch.ResolveAs[*redis.Client](ctx, r, resorch.ID{Kind: "redis", Name: opt.Redis})
		if err != nil {
			return nil, err
		}
		redisStore := &demo.JSONRedisStore[demo.User]{
			Client: redisClient,
			Prefix: "matrix:user:" + opt.Redis,
			TTL:    10 * time.Minute,
		}
		if backend == nil {
			backend = redisStore
		} else {
			layered, err := layerkv.New[int64, demo.User](redisStore, backend, layerkv.WithWriteThrough())
			if err != nil {
				return nil, err
			}
			backend = layered
		}
	}
	if backend == nil {
		return nil, errors.New("invalid userKV composition: at least one of redis/database is required")
	}
	if !opt.Memory {
		return backend, nil
	}

	memoryCache, err := cachekv.NewLRU[int64, demo.User](1024, nil, 2*time.Minute)
	if err != nil {
		return nil, err
	}
	return layerkv.New[int64, demo.User](memoryCache, backend, layerkv.WithWriteThrough())
}

func buildArticleKV(ctx context.Context, r resorch.Resolver, opt composedKVOpt) (kvpkg.KV[int64, demo.Article], error) {
	var backend kvpkg.KV[int64, demo.Article]
	if opt.Database != "" {
		pool, err := resorch.ResolveAs[*pgxpool.Pool](ctx, r, resorch.ID{Kind: "postgres", Name: opt.Database})
		if err != nil {
			return nil, err
		}
		backend = &demo.ArticleDBStore{Pool: pool}
	}
	if opt.Redis != "" {
		redisClient, err := resorch.ResolveAs[*redis.Client](ctx, r, resorch.ID{Kind: "redis", Name: opt.Redis})
		if err != nil {
			return nil, err
		}
		redisStore := &demo.JSONRedisStore[demo.Article]{
			Client: redisClient,
			Prefix: "matrix:article:" + opt.Redis,
			TTL:    10 * time.Minute,
		}
		if backend == nil {
			backend = redisStore
		} else {
			layered, err := layerkv.New[int64, demo.Article](redisStore, backend, layerkv.WithWriteThrough())
			if err != nil {
				return nil, err
			}
			backend = layered
		}
	}
	if backend == nil {
		return nil, errors.New("invalid articleKV composition: at least one of redis/database is required")
	}
	if !opt.Memory {
		return backend, nil
	}

	memoryCache, err := cachekv.NewLRU[int64, demo.Article](2048, nil, 2*time.Minute)
	if err != nil {
		return nil, err
	}
	return layerkv.New[int64, demo.Article](memoryCache, backend, layerkv.WithWriteThrough())
}

func depsFromOpt(opt composedKVOpt) ([]resorch.ID, error) {
	deps := make([]resorch.ID, 0, 2)
	if opt.Redis != "" {
		deps = append(deps, resorch.ID{Kind: "redis", Name: opt.Redis})
	}
	if opt.Database != "" {
		deps = append(deps, resorch.ID{Kind: "postgres", Name: opt.Database})
	}
	if len(deps) == 0 {
		return nil, errors.New("invalid composition: at least one of redis/database is required")
	}
	return deps, nil
}

func (p preset) userOpt() composedKVOpt {
	opt := composedKVOpt{Memory: p.Memory}
	if p.UseRedis {
		opt.Redis = p.UserRedis
	}
	if p.UseDB {
		opt.Database = p.Database
	}
	return opt
}

func (p preset) articleOpt() composedKVOpt {
	opt := composedKVOpt{Memory: p.Memory}
	if p.UseRedis {
		opt.Redis = p.ArtRedis
	}
	if p.UseDB {
		opt.Database = p.Database
	}
	return opt
}

func depsFor(graph resorch.Graph, from resorch.ID) []string {
	deps := make([]string, 0, 2)
	for _, edge := range graph.Edges {
		if edge.From == from {
			deps = append(deps, edge.To.String())
		}
	}
	sort.Strings(deps)
	return deps
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
