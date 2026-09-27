package timesheet

import (
	"strings"
	"testing"
	"time"
)

// at builds a UTC time on a fixed day from "15:04:05", so fixtures read as
// clock times and the arithmetic in each case can be checked by eye.
func at(t *testing.T, clock string) time.Time {
	t.Helper()
	c, err := time.Parse(time.TimeOnly, clock)
	if err != nil {
		t.Fatal(err)
	}
	return time.Date(2026, 9, 27, c.Hour(), c.Minute(), c.Second(), 0, time.UTC)
}

func TestSessionsSplitOnlyOnGapsLongerThanThreshold(t *testing.T) {
	t.Parallel()
	gap := 5 * time.Minute
	cases := []struct {
		name   string
		events []string
		want   [][2]string // start, end per session
		counts []int
	}{
		{name: "empty input has no sessions"},
		{
			name:   "one event is a zero-length session",
			events: []string{"15:00:00"},
			want:   [][2]string{{"15:00:00", "15:00:00"}},
			counts: []int{1},
		},
		{
			// 15:05:00 - 15:00:00 = 5m, equal to the threshold: same session.
			name:   "gap equal to threshold stays in session",
			events: []string{"15:00:00", "15:05:00"},
			want:   [][2]string{{"15:00:00", "15:05:00"}},
			counts: []int{2},
		},
		{
			// 15:05:01 - 15:00:00 = 5m01s > 5m: split.
			name:   "gap one second over threshold splits",
			events: []string{"15:00:00", "15:05:01"},
			want:   [][2]string{{"15:00:00", "15:00:00"}, {"15:05:01", "15:05:01"}},
			counts: []int{1, 1},
		},
		{
			// Gaps are measured from the previous event, not the session start:
			// 15:00 -> 15:04 -> 15:08 -> 15:12 chains (4m each) into one 12m
			// session; 15:12 -> 15:30 (18m) splits.
			name:   "chained short gaps extend one session",
			events: []string{"15:00:00", "15:04:00", "15:08:00", "15:12:00", "15:30:00"},
			want:   [][2]string{{"15:00:00", "15:12:00"}, {"15:30:00", "15:30:00"}},
			counts: []int{4, 1},
		},
		{
			name:   "unsorted input and ties are handled",
			events: []string{"15:30:00", "15:00:00", "15:02:00", "15:02:00"},
			want:   [][2]string{{"15:00:00", "15:02:00"}, {"15:30:00", "15:30:00"}},
			counts: []int{3, 1},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var events []time.Time
			for _, e := range tc.events {
				events = append(events, at(t, e))
			}
			got := Sessions(events, gap)
			if len(got) != len(tc.want) {
				t.Fatalf("got %d sessions %v, want %d", len(got), got, len(tc.want))
			}
			for i, w := range tc.want {
				if !got[i].Start.Equal(at(t, w[0])) || !got[i].End.Equal(at(t, w[1])) || got[i].Events != tc.counts[i] {
					t.Errorf("session %d = %s..%s (%d events), want %s..%s (%d events)",
						i, got[i].Start.Format(time.TimeOnly), got[i].End.Format(time.TimeOnly), got[i].Events,
						w[0], w[1], tc.counts[i])
				}
			}
		})
	}
}

func TestSessionsDoesNotReorderCallersSlice(t *testing.T) {
	t.Parallel()
	events := []time.Time{at(t, "15:30:00"), at(t, "15:00:00")}
	Sessions(events, time.Minute)
	if !events[0].Equal(at(t, "15:30:00")) {
		t.Fatal("Sessions sorted the caller's slice in place")
	}
}

func TestTranscriptTimesKeepsTimestampedRecordsAndSkipsBadLines(t *testing.T) {
	t.Parallel()
	in := strings.Join([]string{
		`{"type":"user","timestamp":"2026-09-27T23:18:23.497Z","message":{"content":"hi"}}`,
		`{"type":"ai-title","title":"no timestamp, not activity"}`,
		`{"type":"assistant","timestamp":"2026-09-27T23:18:29Z"}`,
		``,
		`{"type":"user","timestamp":"2026-09-27T23:2`, // half-written tail of a live transcript
	}, "\n")
	got, skipped, err := TranscriptTimes(strings.NewReader(in), "")
	if err != nil {
		t.Fatal(err)
	}
	// The empty line and the truncated line both fail to parse: 2 skipped.
	if skipped != 2 {
		t.Errorf("skipped = %d, want 2", skipped)
	}
	want := []time.Time{
		time.Date(2026, 9, 27, 23, 18, 23, 497_000_000, time.UTC),
		time.Date(2026, 9, 27, 23, 18, 29, 0, time.UTC),
	}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if !got[i].Equal(want[i]) {
			t.Errorf("time %d = %v, want %v", i, got[i], want[i])
		}
	}
}

