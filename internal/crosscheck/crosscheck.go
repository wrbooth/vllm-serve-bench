// Package crosscheck compares `bench run` against vLLM's own client, `vllm
// bench serve`, run on the same engine with the same workload shape
// (docs/02-architecture.md, "Cross-check"). It is what `bench verify`
// computes; every function here is pure, and the files are read in load.go.
//
// Like is compared with like. vLLM reports numpy-interpolated percentiles;
// ours are nearest-rank (internal/metrics). So vLLM's per-request data is
// recomputed with our method, and the deltas are taken against that. Its
// reported figures are shown beside them.
package crosscheck

import (
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/wrbooth/vllm-serve-bench/internal/metrics"
	"github.com/wrbooth/vllm-serve-bench/internal/results"
)

// Options are the judgement calls, each a flag of `bench verify`.
type Options struct {
	// Trim is cut from each end of vLLM's sends for its steady-state rate.
	Trim time.Duration
	// MinUncachedFraction is the contamination threshold: a side is flagged
	// when it prefilled fewer than this fraction of its prompt's unique
	// part per request (see CheckEngine).
	MinUncachedFraction float64
}

// DefaultOptions are the values the committed comparisons use.
var DefaultOptions = Options{Trim: 10 * time.Second, MinUncachedFraction: 0.9}

// Run is one `bench run` directory, as read from disk.
type Run struct {
	Label     string // e.g. "ours (a)"
	Dir       string
	Config    results.Config
	Summary   results.Summary
	Rows      []results.Row
	Telemetry []Sample
}

// VLLM is the `vllm bench serve` side: its saved result, the telemetry
// `bench sample` recorded alongside it, and the length of its prompts'
// unique part (its --random-input-len).
type VLLM struct {
	Dir          string
	Bench        Bench
	Telemetry    []Sample
	UniqueTokens int
}

// Bench is the part of vLLM's --save-result --save-detailed JSON used here.
// Per-request arrays are parallel, one entry per measured request; times
// are in seconds.
type Bench struct {
	MaxConcurrency    int     `json:"max_concurrency"`
	Duration          float64 `json:"duration"`
	Completed         int     `json:"completed"`
	Failed            int     `json:"failed"`
	RequestThroughput float64 `json:"request_throughput"`
	OutputThroughput  float64 `json:"output_throughput"`
	TotalOutputTokens int     `json:"total_output_tokens"`

	MeanTTFTms float64 `json:"mean_ttft_ms"`
	P50TTFTms  float64 `json:"p50_ttft_ms"`
	P95TTFTms  float64 `json:"p95_ttft_ms"`
	P99TTFTms  float64 `json:"p99_ttft_ms"`
	MeanTPOTms float64 `json:"mean_tpot_ms"`
	P50TPOTms  float64 `json:"p50_tpot_ms"`
	P95TPOTms  float64 `json:"p95_tpot_ms"`
	P99TPOTms  float64 `json:"p99_tpot_ms"`
	MeanE2Ems  float64 `json:"mean_e2el_ms"`
	P50E2Ems   float64 `json:"p50_e2el_ms"`
	P95E2Ems   float64 `json:"p95_e2el_ms"`
	P99E2Ems   float64 `json:"p99_e2el_ms"`

	TTFTs      []float64   `json:"ttfts"`
	ITLs       [][]float64 `json:"itls"`
	OutputLens []int       `json:"output_lens"`
	StartTimes []float64   `json:"start_times"`
	Errors     []string    `json:"errors"`
}

// Sample is one successful row of vllm_metrics.csv: the counters the
// engine-side check needs.
type Sample struct {
	At            time.Time
	PrefixQueries float64 // vllm:prefix_cache_queries_total
	PrefixHits    float64 // vllm:prefix_cache_hits_total
	TTFTSum       float64 // vllm:time_to_first_token_seconds_sum
	TTFTCount     float64 // vllm:time_to_first_token_seconds_count
	PromptTokens  float64 // vllm:prompt_tokens_total
}

