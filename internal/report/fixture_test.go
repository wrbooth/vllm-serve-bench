package report

import (
	"slices"
	"time"

	"github.com/wrbooth/vllm-serve-bench/internal/crosscheck"
	"github.com/wrbooth/vllm-serve-bench/internal/results"
)

var t0 = time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)

func fp(v float64) *float64 { return &v }

// interactiveSLO is the committed interactive SLO's shape.
var interactiveSLO = ProfileSLO{
	Decision: "test",
	Bounds: []Bound{
		{Metric: MetricTTFT, Percentile: 95, MaxMs: 100},
		{Metric: MetricTPOT, Percentile: 95, MaxMs: 25},
	},
}

var testSLO = SLO{Profiles: map[string]ProfileSLO{
	"interactive": interactiveSLO,
	"throughput":  {Decision: "test", Bounds: []Bound{{Metric: MetricE2E, Percentile: 95, MaxMs: 15000}}},
}}

// lvl is a level at concurrency c with the given output tok/s and p95s;
// every level's measured window is 10 s long and starts at c × 100 s.
func lvl(c int, tokPerS, ttft95, tpot95 float64) results.Level {
	start := t0.Add(time.Duration(c) * 100 * time.Second)
	return results.Level{
		Concurrency: c, MeasureStart: start, End: start.Add(10 * time.Second), WindowS: 10,
		Requests: 1, RPS: 0.1, OutputTokPerSec: tokPerS,
		TTFT: results.Dist{N: 1, P50ms: ttft95 / 2, P95ms: ttft95, P99ms: ttft95 + 1},
		TPOT: results.Dist{N: 1, P50ms: tpot95 / 2, P95ms: tpot95, P99ms: tpot95 + 1},
		E2E:  results.Dist{N: 1, P50ms: 1000, P95ms: 2000, P99ms: 3000},
	}
}

// okRow is one successful request at concurrency c, sent inside lvl's
// window, with TTFT and TPOT under the interactive limits.
func okRow(c, tokens int) results.Row {
	return results.Row{
		Concurrency: c, TSend: t0.Add(time.Duration(c)*100*time.Second + time.Second),
		TTFTms: fp(10), TPOTms: fp(10), E2Ems: fp(1000), PromptTokens: 433, CompletionTokens: tokens,
	}
}

// cleanTelemetry is 1 Hz telemetry over each level's window from 1 s
// before it opens to 1 s after it closes, in time order. Every counter is
// a multiple of the seconds since t0 = n, so any two samples give
// Δuncached / Δcount = (1000 − 887)·Δn / Δn = 113 tokens per request
// (unique part 100, so the engine check passes).
func cleanTelemetry(levels []results.Level) (counters []crosscheck.Sample, engine, gpu []Point) {
	for i := range levels {
		l := &levels[i]
		for s := -1; s <= 11; s++ {
			at := l.MeasureStart.Add(time.Duration(s) * time.Second)
			n := at.Sub(t0).Seconds() // counters grow with time, whatever order the levels come in
			counters = append(counters, crosscheck.Sample{At: at, PrefixQueries: 1000 * n, PrefixHits: 887 * n, TTFTSum: 0.05 * n, TTFTCount: n})
			engine = append(engine, Point{At: at, V: []float64{float64(l.Concurrency), 0, 0.25, 0}})
			gpu = append(gpu, Point{At: at, V: []float64{500}})
		}
	}
	slices.SortFunc(counters, func(a, b crosscheck.Sample) int { return a.At.Compare(b.At) })
	byTime := func(a, b Point) int { return a.At.Compare(b.At) }
	slices.SortFunc(engine, byTime)
	slices.SortFunc(gpu, byTime)
	return counters, engine, gpu
}

// newRun is a run with one request row per level (Requests = 1) of the
// given tokens, and clean telemetry.
func newRun(id, profile, engineConfig string, levels ...results.Level) Run {
	r := Run{
		Dir: "results/" + id,
		Config: results.Config{
			RunID: id, WarmupS: 10, DurationS: 10,
			Profile: results.ProfileConfig{Name: profile, UniqueWords: 100},
			Engine:  results.EngineConfig{Config: engineConfig},
		},
		Summary: results.Summary{RunID: id, Levels: levels},
	}
	for i := range levels {
		l := &levels[i]
		r.Rows = append(r.Rows, okRow(l.Concurrency, int(l.OutputTokPerSec*l.WindowS)))
	}
	r.Counters, r.Engine, r.GPU = cleanTelemetry(levels)
	return r
}
