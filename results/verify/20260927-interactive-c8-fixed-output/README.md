# Cross-check: interactive profile, c=8, fixed output length

Raw output of `scripts/cross-check.sh <dir> 8`, run on the GPU host on
2026-09-27 against the baseline engine (warm), copied here unedited.

- `a/`: `bench run`, interactive profile, c=8, 10 s warmup, 60 s window.
- `vllm/`: `vllm bench serve` with the same shape, run inside the engine
  container. `command.txt` has its exact arguments, `stdout.txt` its report,
  and `vllm-bench.json` its per-request results (`--save-detailed`).
  `vllm_metrics.csv` and `gpu.csv` come from `bench sample`, run alongside.
- `b/`: `bench run` again, identical to `a/`, to measure run-to-run drift.

Engine argv, image digest, KV pool and host facts are in each `config.json`.
What the comparison showed, and what changed because of it, is in
[wiki/log.md](../../../wiki/log.md) (entry "Cross-check").
