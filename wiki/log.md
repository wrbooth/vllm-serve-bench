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

## [2026-09-27] decision | `bench run` and the run directory

`bench run` ([cmd/bench/run.go](../cmd/bench/run.go)) waits until
`/v1/models` lists the model, then runs the concurrency levels in order.
Each level gets its own warmup and measured window, and the results go to
`results/<profile>-<engine-config>-<yyyymmdd-hhmmss>/`. The schema lives in
[internal/results](../internal/results/results.go), where `bench report`
will read it too. That package is new; the design's layout now lists it.

- **The engine's identity is required, not optional.** `--engine-config`,
  `--engine-argv` (from `deploy/compose/engine.sh argv`) and
  `--engine-image` are mandatory and recorded verbatim. A run without the
  engine's actual argv could not be defended. An image that is not pinned
  by digest is allowed, but it adds a warning.
- **config.json is written twice.** The first write happens when the
  directory is created, so an interrupted run still says what it was. The
  second happens at the end, with `complete`, `finished_at` and the
  prompt-token check. requests.jsonl and summary.json are updated after
  every level, so an interrupted sweep keeps its finished levels.
- **The prompt-token check is a warning, not a failure.** Every successful
  request's `prompt_tokens` is compared with the generator's prediction.
  Mismatches are counted by observed value in `prompt_token_check`, listed
  under `warnings`, and printed. A run with no successful request gets a
  warning too, because its prediction was never checked.
- **No run directory unless the engine was ready.** A readiness timeout
  leaves nothing behind. An existing run directory is never reused (raw
  results are never overwritten). The run id uses UTC.
- **Nulls, not zeros.** An error row's latencies are `null` in
  requests.jsonl, so a reader cannot average a failure in as a
  zero-latency success. Durations are float milliseconds, exact to the
  nanosecond.
- **Room for the samplers.** `engine.facts` (KV pool tokens, resolved
  scheduler limits, prefix caching) and `host` (GPU name, driver, memory)
  are optional fields in config.json. They stay empty until the
  `internal/sampler` branch is wired in.
- **Per-request timeout defaults to 5 minutes.** A request that takes
  longer becomes an error row.

The end-to-end test runs `bench run` against the fake server at two levels.
It checks that the three files parse and agree with each other and with the
server's request count, and that no prompt was sent twice
(`TestRunWritesAConsistentRunDirectory`).

## [2026-09-27] work | Prompt word list verified on the engine; first real bench run

`bench prompts verify` against the pinned 7B engine found all 5,497 words to
be single tokens. It confirmed the fixed counts (33 interactive, 41
throughput) with 0 mismatches over 40 sample prompts. `--write` re-measured
them on the engine; only the provenance lines changed. A
smoke `bench run` (interactive, c=1 and c=4, 20 s, results not kept) finished
with 0 errors, 0 of 80 prompt-token mismatches, and the full engine argv and
image digest in `config.json`. At c=4 the closed-loop workers ran in lockstep
(fixed output length with `ignore_eos`), so each cycle's prompts arrive
together and TTFT includes a batched prefill. That is real behaviour of this
load model, worth remembering when reading the interactive TTFT curve.

## [2026-09-27] decision | Samplers: a failed tick is a row, a missing metric is an error

[internal/sampler](../internal/sampler/) writes one CSV row per tick. A scrape
that fails still produces a row, with empty values and the reason in an
`error` column, so a telemetry gap shows up in the data instead of silently
narrowing it. A metric missing from a scrape fails the whole row rather than
reading as zero, because "0 preemptions" and "not measured" must not look
alike. For the same reason nvidia-smi's `[N/A]` is recorded as empty, not 0.
Metric names follow vLLM 0.29.0, where `gpu_cache_usage_perc` no longer
exists. Tests run against a real `/metrics` scrape committed as a fixture.
`bench sample` runs the samplers standalone, for load `bench run` does not
drive (the `vllm bench serve` cross-check). First live run: 1 s cadence on both
files, and the KV pool read from `/metrics` matches the startup log (241,680).

## [2026-09-27] decision | Scheduler budgets are explicit flags at their default values

`--max-num-seqs 256 --max-num-batched-tokens 2048` are what vLLM resolves on
this card anyway, but no log line or metric shows a resolved default. Passing
them explicitly puts them in the recorded argv, which is the record the
contract trusts, and makes Experiment A a visible change to two numbers. The
engine restarted with them kept the same KV pool.

