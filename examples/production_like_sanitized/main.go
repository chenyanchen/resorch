package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sort"
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
	"gopkg.in/yaml.v3"
)

type resourceEntry[T any] struct {
	Name    string `yaml:"name"`
	Driver  string `yaml:"driver"`
	Options T      `yaml:"options"`
}

type rawResourceEntry struct {
	Name    string                 `yaml:"name"`
	Driver  string                 `yaml:"driver"`
	Options map[string]any         `yaml:"options"`
	Raw     map[string]interface{} `yaml:",inline"`
}

type configFile struct {
	Redis          []resourceEntry[redisOpt]       `yaml:"redis"`
	Database       []resourceEntry[postgresOpt]    `yaml:"database"`
	ProfileService []resourceEntry[map[string]any] `yaml:"profileService"`
	PolicyService  []resourceEntry[map[string]any] `yaml:"policyService"`
	ScoreService   []resourceEntry[map[string]any] `yaml:"scoreService"`
	UserKV         []resourceEntry[layeredKVOpt]   `yaml:"userKV"`
	ArticleKV      []resourceEntry[layeredKVOpt]   `yaml:"articleKV"`
	Logic          []rawResourceEntry              `yaml:"logic"`
	Stage          []resourceEntry[stageOpt]       `yaml:"stage"`
	Pipeline       []resourceEntry[pipelineOpt]    `yaml:"pipeline"`
	API            []resourceEntry[gatewayOpt]     `yaml:"api"`
}

type postgresOpt struct {
	DSN string `json:"dsn" yaml:"dsn"`
}

type redisOpt struct {
	Addr     string `json:"addr" yaml:"addr"`
	Password string `json:"password" yaml:"password"`
	DB       int    `json:"db" yaml:"db"`
}

type layeredKVOpt struct {
	Redis    string `json:"redis" yaml:"redis"`
	Database string `json:"database" yaml:"database"`
}

type userFeatureOpt struct {
	UserKV         string `json:"userKV"`
	ProfileService string `json:"profileService"`
}

type recallOpt struct {
	Database string `json:"database"`
	Limit    int    `json:"limit"`
}

type articleFeatureOpt struct {
	ArticleKV string `json:"articleKV"`
}

type ruleFilterOpt struct {
	PolicyService string `json:"policyService"`
}

type rankOpt struct {
	ScoreService string `json:"scoreService"`
}

type noopOpt struct {
	Message string `json:"message"`
}

type stageOpt struct {
	ExecMode string   `json:"execMode" yaml:"execMode"`
	Logics   []string `json:"logics" yaml:"logics"`
}

type pipelineOpt struct {
	Timeout string   `json:"timeout" yaml:"timeout"`
	Stages  []string `json:"stages" yaml:"stages"`
}

type gatewayOpt struct {
	Listen   string `json:"listen" yaml:"listen"`
	Pipeline string `json:"pipeline" yaml:"pipeline"`
}

type runningHTTPServer struct {
	server   *http.Server
	listener net.Listener
}

type recommendation struct {
	ArticleID int64   `json:"article_id"`
	Title     string  `json:"title"`
	Category  string  `json:"category"`
	Score     float64 `json:"score"`
}

type pipelineResult struct {
	User            demo.User         `json:"user"`
	Recommendations []recommendation  `json:"recommendations"`
	Notes           []string          `json:"notes"`
	RawScores       map[int64]float64 `json:"-"`
}

type pipelineState struct {
	UserID      int64
	User        demo.User
	Candidates  []demo.Article
	ScoreByID   map[int64]float64
	Notes       []string
	AffinityMul float64
}

type Logic interface {
	Name() string
	Run(context.Context, *pipelineState) error
}

type logicFunc struct {
	name string
	run  func(context.Context, *pipelineState) error
}

func (l logicFunc) Name() string { return l.name }

func (l logicFunc) Run(ctx context.Context, s *pipelineState) error {
	return l.run(ctx, s)
}

type Stage struct {
	Name     string
	ExecMode string
	Logics   []Logic
}

func (s *Stage) Run(ctx context.Context, state *pipelineState) error {
	for _, logic := range s.Logics {
		if err := logic.Run(ctx, state); err != nil {
			return fmt.Errorf("logic %s in stage %s: %w", logic.Name(), s.Name, err)
		}
	}
	return nil
}

