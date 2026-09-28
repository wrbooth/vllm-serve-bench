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
| Compose: pinned vLLM + CDI GPU, first streaming request | `done` | `deploy/compose/` |
| Go bench: client, SSE parser, closed-loop runner | `done` | `internal/openai`, `internal/loadgen` |
| Seeded prompts with a predicted token count; `bench prompts verify` | `done` (verified on the engine) | `internal/prompts` |
| `bench run`: sweep, readiness wait, run directory | `done` | `cmd/bench`, `internal/results` |
| 1 Hz samplers (engine `/metrics`, nvidia-smi) | `in-progress` | `internal/sampler`, `bench sample`; wiring into `bench run` next |
| Fake server + integration tests | `done` | `internal/fakeserver` |
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

## Build order

Remaining work, in order. Time spent is tracked by session in
[docs/worklog.md](../docs/worklog.md).

1. Compose + CDI engine; first streaming request on the 0.5B model; a
   five-minute online-FP8 smoke test on the 7B (informs B2, not reported).
2. Go OpenAI streaming client + SSE parser + fake server + tests; closed-loop
   runner.
3. 1 Hz samplers (engine `/metrics`, nvidia-smi); cross-check against
   `vllm bench serve`.
4. Baseline sweeps for both profiles (write the README while they run); choose
   the SLO from the data.
5. Experiments in order: A batching → B1 prefix caching → B2 llm-compressor
   FP8 dynamic checkpoint (first check that the HF token has write scope) → C
   FP8 KV cache if time allows.
6. Dockerfile + GHCR publish job; Kubernetes manifests + kubeconform in CI.

Stretch, only once the above is done: proxy in front of vLLM; EKS run as a
second hardware point.

## Open questions

- ~~Does the pinned vLLM image's FP8 path run on sm_120?~~ Yes (CUTLASS FP8
  kernel); see [gpu-host.md](gpu-host.md#engine-startup-measured).
- ~~Throughput profile sizing~~: the sweep now extends to 192 and 256 (see
  [log.md](log.md)).
- Does llm-compressor install cleanly next to torch 2.13 / CUDA 13, or does
  quantization need its own container? It will run in the `ubuntu-gpu-v2`
  distrobox either way ([gpu-host.md](gpu-host.md#access)).

Related: [gpu-host.md](gpu-host.md) · [prior-art-5090.md](prior-art-5090.md) ·
[walkthrough-topics.md](walkthrough-topics.md)
