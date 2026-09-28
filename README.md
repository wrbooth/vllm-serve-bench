# vllm-serve-bench

A small, production-style LLM inference service on vLLM, with a load generator and telemetry
harness built to answer one question honestly: **how much useful output can this GPU produce while
still meeting a latency objective, and which knob moves that number?**

Take-home exercise for an LLM inference / platform role; time-boxed to 4–6 hours of build time.

- [Problem statement](docs/01-problem-statement.md): the ask, what "done" means, constraints, non-goals.
- [Architecture](docs/02-architecture.md): serving layer, the Go bench, metric definitions,
  workload profiles, experiments, deployment, CI, scale-out design.
- [Results](docs/03-results.md): the tables are generated from `results/` by `bench report`; experiment write-ups land as the runs do.
- [Worklog](docs/worklog.md): what was done in the time box and what was cut.

## Shape

```text
bench (Go: load generator + 1 Hz vLLM /metrics + 1 Hz nvidia-smi samplers + report)
  └── OpenAI-compatible streaming HTTP ──► vllm/vllm-openai:v0.29.0 (pinned) serving Qwen/Qwen2.5-7B-Instruct
```

Two images: upstream vLLM, pinned, and `ghcr.io/wrbooth/vllm-serve-bench` (this repo). Compose runs
it on one RTX 5090; Kubernetes manifests are validated in CI as the path to EKS.

## Quickstart

Lands with the first working sweep. Until then, see the architecture doc for the intended
`docker compose up` / `bench run` flow.

## Status

Scaffolding. Docs first, then the bench, then numbers.

## License

MIT
