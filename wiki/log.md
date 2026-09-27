# Log

Append-only, chronological, newest last. Entry format:
`## [YYYY-MM-DD] <type> | <title>` with type ∈ `ingest`, `query`, `lint`,
`decision`, `work`. Parseable: `grep "^## \[" wiki/log.md | tail -5`.

## [2026-09-27] decision | Scope and stack

Qwen2.5-7B-Instruct (bf16) on one RTX 5090, served by `vllm/vllm-openai:v0.29.0`
(pinned). Go load generator with closed-loop concurrency sweeps and 1 Hz engine
and GPU samplers. Experiments in order: A batching, B1 prefix caching, B2 a
self-produced FP8 checkpoint (llm-compressor), C FP8 KV cache (stretch). A thin
proxy in front of vLLM only after the core ask is met; EKS as a second hardware
point only if time allows. Contract: [docs/02-architecture.md](../docs/02-architecture.md).

## [2026-09-27] decision | B2 produces the quantized checkpoint rather than using vLLM's online FP8

Serving vLLM's `--quantization fp8` exercises quantized inference but produces
nothing. The owner wants the experiment to cover creating a quantized model:
choosing the scheme, the ignore list and (for static FP8) the calibration set,
then validating the output. Online FP8 stays as a first-hour smoke test only.

## [2026-09-27] ingest | LLM wiki pattern (Karpathy)

Bootstrapped `raw/` + `wiki/` with the schema in [AGENTS.md](../AGENTS.md).
Charter copied from the owner's sibling project, fetch date preserved:
[raw/2026-09-12-llm-wiki-pattern.md](../raw/2026-09-12-llm-wiki-pattern.md).
First pages: [project.md](project.md), [gpu-host.md](gpu-host.md),
[prior-art-5090.md](prior-art-5090.md), [walkthrough-topics.md](walkthrough-topics.md).

## [2026-09-27] work | Repo tooling

Lint canon ported from the owner's Go orchestrator repo (golangci-lint v2
strict suite, versions pinned once in the Makefile, suppressions must carry a
reason) plus actionlint, ruff and markdownlint. Testing standards written into
[AGENTS.md](../AGENTS.md); the unused-function gate ported as
`unused_functions_test.go`; coverage floor of 85% on the result-producing
packages.

## [2026-09-27] decision | Worktrees, and PRs only on request

From here on every change is made in its own git worktree on a typed branch;
the primary checkout stays on a clean `main`. Commits stay local until the
owner asks for a PR, which is opened with the `wrbooth` GitHub account
explicitly (two accounts exist on the machine). Procedure:
[AGENTS.md](../AGENTS.md), "Git". The bootstrap commits before this entry went
straight to `main`.

## [2026-09-27] decision | Time counted by session; build order moves to the wiki

The owner works in separate sessions, so one wall-clock span would overstate
the time spent. [docs/worklog.md](../docs/worklog.md) now records sessions
(start, end, active time) and the cut list; unattended runs are not counted.
The remaining build order moved to [project.md](project.md) so it lives in one
place next to the status table. From here on, wiki log entries give the
reasoning and link to the worklog for timings instead of repeating them.

## [2026-09-27] decision | Sessions are measured from activity, 5-minute gap

The previous entry's start/stop rows depended on someone saying when work
stopped. That doesn't hold up when a conversation stays open for hours and the
owner is interrupted often. `make timesheet`
([cmd/timesheet](../cmd/timesheet/main.go),
[internal/timesheet](../internal/timesheet/timesheet.go)) now rebuilds the
sessions from transcript and commit timestamps. Any silence longer than 5
minutes ends a session; the owner chose 5 minutes because of the
interruptions. Found on the first run: the cloud session that did the first
hour left no local transcript, so that time is a manual entry in
[docs/worklog.md](../docs/worklog.md) until the owner confirms it.

## [2026-09-27] work | Correction: the first build hour was a local conversation

The previous entry said the first hour ran in a cloud session. That was wrong.
It ran in a long-lived Claude Code conversation started from another directory
on the same machine, so its transcript is stored under that directory, not
this repo's. `make timesheet -also` (set as `TIMESHEET_ALSO` in `.env.local`)
now reads such transcripts and counts only the records that name this repo;
see [docs/worklog.md](../docs/worklog.md).