## [2026-09-27] work | Samplers wired into `bench run`

Every run directory now holds `vllm_metrics.csv` and `gpu.csv`. Both cover
the whole sweep, warmups included, so the engine's state going into each
window is on record. `config.json` gains `engine.facts` and `host`:

- the KV pool and prefix-caching state, from one `/metrics` scrape before the
  sweep;
- the scheduler budgets, parsed from the recorded argv, last occurrence
  winning as in vLLM;
- the GPU name, driver and memory, from `nvidia-smi`.

A fact that cannot be read is a warning, never a silent gap. Live on the 5090
(interactive, c=1 and c=4, not kept): 0 warnings, 42 telemetry rows per file,
and the facts matched the startup log.

## [2026-09-27] work | Cross-check: the clients agree; TTFT at fixed concurrency is phase-dependent

[scripts/cross-check.sh](../scripts/cross-check.sh) ran ours → `vllm bench
serve` → ours on the interactive profile at c=8. Raw data:
[results/verify/20260927-interactive-c8-fixed-output/](../results/verify/20260927-interactive-c8-fixed-output/).
Input lengths matched (433 vs 429 prompt tokens).

- **Agree within a few percent:** RPS, output tok/s, TPOT p50 and p99, E2E
  p99, and TTFT p99. For example, TPOT p50 was 9.68 and 9.69 ms (ours) vs
  9.66 ms (vLLM).
- **Median TTFT disagrees, including between our own two runs:** 46.4 and
  28.8 ms (ours) vs 65.8 ms (vLLM). The per-request TTFTs are not spread out.
  They cluster at the same few values in all three runs (about 29, 46 and
  66 ms), and each run lands in a different mix of them.

Reading: with a fixed output length and a closed loop, the workers finish in
lockstep, so arrivals line up with the engine's steps in a few discrete
patterns. Which pattern a 60 s window settles into decides the median TTFT.
The drift between our own two runs is as large as the gap to vLLM's client,
and both clients measure the same clusters. So this is a property of the load
model, not a harness error. The exact scheduling mechanism behind each
cluster was not investigated.

Consequence: the harness passes the cross-check on everything except median
TTFT, and median TTFT is not a stable metric under this load model as it
stands. A fix is needed before the baseline sweep (owner decision, pending).

## [2026-09-27] work | Correction: the cross-check's TTFT clusters were cache hits, not lockstep

The "Cross-check" entry above blamed the lockstep of fixed-length requests
for median TTFT drifting 38% between two identical runs. That diagnosis was
wrong. The per-second prefix-cache counters in the committed telemetry
show what happened. The bench reuses its prompts across runs (same seed,
request index restarting at 0), and the warm engine's prefix cache still
held them from earlier runs:

- run (b) replayed (a)'s prompts and prefilled about 1 uncached token per
  request, all the way through;
- run (a) was fully cached for its first ~14 s (indexes already sent by the
  smoke runs), then uncached at 113 tokens per request;
- vLLM's client was uncached throughout.

The ~29 ms cluster was the cached requests. The same contamination hit the
second round, in which ours prefilled ~1.5 uncached tokens per request
against vLLM's 106.

Lockstep is still real, but the evidence for it is vLLM's own client, which
was uncached in both rounds: median TTFT 65.8 ms with a fixed 128-token
output and 31.7 ms with 96–160. So the output-length spread stays.

Fix: `bench run` resets the prefix cache before every level (commit
`e5aa969`). A `bench run` is refused if the engine cannot reset, unless
`--reset-prefix-cache=false`, which leaves a warning. The lesson: the samplers
caught this, and the client-side numbers alone never would have. Engine
counters are the check on the load generator.

## [2026-09-27] decision | Dev mode has no measurable cost

`/reset_prefix_cache` exists only with `VLLM_SERVER_DEV_MODE=1`. In the pinned
v0.29 source the flag registers the dev routes and defaults
`log_error_stack` to true; nothing else reads it, and `envs.py` excludes it
from the compile-cache hash. vLLM's own sweep tool sets it for the same
reset.

Checked anyway: two fresh engine starts, dev mode off then on, the same
prompts (seed 101, never used before), and c=8 for 30 s
([results/checks/20260927-dev-mode/](../results/checks/20260927-dev-mode/)).
KV pool, RPS, output tok/s and TPOT p50 were identical, and TTFT p50 was
31.6 vs 31.7 ms. Both prefilled 113 uncached tokens per request, which
confirms that a fresh cache behaves as designed. Compose sets it by default.