func TestTranscriptTimesWithMentionKeepsOnlyRecordsNamingTheRepo(t *testing.T) {
	t.Parallel()
	in := strings.Join([]string{
		`{"timestamp":"2026-09-27T15:28:00Z","message":{"content":"other project"}}`,
		`{"timestamp":"2026-09-27T22:20:00Z","message":{"content":"cd ~/work/vllm-serve-bench"}}`,
		`{"timestamp":"2026-09-27T22:23:00Z","message":{"content":"thinking, no path"}}`,
		`{"timestamp":"2026-09-27T22:25:00Z","toolUseResult":{"file":"/x/vllm-serve-bench/Makefile"}}`,
		`not json and no mention`, // filtered before parsing: not counted as skipped
	}, "\n")
	got, skipped, err := TranscriptTimes(strings.NewReader(in), "vllm-serve-bench")
	if err != nil {
		t.Fatal(err)
	}
	// Only the 22:20 and 22:25 records name the repo.
	want := []time.Time{at(t, "22:20:00"), at(t, "22:25:00")}
	if skipped != 0 || len(got) != len(want) || !got[0].Equal(want[0]) || !got[1].Equal(want[1]) {
		t.Fatalf("got %v (skipped %d), want %v (skipped 0)", got, skipped, want)
	}
}

func TestGitTimesParsesAuthorDatesAndRejectsGarbage(t *testing.T) {
	t.Parallel()
	got, err := GitTimes(strings.NewReader("2026-09-27T16:15:02-07:00\n\n2026-09-27T15:30:00-07:00\n"))
	if err != nil {
		t.Fatal(err)
	}
	// 16:15:02 PDT is 23:15:02 UTC.
	if len(got) != 2 || !got[0].Equal(time.Date(2026, 9, 27, 23, 15, 2, 0, time.UTC)) {
		t.Fatalf("got %v", got)
	}
	if _, err := GitTimes(strings.NewReader("yesterday-ish\n")); err == nil {
		t.Fatal("want an error for an unparseable git log line")
	}
}

func TestWriteMarkdownRoundsPerRowAndTotalsTheRoundedRows(t *testing.T) {
	t.Parallel()
	pdt := time.FixedZone("PDT", -7*60*60)
	sessions := []Session{
		// 22:00:00..23:14:40 UTC = 1h14m40s, rounds to 1 h 15 m; 15:00-16:14 PDT.
		{Start: at(t, "22:00:00"), End: at(t, "23:14:40")},
		// 23:18:23..23:20:10 = 1m47s, rounds to 2 m.
		{Start: at(t, "23:18:23"), End: at(t, "23:20:10")},
		// Single event: 0 m.
		{Start: at(t, "23:40:00"), End: at(t, "23:40:00")},
	}
	var b strings.Builder
	if err := WriteMarkdown(&b, sessions, pdt); err != nil {
		t.Fatal(err)
	}
	// Total = 1h15m + 2m + 0m = 1 h 17 m (the unrounded sum, 1h16m27s, would
	// round to 1 h 16 m and the column would not add up).
	want := `| # | Date | Start | End | Active |
|---|---|---|---|---|
| 1 | 2026-09-27 | 15:00 | 16:14 | 1 h 15 m |
| 2 | 2026-09-27 | 16:18 | 16:20 | 0 h 02 m |
| 3 | 2026-09-27 | 16:40 | 16:40 | 0 h 00 m |

**Total:** 1 h 17 m over 3 sessions.
`
	if b.String() != want {
		t.Errorf("got:\n%s\nwant:\n%s", b.String(), want)
	}
}

func TestProjectDirNameMatchesClaudeCodeEncoding(t *testing.T) {
	t.Parallel()
	// Observed on disk: /Users/me/work/vllm-serve-bench is stored as
	// -Users-me-work-vllm-serve-bench; dots and underscores become '-' too.
	cases := map[string]string{
		"/Users/me/work/vllm-serve-bench": "-Users-me-work-vllm-serve-bench",
		"/home/me/.local/a_b":             "-home-me--local-a-b",
	}
	for in, want := range cases {
		if got := ProjectDirName(in); got != want {
			t.Errorf("ProjectDirName(%q) = %q, want %q", in, got, want)
		}
	}
}
