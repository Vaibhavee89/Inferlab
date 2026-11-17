# InferLab

Go control plane (gateway, router, scheduler, benchmark engine) that orchestrates LLM inference backends (Ollama first, vLLM later). We do NOT reimplement inference. Full plan in `docs/PLAN.md`.

## Stack

- Go 1.22+, chi router, `net/http`, SSE for streaming, Prometheus client, Postgres later.
- Standard library first, minimal dependencies.

## Structure

Modular monolith: `cmd/server/main.go` + `internal/{gateway,router,scheduler,inference,benchmark,observability,registry}`.

## Conventions

- Pass `context.Context` everywhere and always honor cancellation.
- No global state.
- Use interfaces for providers (`inference.Provider`).
- Table-driven tests.
- `go vet` and `go test -race ./...` must pass before any commit.

## Workflow

- Work one milestone at a time.
- Write tests with the code.
- Small commits with clear messages.
- Never add features outside the current milestone.

## Commands

- `go run ./cmd/server`
- `go test -race ./...`
- `make bench`