// Dist is a latency distribution in milliseconds.
type Dist struct {
	N   int     `json:"n"`
	P50 float64 `json:"p50_ms"`
	P95 float64 `json:"p95_ms"`
	P99 float64 `json:"p99_ms"`
}

// NearestRank is Dist over an unsorted sample, with internal/metrics'
// nearest-rank percentiles. The sample is not modified.
func NearestRank(sample []float64) Dist {
	s := slices.Clone(sample)
	slices.Sort(s)
	d := Dist{N: len(s)}
	d.P50, _ = metrics.Percentile(s, 50)
	d.P95, _ = metrics.Percentile(s, 95)
	d.P99, _ = metrics.Percentile(s, 99)
	return d
}

// Side is one column of the comparison.
type Side struct {
	Label            string  `json:"label"`
	N                int     `json:"n"` // successful requests
	Failed           int     `json:"failed"`
	TTFT             Dist    `json:"ttft"`
	TPOT             Dist    `json:"tpot"`
	E2E              Dist    `json:"e2e"`
	RPS              float64 `json:"rps"`
	OutputTokPerSec  float64 `json:"output_tok_per_s"`
	MeanOutputTokens float64 `json:"mean_output_tokens"`
}

// Ours reads one run's level at the given concurrency: the percentiles,
// RPS and output tok/s from summary.json, the mean output length from
// requests.jsonl (successful rows of that level).
func Ours(r *Run, concurrency int) (Side, error) {
	i := slices.IndexFunc(r.Summary.Levels, func(l results.Level) bool { return l.Concurrency == concurrency })
	if i < 0 {
		return Side{}, fmt.Errorf("%s: no level at concurrency %d in summary.json", r.Dir, concurrency)
	}
	l := &r.Summary.Levels[i]
	tokens, n := 0, 0
	for j := range r.Rows {
		if row := &r.Rows[j]; row.Concurrency == concurrency && row.OK() {
			tokens += row.CompletionTokens
			n++
		}
	}
	s := Side{
		Label:           r.Label,
		N:               l.TTFT.N,
		Failed:          l.Errors,
		TTFT:            Dist{N: l.TTFT.N, P50: l.TTFT.P50ms, P95: l.TTFT.P95ms, P99: l.TTFT.P99ms},
		TPOT:            Dist{N: l.TPOT.N, P50: l.TPOT.P50ms, P95: l.TPOT.P95ms, P99: l.TPOT.P99ms},
		E2E:             Dist{N: l.E2E.N, P50: l.E2E.P50ms, P95: l.E2E.P95ms, P99: l.E2E.P99ms},
		RPS:             l.RPS,
		OutputTokPerSec: l.OutputTokPerSec,
	}
	if n != s.N {
		return Side{}, fmt.Errorf("%s: requests.jsonl has %d successful rows at concurrency %d, summary.json says %d", r.Dir, n, concurrency, s.N)
	}
	if n > 0 {
		s.MeanOutputTokens = float64(tokens) / float64(n)
	}
	return s, nil
}

// Reported is vLLM's own figures, as it printed them. Its TPOT count is
// not in the file, so TPOT.N is the completed count like the others.
func Reported(b *Bench) Side {
	s := Side{
		Label:            "vLLM reported",
		N:                b.Completed,
		Failed:           b.Failed,
		TTFT:             Dist{N: b.Completed, P50: b.P50TTFTms, P95: b.P95TTFTms, P99: b.P99TTFTms},
		TPOT:             Dist{N: b.Completed, P50: b.P50TPOTms, P95: b.P95TPOTms, P99: b.P99TPOTms},
		E2E:              Dist{N: b.Completed, P50: b.P50E2Ems, P95: b.P95E2Ems, P99: b.P99E2Ems},
		RPS:              b.RequestThroughput,
		OutputTokPerSec:  b.OutputThroughput,
		MeanOutputTokens: 0,
	}
	if b.Completed > 0 {
		s.MeanOutputTokens = float64(b.TotalOutputTokens) / float64(b.Completed)
	}
	return s
}

