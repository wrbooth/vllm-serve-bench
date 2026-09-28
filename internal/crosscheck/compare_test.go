package crosscheck

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wrbooth/vllm-serve-bench/internal/results"
)

// Regenerate the golden files with `go test ./internal/crosscheck -update`
// and review the diff.
var update = flag.Bool("update", false, "rewrite testdata/*.golden")

func ptr(v float64) *float64 { return &v }

// runFixture is one of our runs at concurrency 2: a 60 s window from
// t0+10 s, four successful rows (completion tokens 2, 3, 4, 7: mean 4) and
// one error row, and telemetry at 1 Hz whose uncached tokens grow by
// uncachedPerReq per request.
func runFixture(label string, uncachedPerReq float64) Run {
	lvl := results.Level{
		Concurrency:     2,
		MeasureStart:    t0.Add(10 * time.Second),
		End:             t0.Add(70 * time.Second),
		Requests:        5,
		Errors:          1,
		RPS:             1.5,
		OutputTokPerSec: 3.5,
		TTFT:            results.Dist{N: 4, P50ms: 15, P95ms: 25, P99ms: 25},
		TPOT:            results.Dist{N: 4, P50ms: 5, P95ms: 19.5, P99ms: 19.5},
		E2E:             results.Dist{N: 4, P50ms: 25, P95ms: 32, P99ms: 32},
	}
	var rows []results.Row
	for _, n := range []int{2, 3, 4, 7} {
		rows = append(rows, results.Row{Concurrency: 2, TTFTms: ptr(1), CompletionTokens: n})
	}
	rows = append(rows, results.Row{Concurrency: 2, Error: "boom"}, results.Row{Concurrency: 1, CompletionTokens: 1000})
	// Samples at 0..80 s, one request per second.
	var tel []Sample
	for i := 0; i <= 80; i++ {
		tel = append(tel, Sample{
			At:            t0.Add(time.Duration(i) * time.Second),
			PrefixQueries: 500 * float64(i),
			PrefixHits:    (500 - uncachedPerReq) * float64(i),
			TTFTSum:       float64(i) / 32,
			TTFTCount:     float64(i),
		})
	}
	return Run{
		Label:     label,
		Dir:       "a/run",
		Config:    results.Config{Profile: results.ProfileConfig{UniqueWords: 100}},
		Summary:   results.Summary{Levels: []results.Level{{Concurrency: 1}, lvl}},
		Rows:      rows,
		Telemetry: tel,
	}
}

func vllmFixture() VLLM {
	b := benchFixture()
	b.RequestThroughput = 1.5
	b.OutputThroughput = 3
	b.TotalOutputTokens = 6
	b.P50TTFTms, b.P95TTFTms, b.P99TTFTms = 16, 30, 31
	b.P50TPOTms, b.P95TPOTms, b.P99TPOTms = 12, 19, 19.5
	b.P50E2Ems, b.P95E2Ems, b.P99E2Ems = 27, 31, 31.2
	b.MeanTTFTms = (15.625 + 31.25 + 7.8125) / 3
	b.MeanTPOTms = (4.39453125 + 19.53125) / 2
	b.MeanE2Ems = 27.5
	return VLLM{
		Dir:          "vllm",
		Bench:        b,
		Telemetry:    []Sample{engFrom, engTo},
		UniqueTokens: 100,
	}
}

// Steady state of the vLLM fixture with a 0.25 s trim: sends at 100, 100,
// 101, 101.5 → window [100.25, 101.25] holds only 101: one send, no rate.
// With no trim: all four, span 1.5 s, (4 − 1) / 1.5 = 2.
func TestCompareBuildsTheTableAgainstVLLMRecomputed(t *testing.T) {
	t.Parallel()
	v := vllmFixture()
	c, err := Compare([]Run{runFixture("ours (a)", 110)}, &v, Options{Trim: 0, MinUncachedFraction: 0.9})
	if err != nil {
		t.Fatal(err)
	}
	if !c.Valid || len(c.Warnings) != 0 {
		t.Fatalf("Valid %v, warnings %q; want valid", c.Valid, c.Warnings)
	}
	row := func(name string) Row {
		for _, r := range c.Rows {
			if r.Metric == name {
				return r
			}
		}
		t.Fatalf("no row %q", name)
		return Row{}
	}
	// TTFT p50: ours 15 vs recomputed 15.625 → (15 − 15.625) / 15.625 = −4%.
	if r := row("TTFT p50 (ms)"); r.Ours[0] != 15 || r.Reported != 16 || *r.Recomputed != 15.625 || *r.DeltaPct[0] != -4 {
		t.Errorf("TTFT p50 row %+v", r)
	}
	// RPS: ours 1.5 vs the steady-state 2 (not recomputed-overall 1.5) → −25%.
	if r := row("RPS"); *r.Recomputed != 2 || r.Reported != 1.5 || *r.DeltaPct[0] != -25 {
		t.Errorf("RPS row %+v, want recomputed = steady-state 2, Δ −25%%", r)
	}
	// Mean output: ours (2+3+4+7) / 4 = 4 (the error row and the other
	// level excluded) vs vLLM 6 / 3 = 2 → +100%.
	if r := row("Mean output tokens"); r.Ours[0] != 4 || *r.Recomputed != 2 || *r.DeltaPct[0] != 100 {
		t.Errorf("Mean output row %+v", r)
	}
	// Counts carry no delta.
	if r := row("n"); r.DeltaPct != nil || r.Ours[0] != 4 || *r.Recomputed != 3 {
		t.Errorf("n row %+v", r)
	}
	// Overall RPS delta: 1.5 vs reported 1.5 → 0.
	if len(c.OverallRPSDeltaPct) != 1 || *c.OverallRPSDeltaPct[0] != 0 {
		t.Errorf("overall RPS delta %v", c.OverallRPSDeltaPct)
	}
	// Engine: ours over samples 10..70, 110 per request; vLLM 100.
	if len(c.Engine) != 2 || c.Engine[0].UncachedPerRequest != 110 || c.Engine[1].UncachedPerRequest != 100 {
		t.Errorf("engine %+v", c.Engine)
	}
}

