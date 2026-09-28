package report

import (
	"strings"
	"testing"

	"github.com/wrbooth/vllm-serve-bench/internal/results"
)

// Level c=4, 10 s window, 5 measured rows (TTFT ≤ 100, TPOT ≤ 25):
//
//	TTFT 100, TPOT 25, 120 tokens       meets (both equal to the limit)
//	TTFT 100.001, TPOT 10, 100 tokens   fails TTFT
//	TTFT 50, TPOT 25.001, 110 tokens    fails TPOT
//	TTFT 50, no TPOT, 1 token           meets (TPOT not defined)
//	error                                fails
//
// plus a c=8 row that is not this level's. Met 2, tokens 120 + 1 = 121,
// goodput 121 / 10 s = 12.1 tok/s, share 2 / 5 = 40%.
func TestPerRequestGoodputCountsTheTokensOfRequestsThatMetEveryLimit(t *testing.T) {
	t.Parallel()
	rows := []results.Row{
		{Concurrency: 4, TTFTms: fp(100), TPOTms: fp(25), E2Ems: fp(1), CompletionTokens: 120},
		{Concurrency: 4, TTFTms: fp(100.001), TPOTms: fp(10), E2Ems: fp(1), CompletionTokens: 100},
		{Concurrency: 4, TTFTms: fp(50), TPOTms: fp(25.001), E2Ems: fp(1), CompletionTokens: 110},
		{Concurrency: 4, TTFTms: fp(50), E2Ems: fp(1), CompletionTokens: 1},
		{Concurrency: 4, Error: "timeout"},
		{Concurrency: 8, TTFTms: fp(1), TPOTms: fp(1), E2Ems: fp(1), CompletionTokens: 999},
	}
	l := results.Level{Concurrency: 4, Requests: 5, WindowS: 10}
	g, err := PerRequestGoodput(rows, &l, &interactiveSLO)
	if err != nil {
		t.Fatal(err)
	}
	want := Goodput{Met: 2, Requests: 5, Tokens: 121, TokPerS: 12.1, SharePct: 40}
	if g != want {
		t.Errorf("PerRequestGoodput = %+v, want %+v", g, want)
	}
}

func TestPerRequestGoodputGuardsZeroWindowAndRejectsACountMismatch(t *testing.T) {
	t.Parallel()
	// No window, no rows: nothing to divide by, all zero.
	g, err := PerRequestGoodput(nil, &results.Level{Concurrency: 1}, &interactiveSLO)
	if err != nil || g != (Goodput{}) {
		t.Errorf("empty level = %+v, %v; want zeros", g, err)
	}
	// summary.json says 2 requests, requests.jsonl has 1: the files disagree.
	_, err = PerRequestGoodput([]results.Row{okRow(1, 10)}, &results.Level{Concurrency: 1, Requests: 2, WindowS: 10}, &interactiveSLO)
	if err == nil || !strings.Contains(err.Error(), "summary.json says 2") {
		t.Errorf("mismatch: error %v, want one naming both counts", err)
	}
}

func TestNewTableSortsLevelsAndFailsALevelWithErrors(t *testing.T) {
	t.Parallel()
	a, b := lvl(2, 20, 50, 10), lvl(1, 10, 50, 10)
	b.Errors = 1 // p95s pass, but a request was lost
	r := newRun("run-a", "interactive", "baseline", a, b)
	r.Rows[1] = results.Row{Concurrency: 1, Error: "HTTP 500"}
	tab, err := NewTable(&r, &testSLO, 0.9)
	if err != nil {
		t.Fatal(err)
	}
	if len(tab.Levels) != 2 || tab.Levels[0].Level.Concurrency != 1 || tab.Levels[1].Level.Concurrency != 2 {
		t.Fatalf("levels not in ascending concurrency: %+v", tab.Levels)
	}
	if l := &tab.Levels[0]; l.Meets || !l.Bounds[0].Pass || !l.Bounds[1].Pass {
		t.Errorf("c=1 with an error: Meets %v, bounds %+v; want both bounds passing and Meets false", l.Meets, l.Bounds)
	}
	if l := &tab.Levels[1]; !l.Meets || !l.Compliant() || l.Goodput.Met != 1 {
		t.Errorf("c=2: Meets %v Compliant %v goodput %+v; want compliant, 1 request met", l.Meets, l.Compliant(), l.Goodput)
	}
	if tab.RunID != "run-a" || tab.EngineConfig != "baseline" || tab.MinUncachedFraction != 0.9 {
		t.Errorf("table identity = %q %q %v", tab.RunID, tab.EngineConfig, tab.MinUncachedFraction)
	}
}