// Request is one of vLLM's requests, derived from its per-request arrays
// with docs/02's metric definitions, in milliseconds.
type Request struct {
	Start     float64 // s, vLLM's monotonic clock
	OK        bool
	TTFT, E2E float64
	TPOT      float64
	HasTPOT   bool // false when output_len < 2, as in docs/02
	OutputLen int
}

// Requests derives each request's metrics:
//
//	TTFT = ttfts[i]
//	E2E  = ttfts[i] + Σ itls[i]
//	TPOT = Σ itls[i] / (output_lens[i] − 1), only when output_lens[i] ≥ 2
//
// vLLM's itls hold one gap per chunk after the first token, so Σ itls is
// the time from the first token to the last, the same span as our
// E2E − TTFT, and (output_len − 1) is vLLM's own TPOT divisor. Checked on
// results/verify/20260927-interactive-c8-varied-output: the recomputed
// means match vLLM's reported mean_ttft_ms exactly and mean_tpot_ms and
// mean_e2el_ms to within 0.001% (its latency is stamped a few µs after the
// last token arrives). Dividing by output_len instead would put mean TPOT
// 0.79% under vLLM's, 1,300 times the residual.
// comparison.md prints this check for every round (MeanCheck).
func Requests(b *Bench) ([]Request, error) {
	n := len(b.TTFTs)
	if n == 0 {
		return nil, errors.New("vllm-bench.json has no per-request data (run with --save-detailed)")
	}
	if len(b.ITLs) != n || len(b.OutputLens) != n || len(b.StartTimes) != n {
		return nil, fmt.Errorf("vllm-bench.json: per-request arrays differ in length (ttfts %d, itls %d, output_lens %d, start_times %d)",
			n, len(b.ITLs), len(b.OutputLens), len(b.StartTimes))
	}
	out := make([]Request, n)
	for i := range n {
		r := Request{Start: b.StartTimes[i], OK: i >= len(b.Errors) || b.Errors[i] == "", OutputLen: b.OutputLens[i]}
		if r.OK {
			gaps := 0.0
			for _, g := range b.ITLs[i] {
				gaps += g
			}
			r.TTFT = b.TTFTs[i] * 1000
			r.E2E = (b.TTFTs[i] + gaps) * 1000
			if r.OutputLen >= 2 {
				r.TPOT = gaps * 1000 / float64(r.OutputLen-1)
				r.HasTPOT = true
			}
		}
		out[i] = r
	}
	return out, nil
}

// Recomputed applies our nearest-rank method to vLLM's requests. RPS and
// output tok/s are over vLLM's whole duration, as it reports them;
// Comparison.Steady is the like-for-like rate.
func Recomputed(reqs []Request, duration float64) Side {
	var ttft, tpot, e2e []float64
	s := Side{Label: "vLLM recomputed"}
	tokens := 0
	for i := range reqs {
		r := &reqs[i]
		if !r.OK {
			s.Failed++
			continue
		}
		ttft = append(ttft, r.TTFT)
		e2e = append(e2e, r.E2E)
		if r.HasTPOT {
			tpot = append(tpot, r.TPOT)
		}
		tokens += r.OutputLen
	}
	s.N = len(ttft)
	s.TTFT, s.TPOT, s.E2E = NearestRank(ttft), NearestRank(tpot), NearestRank(e2e)
	if duration > 0 {
		s.RPS = float64(s.N) / duration
		s.OutputTokPerSec = float64(tokens) / duration
	}
	if s.N > 0 {
		s.MeanOutputTokens = float64(tokens) / float64(s.N)
	}
	return s
}

// byStart returns request indexes in send order; ties keep file order.
func byStart(reqs []Request) []int {
	idx := make([]int, len(reqs))
	for i := range idx {
		idx[i] = i
	}
	slices.SortStableFunc(idx, func(a, b int) int {
		switch {
		case reqs[a].Start < reqs[b].Start:
			return -1
		case reqs[a].Start > reqs[b].Start:
			return 1
		}
		return 0
	})
	return idx
}

// Burst is vLLM's TTFT without its start burst.
type Burst struct {
	Excluded int  `json:"excluded"` // the first max_concurrency requests by send time
	TTFT     Dist `json:"ttft"`
}