## [2026-09-27] work | Cross-check passes

Rerun with both clients starting from an emptied prefix cache and output
lengths drawn from 96–160 on both sides
([results/verify/20260927-interactive-c8-varied-output/](../results/verify/20260927-interactive-c8-varied-output/)).
Telemetry confirms both were uncached: 113 unique tokens prefilled per
request for ours and 106 for vLLM's, whose lead text is shorter.

| | ours (a) | ours (b) | vLLM |
|---|---|---|---|
| TTFT p50 / p95 | 31.7 / 36.6 ms | 31.7 / 36.7 ms | 31.83 / 36.46 ms |
| TPOT p50 | 9.78 ms | 9.79 ms | 9.80 ms |
| E2E p99 | 1,590 ms | 1,588 ms | 1,587 ms |
| RPS | 6.317 | 6.317 | 6.171 |

Our two runs are the same prompts on the same reset cache, and their
throughput is identical. The remaining differences are accounted for:

- **TTFT p99** (ours 40.5 and 38.3 ms, vLLM 67.8 ms): vLLM's only high TTFTs
  are its requests 1–7, the start burst when all 8 workers send at once.
  Seven of 400 is enough to set its p99. Without those 8 requests vLLM's
  TTFT is p50/p95/p99 31.8/35.7/37.0 ms. Our warmup absorbs the burst
  before the window opens.
