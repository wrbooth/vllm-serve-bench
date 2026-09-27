# Prior art on this card

Before this repo, the owner ran vLLM on the same RTX 5090 for weeks as the
local engine behind an agent-orchestration project (private repo; tuning logs
dated 2026-09-03 through 2026-09-23). Different model (a 27B hybrid
attention/DeltaNet model in NVFP4) and a very different workload (long-context
agent sessions), so **none of these numbers are results for this repo**. They
are here because they predict where this build's surprises will be, and
because they are the owner's own measured experience with the knobs the
walkthrough asks about.

## Lessons that carry over

- **The startup log is the oracle for KV capacity, not arithmetic.** vLLM
  prints `GPU KV cache size: N tokens` and `Maximum concurrency for L tokens
  per request: X`. On that project the hand estimate was off by ~2x (the hybrid
  architecture padded KV blocks and the "4-bit" checkpoint kept large layers in
  bf16). This repo captures the log into each run's `config.json` and treats
  the arithmetic in [docs/02-architecture.md](../docs/02-architecture.md) as a
  prediction to check.
- **vLLM takes the last occurrence of a repeated flag.** A wrapper script
  appended a default `--max-num-batched-tokens` after the tuned value and
  silently overrode it for weeks. Hence the rule: record the running engine's
  argv, not the config file ([AGENTS.md](../AGENTS.md)).
- **Prefix caching can be off by default.** vLLM disabled it for the hybrid
  model; the hit counter sat at zero on identical prompts until it was enabled
  explicitly. For Qwen2.5 on vLLM's V1 engine it is on by default, so
  experiment B1's baseline must turn it **off** explicitly, and the run must
  confirm the state from the engine's startup config and `/metrics`, not from
  assumption.
- **Warm vs cold prefill is the biggest single effect.** Measured on the 5090:
  8.7 s cold → 0.7 s warm on a 33k-token shared prefix, 98% of prompt tokens
  served from cache. Expect B1's effect on this repo's ~300-token prefix to be
  proportionally smaller but clearly visible in TTFT.
- **Preemption is silent and expensive.** When demand exceeds the KV pool,
  vLLM preempts and *recomputes*, which burns prefill doing the same work
  twice and shows up as tail latency, not errors. In that project, raising
  concurrency from 2 to 3 sessions doubled wall time and dropped the cache hit
  rate from 77% to 46%. This is why the throughput profile here is sized to
  cross the baseline KV pool: the cliff should be measured, not asserted.
- **Aggregate decode throughput scaled linearly to 8 streams** on that model
  (65 → 116 → 224 → 344 → 447 tok/s at 1/2/4/6/8 streams, 8k shared prefix),
  and a single stream ran at ~72% of the memory-bandwidth ceiling. Decode at
  low batch is bandwidth-bound on this card; that is the mechanism experiment
  B2 relies on.
- **Admission caps can hurt more than they protect.** A
  `--max-num-queued-tokens` cap sized from KV pressure cut warm throughput by
  42%, because the cap counts full prompt length and ignores prefix-cache hits.
  Not an experiment here, but a good answer if asked about admission control.
- **Version matters.** Upgrading vLLM 0.27.1 → 0.29.0 on that workload gave
  +21% warm steady-state throughput with no config change. This repo pins
  0.29.0 and records the image digest.

Related: [gpu-host.md](gpu-host.md) · [walkthrough-topics.md](walkthrough-topics.md)