type Pipeline struct {
	Name    string
	Timeout time.Duration
	Stages  []*Stage
}

func (p *Pipeline) Run(ctx context.Context, userID int64) (pipelineResult, error) {
	state := &pipelineState{
		UserID:    userID,
		ScoreByID: make(map[int64]float64),
		Notes:     make([]string, 0, 8),
	}
	if p.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, p.Timeout)
		defer cancel()
	}
	for _, stage := range p.Stages {
		if err := stage.Run(ctx, state); err != nil {
			return pipelineResult{}, err
		}
	}

	sort.SliceStable(state.Candidates, func(i, j int) bool {
		left := state.ScoreByID[state.Candidates[i].ID]
		right := state.ScoreByID[state.Candidates[j].ID]
		if left == right {
			return state.Candidates[i].ID < state.Candidates[j].ID
		}
		return left > right
	})

	recs := make([]recommendation, 0, len(state.Candidates))
	for _, item := range state.Candidates {
		recs = append(recs, recommendation{
			ArticleID: item.ID,
			Title:     item.Title,
			Category:  item.Category,
			Score:     state.ScoreByID[item.ID],
		})
	}

	return pipelineResult{
		User:            state.User,
		Recommendations: recs,
		Notes:           append([]string(nil), state.Notes...),
		RawScores:       state.ScoreByID,
	}, nil
}

type mockProfileService struct{}

func (m *mockProfileService) AffinityMultiplier(user demo.User) float64 {
	switch user.Tier {
	case "premium":
		return 1.15
	case "basic":
		return 1.00
	default:
		return 0.95
	}
}

type mockPolicyService struct{}

func (m *mockPolicyService) Allow(user demo.User, article demo.Article) bool {
	if user.Tier == "basic" && article.Category == "premium" {
		return false
	}
	return true
}

type mockScoreService struct{}

func (m *mockScoreService) Score(user demo.User, article demo.Article, base, affinity float64) float64 {
	tierBoost := 1.0
	if user.Tier == "premium" {
		tierBoost = 1.08
	}
	categoryBoost := 1.0
	if article.Category == "operations" {
		categoryBoost = 1.03
	}
	return base * tierBoost * categoryBoost * affinity
}

func main() {
	configPath := flag.String("config", "./examples/production_like_sanitized/config.sanitized.yaml", "path to sanitized config")
	flag.Parse()

	cfg, err := loadConfig(*configPath)
	must(err)

	reg := resorch.NewRegistry()
	registerCoreDefinitions(reg)
	registerPipelineDefinitions(reg)

	specs, err := buildNodeSpecs(cfg)
	must(err)

	container, err := resorch.NewContainer(reg, specs)
	must(err)

	_, err = container.Resolve(context.Background(), resorch.ID{Kind: "api", Name: "recommendation"})
	must(err)

	fmt.Printf("sanitized production-like API ready on http://127.0.0.1:18081/v1/recommend?user_id=1\n")
	fmt.Println("press Ctrl+C to exit")
	waitForSignal()
	must(container.Close(context.Background()))
}

