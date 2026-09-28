# GPU host (RTX 5090)

The only hardware the reported numbers come from. Checked 2026-09-27.

## The card

- NVIDIA GeForce RTX 5090: Blackwell (sm_120), 32 GB GDDR7 (32,607 MiB
  reported), ~1.79 TB/s memory bandwidth, PCIe 5.0.
- Driver 595.58.03.
- **Not available on this card:** NVLink (single consumer GPU), DCGM
  profiling counters (consumer SKUs), tensor parallelism (one GPU). GPU-side
  telemetry therefore comes from `nvidia-smi` at 1 Hz; engine-side telemetry
  from vLLM's `/metrics`.
- Supports FP8 and NVFP4 natively (Blackwell), which is why FP8 is the
  quantization experiment rather than INT8/AWQ.

## Access

- NixOS host on the owner's LAN, reached over SSH. The address lives in the
  gitignored `.env.local` as `GPU_HOST`; it is never committed.
- Docker 29.4 with **CDI** GPU injection: the CDI spec from
  nvidia-container-toolkit is present, but no `nvidia` runtime is registered.
  So `--gpus all` is the wrong spelling here; use
  `--device nvidia.com/gpu=all`, and in Compose `devices: ["nvidia.com/gpu=all"]`.
  The `{driver: cdi, device_ids: [...]}` form that the docs first gave belongs
  under `deploy.resources.reservations.devices`; under a service's `devices`,
  Compose 5.0.2 rejects it ("missing a mount target").
  Verified: `docker run --rm --device nvidia.com/gpu=all ubuntu:24.04 nvidia-smi`
  sees the card.
- No Python on the host itself. Host-side Python (llm-compressor for B2, any
  helper script) runs in the GPU-enabled distrobox `ubuntu-gpu-v2`, at the owner's
  direction: Python 3.12, sees the card, shares the home directory and so the
  HF cache. No `uv` or `pip` in it yet. The engine does not use it; it runs
  under Docker Compose.
- The compose directory is rsynced to the host rather than checked out there;
  the host copy gets its own gitignored `.env.local` (`HF_CACHE`).
- No Go toolchain on the host. The bench runs from its container image, or is
  cross-compiled (`GOOS=linux GOARCH=amd64 make build`) and copied over.

## What is cached there

- Image `vllm/vllm-openai:v0.29.0` (pulled 2026-09-27; the same vLLM version
  that already ran on this card in a venv, with torch 2.13.0+cu130).
- `Qwen/Qwen2.5-7B-Instruct` (15 GB) and `Qwen/Qwen2.5-0.5B-Instruct` in the
  host's Hugging Face cache, which the Compose file mounts read-only.
- An HF token is present on the host; whether it has write scope (needed to
  publish the B2 checkpoint) is unchecked.

## Engine startup, measured

Pinned image, 2026-09-27, `--gpu-memory-utilization 0.90 --max-model-len 8192`.
Startup logs: [raw/engine-logs/](../raw/engine-logs/).

| Config | Weights | KV pool | KV tokens | Ready after |
|---|---|---|---|---|
| 0.5B, bf16 (dev) | n/a | 26.13 GiB | 2,283,184 | 132 s (cold compile cache) |
| 7B bf16, prefix caching on (**the baseline**) | 14.29 GiB | 11.17 GiB | 209,120 | 116 s |
| 7B bf16, prefix caching off (B1) | 14.29 GiB | 12.91 GiB | 241,680 | 108 s |
| 7B online FP8 (`--quantization fp8`) | 8.2 GiB | 17.24 GiB | 322,880 | 127 s |

- **FP8 runs on sm_120** in this image: vLLM selects
  `CutlassFP8ScaledMMLinearKernel`, and the output is coherent (one
  temperature-0 prompt checked by eye). B2 is not blocked on kernels.
- **Prefix caching changed the KV pool by 1.74 GiB** with nothing else
  changed. Cause not investigated. The baseline (caching on) is the one to
  size against, and B1 reports the difference as part of its trade-off.
- The first SSE chunk is a role-only delta with empty content, so TTFT has to
  be timed at the first non-empty content.
- The model's `generation_config.json` overrides vLLM's default sampling
  (temperature 0.7, top_k 20, repetition_penalty 1.1), so the bench must set
  sampling parameters explicitly.
- The chat template adds a system prompt of about 30 tokens: "Say hi."
  counted as 32 prompt tokens.

The startup-log figures are measurements. The design's
[KV arithmetic](../docs/02-architecture.md#kv-cache-arithmetic-why-the-profiles-are-sized-the-way-they-are)
is the prediction: about 215k tokens for bf16 and about 350k for FP8 weights.

## Sharing the card

The card normally serves another project's always-on vLLM engine (a 27B NVFP4
model that takes ~31 GB). It must be **stopped before any benchmark run**, since
two engines cannot share 32 GB and even an idle co-tenant distorts memory and
utilization readings, and **restarted when the build is done**. It was stopped
on 2026-09-27; it runs under a restart-loop wrapper, so stop the wrapper first
or the engine comes back.

Related: [prior-art-5090.md](prior-art-5090.md) · [project.md](project.md)