func TestNewTableRejectsBadInputs(t *testing.T) {
	t.Parallel()
	dup := newRun("dup", "interactive", "baseline", lvl(1, 10, 50, 10), lvl(1, 10, 50, 10))
	if _, err := NewTable(&dup, &testSLO, 0.9); err == nil || !strings.Contains(err.Error(), "appears twice") {
		t.Errorf("duplicate level: %v", err)
	}
	noSLO := newRun("x", "batch", "baseline", lvl(1, 10, 50, 10))
	if _, err := NewTable(&noSLO, &testSLO, 0.9); err == nil || !strings.Contains(err.Error(), `no SLO for profile "batch"`) {
		t.Errorf("unknown profile: %v", err)
	}
	short := newRun("short", "interactive", "baseline", lvl(1, 10, 50, 10))
	short.Rows = nil
	if _, err := NewTable(&short, &testSLO, 0.9); err == nil || !strings.Contains(err.Error(), "summary.json says 1") {
		t.Errorf("missing rows: %v", err)
	}
}

// table builds a judged table directly: tok/s per level, and whether it
// meets the SLO and passes the engine check.
func table(levels ...LevelRow) Table { return Table{RunID: "r", EngineConfig: "e", Levels: levels} }

func lr(c int, tokPerS float64, meets, valid bool) LevelRow {
	l := LevelRow{Level: results.Level{Concurrency: c, OutputTokPerSec: tokPerS}, Meets: meets}
	l.Engine.Check.Cached = !valid
	l.Engine.Check.OK = true
	return l
}

func TestMaxCompliantPicksTheHighestOutputAmongCompliantValidLevels(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		t     Table
		found bool
		best  int
		nextC int // 0: no next level
	}{
		{
			// 300 at c=2 and c=4 tie: the lower concurrency wins. c=8 has
			// more output but fails; c=16 has the most but is CACHED.
			"TieGoesToLowerConcurrency",
			table(lr(1, 100, true, true), lr(2, 300, true, true), lr(4, 300, true, true), lr(8, 500, false, true), lr(16, 900, true, false)),
			true, 2, 4,
		},
		{
			// Compliance need not be monotone: c=4 fails, c=8 meets with more.
			"NotTheFirstFailureBoundary",
			table(lr(2, 200, true, true), lr(4, 250, false, true), lr(8, 400, true, true)),
			true, 8, 0,
		},
		{"NoCompliantLevel", table(lr(1, 100, false, true), lr(2, 200, true, false)), false, 0, 0},
		{"Empty", table(), false, 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h := MaxCompliant(&tt.t)
			if h.Found != tt.found {
				t.Fatalf("Found = %v, want %v", h.Found, tt.found)
			}
			if !h.Found {
				if h.Best != nil || h.Next != nil {
					t.Errorf("no headline but Best %v Next %v", h.Best, h.Next)
				}
				return
			}
			if h.Best.Level.Concurrency != tt.best {
				t.Errorf("best at c=%d, want c=%d", h.Best.Level.Concurrency, tt.best)
			}
			switch {
			case tt.nextC == 0 && h.Next != nil:
				t.Errorf("Next = c=%d, want none", h.Next.Level.Concurrency)
			case tt.nextC != 0 && (h.Next == nil || h.Next.Level.Concurrency != tt.nextC):
				t.Errorf("Next = %v, want c=%d", h.Next, tt.nextC)
			}
		})
	}
}
