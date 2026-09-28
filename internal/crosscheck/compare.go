package crosscheck

import (
	"errors"
	"fmt"
)

// Comparison is everything `bench verify` reports; comparison.json is this
// struct and comparison.md renders it.
type Comparison struct {
	Concurrency         int     `json:"concurrency"`
	TrimS               float64 `json:"trim_s"`
	MinUncachedFraction float64 `json:"min_uncached_fraction"`

	Ours           []Side `json:"ours"`
	VLLMReported   Side   `json:"vllm_reported"`
	VLLMRecomputed Side   `json:"vllm_recomputed"`

	// Rows is the table: each metric for every side, and the deltas of each
	// of ours against vLLM recomputed.
	Rows []Row `json:"rows"`

	Means  []MeanCheck `json:"vllm_mean_check"`
	Burst  Burst       `json:"vllm_without_start_burst"`
	Steady Steady      `json:"vllm_steady_state"`
	// OverallRPSDeltaPct is each of ours against vLLM's reported
	// request_throughput, ramp-up and drain included.
	OverallRPSDeltaPct []*float64 `json:"overall_rps_delta_pct"`
	Engine             []Engine   `json:"engine"`

	// Valid is false when any side's engine-side check failed; Warnings
	// says which and why.
	Valid    bool     `json:"valid"`
	Warnings []string `json:"warnings"`
}

// Row is one metric across the sides.
type Row struct {
	Metric   string    `json:"metric"`
	Ours     []float64 `json:"ours"`
	Reported float64   `json:"vllm_reported"`
	// Recomputed is null when it cannot be computed: the steady-state RPS
	// of a run too short for the trim.
	Recomputed *float64 `json:"vllm_recomputed"`
	// DeltaPct is (ours − recomputed) / recomputed × 100, one per run,
	// null where recomputed is null or 0; absent for counts.
	DeltaPct []*float64 `json:"delta_pct,omitempty"`
	digits   int        // decimals in comparison.md
}

// Compare builds the comparison of our runs against vLLM's.
func Compare(runs []Run, v *VLLM, opt Options) (Comparison, error) {
	if len(runs) == 0 {
		return Comparison{}, errors.New("no run directories to compare")
	}
	b := &v.Bench
	if b.MaxConcurrency <= 0 {
		return Comparison{}, fmt.Errorf("vllm-bench.json: max_concurrency %d; the cross-check needs a fixed concurrency", b.MaxConcurrency)
	}
	reqs, err := Requests(b)
	if err != nil {
		return Comparison{}, err
	}
	c := Comparison{
		Concurrency:         b.MaxConcurrency,
		TrimS:               opt.Trim.Seconds(),
		MinUncachedFraction: opt.MinUncachedFraction,
		VLLMReported:        Reported(b),
		VLLMRecomputed:      Recomputed(reqs, b.Duration),
		Means:               MeanChecks(reqs, b),
		Burst:               ExcludeBurst(reqs, b.MaxConcurrency),
		Steady:              SteadyState(reqs, opt.Trim),
		Valid:               true,
		Warnings:            []string{},
	}
	for i := range runs {
		s, err := Ours(&runs[i], c.Concurrency)
		if err != nil {
			return Comparison{}, err
		}
		c.Ours = append(c.Ours, s)
		c.OverallRPSDeltaPct = append(c.OverallRPSDeltaPct, deltaPtr(s.RPS, b.RequestThroughput))
		c.Engine = append(c.Engine, EngineOurs(&runs[i], c.Concurrency, opt.MinUncachedFraction))
	}
	c.Engine = append(c.Engine, EngineVLLM(v, opt.MinUncachedFraction))
	for i := range c.Engine {
		if e := &c.Engine[i]; e.Cached {
			c.Valid = false
			c.Warnings = append(c.Warnings, e.Label+": "+e.Note+". The comparison is not valid.")
		}
	}
	c.Rows = rows(&c)
	return c, nil
}

// rows lays out the table. RPS is set against vLLM's steady-state rate,
// which, like ours, excludes ramp-up and drain.
func rows(c *Comparison) []Row {
	rec := c.VLLMRecomputed
	rec.RPS = c.Steady.RPS
	type metric struct {
		name   string
		digits int
		get    func(*Side) float64
		count  bool
	}
	ms := []metric{
		{"TTFT p50 (ms)", 2, func(s *Side) float64 { return s.TTFT.P50 }, false},
		{"TTFT p95 (ms)", 2, func(s *Side) float64 { return s.TTFT.P95 }, false},
		{"TTFT p99 (ms)", 2, func(s *Side) float64 { return s.TTFT.P99 }, false},
		{"TPOT p50 (ms)", 3, func(s *Side) float64 { return s.TPOT.P50 }, false},
		{"TPOT p99 (ms)", 3, func(s *Side) float64 { return s.TPOT.P99 }, false},
		{"E2E p50 (ms)", 1, func(s *Side) float64 { return s.E2E.P50 }, false},
		{"E2E p99 (ms)", 1, func(s *Side) float64 { return s.E2E.P99 }, false},
		{"RPS", 3, func(s *Side) float64 { return s.RPS }, false},
		{"Output tok/s", 1, func(s *Side) float64 { return s.OutputTokPerSec }, false},
		{"Mean output tokens", 2, func(s *Side) float64 { return s.MeanOutputTokens }, false},
		{"n", 0, func(s *Side) float64 { return float64(s.N) }, true},
	}
	out := make([]Row, 0, len(ms))
	for _, m := range ms {
		r := Row{Metric: m.name, Reported: m.get(&c.VLLMReported), digits: m.digits}
		if m.name != "RPS" || c.Steady.OK {
			v := m.get(&rec)
			r.Recomputed = &v
		}
		for i := range c.Ours {
			v := m.get(&c.Ours[i])
			r.Ours = append(r.Ours, v)
			if m.count {
				continue
			}
			var d *float64
			if r.Recomputed != nil {
				d = deltaPtr(v, *r.Recomputed)
			}
			r.DeltaPct = append(r.DeltaPct, d)
		}
		out = append(out, r)
	}
	return out
}

// deltaPtr is deltaPct, nil when it is undefined.
func deltaPtr(x, ref float64) *float64 {
	d, ok := deltaPct(x, ref)
	if !ok {
		return nil
	}
	return &d
}
