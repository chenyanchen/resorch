# 09_production_like_sanitized

A production-like, sanitized configuration example inspired by a large real-world pipeline config.

This example keeps the shape of a complex online setup (stage/pipeline/logic decomposition), while replacing
external dependencies with local infrastructure and hardcoded service implementations.

## What is simplified and sanitized

- All Redis endpoints are local.
- Database is local PostgreSQL.
- External services are hardcoded mocks (`profileService`, `policyService`, `scoreService`).
- API surface is reduced to one endpoint.

## Prerequisites

From repository root, enter examples module and start local dependencies:

```bash
cd examples
docker compose -f ./docker-compose.yml up -d
```

## Run

```bash
go run ./09_production_like_sanitized -config ./09_production_like_sanitized/config.sanitized.yaml
```

## Try API

```bash
curl "http://127.0.0.1:18081/v1/recommend?user_id=1"
```