func registerCoreDefinitions(reg *resorch.Registry) {
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
			cli := redis.NewClient(&redis.Options{Addr: opt.Addr, Password: opt.Password, DB: opt.DB})
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
		Close: func(_ context.Context, cli *redis.Client) error { return cli.Close() },
	})

	resorch.MustRegister(reg, "profileService", "mock", resorch.Definition[map[string]any, *mockProfileService]{
		Build: func(_ context.Context, _ resorch.Resolver, _ map[string]any) (*mockProfileService, error) {
			return &mockProfileService{}, nil
		},
	})
	resorch.MustRegister(reg, "policyService", "mock", resorch.Definition[map[string]any, *mockPolicyService]{
		Build: func(_ context.Context, _ resorch.Resolver, _ map[string]any) (*mockPolicyService, error) {
			return &mockPolicyService{}, nil
		},
	})
	resorch.MustRegister(reg, "scoreService", "mock", resorch.Definition[map[string]any, *mockScoreService]{
		Build: func(_ context.Context, _ resorch.Resolver, _ map[string]any) (*mockScoreService, error) {
			return &mockScoreService{}, nil
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
			cli, err := resorch.ResolveAs[*redis.Client](ctx, r, resorch.ID{Kind: "redis", Name: opt.Redis})
			if err != nil {
				return nil, err
			}
			pool, err := resorch.ResolveAs[*pgxpool.Pool](ctx, r, resorch.ID{Kind: "postgres", Name: opt.Database})
			if err != nil {
				return nil, err
			}

			dbStore := &demo.UserDBStore{Pool: pool}
			redisStore := &demo.JSONRedisStore[demo.User]{Client: cli, Prefix: "prod:user", TTL: 10 * time.Minute}
			redisLayer, err := layerkv.New[int64, demo.User](redisStore, dbStore, layerkv.WithWriteThrough())
			if err != nil {
				return nil, err
			}
			cache, err := cachekv.NewLRU[int64, demo.User](2048, nil, 2*time.Minute)
			if err != nil {
				return nil, err
			}
			return layerkv.New[int64, demo.User](cache, redisLayer, layerkv.WithWriteThrough())
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
			cli, err := resorch.ResolveAs[*redis.Client](ctx, r, resorch.ID{Kind: "redis", Name: opt.Redis})
			if err != nil {
				return nil, err
			}
			pool, err := resorch.ResolveAs[*pgxpool.Pool](ctx, r, resorch.ID{Kind: "postgres", Name: opt.Database})
			if err != nil {
				return nil, err
			}

			dbStore := &demo.ArticleDBStore{Pool: pool}
			redisStore := &demo.JSONRedisStore[demo.Article]{Client: cli, Prefix: "prod:article", TTL: 10 * time.Minute}
			redisLayer, err := layerkv.New[int64, demo.Article](redisStore, dbStore, layerkv.WithWriteThrough())
			if err != nil {
				return nil, err
			}
			cache, err := cachekv.NewLRU[int64, demo.Article](4096, nil, 2*time.Minute)
			if err != nil {
				return nil, err
			}
			return layerkv.New[int64, demo.Article](cache, redisLayer, layerkv.WithWriteThrough())
		},
	})
}

