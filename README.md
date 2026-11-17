# InferLab

A Go control plane for LLM inference. InferLab sits in front of inference backends (Ollama today, vLLM planned), exposes an OpenAI-compatible API, measures how the backends perform, and includes a benchmark tool for load-testing them.

InferLab does not reimplement inference. Model execution stays in the backends, and Go handles the gateway, streaming, routing, scheduling and measurement. The full roadmap is in [`docs/PLAN.md`](docs/PLAN.md).

## Status

| Area | State |
|---|---|
| Gateway: `POST /v1/chat/completions`, with SSE streaming and cancellation | Done |
| Ollama provider behind the `inference.Provider` interface | Done |
| TTFT / TPOT / latency metrics, Prometheus `/metrics`, JSON request logs | Done |
| Benchmark runner and `cmd/bench` CLI | Done |
| Prometheus + Grafana stack and provisioned dashboard (`docker-compose.yml`) | Done |
| Router, scheduler, model registry, vLLM provider, Postgres | Planned |

## Quick start

You need Go 1.27+ and a running [Ollama](https://ollama.com) with a model pulled.

```sh
ollama pull llama3.2
go run ./cmd/server        # listens on :8080
```

Call it like any OpenAI-style chat endpoint:

```sh
curl -N http://localhost:8080/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -d '{"model":"llama3.2","stream":true,
       "messages":[{"role":"user","content":"Say hi in five words."}]}'
```

With `"stream": false` you get a single JSON response. With `"stream": true` you get SSE chunks ending in `data: [DONE]`. The gateway doesn't return a `usage` field.

### Endpoints

| Route | Purpose |
|---|---|
| `POST /v1/chat/completions` | Chat completion, streaming or not |
| `GET /healthz` | Liveness check |
| `GET /metrics` | Prometheus metrics |

### Configuration

All settings are environment variables.

| Variable | Default | Meaning |
|---|---|---|
| `INFERLAB_ADDR` | `:8080` | Listen address |
| `INFERLAB_OLLAMA_URL` | `http://localhost:11434` | Ollama base URL |
| `INFERLAB_SHUTDOWN_TIMEOUT` | `10s` | Graceful shutdown timeout |
| `INFERLAB_LOG_LEVEL` | `info` | Log level |

## Benchmarking

`cmd/bench` fires concurrent requests at a running gateway and reports latency percentiles and throughput.

```sh
go run ./cmd/bench \
  --model llama3.2 \
  --prompt-file benchmarks/prompts/short.txt \
  --concurrency 4 --requests 20 --stream
```

or `make bench MODEL=llama3.2`.

| Flag | Default | Meaning |
|---|---|---|
| `--model` | required | Model name to request |
| `--prompt-file` | required | File containing the prompt |
| `--url` | `http://localhost:8080` | Gateway base URL |
| `--concurrency` | `4` | Number of concurrent workers |
| `--requests` | `20` | Total number of requests |
| `--stream` | off | Use SSE streaming (enables TPOT) |
| `--out` | `benchmarks/results` | Directory for the JSON result |

It prints a table of P50/P95/P99 for TTFT, TPOT and latency, plus requests/s and tokens/s. It also writes the same data, with per-request samples, to `benchmarks/results/bench-<timestamp>.json`.

Notes on the numbers:

- **TTFT** is measured on the client, so it includes network time.
- **TPOT** is reported for streaming runs only.
- **Tokens** are counted as content chunks, which approximates backend tokens.
- **Percentiles** cover successful requests only. Failures are counted separately.
- **Ctrl-C** stops early and still reports the requests that ran.

## Experiments

`go run ./cmd/bench --config <suite.json>` runs a whole experiment: a warm-up request, then each run in the file, with results and a markdown table written to `benchmarks/results/<suite>/`. Suites live in `benchmarks/`; write-ups are in `docs/experiments/`.

- [01: prefill vs decode, sequential vs concurrent](docs/experiments/01-prefill-vs-decode.md)

## Monitoring

`docker compose up -d` starts Prometheus (<http://localhost:9090>) and Grafana (<http://localhost:3000>, login `admin` / `admin`). Prometheus scrapes the gateway's `/metrics` every 5s at `host.docker.internal:8080`, so run the server on the host with the default `:8080` address.

Grafana provisions the **InferLab Gateway** dashboard automatically: TTFT P95, TPOT (P50/P95/mean), requests/sec by outcome, and in-flight requests, with a model selector. Run `make bench` to generate load.

## Development

```sh
make test     # go vet ./... && go test -race ./...
make build    # builds bin/inferlab
```

Tests use a fake provider (`internal/inference/fake`) and `httptest`, so they don't need Ollama.

### Layout

```
cmd/server        gateway entry point
cmd/bench         benchmark CLI
internal/
  gateway         HTTP server, chat handler, SSE streaming
  inference       Provider interface and the Ollama implementation
  observability   TTFT/TPOT timing and Prometheus metrics
  benchmark       runner, percentiles and reports
  config          environment configuration
benchmarks/       sample prompts and results
docs/PLAN.md      roadmap
```

### Conventions

Standard library first, with `context.Context` passed everywhere and no global state. Providers sit behind interfaces, and tests are table-driven. See [`CLAUDE.md`](CLAUDE.md) for the working agreement.
