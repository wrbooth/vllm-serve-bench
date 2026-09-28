package crosscheck

import (
	"strings"
	"testing"
	"time"

	"github.com/wrbooth/vllm-serve-bench/internal/results"
)

var t0 = time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)

// samplesEvery returns one sample per second from t0, with the TTFT count
// equal to the second, so a test can see which two were picked.
func samplesEvery(n int) []Sample {
	s := make([]Sample, n)
	for i := range s {
		s[i] = Sample{At: t0.Add(time.Duration(i) * time.Second), TTFTCount: float64(i)}
	}
	return s
}

func TestCoveringPicksTheSamplesJustOutsideTheWindow(t *testing.T) {
	t.Parallel()
	s := samplesEvery(6) // at 0..5 s
	ms := func(v int) time.Duration { return time.Duration(v) * time.Millisecond }
	tests := []struct {
		name       string
		start, end time.Duration
		from, to   float64
		ok         bool
	}{
		// [1.5, 3.5] → last at or before 1.5 is 1; first at or after 3.5 is 4.
		{"Inside", ms(1500), ms(3500), 1, 4, true},
		// On a sample: that sample is at or before / at or after.
		{"OnSamples", 2 * time.Second, 4 * time.Second, 2, 4, true},
		// Sub-millisecond: a sample 0.9 ms before the window's start and
		// end is before both (a sample row is truncated to the ms, the
		// window is not), so it starts the span and cannot end it.
		{"SubMillisecond", ms(2000) + 900*time.Microsecond, ms(4000) + 900*time.Microsecond, 2, 5, true},
		// Telemetry starts after the window opens.
		{"StartsLate", -time.Second, 3 * time.Second, 0, 0, false},
		// Telemetry stops before the window closes.
		{"EndsEarly", time.Second, 6 * time.Second, 0, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			from, to, ok := Covering(s, t0.Add(tt.start), t0.Add(tt.end))
			if ok != tt.ok || (ok && (from.TTFTCount != tt.from || to.TTFTCount != tt.to)) {
				t.Errorf("Covering = %v..%v, %v; want %v..%v, %v", from.TTFTCount, to.TTFTCount, ok, tt.from, tt.to, tt.ok)
			}
		})
	}
	if _, _, ok := Covering(nil, t0, t0); ok {
		t.Error("Covering(no samples): want ok false")
	}
}

// from/to counters:
//
//	uncached = (3000 − 1000) − (1000 − 600) = 2000 − 400 = 1600 tokens
//	Δcount   = 26 − 10 = 16 requests → 1600 / 16 = 100 tokens per request
//	TTFT     = (1.5 − 1.0) / 16 × 1000 = 31.25 ms
//
// cached: hits 2560 → (3000 − 2560) − 400 = 40 → 40 / 16 = 2.5 per request.
var (
	engFrom   = Sample{At: t0, PrefixQueries: 1000, PrefixHits: 600, TTFTSum: 1.0, TTFTCount: 10}
	engTo     = Sample{At: t0.Add(61 * time.Second), PrefixQueries: 3000, PrefixHits: 1000, TTFTSum: 1.5, TTFTCount: 26}
	engCached = Sample{At: t0.Add(61 * time.Second), PrefixQueries: 3000, PrefixHits: 2560, TTFTSum: 1.5, TTFTCount: 26}
)

func TestCheckEngineFlagsASideThatPrefilledLessThanItsUniquePart(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		to       Sample
		have     bool
		unique   int
		ok       bool
		cached   bool
		uncached float64
		note     string
	}{
		// 100 ≥ 0.9 × 100 = 90: passes.
		{"Uncached", engTo, true, 100, true, false, 100, ""},
		// 100 ≥ 0.9 × 111 = 99.9: still passes at the edge.
		{"JustAbove", engTo, true, 111, true, false, 100, ""},
		// 100 < 0.9 × 112 = 100.8: flagged.
		{"JustBelow", engTo, true, 112, true, true, 100, "under 100.8"},
		// 2.5 < 90: the contaminated case.
		{"Cached", engCached, true, 100, true, true, 2.5, "served from the prefix cache"},
		// Unknown unique part: cannot rule the cache out.
		{"UnknownUnique", engTo, true, 0, true, true, 100, "unknown"},
		// Zero Δcount: nothing to divide by; unverified, so flagged.
		{"ZeroCount", engFrom, true, 100, false, true, 0, "no request reached its first token"},
		// No telemetry for the span.
		{"NoSpan", Sample{}, false, 100, false, true, 0, "does not cover"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			e := CheckEngine("side", "measured window", engFrom, tt.to, tt.have, tt.unique, 0.9)
			if e.OK != tt.ok || e.Cached != tt.cached || e.UncachedPerRequest != tt.uncached {
				t.Errorf("CheckEngine = ok %v cached %v uncached %v; want %v %v %v", e.OK, e.Cached, e.UncachedPerRequest, tt.ok, tt.cached, tt.uncached)
			}
			if !strings.Contains(e.Note, tt.note) {
				t.Errorf("note %q lacks %q", e.Note, tt.note)
			}
			if tt.ok && e.MeanTTFTms != 31.25 {
				t.Errorf("engine mean TTFT %v ms, want 31.25", e.MeanTTFTms)
			}
		})
	}
}

func TestEngineOursUsesTheLevelsMeasuredWindowAndEngineVLLMTheWholeFile(t *testing.T) {
	t.Parallel()
	// Samples at 0..9 s; counters grow 10 per second except hits.
	s := make([]Sample, 10)
	for i := range s {
		s[i] = Sample{At: t0.Add(time.Duration(i) * time.Second), PrefixQueries: float64(100 * i), TTFTCount: float64(i)}
	}
	r := Run{
		Label:     "ours (a)",
		Config:    results.Config{Profile: results.ProfileConfig{UniqueWords: 50}},
		Summary:   results.Summary{Levels: []results.Level{{Concurrency: 1, MeasureStart: t0.Add(2500 * time.Millisecond), End: t0.Add(6500 * time.Millisecond)}, {Concurrency: 2}}},
		Telemetry: s,
	}
	// Window [2.5, 6.5] → samples 2 and 7: Δcount 5, Δuncached 500 → 100.
	e := EngineOurs(&r, 1, 0.9)
	if e.FromMs != t0.Add(2*time.Second).UnixMilli() || e.ToMs != t0.Add(7*time.Second).UnixMilli() || e.UncachedPerRequest != 100 || e.Cached {
		t.Errorf("EngineOurs = %+v, want samples 2..7, 100 per request, not cached", e)
	}
	// A level without a window (never measured) cannot be checked.
	if e := EngineOurs(&r, 2, 0.9); !e.Cached || e.OK {
		t.Errorf("EngineOurs on a level with no window = %+v, want unverified", e)
	}
	// vLLM: first to last sample, 0..9 → Δcount 9, Δuncached 900 → 100.
	v := VLLM{Telemetry: s, UniqueTokens: 100}
	if e := EngineVLLM(&v, 0.9); e.UncachedPerRequest != 100 || e.Cached || e.Span != "whole file" {
		t.Errorf("EngineVLLM = %+v, want 100 per request over the whole file", e)
	}
	if e := EngineVLLM(&VLLM{UniqueTokens: 100}, 0.9); !e.Cached || e.OK {
		t.Errorf("EngineVLLM without telemetry = %+v, want unverified", e)
	}
}