func registerPipelineDefinitions(reg *resorch.Registry) {
	resorch.MustRegister(reg, "logic", "user-feature", resorch.Definition[userFeatureOpt, Logic]{
		Deps: func(opt userFeatureOpt) ([]resorch.ID, error) {
			return []resorch.ID{{Kind: "userKV", Name: opt.UserKV}, {Kind: "profileService", Name: opt.ProfileService}}, nil
		},
		Build: func(ctx context.Context, r resorch.Resolver, opt userFeatureOpt) (Logic, error) {
			userKV, err := resorch.ResolveAs[kvpkg.KV[int64, demo.User]](ctx, r, resorch.ID{Kind: "userKV", Name: opt.UserKV})
			if err != nil {
				return nil, err
			}
			profileSvc, err := resorch.ResolveAs[*mockProfileService](ctx, r, resorch.ID{Kind: "profileService", Name: opt.ProfileService})
			if err != nil {
				return nil, err
			}
			return logicFunc{name: "user-feature", run: func(ctx context.Context, state *pipelineState) error {
				user, err := userKV.Get(ctx, state.UserID)
				if errors.Is(err, kvpkg.ErrNotFound) {
					user = demo.User{ID: state.UserID, Name: "Guest", Tier: "guest"}
				} else if err != nil {
					return err
				}
				state.User = user
				state.AffinityMul = profileSvc.AffinityMultiplier(user)
				state.Notes = append(state.Notes, "user features prepared")
				return nil
			}}, nil
		},
	})

	resorch.MustRegister(reg, "logic", "recall", resorch.Definition[recallOpt, Logic]{
		Deps: func(opt recallOpt) ([]resorch.ID, error) {
			return []resorch.ID{{Kind: "postgres", Name: opt.Database}}, nil
		},
		Build: func(ctx context.Context, r resorch.Resolver, opt recallOpt) (Logic, error) {
			pool, err := resorch.ResolveAs[*pgxpool.Pool](ctx, r, resorch.ID{Kind: "postgres", Name: opt.Database})
			if err != nil {
				return nil, err
			}
			return logicFunc{name: "recall", run: func(ctx context.Context, state *pipelineState) error {
				store := demo.ArticleDBStore{Pool: pool}
				candidates, err := store.ListTopArticles(ctx, opt.Limit)
				if err != nil {
					return err
				}
				state.Candidates = candidates
				for _, item := range candidates {
					state.ScoreByID[item.ID] = item.Score
				}
				state.Notes = append(state.Notes, fmt.Sprintf("recalled %d candidates", len(candidates)))
				return nil
			}}, nil
		},
	})

	resorch.MustRegister(reg, "logic", "article-feature", resorch.Definition[articleFeatureOpt, Logic]{
		Deps: func(opt articleFeatureOpt) ([]resorch.ID, error) {
			return []resorch.ID{{Kind: "articleKV", Name: opt.ArticleKV}}, nil
		},
		Build: func(ctx context.Context, r resorch.Resolver, opt articleFeatureOpt) (Logic, error) {
			articleKV, err := resorch.ResolveAs[kvpkg.KV[int64, demo.Article]](ctx, r, resorch.ID{Kind: "articleKV", Name: opt.ArticleKV})
			if err != nil {
				return nil, err
			}
			return logicFunc{name: "article-feature", run: func(ctx context.Context, state *pipelineState) error {
				for i := range state.Candidates {
					id := state.Candidates[i].ID
					article, err := articleKV.Get(ctx, id)
					if err != nil {
						return err
					}
					state.Candidates[i] = article
					state.ScoreByID[id] = state.ScoreByID[id] + 0.02
				}
				state.Notes = append(state.Notes, "article features enriched")
				return nil
			}}, nil
		},
	})

	resorch.MustRegister(reg, "logic", "rule-filter", resorch.Definition[ruleFilterOpt, Logic]{
		Deps: func(opt ruleFilterOpt) ([]resorch.ID, error) {
			return []resorch.ID{{Kind: "policyService", Name: opt.PolicyService}}, nil
		},
		Build: func(ctx context.Context, r resorch.Resolver, opt ruleFilterOpt) (Logic, error) {
			policySvc, err := resorch.ResolveAs[*mockPolicyService](ctx, r, resorch.ID{Kind: "policyService", Name: opt.PolicyService})
			if err != nil {
				return nil, err
			}
			return logicFunc{name: "rule-filter", run: func(ctx context.Context, state *pipelineState) error {
				filtered := make([]demo.Article, 0, len(state.Candidates))
				for _, item := range state.Candidates {
					if policySvc.Allow(state.User, item) {
						filtered = append(filtered, item)
						continue
					}
					delete(state.ScoreByID, item.ID)
				}
				state.Candidates = filtered
				state.Notes = append(state.Notes, "policy filter applied")
				return nil
			}}, nil
		},
	})

	resorch.MustRegister(reg, "logic", "rank", resorch.Definition[rankOpt, Logic]{
		Deps: func(opt rankOpt) ([]resorch.ID, error) {
			return []resorch.ID{{Kind: "scoreService", Name: opt.ScoreService}}, nil
		},
		Build: func(ctx context.Context, r resorch.Resolver, opt rankOpt) (Logic, error) {
			scoreSvc, err := resorch.ResolveAs[*mockScoreService](ctx, r, resorch.ID{Kind: "scoreService", Name: opt.ScoreService})
			if err != nil {
				return nil, err
			}
			return logicFunc{name: "rank", run: func(ctx context.Context, state *pipelineState) error {
				for _, item := range state.Candidates {
					base := state.ScoreByID[item.ID]
					state.ScoreByID[item.ID] = scoreSvc.Score(state.User, item, base, state.AffinityMul)
				}
				state.Notes = append(state.Notes, "ranking completed")
				return nil
			}}, nil
		},
	})

	resorch.MustRegister(reg, "logic", "noop", resorch.Definition[noopOpt, Logic]{
		Build: func(_ context.Context, _ resorch.Resolver, opt noopOpt) (Logic, error) {
			return logicFunc{name: "noop", run: func(_ context.Context, state *pipelineState) error {
				if opt.Message != "" {
					state.Notes = append(state.Notes, opt.Message)
				}
				return nil
			}}, nil
		},
	})

	resorch.MustRegister(reg, "stage", "default", resorch.Definition[stageOpt, *Stage]{
		Deps: func(opt stageOpt) ([]resorch.ID, error) {
			deps := make([]resorch.ID, 0, len(opt.Logics))
			for _, logic := range opt.Logics {
				deps = append(deps, resorch.ID{Kind: "logic", Name: logic})
			}
			return deps, nil
		},
		Build: func(ctx context.Context, r resorch.Resolver, opt stageOpt) (*Stage, error) {
			logics := make([]Logic, 0, len(opt.Logics))
			for _, logic := range opt.Logics {
				item, err := resorch.ResolveAs[Logic](ctx, r, resorch.ID{Kind: "logic", Name: logic})
				if err != nil {
					return nil, err
				}
				logics = append(logics, item)
			}
			return &Stage{ExecMode: opt.ExecMode, Logics: logics}, nil
		},
	})

	resorch.MustRegister(reg, "pipeline", "default", resorch.Definition[pipelineOpt, *Pipeline]{
		Deps: func(opt pipelineOpt) ([]resorch.ID, error) {
			deps := make([]resorch.ID, 0, len(opt.Stages))
			for _, stage := range opt.Stages {
				deps = append(deps, resorch.ID{Kind: "stage", Name: stage})
			}
			return deps, nil
		},
		Build: func(ctx context.Context, r resorch.Resolver, opt pipelineOpt) (*Pipeline, error) {
			stages := make([]*Stage, 0, len(opt.Stages))
			for _, stage := range opt.Stages {
				item, err := resorch.ResolveAs[*Stage](ctx, r, resorch.ID{Kind: "stage", Name: stage})
				if err != nil {
					return nil, err
				}
				item.Name = stage
				stages = append(stages, item)
			}

			timeout := 2 * time.Second
			if opt.Timeout != "" {
				parsed, err := time.ParseDuration(opt.Timeout)
				if err != nil {
					return nil, err
				}
				timeout = parsed
			}
			return &Pipeline{Timeout: timeout, Stages: stages}, nil
		},
	})

	resorch.MustRegister(reg, "api", "std-http", resorch.Definition[gatewayOpt, *runningHTTPServer]{
		Deps: func(opt gatewayOpt) ([]resorch.ID, error) {
			return []resorch.ID{{Kind: "pipeline", Name: opt.Pipeline}}, nil
		},
		Build: func(ctx context.Context, r resorch.Resolver, opt gatewayOpt) (*runningHTTPServer, error) {
			pipeline, err := resorch.ResolveAs[*Pipeline](ctx, r, resorch.ID{Kind: "pipeline", Name: opt.Pipeline})
			if err != nil {
				return nil, err
			}
			mux := http.NewServeMux()
			mux.HandleFunc("/v1/recommend", func(w http.ResponseWriter, req *http.Request) {
				userID := queryInt64(req, "user_id", 1)
				result, err := pipeline.Run(req.Context(), userID)
				if err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(result)
			})

			server := &http.Server{Addr: opt.Listen, Handler: mux}
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
}

func buildNodeSpecs(cfg configFile) ([]resorch.NodeSpec, error) {
	specs := make([]resorch.NodeSpec, 0, 64)

	if err := appendSpecs(&specs, "redis", cfg.Redis); err != nil {
		return nil, err
	}
	if err := appendSpecs(&specs, "postgres", cfg.Database); err != nil {
		return nil, err
	}
	if err := appendSpecs(&specs, "profileService", cfg.ProfileService); err != nil {
		return nil, err
	}
	if err := appendSpecs(&specs, "policyService", cfg.PolicyService); err != nil {
		return nil, err
	}
	if err := appendSpecs(&specs, "scoreService", cfg.ScoreService); err != nil {
		return nil, err
	}
	if err := appendSpecs(&specs, "userKV", cfg.UserKV); err != nil {
		return nil, err
	}
	if err := appendSpecs(&specs, "articleKV", cfg.ArticleKV); err != nil {
		return nil, err
	}
	if err := appendRawSpecs(&specs, "logic", cfg.Logic); err != nil {
		return nil, err
	}
	if err := appendSpecs(&specs, "stage", cfg.Stage); err != nil {
		return nil, err
	}
	if err := appendSpecs(&specs, "pipeline", cfg.Pipeline); err != nil {
		return nil, err
	}
	if err := appendSpecs(&specs, "api", cfg.API); err != nil {
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

func appendRawSpecs(specs *[]resorch.NodeSpec, kind string, entries []rawResourceEntry) error {
	for _, entry := range entries {
		opt := entry.Options
		if opt == nil {
			opt = map[string]any{}
		}
		raw, err := json.Marshal(opt)
		if err != nil {
			return fmt.Errorf("marshal raw options for %s/%s: %w", kind, entry.Name, err)
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

func must(err error) {
	if err != nil {
		panic(err)
	}
}