- **RPS** (2.4% overall, 1.6% in vLLM's steady-state middle): vLLM's client
  drew a longer mean output, 129.21 vs our 127.47 tokens (1.4%).
  TTFT + output × TPOT predicts a 1.6% longer E2E, which is the whole gap.
  The rest of the 2.4% is vLLM dividing by its full duration, ramp-up and
  drain included.

The harness agrees with `vllm bench serve` to within 0.5% on median and p95
TTFT and TPOT, and every other difference has a measured cause. The
comparison figures here were computed from the committed files. A
`bench verify` command that generates them, per docs/02, is still to do.

## [2026-09-27] work | `bench verify` generates the cross-check

The "Cross-check passes" figures above were computed with one-off scripts.
`bench verify` now generates them from the committed files
([internal/crosscheck](../internal/crosscheck/)). Its output sits beside
each round:
[comparison.md](../results/verify/20260927-interactive-c8-varied-output/comparison.md)
for the valid round, and
[comparison.md](../results/verify/20260927-interactive-c8-varied-output-cache-contaminated/comparison.md)
for the round before the cache reset. How it computes each figure is in
[docs/02](../docs/02-architecture.md), "Cross-check". A test reruns the
command in each file's header and requires the same bytes.

Every figure in "Cross-check passes" came out the same when rounded as the
entry rounds it: the table, TTFT p99 and its burst-free
31.8/35.7/37.0 ms, the 6.215 steady-state rate, the 2.4% and 1.6% RPS
gaps, the 129.21 vs 127.47 mean output, and 113.0 and 106.1 uncached
tokens per request. Two corrections:

- **"Within 0.5% on median and p95 TTFT and TPOT" is too strong.** Run (b)'s
  TTFT p95 is 36.74 ms against vLLM's 36.45, +0.77%. Run (a)'s is +0.27%,
  and the other TTFT p50 and TPOT deltas are within 0.5%.
- **113.0 for run (a) depends on the window edges.** Taking the deltas
  between the first and last samples inside the window gives 113.3. The
  last sample inside caught a prefill whose first token was counted a
  second later. `bench verify` takes the samples that bracket the window
  (last at or before its start, first at or after its end), which gives
  113.0. The sample time is truncated to the millisecond and the window
  edge is not, so the comparison uses full precision.

The contaminated round is flagged: 1.6 and 1.0 uncached tokens per request
for ours over the measured window (the "~1.5" in the correction entry was
over the whole file), and 106.0 for vLLM's. The threshold is 0.9 × the
prompt's unique part. An uncached prompt prefills at least its unique part
(ours 113, vLLM's 106, against 100). A cached one prefills about one token.
The 0.1 margin absorbs the ±1-request error at the sample edges. A flagged
comparison is still written, marked NOT VALID, and the command exits 1
unless `--allow-cached`.

## [2026-09-27] work | Baseline sweep, both profiles

[scripts/sweep.sh](../scripts/sweep.sh) ran both profiles against the warm
baseline engine: 10 s warmup and 60 s window per level, seed 1, and the
prefix cache reset before every level. Raw data is in
[results/baseline/](../results/baseline/). There were 0 errors, 0 warnings
and 0 prompt-token mismatches over 4,611 measured requests. The telemetry
shows every level prefilled its intended unique tokens (113 per request for
interactive, about 1,500 for throughput), so nothing was served from cache.

What the data shows. These figures were read off `summary.json` and the
telemetry by hand; `bench report` will generate them for docs/03.

- **The interactive profile never saturates within its sweep (1–32).** At
  c=32 the KV cache is 2.9% used, nothing waits, TTFT p95 is 48 ms and
  requests per second are still rising almost linearly. The sweep stops
  short of the knee.
- **Throughput peaks at c=64** (1,584 output tok/s), **drops at c=128**
  (1,413) before any preemption (0 events, KV at 85% on average), and holds
  at about 1,250 at 192–256.
- **At c≥192 the KV pool is the limit, as designed.** KV usage is 99.9–100%,
  only about 150 sequences run (below the `max_num_seqs` cap of 256), up to
  158 wait, and TTFT p50 reaches 8.1 s (c=192) and 20.4 s (c=256). Preemption
  happened but is small: 19 and 31 events. Queueing, not recomputation,
  dominates.
- **The GPU runs at its ~600 W power cap from c=64 up**, at 100% utilisation
  throughout.
- **The drop from 64 to 128 is unexplained.** The prefill budget
  (`max_num_batched_tokens` 2048 against 1,500-token documents) and the power
  cap are the candidates. That is Experiment A's question; it is not
  answered here.

## [2026-09-27] work | Interactive baseline extended to 256; the knee is at 128

Rerun of the interactive profile with its sweep extended to 256
([results/baseline/interactive-baseline-20260928-015502/](../results/baseline/interactive-baseline-20260928-015502/)).
It supersedes
[interactive-baseline-20260928-013307](../results/baseline/interactive-baseline-20260928-013307/),
which stopped at 32 and stays committed as it is. It had 0 errors, 0 warnings and
0 of 9,214 prompt-token mismatches, and 113 uncached tokens prefilled per
request at every level.

- **Repeatability:** levels 1–32 match the first run to the same requests per
  second, with TTFT p50 within 0.7 ms. Two runs 20 minutes apart on the same
  engine agree.
- **The knee is at c=128:** 38.4 req/s. c=256 adds nothing (38.6) while TPOT
  p50 doubles (25.5 to 51.3 ms) and TTFT p95 goes from 117 to 212 ms.
- **What limits it is not memory:** KV usage is 21% at c=256, nothing waits,
  all 256 sequences run (the `max_num_seqs` cap) and there are 0 preemptions.
  The GPU is at its ~600 W power cap from c=64 up, so decode is compute- or
  power-bound at this batch size. That is an inference from the power and
  utilisation telemetry, not a profile.

## [2026-09-27] decision | Interactive SLO: TTFT p95 ≤ 100 ms and TPOT p95 ≤ 25 ms

Chosen after the baseline, from
[interactive-baseline-20260928-015502](../results/baseline/interactive-baseline-20260928-015502/),
per the contract. A request level meets the SLO when both p95s are within
bounds; goodput counts output tokens only at levels that do.

- **Where it bites:** the baseline meets it at c=64 (TTFT p95 73 ms, TPOT
  p95 15.5 ms) and fails at c=128 (117 ms, 26.2 ms) on both metrics. The
  SLO sits on the knee, so the highest compliant goodput is c=64's, and
  every experiment has room to move it:
  - A trades TTFT against TPOT through the scheduler budgets;
  - B1 adds about 300 prefill tokens per request;
  - B2 speeds up decode.
- **Why these numbers:** 25 ms per token is 40 tok/s per stream, well above
  reading speed. 100 ms to first token reads as instant. Both are user-facing
  budgets, not fitted to a curve.
- **Alternatives considered:**
  - Looser (TTFT 250 ms, TPOT 50 ms): its boundary falls between c=128 and
    c=256, which have the same throughput, so no experiment could show a
    goodput gain.
  - Tail (p99 150 ms, 20 ms): the same knee, but each p99 rests on about 20
    samples.
- **Known weakness, to state in the write-up:** c=128's TPOT p95 is 26.2 ms,
  5% over the bound. A small TPOT improvement brings c=128 inside and
  roughly adds 19% goodput, which would overstate a small effect. Each
  experiment reports its per-level p95s next to the pass/fail, so the
  margin stays visible.

## [2026-09-27] decision | Throughput SLO: E2E p95 ≤ 15 s

Chosen after the baseline, from
[throughput-baseline-20260928-014014](../results/baseline/throughput-baseline-20260928-014014/).
A batch summarization user waits for the finished summary, so the SLO is
completion time. TTFT and TPOT stay in the tables as diagnostics.

- **Where it bites:** the baseline meets it at c=64 (E2E p95 13.0 s) and fails
  at c=128 (26.2 s). The margins on both sides are wide, unlike the
  interactive TPOT bound. The compliant peak is also the throughput peak
  (1,584 tok/s at c=64). B2 (FP8: faster decode, larger KV pool) and A
  (scheduler budgets) both have room to move c=128 inside.
- **Alternatives considered:**
  - TTFT p95 ≤ 1 s with TPOT p95 ≤ 100 ms: c=128 passes by 1.6% on TTFT
    (984 ms), a knife edge, and the boundary lands on a level with no
    throughput advantage.
  - TTFT ≤ 2 s with TPOT ≤ 50 ms: the same boundary as E2E, but it fails on a
    metric a batch user does not see.
- **Caveat:** E2E p95 is dominated by the longer outputs in the 192–320 range.
  The lengths are fixed by seed and request index, so every experiment sees
  the same ones.

## [2026-09-27] work | `bench report` generates docs/03; two goodputs defined

[`bench report`](../cmd/bench/report.go) ([internal/report](../internal/report/))
now writes [docs/03-results.md](../docs/03-results.md) from committed run
directories and [docs/slo.json](../docs/slo.json), which holds the two SLO
decisions above in machine-readable form. The generated text sits between
marker comments; the prose outside them is hand-written and number-free.
docs/03 now has the SLO, both baselines and empty experiment sections. A
test regenerates it from the committed runs and fails on any difference.

- **Goodput is reported two ways** ([docs/02](../docs/02-architecture.md#metric-definitions-client-side-per-request)).
  The headline is *max compliant goodput*: the highest output tok/s among
  levels whose p95s are within the SLO, that had no errors and that passed
  the engine check. *Per-request goodput* counts the tokens of the requests
  that each met every limit on their own. The headline follows the SLO's
  own shape, a per-level tail bound. Per-request goodput is shown beside it
  because it shows how much of a failing level was still useful: at the
  interactive baseline's first failing level most requests individually
  miss the TPOT limit, since that level's TPOT p50 is already over it.
- **Two judgement calls, stated in docs/02:** a value equal to the limit
  passes (the SLOs are written "≤"), and a level with any error does not
  meet the SLO. The baseline had no errors, so the second one did not
  change anything yet.
- **Generated figures differ slightly from the hand-read ones above.**
  Preemptions at throughput c=192 and c=256 come out one higher than the
  baseline entry's hand-read 19 and 31. The counter delta runs between the
  samples covering the window, up to a second on each side, as the
  cross-check does. docs/03 is the reference; the log entries stay as
  written.
- **Charts cut** for time ([worklog](../docs/worklog.md)).

## [2026-09-27] decision | Experiment A: hypothesis, recorded before the data

Change: `--max-num-batched-tokens` 2048 → 8192 (`engine/a-mnbt8192.env`), the
value vLLM itself uses on cards of 70 GB and up. Nothing else changes: same
`max_num_seqs` 256, seed, per-level cache reset and windows. The levels run
are the ones around the baseline knees: interactive 32/64/128/256 and
throughput 32/64/128/192. Committed before the sweep finished.

- **Throughput:** at 2048, each step fits about one ~1,500-token document
  alongside the running decodes, so documents are admitted slowly and every
  decode step carries a large prefill chunk. With a 4× budget, I expect the
  dip from c=64 to c=128 to shrink or vanish, output tok/s to rise from c=64
  up, and TPOT p95 to rise, because a step that carries more prefill takes
  longer.
- **Interactive (the control):** prefills are about 113 tokens, so the budget
  rarely binds. I expect no material change.
- **What would refute it:** no throughput gain at c=64 to 128, or worse
  latency with no throughput to show for it. If the dip at c=128 survives
  unchanged, the prefill budget is not its cause.
- **Side effect seen at startup, before any load:** the warm KV pool is
  227,200 tokens against the baseline's 241,680 (−6%). A larger step budget
  reserves more activation memory, so less capacity is left at c=192, where
  the baseline was already KV-bound. It is part of this change's cost.

## [2026-09-27] work | Experiment A result: hypothesis partly supported; SLO-level goodput unchanged

Data: [results/a-mnbt8192/](../results/a-mnbt8192/), compared at matching
levels against the baseline runs. All levels are valid: 0 errors, 0
prompt-token mismatches, and 113 or about 1,500 uncached tokens per request.
These figures were read off the files by hand; `bench report` will generate
them for docs/03.

- **Supported:** a larger budget admits documents faster.
  - Throughput TTFT p95 fell 12.6% at c=64 (526 to 460 ms) and 24.5% at
    c=128 (984 to 743 ms).
  - Output tok/s rose 4.9% at c=128 and 5.4% at c=192, where admission and
    queueing matter.
- **Refuted:**
  - The dip from c=64 to c=128 survives: 1,594 to 1,482 tok/s (−7%) against
    the baseline's −11%. The prefill budget is at most part of its cause; the
    rest is still unexplained. The power cap is the remaining candidate,
    untested.
  - TPOT did not rise as predicted; it was flat or 1–8% lower.
- **Cost, as flagged at startup:** the KV pool is 6% smaller (227,200 tokens).
  At c=192 that shows as 44 preemptions against 19, and TTFT p95 15% worse
  (11.6 s against 10.1 s).
- **Against the SLOs:**
  - **Interactive:** the boundary is unchanged. c=64 passes and c=128 fails,
    at TTFT 113 ms and TPOT 26.0 ms, which are near misses again. Compliant
    goodput is +1.7%.
  - **Throughput:** c=64 passes (13.3 s) and c=128 fails (26.6 s). Compliant
    goodput is +0.6%.
- **Interactive as the control:** every metric moved at most 1.7%, in A's
  favour. With one run per config that cannot be told apart from run-to-run
  noise. The baseline's own repeat matched to within 0.7 ms TTFT p50 at
  1–32, but was not repeated above that.

Trade-off, for the write-up: 8192 buys faster admission of long prompts, with
lower TTFT at mid-to-high load and about 5% more throughput past the knee. It
costs 6% of KV capacity, which shows up as more preemption at the top. It
does not move either SLO boundary. For this card and these SLOs the default
2048 stays; A is reported as a measured null result on goodput, with a real
effect on TTFT.

## [2026-09-28] decision | Experiment B2: checkpoint made; hypothesis recorded before serving it

[scripts/quantize/quantize.py](../scripts/quantize/quantize.py) produced
`Qwen2.5-7B-Instruct-FP8-Dynamic` on the 5090, in the GPU distrobox, with
`llmcompressor` 0.14.0 (pinned in `requirements.txt`).

- **What the scheme does:** every Linear layer's weights become FP8 E4M3 with
  one static scale per output channel. Activations are FP8, with a scale
  computed per token at run time, so no calibration set is needed.
  `lm_head` stays in bf16 and the KV cache is unquantized. The checkpoint's
  `quantization_config` confirms all of this.
- **Result of the quantization:** it took 17 s and needed no data. The
  checkpoint is 8.2 GB against 15 GB, and `provenance.json` records the
  source snapshot and tool versions.
- **The engine:** the checkpoint is served as `/models/…` through a
  read-only mount (`MODELS_DIR` in `.env.local`), with exactly the baseline
  flags (`engine/b2-fp8.env`). vLLM reads the scheme from the checkpoint.

Hypothesis, recorded before any serving data:

- **Decode is bandwidth-bound at low concurrency.** Halving the weight bytes
  should cut TPOT at c=1 noticeably, though by less than half, since KV
  reads, activations and kernel overheads do not shrink.
- **The KV pool grows.** The online-FP8 smoke test's cold start gave about
  323k tokens against the baseline's 209k cold (241k warm). I expect a
  warm pool well above the baseline's.
- **Throughput:** more KV room and cheaper decode should push the knee to the
  right. c=128 may come inside E2E p95 ≤ 15 s, and c=192 and 256 should
  queue less.
- **Interactive:** the knee sits at the power cap. FP8 GEMMs do less work per
  token, so tok/s at c=128 and 256 should rise, and c=128 may come inside the
  TPOT bound.
- **Quality:** checked separately, on the same fixed prompts, by comparing
  greedy outputs against the bf16 model.
- **What would refute it:** no TPOT gain at c=1, or no throughput gain past the
  baseline knee.

## [2026-09-28] work | Experiment B2 result: large gains; one prediction refuted; quality level with bf16

Data: [results/b2-fp8/](../results/b2-fp8/), compared in docs/03 by
`bench report` against the baseline. Every level passes the engine check,
with 0 errors and 0 prompt-token mismatches. The figures below were read off
the generated tables.

- **Decode speed:** TPOT at c=1 fell from 9.5 to 6.1 ms (−36%), less than
  half as predicted, since only the weight bytes halve.
- **KV pool:** warm, it is 354,832 tokens against 241,680 (+47%).
- **Interactive:** throughput is up 36–58% across the sweep. c=128 now meets
  the SLO with margin (TTFT p95 87 ms, TPOT p95 18.9 ms), so max compliant
  goodput goes from 4,140 tok/s at c=64 to 6,898 at c=128 (+66.6%).
- **Throughput:** up 24–61% per level, but the SLO boundary does not move.
  c=128's E2E p95 is 18.4 s against 15 s, so that prediction was wrong. Max
  compliant goodput is 1,584 → 2,380 tok/s at the same c=64 (+50.3%).
  Queueing TTFT at c=192 falls from 10.1 s to 0.86 s p95.
- **Trade-off not predicted:** at throughput c=256 the larger pool runs about
  206 sequences against about 150, so TPOT p95 is 14.7% worse there even
  though E2E improves.
- **Quality** ([results/quality/report.md](../results/quality/report.md)):
  - 28 of 30 exact-answer items correct for both models. The same two missed
    items were genuine model errors, wrong the same way in both ("deserts",
    and "Monday" for ten days after Monday).
  - 0 items changed correctness.
  - 31 of 40 replies were identical; mean common prefix 0.818.
  - This is a smoke-level check on a small hand-written set, not an eval.
- **Not done:** publishing the checkpoint to Hugging Face. It is public and
  under the owner's account, so it waits for the owner's go-ahead, and the
  HF token's write scope is still unchecked.

## [2026-09-28] work | Bench image, Compose bench service, Kubernetes manifests, deploy CI

Build-order step 6. What was built and where it is described:
[docs/02-architecture.md](../docs/02-architecture.md#deployment) (Deployment
and CI/CD), [deploy/k8s/README.md](../deploy/k8s/README.md).

- **The image is distroless `base`, not `static`.** The Go binary is static,
  but the GPU sampler execs `nvidia-smi`, which the NVIDIA toolkit injects
  from the host, and `nvidia-smi` is a dynamically linked glibc program.
  distroless/static has no glibc, so the sampler would fail to start there.
  Not yet confirmed on the GPU host: that the CDI spec injects `nvidia-smi`
  into a distroless container and it runs.
- **The bench cannot read the engine's argv from its own container.** In
  Compose the caller passes `ENGINE_ARGV="$(./engine.sh argv)"`; on
  Kubernetes a ConfigMap carries it, and the README replaces the declared
  value with the pod's `/proc/1/cmdline` before a run. Without it `bench run`
  refuses to start, as designed.
- **On Kubernetes the bench gets no GPU sampler.** The engine holds the
  node's one GPU, so nothing injects `nvidia-smi` into the bench pod; the Job
  runs with `--gpu-sampler=false` and is pinned to the engine's node by pod
  affinity so TTFT carries no extra hop.
- **The two profiles run as init container, then container**, so they never
  overlap, with no shell in the image to sequence them.
- **Deploy lint is its own target (`make lint-deploy`) and CI job, not part
  of `make lint`.** kubeconform downloads schemas on every run and
  `docker compose config` needs the Docker CLI; `make lint` stays offline and
  Docker-free. kubeconform is pinned at v0.7.0 because v0.8.0 needs Go 1.26.
- **Checked here:** kubeconform passes on all eight objects and rejects a
  misspelt field; `docker compose config` passes for every engine config and
  rejects an unknown key; the build stage's exact `go build` produced a static
  linux/amd64 binary from the `.dockerignore`d context, and `version` printed
  the build arg. **Not checked here:** `docker build` itself (no Docker daemon
  running on the dev machine); the CI image job is the first real build.
- The planned Compose `observability` profile (Prometheus + Grafana) is not
  built; docs/02 now says so.
