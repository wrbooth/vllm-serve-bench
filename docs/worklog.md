# Worklog

Time-boxed take-home. Times are wall clock, Pacific. Commits are the primary record; this file is
the narrative and the list of what was cut.

## 2026-09-27 (Sun) — build day

- 15:00 Read the brief. Decided: Qwen2.5-7B-Instruct bf16 on the lab RTX 5090; pinned
  `vllm/vllm-openai:v0.29.0` (the version already running on this card); Go load generator;
  closed-loop concurrency sweeps; experiments A (batching), B1 (prefix caching), B2 (FP8),
  C (FP8 KV, stretch). Verified Docker CDI GPU passthrough on the host, freed the GPU, started
  the image pull and model downloads in the background.
- 15:30 Wrote the problem statement and architecture doc before any code so the metric
  definitions and the SLO-after-baseline rule are fixed in writing first.

## Cut / deferred

(filled in as decisions are made)
