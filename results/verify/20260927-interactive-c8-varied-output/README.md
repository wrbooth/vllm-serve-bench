# Cross-check: interactive profile, c=8, varied output, cache reset

Raw output of `scripts/cross-check.sh <dir> 8` (commit `ca766db`'s bench and
script), run on the GPU host on 2026-09-27 against the baseline engine with
`VLLM_SERVER_DEV_MODE=1`, copied here unedited. This is the comparison that
counts. Both clients start from an emptied prefix cache: `bench run` resets
before its level, and the script resets before vLLM's client
(`vllm/reset.json`). Both draw output lengths uniformly from 96–160 tokens.

- `a/`, `b/`: `bench run`, interactive profile, c=8, 10 s warmup, 60 s window.
- `vllm/`: `vllm bench serve`. `command.txt` has its exact arguments,
  `stdout.txt` its report, and `vllm-bench.json` its per-request results.
  `vllm_metrics.csv` and `gpu.csv` come from `bench sample`, run alongside.

The comparison and how each remaining difference is accounted for are in
[wiki/log.md](../../../wiki/log.md), in the entry "Cross-check passes".
