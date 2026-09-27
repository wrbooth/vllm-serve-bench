# Walkthrough topics

The walkthrough must show: model/workload and architecture; benchmark
methodology; saturation behaviour; optimization and measured impact; how the
design scales to larger models or multiple GPUs. The brief also lists terms to
be ready to discuss. Each is mapped to where this repo answers it, so gaps are
visible before the slides are drafted.

| Topic | Where it is answered | Status |
|---|---|---|
| Model / workload / architecture | [docs/02-architecture.md](../docs/02-architecture.md) overview, profiles | `in-progress` |
| Benchmark methodology | architecture: load model, metric definitions, cross-check | `in-progress` |
| Saturation behaviour | baseline sweeps, `docs/03-results.md` | `todo` |
| Optimization and measured impact | experiments A, B1, B2 | `todo` |
| Scaling to larger models / multi-GPU | architecture: scale-out design | `in-progress` (design only; no multi-GPU hardware) |
| **TTFT** | metric definitions; B1 moves it | `todo` (needs data) |
| **TPOT** | metric definitions; B2 moves it at low concurrency | `todo` (needs data) |
| **KV cache** | KV arithmetic table, startup-log capture, `kv_cache_usage` sampling, preemption counts | `todo` (needs data) |
| **Batching** | experiment A; continuous batching explained from the sweep curve | `todo` |
| **Concurrency** | the sweep itself; closed vs open loop trade-off | `in-progress` |
| **Tensor parallelism** | scale-out design; why not justified for 7B on one GPU | `in-progress` (design only) |
| **PCIe vs NVLink** | scale-out design; the 5090 is a PCIe-only card | `in-progress` (design only) |
| **Production monitoring** | samplers; what to alert on (queue depth, preemptions, TTFT/TPOT SLOs, goodput) rather than GPU utilization | `in-progress` |

Honesty line for the design-only rows: say what would be measured and with
what (`nvidia-smi topo -m`, `nccl-tests` all-reduce bus bandwidth in-node vs
cross-node, `NCCL_DEBUG=INFO` for the transport), and that it was not run
here.

Related: [project.md](project.md) · [prior-art-5090.md](prior-art-5090.md)
