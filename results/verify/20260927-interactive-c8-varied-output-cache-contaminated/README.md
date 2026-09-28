# Superseded: cross-check with varied output, prefix cache not reset

Raw output of `scripts/cross-check.sh <dir> 8` after output lengths were
spread to 96–160 tokens, copied here unedited. **Not a valid comparison.**
Our runs reused seed 1 on a warm engine, so their prompts were still in the
prefix cache from earlier runs: the telemetry shows ~1.5 uncached tokens per
request for ours against 106 for vLLM's client. Superseded by
[20260927-interactive-c8-varied-output](../20260927-interactive-c8-varied-output/),
run after `bench run` began resetting the cache before every level. Kept
because it is the evidence for that fix; see [wiki/log.md](../../../wiki/log.md).
