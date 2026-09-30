# Walkthrough topics

The walkthrough must show: model/workload and architecture; benchmark
methodology; saturation behaviour; optimization and measured impact; how the
design scales to larger models or multiple GPUs. The brief also lists terms to
be ready to discuss. Each is mapped to where this repo answers it, so gaps are
visible before the slides are drafted.

| Topic | Where it is answered | Status |
|---|---|---|
| Model / workload / architecture | [docs/02 overview](../docs/02-architecture.md#overview), [workload profiles](../docs/02-architecture.md#workload-profiles) | `done` |
| Benchmark methodology | docs/02: [load model](../docs/02-architecture.md#load-model), [metric definitions](../docs/02-architecture.md#metric-definitions-client-side-per-request), [cross-check](../docs/02-architecture.md#cross-check); README "How the numbers are kept honest" | `done` |
| Saturation behaviour | baselines: [interactive](../docs/03-results.md#baseline-interactive), [throughput](../docs/03-results.md#baseline-throughput); the engine tables show queueing and preemption past the knee | `done` (tables; charts pending) |
| Optimization and measured impact | [headline](../docs/03-results.md#headline); experiments [A](../docs/03-results.md#experiment-a-scheduler-budgets-batching), [B1](../docs/03-results.md#experiment-b1-prefix-caching-off), [B2](../docs/03-results.md#experiment-b2-self-made-fp8-checkpoint) | `done` |
| Scaling to larger models / multi-GPU | [docs/02 scale-out design](../docs/02-architecture.md#scale-out-design-presentation-material-not-built) | `done` (design only; no multi-GPU hardware) |
| **TTFT** | metric definitions; B1 raises it at every level; A lowers it for long prompts under load | `done` |
| **TPOT** | metric definitions; the interactive SLO's second bound; B2 lowers it at low concurrency and raises it at the top throughput level | `done` |
| **KV cache** | [KV arithmetic](../docs/02-architecture.md#kv-cache-arithmetic-why-the-profiles-are-sized-the-way-they-are) (prediction vs the engine's pool), engine tables (KV usage, preemptions in the throughput profile past its knee), B2's larger pool, A's smaller one | `done` |
| **Batching** | experiment A (scheduler token budget, chunked prefill); the sweep curves show continuous batching filling up | `done` |
| **Concurrency** | the sweeps themselves; closed vs open loop in [load model](../docs/02-architecture.md#load-model) | `done` |
| **Tensor parallelism** | scale-out design; why it is not justified for a 7B on one GPU | `done` (design only) |
| **PCIe vs NVLink** | scale-out design; the 5090 is a PCIe-only card | `done` (design only) |
| **Production monitoring** | [telemetry samplers](../docs/02-architecture.md#telemetry-samplers) (built); what to alert on (queue depth, preemptions, TTFT/TPOT SLOs, goodput) rather than GPU utilization | `done` (samplers built; alerting is design only) |

Honesty line for the design-only rows: say what would be measured and with
what (`nvidia-smi topo -m`, `nccl-tests` all-reduce bus bandwidth in-node vs
cross-node, `NCCL_DEBUG=INFO` for the transport), and that it was not run
here.

Related: [project.md](project.md) · [prior-art-5090.md](prior-art-5090.md)
