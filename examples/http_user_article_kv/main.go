package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	kvpkg "github.com/chenyanchen/kv"
	"github.com/chenyanchen/kv/cachekv"
	"github.com/chenyanchen/kv/layerkv"
	"github.com/chenyanchen/resorch"
	"github.com/chenyanchen/resorch/examples/internal/demo"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

type postgresOpt struct {
	DSN string `json:"dsn"`
}

type redisOpt struct {
	Addr     string `json:"addr"`
	Password string `json:"password"`
	DB       int    `json:"db"`
}

type layeredKVOpt struct {
	Redis    string `json:"redis"`
	Database string `json:"database"`
}

type httpOpt struct {
	Listen    string `json:"listen"`
	UserKV    string `json:"userKV"`
	ArticleKV string `json:"articleKV"`
}

type runningHTTPServer struct {
	server   *http.Server
	listener net.Listener
}

func main() {
	reg := resorch.NewRegistry()

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
			cli := redis.NewClient(&redis.Options{
				Addr:     opt.Addr,
				Password: opt.Password,
				DB:       opt.DB,
			})
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

	resorch.MustRegister(reg, "userKV", "layered", resorch.Definition[layeredKVOpt, kvpkg.KV[int64, demo.User]]{
		Deps: func(opt layeredKVOpt) ([]resorch.ID, error) {
			return []resorch.ID{
				{Kind: "redis", Name: opt.Redis},
				{Kind: "postgres", Name: opt.Database},
			}, nil
		},
		Build: func(ctx context.Context, r resorch.Resolver, opt layeredKVOpt) (kvpkg.KV[int64, demo.User], error) {
			redisClient, err := resorch.ResolveAs[*redis.Client](ctx, r, resorch.ID{Kind: "redis", Name: opt.Redis})
			if err != nil {
				return nil, err
			}
			pool, err := resorch.ResolveAs[*pgxpool.Pool](ctx, r, resorch.ID{Kind: "postgres", Name: opt.Database})
			if err != nil {
				return nil, err
			}

			dbStore := &demo.UserDBStore{Pool: pool}
			redisStore := &demo.JSONRedisStore[demo.User]{
				Client: redisClient,
				Prefix: "user",
				TTL:    10 * time.Minute,
			}

			redisLayer, err := layerkv.New[int64, demo.User](redisStore, dbStore, layerkv.WithWriteThrough())
			if err != nil {
				return nil, err
			}
			memoryCache, err := cachekv.NewLRU[int64, demo.User](2048, nil, 2*time.Minute)
			if err != nil {
				return nil, err
			}
			return layerkv.New[int64, demo.User](memoryCache, redisLayer, layerkv.WithWriteThrough())
		},
	})

	resorch.MustRegister(reg, "articleKV", "layered", resorch.Definition[layeredKVOpt, kvpkg.KV[int64, demo.Article]]{
		Deps: func(opt layeredKVOpt) ([]resorch.ID, error) {
			return []resorch.ID{
				{Kind: "redis", Name: opt.Redis},
				{Kind: "postgres", Name: opt.Database},
			}, nil
		},
		Build: func(ctx context.Context, r resorch.Resolver, opt layeredKVOpt) (kvpkg.KV[int64, demo.Article], error) {
			redisClient, err := resorch.ResolveAs[*redis.Client](ctx, r, resorch.ID{Kind: "redis", Name: opt.Redis})
			if err != nil {
				return nil, err
			}
			pool, err := resorch.ResolveAs[*pgxpool.Pool](ctx, r, resorch.ID{Kind: "postgres", Name: opt.Database})
			if err != nil {
				return nil, err
			}

			dbStore := &demo.ArticleDBStore{Pool: pool}
			redisStore := &demo.JSONRedisStore[demo.Article]{
				Client: redisClient,
				Prefix: "article",
				TTL:    10 * time.Minute,
			}

			redisLayer, err := layerkv.New[int64, demo.Article](redisStore, dbStore, layerkv.WithWriteThrough())
			if err != nil {
				return nil, err
			}
			memoryCache, err := cachekv.NewLRU[int64, demo.Article](2048, nil, 2*time.Minute)
			if err != nil {
				return nil, err
			}
			return layerkv.New[int64, demo.Article](memoryCache, redisLayer, layerkv.WithWriteThrough())
		},
	})

	resorch.MustRegister(reg, "httpServer", "summary", resorch.Definition[httpOpt, *runningHTTPServer]{
		Deps: func(opt httpOpt) ([]resorch.ID, error) {
			return []resorch.ID{
				{Kind: "userKV", Name: opt.UserKV},
				{Kind: "articleKV", Name: opt.ArticleKV},
			}, nil
		},
		Build: func(ctx context.Context, r resorch.Resolver, opt httpOpt) (*runningHTTPServer, error) {
			userKV, err := resorch.ResolveAs[kvpkg.KV[int64, demo.User]](ctx, r, resorch.ID{Kind: "userKV", Name: opt.UserKV})
			if err != nil {
				return nil, err
			}
			articleKV, err := resorch.ResolveAs[kvpkg.KV[int64, demo.Article]](ctx, r, resorch.ID{Kind: "articleKV", Name: opt.ArticleKV})
			if err != nil {
				return nil, err
			}

			mux := http.NewServeMux()
			mux.HandleFunc("/v1/summary", func(w http.ResponseWriter, req *http.Request) {
				userID := queryInt64(req, "user_id", 1)
				articleID := queryInt64(req, "article_id", 101)

				user, err := userKV.Get(req.Context(), userID)
				if errors.Is(err, kvpkg.ErrNotFound) {
					http.Error(w, "user not found", http.StatusNotFound)
					return
				}
				if err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}

				article, err := articleKV.Get(req.Context(), articleID)
				if errors.Is(err, kvpkg.ErrNotFound) {
					http.Error(w, "article not found", http.StatusNotFound)
					return
				}
				if err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}

				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{
					"user": map[string]any{
						"id":   user.ID,
						"name": user.Name,
						"tier": user.Tier,
					},
					"article": map[string]any{
						"id":       article.ID,
						"title":    article.Title,
						"category": article.Category,
						"score":    article.Score,
					},
				})
			})

			server := &http.Server{
				Addr:    opt.Listen,
				Handler: mux,
			}
			listener, err := net.Listen("tcp", opt.Listen)
			if err != nil {
				return nil, err
			}
			go func() {
				_ = server.Serve(listener)
			}()
			return &runningHTTPServer{server: server, listener: listener}, nil
		},
		Close: func(ctx context.Context, srv *runningHTTPServer) error {
			shutdownCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
			defer cancel()
			return srv.server.Shutdown(shutdownCtx)
		},
	})

	specs := []resorch.NodeSpec{
		{
			Kind:    "postgres",
			Name:    "main",
			Driver:  "pgx",
			Options: rawJSON(postgresOpt{DSN: "postgres://resorch:resorch@127.0.0.1:55432/resorch?sslmode=disable"}),
		},
		{
			Kind:    "redis",
			Name:    "user-cache",
			Driver:  "single",
			Options: rawJSON(redisOpt{Addr: "127.0.0.1:6381", DB: 0}),
		},
		{
			Kind:    "redis",
			Name:    "article-cache",
			Driver:  "single",
			Options: rawJSON(redisOpt{Addr: "127.0.0.1:6382", DB: 0}),
		},
		{
			Kind:    "userKV",
			Name:    "main",
			Driver:  "layered",
			Options: rawJSON(layeredKVOpt{Redis: "user-cache", Database: "main"}),
		},
		{
			Kind:    "articleKV",
			Name:    "main",
			Driver:  "layered",
			Options: rawJSON(layeredKVOpt{Redis: "article-cache", Database: "main"}),
		},
		{
			Kind:    "httpServer",
			Name:    "api",
			Driver:  "summary",
			Options: rawJSON(httpOpt{Listen: "127.0.0.1:18080", UserKV: "main", ArticleKV: "main"}),
		},
	}

	container, err := resorch.NewContainer(reg, specs)
	must(err)

	_, err = container.Resolve(context.Background(), resorch.ID{Kind: "httpServer", Name: "api"})
	must(err)

	fmt.Println("http server ready on http://127.0.0.1:18080/v1/summary?user_id=1&article_id=101")
	fmt.Println("press Ctrl+C to exit")
	waitForSignal()
	must(container.Close(context.Background()))
}

func queryInt64(r *http.Request, key string, fallback int64) int64 {
	v := r.URL.Query().Get(key)
	if v == "" {
		return fallback
	}
	parsed, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return fallback
	}
	return parsed
}

func waitForSignal() {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
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
