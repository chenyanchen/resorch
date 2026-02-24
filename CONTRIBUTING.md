# Contributing

## Development

- Use Go `1.26+`
- Run full checks before opening a PR:

```bash
go test ./...
go test -race ./...
go vet ./...
go build ./...
```

## Pull Requests

- Keep changes focused and well-tested
- Add or update tests for behavior changes
- Keep public comments/docs/examples in English
