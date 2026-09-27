# Project

**What:** a small production-style LLM inference service plus the harness to
measure and optimize it, built as a 4–6 hour take-home and presented in a
30-minute walkthrough with 2–3 slides. The ask and constraints:
[docs/01-problem-statement.md](../docs/01-problem-statement.md). The design:
[docs/02-architecture.md](../docs/02-architecture.md).

**Dates:** build started 2026-09-27. Materials are due before the
walkthrough, which is on 2026-09-30 or 2026-10-01 (slot not yet confirmed).
Slides are drafted 2026-09-29.

## Deliverables

| Deliverable | Status | Where |
|---|---|---|
| Problem statement + architecture | `done` | `docs/01`, `docs/02` |
| Lint, tests standards, CI (lint + test) | `done` | `Makefile`, `.golangci.yml`, `AGENTS.md`, `.github/workflows/ci.yml` |
| Compose: pinned vLLM + CDI GPU, first streaming request | `todo` | `deploy/compose/` |
| Go bench: client, SSE parser, closed-loop runner | `todo` | `cmd/bench`, `internal/` |
| 1 Hz samplers (engine `/metrics`, nvidia-smi) | `todo` | `internal/sampler` |
| Fake server + integration tests | `todo` | `internal/fakeserver` |
| Cross-check against `vllm bench serve` | `todo` | `results/verify/` |
| Baseline sweeps, both profiles | `todo` | `results/` |
| SLO chosen from baseline | `todo` | `docs/03-results.md` |
| Experiment A — batching | `todo` | |
| Experiment B1 — prefix caching | `todo` | |
| Experiment B2 — self-made FP8 checkpoint | `todo` | `scripts/quantize/` |
| Experiment C — FP8 KV cache | `todo` (stretch) | |
| Dockerfile + GHCR publish in CI | `todo` | |
| Kubernetes manifests (kubeconform in CI) | `todo` | `deploy/k8s/` |
| Results write-up + slides | `todo` | `docs/03-results.md`, `docs/slides/` |
| Proxy in front of vLLM | `deferred` (stretch) | |
| EKS run as a second hardware point | `deferred` (stretch) | |

## Open questions

- Does the pinned vLLM image's FP8 path run on sm_120 (consumer Blackwell)?
  Answered by the first-hour smoke test; see [gpu-host.md](gpu-host.md).
- Does llm-compressor install cleanly next to torch 2.13 / CUDA 13, or does
  quantization need its own container?

Related: [gpu-host.md](gpu-host.md) · [prior-art-5090.md](prior-art-5090.md) ·
[walkthrough-topics.md](walkthrough-topics.md)
