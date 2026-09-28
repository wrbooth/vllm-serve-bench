# Engine startup logs

Provenance: `docker logs vsb-vllm` on the GPU host, 2026-09-27, captured
right after each start via `deploy/compose/engine.sh up`. Image
`vllm/vllm-openai:v0.29.0` (digest in `deploy/compose/.env`). Immutable, like
everything in `raw/`.

| File | Config | Argv tail |
|---|---|---|
| `2026-09-27-0.5b.log` | `Qwen/Qwen2.5-0.5B-Instruct` | `--model … --gpu-memory-utilization 0.90 --max-model-len 8192` |
| `2026-09-27-7b-baseline.log` | 7B, prefix caching on: **the baseline config** | same flags |
| `2026-09-27-7b-fp8-online.log` | 7B, smoke test | same flags + `--quantization fp8` |
| `2026-09-27-7b-baseline-no-prefix-cache.log` | 7B, prefix caching off: the B1 config, briefly the baseline | positional model, same flags + `--no-enable-prefix-caching` |

The first three were started with `--model`, before the switch to the
positional model argument.
