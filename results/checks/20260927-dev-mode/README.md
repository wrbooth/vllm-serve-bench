# Check: does VLLM_SERVER_DEV_MODE=1 change engine performance?

Run on the GPU host on 2026-09-27, copied here unedited. For each mode, the
engine was started fresh (`VLLM_SERVER_DEV_MODE=<m> ./engine.sh up baseline
Qwen/Qwen2.5-7B-Instruct`), which empties the prefix cache without the reset
endpoint. It then ran `bench run --profile interactive --concurrency 8
--warmup 10s --duration 30s --seed 101 --reset-prefix-cache=false`: the same
prompts both times, from a seed no earlier run had used.

- `engine-devmode-0.log`, `engine-devmode-1.log`: engine startup logs.
- `devmode-0/`, `devmode-1/`: the run directories. Their `vllm_metrics.csv`
  shows the prefix-cache queries and hits per request.

The reading is in [wiki/log.md](../../../wiki/log.md), in the entry "Dev mode
has no measurable cost".