// Too short for the trim: no steady-state rate, so the RPS row has no
// recomputed value and no delta rather than a delta against 0.
func TestCompareLeavesRPSUncomparedWithoutASteadyState(t *testing.T) {
	t.Parallel()
	v := vllmFixture()
	c, err := Compare([]Run{runFixture("ours (a)", 110)}, &v, Options{Trim: 250 * time.Millisecond, MinUncachedFraction: 0.9})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range c.Rows {
		if r.Metric == "RPS" && (r.Recomputed != nil || len(r.DeltaPct) != 1 || r.DeltaPct[0] != nil) {
			t.Errorf("RPS row %+v, want no recomputed value and a nil delta", r)
		}
	}
	// vLLM reported RPS of 0 makes the overall delta undefined too.
	v.Bench.RequestThroughput = 0
	if c, err = Compare([]Run{runFixture("ours (a)", 110)}, &v, DefaultOptions); err != nil || c.OverallRPSDeltaPct[0] != nil {
		t.Errorf("overall RPS delta against 0 = %v, %v; want nil", c.OverallRPSDeltaPct, err)
	}
}

func TestCompareMarksTheComparisonInvalidWhenASideWasCached(t *testing.T) {
	t.Parallel()
	v := vllmFixture()
	runs := []Run{runFixture("ours (a)", 110), runFixture("ours (b)", 1)}
	c, err := Compare(runs, &v, DefaultOptions)
	if err != nil {
		t.Fatal(err)
	}
	if c.Valid || len(c.Warnings) != 1 || !strings.HasPrefix(c.Warnings[0], "ours (b): prefilled 1.0 uncached") {
		t.Errorf("Valid %v, warnings %q; want one warning for ours (b)", c.Valid, c.Warnings)
	}
}

func TestCompareRejectsInputsItCannotCompare(t *testing.T) {
	t.Parallel()
	good := vllmFixture()
	if _, err := Compare(nil, &good, DefaultOptions); err == nil {
		t.Error("no runs: want an error")
	}
	noConc := vllmFixture()
	noConc.Bench.MaxConcurrency = 0
	if _, err := Compare([]Run{runFixture("a", 110)}, &noConc, DefaultOptions); err == nil {
		t.Error("max_concurrency 0: want an error")
	}
	noData := vllmFixture()
	noData.Bench.TTFTs = nil
	if _, err := Compare([]Run{runFixture("a", 110)}, &noData, DefaultOptions); err == nil {
		t.Error("no per-request data: want an error")
	}
	other := vllmFixture()
	other.Bench.MaxConcurrency = 8
	if _, err := Compare([]Run{runFixture("a", 110)}, &other, DefaultOptions); err == nil {
		t.Error("no level at vLLM's concurrency: want an error")
	}
	mismatch := runFixture("a", 110)
	mismatch.Rows = mismatch.Rows[1:]
	if _, err := Compare([]Run{mismatch}, &good, DefaultOptions); err == nil {
		t.Error("requests.jsonl disagrees with summary.json: want an error")
	}
}

// The golden files pin comparison.md and comparison.json byte for byte:
// one valid comparison and one flagged, with two of our runs each.
func TestComparisonOutputMatchesGolden(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		cached float64
		trim   time.Duration
	}{
		// Valid, and a steady-state rate (no trim: 2 req/s).
		{"valid", 110, 0},
		// ours (b) cached, and a trim that leaves one send: no rate.
		{"cached", 1, 250 * time.Millisecond},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			v := vllmFixture()
			c, err := Compare([]Run{runFixture("ours (a)", 110), runFixture("ours (b)", tt.cached)}, &v, Options{Trim: tt.trim, MinUncachedFraction: 0.9})
			if err != nil {
				t.Fatal(err)
			}
			js, err := json.MarshalIndent(&c, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			md := c.Markdown("bench verify --vllm vllm a/run b/run")
			golden(t, filepath.Join("testdata", tt.name+".md.golden"), md)
			golden(t, filepath.Join("testdata", tt.name+".json.golden"), append(js, '\n'))
		})
	}
}

func golden(t *testing.T, path string, got []byte) {
	t.Helper()
	if *update {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run `go test ./internal/crosscheck -update` to create it)", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("output differs from %s; if the change is intended, rerun with -update and review the diff\n--- got\n%s", path, got)
	}
}

func TestPctSignsAndNeverPrintsNegativeZero(t *testing.T) {
	t.Parallel()
	for v, want := range map[float64]string{1.234: "+1.23%", -1.234: "-1.23%", 0: "+0.00%", -0.001: "+0.00%"} {
		if got := pct(v, 2); got != want {
			t.Errorf("pct(%v) = %q, want %q", v, got, want)
		}
	}
}
