# Problem statement

## The ask

Build and optimize a small, production-style open-source LLM inference service, and show the
whole path:

    model/workload → deployment → benchmark → optimization → production recommendation

This is a time-boxed take-home (4–6 hours of build time) for an LLM inference / platform role.
The deliverable is a repository plus a 30-minute walkthrough. The evaluators have said what they
grade on: ML systems understanding, experimental design, reproducibility, inference optimization,
software quality, observability, and engineering judgment. Their own framing of the goal is the
one I am building to:

> The goal is not maximum GPU utilization; it is better useful output while meeting service
> requirements.

## What "done" looks like

1. A public Hugging Face model served through a repeatable, OpenAI-compatible HTTP interface.
2. At least two workload profiles with different shapes (latency-sensitive interactive traffic
   vs. batch-style throughput traffic), driven by a load generator I wrote and can defend.
3. A concurrency sweep per profile that shows where throughput improves, where it plateaus, and
   where tail latency becomes unacceptable, measured as requests/s, output tokens/s, p50/p95/p99
   end-to-end latency, TTFT, TPOT, input/output token lengths, concurrency, GPU memory and
   utilization, and KV-cache metrics from the engine.
4. A stated production objective, chosen from the baseline data rather than declared up front,
   in the form *maximize output tokens/s subject to a latency SLO*.
5. One or two optimizations, each written up as
   Baseline → Hypothesis → Change → Benchmark → Result → Trade-off, with the raw results committed.
6. Engineering hygiene that would survive a second engineer: Dockerfile, Compose (and Kubernetes
   manifests), tests, README, raw results, and GitHub Actions CI that tests, builds, and publishes
   the application image to GHCR.
7. A scale-out design: what changes for larger models or multiple GPUs, and why.

## Production objective (form chosen first; values from the baseline)

    maximize   output tokens/s per profile
    subject to interactive profile: TTFT p95 ≤ 100 ms  and  TPOT p95 ≤ 25 ms
               throughput profile:  E2E p95 ≤ 15 s

The form was fixed before any data and the thresholds were chosen after the baseline sweep, so
that they sit at a real knee in the data. Declaring the SLO before measuring would let me pick
numbers the baseline already meets, which proves nothing. The bounds changed from the planned
form once the data was in (TPOT instead of end-to-end for interactive, p95 instead of p99 for
throughput); the values are in [`docs/slo.json`](slo.json) and the reasoning, with the rejected
alternatives, is in the two SLO entries of [`wiki/log.md`](../wiki/log.md).

## Constraints and honesty

- **Hardware is one RTX 5090** (Blackwell, 32 GB GDDR7, ~1.79 TB/s, PCIe 5.0, no NVLink) in my
  home lab. It is a consumer card: no NVLink, no DCGM profiling counters, one GPU. Tensor
  parallelism cannot be exercised here and I will not pretend otherwise; TP, PCIe vs NVLink, and
  multi-node placement are covered in the scale-out design only; no multi-GPU run was done. The
  load generator and engine sampler are independent of GPU count, but the GPU sampler reads
  GPU 0 only (`nvidia-smi --id=0`).
- **Time box is real.** Time is measured by `make timesheet` from transcript and commit
  timestamps; `docs/worklog.md` records the sessions and what was cut and why. Anything listed under "stretch" in the architecture doc was optional from the
  start.
- **The engine is upstream vLLM at a pinned version.** I am not writing a serving engine; I am
  operating one well and measuring it honestly. The application image is the load generator,
  telemetry sampler, and report tooling.
- **Numbers come from the engine's own counters where they exist** (`/metrics`: KV-cache usage,
  preemptions, prefix-cache hits, running/waiting queues) and from `nvidia-smi` sampled at 1 Hz
  for the GPU side. Client-side timings are cross-checked once against vLLM's bundled benchmark
  client so that a bug in my load generator cannot silently become a finding.

## Non-goals

- Model quality evaluation beyond a sanity check. Quantization experiments include an
  output-diff spot check on a small fixed prompt set; no accuracy benchmark was run or cited. A
  real rollout would gate on an eval set, and the write-up says so.
- Multi-model routing, auth, rate limiting, or a gateway. A thin proxy in front of vLLM is a
  stretch goal, not part of the core ask.
- Slurm, Ray, or a training-side story.
- Kubernetes as the primary runtime. Compose is what runs on the lab box; the Kubernetes
  manifests are validated in CI and are the path to EKS; they were not applied to a cluster.

## Deliverables map

| Requirement | Where |
|---|---|
| Application / client source | `cmd/bench`, `internal/` (Go) |
| Benchmark / load generator | `bench run` subcommand |
| Dockerfile | `Dockerfile` |
| Compose / Kubernetes | `deploy/compose/`, `deploy/k8s/` |
| Tests | `go test ./...` (unit + fake-server integration) |
| README | `README.md` |
| Raw baseline / optimized results | `results/<run-id>/` (committed) |
| CI/CD → GHCR | `.github/workflows/ci.yml` |
| Write-up | `docs/03-results.md` and the README; slides are shared with the walkthrough, not committed |
