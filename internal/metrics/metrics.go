// Package metrics turns per-request timing facts into the client-side metrics
// defined in docs/02-architecture.md ("Metric definitions") and aggregates
// them. Every function here is pure: timestamps come in, numbers come out.
package metrics

import (
	"cmp"
	"math"
	"slices"
	"time"
)

// Timing is what one request produced on the wire. Times are from a single
// clock; zero means "did not happen".
type Timing struct {
	Send             time.Time // request body fully written
	FirstContent     time.Time // first chunk with non-empty delta.content
	Done             time.Time // [DONE] received
	PromptTokens     int       // from the usage chunk
	CompletionTokens int       // from the usage chunk
	FinishReason     string
	Err              error
}

// Record is one row of requests.jsonl before serialisation. An error row
// keeps Send (so it can be placed in or out of the measured window) and Err,
// and has no latency metrics.
type Record struct {
	Worker           int
	Send             time.Time
	TTFT             time.Duration
	E2E              time.Duration
	TPOT             time.Duration
	HasTPOT          bool // false when completion_tokens < 2 or on error
	PromptTokens     int
	CompletionTokens int
	FinishReason     string
	Err              string // non-empty marks an error row
}

// OK reports whether the request succeeded.
func (r *Record) OK() bool { return r.Err == "" }

// NewRecord applies the metric definitions to one request:
//
//	TTFT = FirstContent − Send
//	E2E  = Done − Send
//	TPOT = (E2E − TTFT) / (completion_tokens − 1), only when completion_tokens ≥ 2
//
// TPOT is computed in integer nanoseconds, so it truncates toward zero by
// under 1 ns. A timing with Err set, or without the timestamps a success
// needs, becomes an error row.
func NewRecord(worker int, t *Timing) Record {
	r := Record{
		Worker:           worker,
		Send:             t.Send,
		PromptTokens:     t.PromptTokens,
		CompletionTokens: t.CompletionTokens,
		FinishReason:     t.FinishReason,
	}
	switch {
	case t.Err != nil:
		r.Err = t.Err.Error()
		return r
	case t.Send.IsZero() || t.FirstContent.IsZero() || t.Done.IsZero():
		r.Err = "incomplete timing"
		return r
	}
	r.TTFT = t.FirstContent.Sub(t.Send)
	r.E2E = t.Done.Sub(t.Send)
	if t.CompletionTokens >= 2 {
		r.TPOT = (r.E2E - r.TTFT) / time.Duration(t.CompletionTokens-1)
		r.HasTPOT = true
	}
	return r
}

// Percentile is the nearest-rank percentile of an ascending-sorted sample:
// the value at 1-based rank ceil(p/100 · n), with p ≤ 0 giving the minimum
// and p ≥ 100 the maximum. ok is false for an empty sample.
//
// The rank is computed as ceil(p·n / 100), multiplying first: p·n is exact
// for integer p and n, whereas (p/100)·n is not (0.28·25 is
// 7.000000000000001 in float64, which would ceil to rank 8, not 7).
func Percentile[T cmp.Ordered](sorted []T, p float64) (v T, ok bool) {
	n := len(sorted)
	if n == 0 {
		return v, false
	}
	rank := int(math.Ceil(p * float64(n) / 100))
	rank = max(1, min(rank, n))
	return sorted[rank-1], true
}

// Dist summarises one latency metric over the requests that define it.
// N is reported next to the percentiles so a p99 over 40 samples is visibly
// weak. All values are zero when N is 0.
type Dist struct {
	N             int
	P50, P95, P99 time.Duration
}

func newDist(sample []time.Duration) Dist {
	slices.Sort(sample)
	d := Dist{N: len(sample)}
	d.P50, _ = Percentile(sample, 50)
	d.P95, _ = Percentile(sample, 95)
	d.P99, _ = Percentile(sample, 99)
	return d
}

// Summary aggregates the records of one measured window.
type Summary struct {
	Requests  int // all rows, errors included
	Errors    int
	ErrorRate float64 // Errors / Requests; 0 when there are no requests
	Window    time.Duration
	// RPS is completed (successful) requests per second of Window.
	RPS float64
	// OutputTokPerSec is Σ completion_tokens of successful requests per
	// second of Window.
	OutputTokPerSec float64
	TTFT, E2E, TPOT Dist
}

// Summarize aggregates records over a measured window of the given length.
// Error rows count toward Requests and Errors and toward nothing else.
func Summarize(records []Record, window time.Duration) Summary {
	s := Summary{Requests: len(records), Window: window}
	var ttft, e2e, tpot []time.Duration
	completed, tokens := 0, 0
	for i := range records {
		r := &records[i]
		if !r.OK() {
			s.Errors++
			continue
		}
		completed++
		tokens += r.CompletionTokens
		ttft = append(ttft, r.TTFT)
		e2e = append(e2e, r.E2E)
		if r.HasTPOT {
			tpot = append(tpot, r.TPOT)
		}
	}
	if s.Requests > 0 {
		s.ErrorRate = float64(s.Errors) / float64(s.Requests)
	}
	if secs := window.Seconds(); secs > 0 {
		s.RPS = float64(completed) / secs
		s.OutputTokPerSec = float64(tokens) / secs
	}
	s.TTFT, s.E2E, s.TPOT = newDist(ttft), newDist(e2e), newDist(tpot)
	return s
}
