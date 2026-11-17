# InferLab: Go-powered LLM inference platform

Recommended architecture: Go control plane + vLLM/Ollama inference backends.

Use Go for the entire control plane and backend of InferLab, and keep Python-based inference engines where they make sense. The goal is a high-concurrency backend that orchestrates GPU workloads rather than just wrapping an LLM API.

## 1. Technology stack

| Component | Technology | Responsibility |
|---|---|---|
| Backend | Go | API gateway and orchestration |
| HTTP framework | Gin or Chi | REST APIs |
| Streaming | SSE / WebSockets | Stream generated tokens |
| Inference | vLLM | Model execution |
| Local inference | Ollama | Local model experimentation |
| Cache | Redis | Metadata, routing and cache coordination |
| Database | PostgreSQL | Experiments, models and benchmark history |
| Messaging | NATS | Async jobs and worker events |
| Monitoring | Prometheus + Grafana | Metrics and dashboards |
| Tracing | OpenTelemetry | Request tracing |
| Load testing | k6 | Concurrent request simulation |
| Deployment | Docker + Kubernetes | Container orchestration |
| Frontend | React + TypeScript | Inference playground |

Why Go? It is particularly suitable for:

- Handling thousands of concurrent API requests.
- Managing goroutines for streaming and request orchestration.
- Building worker pools and job schedulers.
- Managing distributed services.
- Implementing rate limiting and request queues.
- Building lightweight infrastructure services.

## 2. Architecture

**Layer 1: Go control plane (the main engineering contribution)**

- API gateway
- Authentication and rate limiting
- Model registry
- Request router
- Queue manager
- Worker manager
- Autoscaling controller
- Benchmark orchestration

**Layer 2: Inference data plane**

- vLLM model servers
- Ollama local model servers
- GPU workers
- KV cache management
- Continuous batching
- Speculative decoding
- Quantization

Important: vLLM manages the actual GPU execution, KV cache allocation, PagedAttention and continuous batching. The Go services orchestrate and configure these capabilities rather than reimplementing them.

**Layer 3: Observability**

- TTFT
- TPOT
- Throughput
- P50/P95 latency
- GPU utilization
- KV cache utilization
- Queue depth
- Cost per token

## 3. What to build in Go

**1. API Gateway**

- Accept inference requests.
- Authenticate users.
- Apply rate limits.
- Support streaming responses.
- Forward requests to appropriate model workers.

**2. Model Router**

- Route simple prompts to smaller models.
- Route complex prompts to larger models.
- Consider worker availability and queue length.
- Implement fallback when a worker becomes unavailable.

**3. Worker Scheduler**

- Maintain worker health.
- Track active requests.
- Manage request queues.
- Distribute workloads.
- Implement cache-aware routing.

**4. Benchmark Engine**

- Generate concurrent inference requests.
- Measure TTFT and TPOT.
- Compare different inference configurations.
- Store experiment results.
- Generate performance reports.

**5. Autoscaling Controller**

- Monitor queue depth.
- Monitor GPU utilization.
- Monitor active requests.
- Scale inference workers based on workload.
- Scale down when demand decreases.

## 4. Go project structure

Start with a modular monolith rather than immediately creating five microservices.

```
inferlab/
├── cmd/
│   └── server/
│       └── main.go
├── internal/
│   ├── gateway/
│   │   ├── handlers.go
│   │   ├── middleware.go
│   │   └── streaming.go
│   ├── router/
│   │   ├── router.go
│   │   └── strategy.go
│   ├── scheduler/
│   │   ├── scheduler.go
│   │   ├── queue.go
│   │   └── worker_pool.go
│   ├── inference/
│   │   ├── provider.go
│   │   ├── vllm.go
│   │   └── ollama.go
│   ├── benchmark/
│   │   ├── runner.go
│   │   ├── metrics.go
│   │   ├── report.go
│   │   └── experiments.go
│   ├── autoscaler/
│   │   └── controller.go
│   ├── observability/
│   │   ├── prometheus.go
│   │   └── tracing.go
│   └── registry/
│       └── models.go
├── deployments/
│   ├── docker/
│   └── kubernetes/
├── frontend/
├── benchmarks/          (prompts/, results/)
├── configs/
├── docker-compose.yml
└── README.md
```

## 5. Experiments to demonstrate inference concepts

| Experiment | What you'll implement or measure |
|---|---|
| Prefill vs Decode | Compare latency at different prompt lengths |
| KV Cache | Observe memory usage as context grows |
| Prefix Caching | Compare repeated vs unique prompts |
| Continuous Batching | Compare sequential and concurrent workloads |
| Chunked Prefill | Measure interference from long prompts |
| Quantization | Compare FP16, INT8 and INT4 |
| Speculative Decoding | Compare draft and target model configurations |
| Model Routing | Compare single-model and cascaded serving |
| Parallelism | Compare supported multi-GPU configurations |
| Autoscaling | Compare static and dynamic worker allocation |
| Cost Optimization | Calculate cost per million tokens |
| Diffusion | Benchmark a separate image-generation worker |

Useful addition: build an experiment configuration system so users can run the same workload against different model servers and compare results.

## 6. First two weeks

Don't start with autoscaling or distributed inference. Build the foundation first.

**Week 1: Go inference gateway**

- [ ] Set up Go project with Chi
- [ ] Run an open-source model using Ollama
- [ ] Build /v1/chat/completions endpoint
- [ ] Implement streaming using SSE
- [ ] Add request cancellation using context.Context
- [ ] Record TTFT, TPOT and total latency

**Week 2: Benchmarking and observability**

- [x] Build concurrent request generator (`internal/benchmark`, `cmd/bench`; results in `benchmarks/results/`)
- [ ] Implement TTFT and TPOT calculations
- [x] Integrate Prometheus metrics
- [x] Create Grafana dashboard (`docker-compose.yml`, `deploy/`)
- [ ] Test different prompt lengths
- [ ] Compare sequential vs concurrent requests

## 7. Learning in public (X/Twitter)

Make posts about the engineering decisions, not just announcements.

Example launch post:

> Starting something new: #BuildingInferLab
>
> I'm building a mini LLM inference platform, and decided to use Go for its backend.
>
> The idea is to explore what happens beyond a simple LLM API call:
>
> - Request scheduling and concurrency
> - Streaming token generation
> - Model routing
> - Worker orchestration
> - Inference benchmarking
> - GPU utilization and cost optimization
>
> Using Go for the control plane and vLLM for actual model execution.
>
> Going to document the architecture, implementation and experiments as I build.

Advice: make Go the centrepiece of the backend, not Python. Use Python only where the ML ecosystem makes it necessary, such as model execution, custom benchmarking or diffusion pipelines. That combination demonstrates Go backend engineering, distributed systems, concurrency, AI infrastructure and system design in one project.

First milestone: a Go API gateway that streams responses from a locally running Ollama model and measures TTFT and TPOT. Once that works, it's the foundation for everything else.
