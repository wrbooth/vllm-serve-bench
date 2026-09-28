# Worklog

Time-boxed take-home (4–6 h budget), worked in separate sessions rather than one
sitting. Times are wall clock, Pacific.

**How time is counted.** Sessions are measured, not declared. `make timesheet`
rebuilds them from activity timestamps: every record in this repo's Claude Code
transcripts (the primary checkout and its worktrees), every record that names
this repo in conversations started elsewhere (`TIMESHEET_ALSO`), and every
commit. Any
silence longer than 5 minutes ends a session, and a session's active time runs
from its first event to its last. Two consequences. Time spent while an agent
works counts, even if the owner has been called away. Time spent off the
keyboard (reading, thinking, watching a run) only counts when it falls inside a
gap of 5 minutes or less. The logic and its tests are in
[internal/timesheet](../internal/timesheet/).

The Sessions table below is pasted from `make timesheet` output and is never
edited by hand. Work that left no trace in a transcript or
a commit (reading the brief on paper, whiteboarding) goes under Manual entries, one line each with the reason, and is
added to the total separately.

The build order and the status of each deliverable live in
[wiki/project.md](../wiki/project.md). The reasons behind the decisions live in
[wiki/log.md](../wiki/log.md).

## Sessions

Generated 2026-09-27 17:15 (the last session was still in progress):

| # | Date | Start | End | Active |
|---|---|---|---|---|
| 1 | 2026-09-27 | 08:32 | 08:32 | 0 h 00 m |
| 2 | 2026-09-27 | 11:01 | 11:04 | 0 h 02 m |
| 3 | 2026-09-27 | 15:20 | 15:39 | 0 h 20 m |
| 4 | 2026-09-27 | 16:02 | 16:37 | 0 h 35 m |
| 5 | 2026-09-27 | 16:49 | 17:15 | 0 h 27 m |

**Total:** 1 h 24 m over 5 sessions.

### Manual entries

None yet.

### Notes

- The first build hour (sessions 3 and 4) ran in a conversation started
  outside this repo, so it is counted through `TIMESHEET_ALSO`. Sessions 1 and
  2 are planning in that same conversation (reading the brief, choosing the
  GPU and experiments); only their records that name the repo are counted.
- Docs were written before any code, so the metric definitions and the rule
  that the SLO comes after the baseline were in writing first. By 16:15 all
  time had gone into docs and tooling; no bench code yet.
- **Session 5.** The engine was brought up under Compose (0.5B, 7B, the FP8
  smoke test) while a background agent built the Go client, metrics, fake
  server and runner in a second worktree. Its transcript (16:50 to 17:02)
  falls inside this session, so it adds no time.

## Cut / deferred

- **`bench report` SVG charts** (output tok/s and p95 latency against
  concurrency, baseline against experiments): cut for time. The tables and
  comparisons carry every figure; charts can be drawn from the same
  `internal/report` tables later without changing any number.
- **Compose `observability` profile (Prometheus + Grafana)**: cut for time. It
  was planned as live-demo material only. The run directories already hold the
  same engine counters (`vllm_metrics.csv`) at 1 Hz, and docs/02 now says it was
  not built.
- **Experiment A as a dose-response curve** (4096 as well as 8192) and **a
  lower `max_num_seqs`**: cut to keep A to one engine config. A lower
  `max_num_seqs` trades TTFT for TPOT with no goodput gain at the chosen SLOs.
- **Experiments B1 and B2 on a subset of levels**: run only around each
  baseline knee (plus interactive c=1 for B2), not the full sweeps, to save
  GPU time. The comparison tables show which levels were run on only one side.
