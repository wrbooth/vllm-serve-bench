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

## [2026-09-27] decision | Closed-loop window: by attempt start, tail kept

The runner ([internal/loadgen](../internal/loadgen/loadgen.go)) decides a
request's window from the instant its worker chose to send it, the same
instant that decides whether to send at all. Attempts started before
`start + warmup` are counted as warmup and discarded; attempts started in the
measured window are records, errors included; none start after it. Requests
still in flight when the window closes run to completion and stay in the
results. Cutting them off would drop exactly the slowest requests and make
the tail look better than it was. RPS and output tok/s divide by the window
length. In steady state the requests carried in from warmup balance the ones
carried out past the end.

Percentiles are nearest-rank, with the rank computed as `ceil(p·n/100)`, not
`ceil(p/100·n)`. The second form is off by one rank for some (p, n) pairs in
float64 (0.28·25 = 7.000000000000001). The test
`RankMultipliesBeforeDividingToAvoidFloatError` in
[internal/metrics](../internal/metrics/metrics_test.go) pins it.

Not built yet: the `bench run` subcommand, which needs the prompt generator
and the results-directory writer.

## [2026-09-27] decision | Prompts: a single-token word list, not a tokenizer at run time

The design said to trim generated text to a target length with the
tokenizer at profile build time. The generator
([internal/prompts](../internal/prompts/prompts.go)) does without a
tokenizer entirely:

- **Every word in the list is one token.** Each word in
  [words.txt](../internal/prompts/words.txt), written with a leading space,
  is exactly one Qwen2.5 token. Qwen2's pre-tokenizer splits text with a
  regex before BPE, and a space followed by letters is always its own
  piece, so words cannot merge with their neighbours. A body of *K* words is
  *K* tokens.
- **The rest is a per-profile constant.** The chat template plus the
  profile's lead text ("Notes:", "Question:", the summarize instruction) is
  measured once and embedded as
  [fixed_tokens.json](../internal/prompts/fixed_tokens.json): the engine's
  count of a reference prompt minus its body words. Each lead ends in ":" so
  the first body word starts a new piece.
- **Uniqueness by construction.** Prefix caching is on in the baseline, so
  a repeated prompt would be served from cache. The first three words of
  each request's unique part spell its request index in base
  `len(words)` (at least 10^9 indexes); the index counter runs across the
  whole run, warmup included. The interactive system prompt is the only
  shared text, fixed per seed.
- **Greedy sampling, set explicitly.** Temperature 0 and repetition penalty
  1.0 are sent with every request. Otherwise vLLM applies the checkpoint's
  `generation_config.json` (temperature 0.7, top_k 20, repetition penalty
  1.1). Greedy makes the outputs reproducible for a seed. Decode cost does
  not depend on which token is picked, and with `ignore_eos` the length is
  fixed either way. At temperature 0 vLLM ignores top-k and top-p, so they
  are not sent.
- **Throughput layout:** the instruction comes first, then the document,
  as the profile table in the design says ("no shared prefix beyond the
  instruction line").

The committed candidate list and counts come from an offline pre-screen
([scripts/wordlist/candidates.py](../scripts/wordlist/candidates.py)): the
"usa-no-swears" list from the first20hours/google-10000-english repository,
intersected with the macOS `/usr/share/dict/words` to drop acronyms and brand
names, minus a short list of words that read badly in word salad. Each word
was then checked for one token against the model repo's `tokenizer.json`.
That leaves 5,497 words. Rendering the chat template by hand with that
tokenizer reproduces the engine's count for the "You are terse." / "Say hi."
probe (20), and gives fixed counts of 33 (interactive) and 41 (throughput),
the same across seeds and indexes. None of this has been checked against
the engine yet. `bench prompts verify --write internal/prompts` on the GPU
host does that: it prunes any failing words and re-measures the counts from
the engine's `/tokenize`, and its `source` field says which kind of count a
run used.
