// Package timesheet reconstructs work sessions from activity timestamps, so
// the time spent on the take-home is measured rather than remembered.
//
// The owner works in short bursts between interruptions and leaves agent
// conversations open for hours, so neither "conversation start to end" nor
// "first to last commit" says how long anything took. Instead every timestamp
// that shows the project being worked (a Claude Code transcript record, a
// commit) is an activity event, and any silence longer than the gap threshold
// ends a session. A session's active time runs from its first event to its
// last; a session with a single event counts as zero.
package timesheet

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"
)

// Session is one stretch of continuous activity.
type Session struct {
	Start, End time.Time
	Events     int
}

// Active is the session's measured duration.
func (s Session) Active() time.Duration { return s.End.Sub(s.Start) }

// Sessions groups events into sessions: a gap strictly longer than gap starts
// a new one, a gap exactly equal to it does not. Input order does not matter.
func Sessions(events []time.Time, gap time.Duration) []Session {
	if len(events) == 0 {
		return nil
	}
	ts := slices.Clone(events)
	slices.SortFunc(ts, func(a, b time.Time) int { return a.Compare(b) })

	out := []Session{{Start: ts[0], End: ts[0], Events: 1}}
	for _, t := range ts[1:] {
		cur := &out[len(out)-1]
		if t.Sub(cur.End) > gap {
			out = append(out, Session{Start: t, End: t, Events: 1})
			continue
		}
		cur.End = t
		cur.Events++
	}
	return out
}

// TranscriptTimes returns the timestamp of every record in a Claude Code
// JSONL transcript that has one. Records without a timestamp (titles, mode
// changes) are not activity. A line that does not parse is skipped and
// counted rather than failing the read: the transcript of a live conversation
// can end in a half-written line.
//
// A non-empty mention keeps only records whose raw line contains it. That is
// for a conversation started from another directory that worked on this repo
// among other things: a record there counts only if it names this repo (a
// path, a command, a file it read), so the other work in it is not billed
// here. It undercounts slightly, since a turn spent thinking without naming
// the repo is dropped.
func TranscriptTimes(r io.Reader, mention string) (times []time.Time, skipped int, err error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 1<<20), 64<<20) // tool results can be large lines
	for sc.Scan() {
		if mention != "" && !bytes.Contains(sc.Bytes(), []byte(mention)) {
			continue
		}
		var rec struct {
			Timestamp *time.Time `json:"timestamp"`
		}
		if json.Unmarshal(sc.Bytes(), &rec) != nil {
			skipped++
			continue
		}
		if rec.Timestamp != nil {
			times = append(times, *rec.Timestamp)
		}
	}
	return times, skipped, sc.Err()
}

// GitTimes parses `git log --format=%aI` output: one RFC 3339 time per line.
// Unlike a transcript this is our own command's output, so a bad line is an
// error.
func GitTimes(r io.Reader) ([]time.Time, error) {
	var times []time.Time
	sc := bufio.NewScanner(r)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		t, err := time.Parse(time.RFC3339, line)
		if err != nil {
			return nil, fmt.Errorf("git log line %d: %w", n, err)
		}
		times = append(times, t)
	}
	return times, sc.Err()
}

// WriteMarkdown writes the sessions as the worklog's table, times in loc,
// followed by the total. Active time is rounded to the minute per session and
// the total is the sum of the rounded rows, so the table adds up on the page.
func WriteMarkdown(w io.Writer, sessions []Session, loc *time.Location) error {
	var b strings.Builder
	b.WriteString("| # | Date | Start | End | Active |\n|---|---|---|---|---|\n")
	var total time.Duration
	for i, s := range sessions {
		active := s.Active().Round(time.Minute)
		total += active
		start, end := s.Start.In(loc), s.End.In(loc)
		fmt.Fprintf(&b, "| %d | %s | %s | %s | %s |\n",
			i+1, start.Format(time.DateOnly), start.Format("15:04"), end.Format("15:04"), hm(active))
	}
	fmt.Fprintf(&b, "\n**Total:** %s over %d sessions.\n", hm(total), len(sessions))
	_, err := io.WriteString(w, b.String())
	return err
}

func hm(d time.Duration) string {
	m := int(d / time.Minute)
	return fmt.Sprintf("%d h %02d m", m/60, m%60)
}

// ProjectDirName is the directory name Claude Code keeps a checkout's
// transcripts under in ~/.claude/projects: the absolute path with every
// character that is not a letter or digit replaced by '-'. Worktrees sit
// beside the primary checkout as <primary>-<slug>, so their directories share
// this name as a prefix.
func ProjectDirName(absPath string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			return r
		}
		return '-'
	}, absPath)
}
