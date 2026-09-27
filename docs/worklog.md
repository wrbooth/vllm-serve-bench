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

Generated 2026-09-27 16:33 (the last session was still in progress):

| # | Date | Start | End | Active |
|---|---|---|---|---|
| 1 | 2026-09-27 | 08:32 | 08:32 | 0 h 00 m |
| 2 | 2026-09-27 | 11:01 | 11:04 | 0 h 02 m |
| 3 | 2026-09-27 | 15:20 | 15:39 | 0 h 20 m |
| 4 | 2026-09-27 | 16:02 | 16:33 | 0 h 31 m |

**Total:** 0 h 53 m over 4 sessions.

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

## Cut / deferred

(Filled in as decisions are made.)
