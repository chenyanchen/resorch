// Package demo contains shared demo domain models and storage adapters for examples.
package demo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	kvpkg "github.com/chenyanchen/kv"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// User is a demo entity used by examples.
type User struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Tier string `json:"tier"`
}

// Article is a demo entity used by examples.
type Article struct {
	ID       int64   `json:"id"`
	Title    string  `json:"title"`
	Category string  `json:"category"`
	Score    float64 `json:"score"`
}

// EnsureSchema creates required demo tables.
func EnsureSchema(ctx context.Context, pool *pgxpool.Pool) error {
	if pool == nil {
		return errors.New("nil postgres pool")
	}
	_, err := pool.Exec(ctx, `
CREATE TABLE IF NOT EXISTS users (
    id BIGINT PRIMARY KEY,
    name TEXT NOT NULL,
    tier TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS articles (
    id BIGINT PRIMARY KEY,
    title TEXT NOT NULL,
    category TEXT NOT NULL,
    score DOUBLE PRECISION NOT NULL
);`)
	return err
}

// SeedDemoData inserts deterministic demo data.
func SeedDemoData(ctx context.Context, pool *pgxpool.Pool) error {
	if pool == nil {
		return errors.New("nil postgres pool")
	}
	_, err := pool.Exec(ctx, `
INSERT INTO users (id, name, tier) VALUES
  (1, 'Ada', 'premium'),
  (2, 'Bob', 'basic'),
  (3, 'Cora', 'basic')
ON CONFLICT (id) DO UPDATE SET
  name = EXCLUDED.name,
  tier = EXCLUDED.tier;

INSERT INTO articles (id, title, category, score) VALUES
  (101, 'Go Resource Graphs', 'engineering', 0.86),
  (102, 'Debugging Dependency Cycles', 'engineering', 0.73),
  (103, 'Premium Architecture Weekly', 'premium', 0.91),
  (104, 'Latency Budget Basics', 'engineering', 0.64),
  (105, 'Stable Rollout Playbook', 'operations', 0.77)
ON CONFLICT (id) DO UPDATE SET
  title = EXCLUDED.title,
  category = EXCLUDED.category,
  score = EXCLUDED.score;
`)
	return err
}

// UserDBStore implements kv.KV backed by PostgreSQL.
type UserDBStore struct {
	Pool *pgxpool.Pool
}

func (s *UserDBStore) Get(ctx context.Context, id int64) (User, error) {
	var out User
	err := s.Pool.QueryRow(ctx, `SELECT id, name, tier FROM users WHERE id=$1`, id).Scan(&out.ID, &out.Name, &out.Tier)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, kvpkg.ErrNotFound
	}
	return out, err
}

func (s *UserDBStore) Set(ctx context.Context, id int64, v User) error {
	_, err := s.Pool.Exec(ctx, `
INSERT INTO users (id, name, tier) VALUES ($1, $2, $3)
ON CONFLICT (id) DO UPDATE SET name=EXCLUDED.name, tier=EXCLUDED.tier`, id, v.Name, v.Tier)
	return err
}

func (s *UserDBStore) Del(ctx context.Context, id int64) error {
	_, err := s.Pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, id)
	return err
}

// ArticleDBStore implements kv.KV backed by PostgreSQL.
type ArticleDBStore struct {
	Pool *pgxpool.Pool
}

func (s *ArticleDBStore) Get(ctx context.Context, id int64) (Article, error) {
	var out Article
	err := s.Pool.QueryRow(ctx, `SELECT id, title, category, score FROM articles WHERE id=$1`, id).Scan(
		&out.ID,
		&out.Title,
		&out.Category,
		&out.Score,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Article{}, kvpkg.ErrNotFound
	}
	return out, err
}

func (s *ArticleDBStore) Set(ctx context.Context, id int64, v Article) error {
	_, err := s.Pool.Exec(ctx, `
INSERT INTO articles (id, title, category, score) VALUES ($1, $2, $3, $4)
ON CONFLICT (id) DO UPDATE SET title=EXCLUDED.title, category=EXCLUDED.category, score=EXCLUDED.score`,
		id,
		v.Title,
		v.Category,
		v.Score,
	)
	return err
}

func (s *ArticleDBStore) Del(ctx context.Context, id int64) error {
	_, err := s.Pool.Exec(ctx, `DELETE FROM articles WHERE id=$1`, id)
	return err
}

// ListTopArticles returns top-N articles by score.
func (s *ArticleDBStore) ListTopArticles(ctx context.Context, limit int) ([]Article, error) {
	if limit <= 0 {
		limit = 10
	}
	rows, err := s.Pool.Query(ctx, `
SELECT id, title, category, score
FROM articles
ORDER BY score DESC, id ASC
LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]Article, 0, limit)
	for rows.Next() {
		var item Article
		if err := rows.Scan(&item.ID, &item.Title, &item.Category, &item.Score); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// JSONRedisStore is a typed JSON-backed Redis KV implementation.
type JSONRedisStore[T any] struct {
	Client *redis.Client
	Prefix string
	TTL    time.Duration
}

func (s *JSONRedisStore[T]) key(id int64) string {
	return fmt.Sprintf("%s:%d", s.Prefix, id)
}

func (s *JSONRedisStore[T]) Get(ctx context.Context, id int64) (T, error) {
	var zero T
	raw, err := s.Client.Get(ctx, s.key(id)).Result()
	if errors.Is(err, redis.Nil) {
		return zero, kvpkg.ErrNotFound
	}
	if err != nil {
		return zero, err
	}
	if err := json.Unmarshal([]byte(raw), &zero); err != nil {
		return zero, err
	}
	return zero, nil
}

func (s *JSONRedisStore[T]) Set(ctx context.Context, id int64, v T) error {
	payload, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return s.Client.Set(ctx, s.key(id), payload, s.TTL).Err()
}

func (s *JSONRedisStore[T]) Del(ctx context.Context, id int64) error {
	return s.Client.Del(ctx, s.key(id)).Err()
}
