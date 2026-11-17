# InferLab

Go control plane (gateway, router, scheduler, benchmark engine) that orchestrates LLM inference backends (Ollama first, vLLM later). We do NOT reimplement inference. Full plan in `docs/PLAN.md`.

## Stack

- Go 1.27.3, chi router, `net/http`, SSE for streaming, Prometheus client, Postgres later.
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

### Milestone loop

1. Enter plan mode (Shift+Tab) and ask for a plan for the milestone.
2. Read the plan and fix anything that's off before approving.
3. Only then implement; do not write code before the plan is approved.
4. Run `/clear` between milestones to keep context clean. `CLAUDE.md` and `docs/PLAN.md` are the only context carried over, so keep them current.

## Commands

- `go run ./cmd/server`
- `go test -race ./...`
- `make bench` (needs a running server; `MODEL=<name>` to override)
- `docker compose up -d` (Prometheus :9090, Grafana :3000 admin/admin; gateway must run on the host at :8080)
- `go run ./cmd/bench --config benchmarks/<suite>.json` (runs an experiment suite; results in `benchmarks/results/<suite>/`)
- `go run ./cmd/bench --model <name> --prompt-file <file> [--url --concurrency --requests --stream --out]`
