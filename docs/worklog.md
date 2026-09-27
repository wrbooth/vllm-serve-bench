# Worklog

Time-boxed take-home (4–6 h budget), worked in separate sessions rather than one
sitting. Times are wall clock, Pacific.

**How time is counted.** Sessions are measured, not declared. `make timesheet`
rebuilds them from activity timestamps: every record in this repo's Claude Code
transcripts (the primary checkout and its worktrees) plus every commit. Any
silence longer than 5 minutes ends a session, and a session's active time runs
from its first event to its last. Two consequences. Time spent while an agent
works counts, even if the owner has been called away. Time spent off the
keyboard (reading, thinking, watching a run) only counts when it falls inside a
gap of 5 minutes or less. The logic and its tests are in
[internal/timesheet](../internal/timesheet/).

The Sessions table below is pasted from `make timesheet` output and is never
edited by hand. Work that left no local trace (a claude.ai cloud session, work
done offline) goes under Manual entries, one line each with the reason, and is
added to the total separately.

The build order and the status of each deliverable live in
[wiki/project.md](../wiki/project.md). The reasons behind the decisions live in
[wiki/log.md](../wiki/log.md).

## Sessions

Generated 2026-09-27 16:29 (the last session was still in progress):

| # | Date | Start | End | Active |
|---|---|---|---|---|
| 1 | 2026-09-27 | 15:30 | 15:30 | 0 h 00 m |
| 2 | 2026-09-27 | 15:36 | 15:36 | 0 h 00 m |
| 3 | 2026-09-27 | 16:06 | 16:29 | 0 h 23 m |

**Total:** 0 h 23 m over 3 sessions.

### Manual entries

- **2026-09-27, 15:00 to 16:06: needs the owner's confirmation.** This work
  was done in a claude.ai cloud session, which leaves no local transcript, so
  only the commits at 15:30 and 15:36 show up above. It covered the brief,
  scope, the problem statement and architecture docs, and the repo tooling. An
  earlier version of this worklog put it at 15:00 to 16:10 in one sitting;
  that has not been checked.

### Notes

- Docs were written before any code, so the metric definitions and the rule
  that the SLO comes after the baseline were in writing first. By 16:15 all
  time had gone into docs and tooling; no bench code yet.

## Cut / deferred

(Filled in as decisions are made.)