## [2026-09-27] work | Engine up under Compose; FP8 runs on sm_120

The pinned engine runs under Compose with the CDI device named directly (the
`{driver: cdi}` form in the design was the wrong Compose key). It served the
0.5B and 7B models. Online FP8 works with a CUTLASS kernel, so B2 is not
blocked. KV figures and SSE quirks are in
[gpu-host.md](gpu-host.md#engine-startup-measured).

## [2026-09-27] decision | The baseline runs with prefix caching off

The design contradicted itself: "Serving layer" said vLLM defaults (caching
on), while B1 said the baseline runs with it off. B1 only makes sense as
off → on, so the baseline has `--no-enable-prefix-caching` and the design
now says so. Side effect: the KV pool grew by 1.74 GiB, which reopens the
throughput profile's sizing (open question in [project.md](project.md)).

## [2026-09-27] decision | Host-side Python runs in the GPU distrobox

The host has no Python. At the owner's direction, host tooling (llm-compressor
for B2) runs in the `ubuntu-gpu-v2` distrobox; the engine stays in Docker.

## [2026-09-27] decision | Reversed: the baseline keeps prefix caching on; B1 turns it off

Supersedes the entry above that turned caching off in the baseline. The owner
chose defaults as the baseline: it is what a stock deployment runs, the SLO
then comes from realistic data, and the other experiments run on a realistic
config. B1 becomes an ablation of a default ("what is it worth?"). The
alternative, off → on, reads more naturally but invites "you turned off a
default to win it back". The KV-pool side effect (209,120 tokens with caching
on vs 241,680 off) goes into B1's trade-off.

## [2026-09-27] work | Correction: the KV pool moved with compile-cache state, not prefix caching

Two entries above say that turning prefix caching off grew the KV pool by
1.74 GiB. That was wrong. Rerunning the new baseline (caching on) gave the
same 241,680 tokens as caching off. The only small pool, 209,120, came from
the first 7B start, which compiled its graphs cold. Warm starts load the
compiled graph and get the larger pool. Rule adopted: benchmark runs start
warm, and `config.json` records the pool size. Details and the per-start
table are in [gpu-host.md](gpu-host.md#engine-startup-measured).

## [2026-09-27] decision | Throughput sweep extends to 192 and 256

On a warm start the baseline KV pool (241,680 tokens) holds the old top level
(128 × ~1.8k), so the design's promise to measure preemption was at risk. The
options were to extend the sweep, lengthen the documents, shrink the pool
artificially, or drop the goal. The owner chose to extend it. The sweep stops at 256
because that is vLLM's default `max_num_seqs` on a 32 GB card; above it, the
scheduler would queue requests rather than preempt them. The 2048-token
`max_num_batched_tokens` default is noted for Experiment A. Design:
[docs/02-architecture.md](../docs/02-architecture.md#workload-profiles).

## [2026-09-27] decision | Streaming client: what counts as a measured request

The client ([internal/openai](../internal/openai/client.go)) was built
against chunk shapes captured from the pinned engine (vLLM 0.29.0, 0.5B
model), and the fake server ([internal/fakeserver](../internal/fakeserver/fakeserver.go))
replays the same shapes. Decisions, each pinned by a test:

- **TTFT starts at the first non-empty `delta.content`.** vLLM's first chunk
  is a role-only delta with `"content":""`; timing it would put TTFT near
  zero. The fake server sends that chunk immediately and delays the first
  content chunk, so the loopback tests fail if this regresses.
- **`t_send` is when the body is fully written** (httptrace `WroteRequest`),
  as the architecture doc defines it, not when the call starts.
- **A 200 that is not a complete measurement is an error row:** no
  `[DONE]`, no usage chunk, or no content token. Token counts come only from
  the usage chunk (the chat template adds ~30 prompt tokens, so estimating
  would be wrong), so a stream without one cannot produce TPOT.
- **Sampling is set explicitly or not at all.** vLLM's defaults come from the
  model's `generation_config.json` (Qwen2.5: temperature 0.7, top_k 20,
  repetition_penalty 1.1), not from the OpenAI spec, so the request carries
  optional pointers and a run that must not depend on the checkpoint sets
  them.
- **Idle connections per host raised to 1024.** net/http keeps 2 by default;
  at concurrency N a closed loop would redial N-2 connections after every
  request, and the redial lands inside TTFT.
