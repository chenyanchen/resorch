# http_user_article_kv

A runnable HTTP service example built with `resorch`.

It demonstrates a three-layer KV stack for two domains (user and article):

1. In-memory cache
2. Redis
3. PostgreSQL

`github.com/chenyanchen/kv` is used as the KV abstraction and composition layer.

## Prerequisites

From repository root, enter examples module and start local dependencies:

```bash
cd examples
docker compose -f ./docker-compose.yml up -d
```

## Run

```bash
go run ./http_user_article_kv
```

## Try API

```bash
curl "http://127.0.0.1:18080/v1/summary?user_id=1&article_id=101"
```