// ExcludeBurst drops the first `burst` requests by send time and returns
// the nearest-rank TTFT of the successful rest. vLLM's measured run opens
// with all max_concurrency requests sent at once, into an idle engine that
// prefills them together; our warmup absorbs that burst before the window
// opens.
func ExcludeBurst(reqs []Request, burst int) Burst {
	order := byStart(reqs)
	burst = max(0, min(burst, len(order)))
	var ttft []float64
	for _, i := range order[burst:] {
		if reqs[i].OK {
			ttft = append(ttft, reqs[i].TTFT)
		}
	}
	return Burst{Excluded: burst, TTFT: NearestRank(ttft)}
}

// Steady is vLLM's send rate away from its ramp-up and drain.
type Steady struct {
	TrimS float64 `json:"trim_s"`
	// FromS and ToS bound the window, in seconds after the first send.
	FromS float64 `json:"from_s"`
	ToS   float64 `json:"to_s"`
	Sends int     `json:"sends"`  // sends inside the window, edges included
	SpanS float64 `json:"span_s"` // last send in the window − first send in it
	RPS   float64 `json:"rps"`    // (Sends − 1) / SpanS
	OK    bool    `json:"ok"`     // false when fewer than two sends, or zero span
}

// SteadyState counts the sends in [first + trim, last − trim] and returns
// (n − 1) / (t_last − t_first) over the sends inside: n sends bound n − 1
// intervals, so the rate is intervals over the time they span. In a closed
// loop at a fixed concurrency the send rate is the completion rate.
func SteadyState(reqs []Request, trim time.Duration) Steady {
	st := Steady{TrimS: trim.Seconds()}
	if len(reqs) == 0 {
		return st
	}
	order := byStart(reqs)
	first, last := reqs[order[0]].Start, reqs[order[len(order)-1]].Start
	lo, hi := first+trim.Seconds(), last-trim.Seconds()
	st.FromS, st.ToS = lo-first, hi-first
	var in []float64
	for _, i := range order {
		if s := reqs[i].Start; s >= lo && s <= hi {
			in = append(in, s)
		}
	}
	st.Sends = len(in)
	if len(in) < 2 {
		return st
	}
	st.SpanS = in[len(in)-1] - in[0]
	if st.SpanS <= 0 {
		return st
	}
	st.RPS = float64(len(in)-1) / st.SpanS
	st.OK = true
	return st
}

// MeanCheck sets one recomputed mean against vLLM's reported one: the
// check that the per-request derivation reads vLLM's data as vLLM does.
type MeanCheck struct {
	Metric     string  `json:"metric"`
	Reported   float64 `json:"reported_ms"`
	Recomputed float64 `json:"recomputed_ms"`
	DeltaPct   float64 `json:"delta_pct"`
}

// MeanChecks compares the recomputed mean TTFT, TPOT and E2E with vLLM's.
func MeanChecks(reqs []Request, b *Bench) []MeanCheck {
	var sums [3]float64
	var ns [3]int
	for i := range reqs {
		r := &reqs[i]
		if !r.OK {
			continue
		}
		sums[0] += r.TTFT
		sums[2] += r.E2E
		ns[0]++
		ns[2]++
		if r.HasTPOT {
			sums[1] += r.TPOT
			ns[1]++
		}
	}
	reported := [3]float64{b.MeanTTFTms, b.MeanTPOTms, b.MeanE2Ems}
	out := make([]MeanCheck, 3)
	for i, name := range []string{"TTFT", "TPOT", "E2E"} {
		c := MeanCheck{Metric: name, Reported: reported[i]}
		if ns[i] > 0 {
			c.Recomputed = sums[i] / float64(ns[i])
		}
		if d, ok := deltaPct(c.Recomputed, c.Reported); ok {
			c.DeltaPct = d
		}
		out[i] = c
	}
	return out
}

// deltaPct is (x − ref) / ref × 100; ok is false when ref is 0.
func deltaPct(x, ref float64) (float64, bool) {
	if ref == 0 {
		return 0, false
	}
	return (x - ref) / ref * 100, true
}
