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
