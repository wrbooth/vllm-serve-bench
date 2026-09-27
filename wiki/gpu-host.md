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
  `--device nvidia.com/gpu=all`, and in Compose
  `devices: [{driver: cdi, device_ids: ["nvidia.com/gpu=all"]}]`.
  Verified: `docker run --rm --device nvidia.com/gpu=all ubuntu:24.04 nvidia-smi`
  sees the card.
- No Go toolchain on the host. The bench runs from its container image, or is
  cross-compiled (`GOOS=linux GOARCH=amd64 make build`) and copied over.

## What is cached there

- Image `vllm/vllm-openai:v0.29.0` (pulled 2026-09-27; the same vLLM version
  that already ran on this card in a venv, with torch 2.13.0+cu130).
- `Qwen/Qwen2.5-7B-Instruct` (15 GB) and `Qwen/Qwen2.5-0.5B-Instruct` in the
  host's Hugging Face cache, which the Compose file mounts read-only.
- An HF token is present on the host; whether it has write scope (needed to
  publish the B2 checkpoint) is unchecked.

## Sharing the card

The card normally serves another project's always-on vLLM engine (a 27B NVFP4
model that takes ~31 GB). It must be **stopped before any benchmark run**, since
two engines cannot share 32 GB and even an idle co-tenant distorts memory and
utilization readings, and **restarted when the build is done**. It was stopped
on 2026-09-27; it runs under a restart-loop wrapper, so stop the wrapper first
or the engine comes back.

Related: [prior-art-5090.md](prior-art-5090.md) · [project.md](project.md)
