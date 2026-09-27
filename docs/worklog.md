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

- 15:40–16:10 Repo tooling: strict golangci-lint suite, actionlint, ruff,
  markdownlint, testing standards and the unused-function gate, AGENTS.md
  with the worktree / PR-on-request rules, LLM wiki. CI green. Counts against
  the time box: ~1 h 10 m spent before the first line of bench code.

## Plan for the remaining time (~5 h budget)

1. Compose + CDI engine; first streaming request on the 0.5B model; five-minute
   online-FP8 smoke test on the 7B (informs B2, not reported).
2. Go OpenAI streaming client + SSE parser + fake server + tests; closed-loop
   runner.
3. 1 Hz samplers (engine `/metrics`, nvidia-smi); cross-check against
   `vllm bench serve`.
4. Baseline sweeps for both profiles (README written while they run); choose
   the SLO from the data.
5. Experiments in order: A batching → B1 prefix caching → B2 llm-compressor
   FP8 dynamic checkpoint (check the HF token has write scope first) → C FP8
   KV cache if time.
6. Dockerfile + GHCR publish job; Kubernetes manifests + kubeconform in CI.

Stretch, only if the above is done: proxy in front of vLLM; EKS run as a
second hardware point.

## Cut / deferred

(filled in as decisions are made)
