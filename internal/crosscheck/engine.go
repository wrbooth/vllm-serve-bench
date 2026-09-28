package crosscheck

import (
	"fmt"
	"time"
)

// Engine is the engine-side check on one side: what the engine itself saw
// while that side's load ran, from the 1 Hz vllm_metrics.csv.
//
//	uncached prompt tokens / request = Δ(prefix_cache_queries − prefix_cache_hits) / Δ ttft_count
//	engine mean TTFT                 = Δ ttft_sum / Δ ttft_count
//
// This is the guard that caught the cache contamination (wiki/log.md,
// "Correction: the cross-check's TTFT clusters were cache hits"). When a
// prompt's unique part is not in the prefix cache, the engine prefills at
// least that many tokens for it, plus the template tokens after it, so
// uncached tokens per request is at or above the unique part. A side whose
// prompts are already resident from an earlier run prefills about one
// token per request, because the engine always recomputes the last prompt
// token. MinUncachedFraction (default 0.9) sits between the two. The margin
// below 1.0 covers the ratio's edge error: a request prefilled just before
// a sample is counted in the next one, which moves the ratio by about 1/n.
type Engine struct {
	Label string `json:"label"`
	Span  string `json:"span"` // "measured window" or "whole file"
	// FromMs and ToMs are the two samples the deltas are taken between
	// (Unix ms), zero when the telemetry does not cover the span.
	FromMs    int64   `json:"from_unix_ms"`
	ToMs      int64   `json:"to_unix_ms"`
	TTFTCount float64 `json:"ttft_count"`
	// UncachedPerRequest and MeanTTFTms are zero when OK is false.
	UncachedPerRequest float64 `json:"uncached_prompt_tokens_per_request"`
	MeanTTFTms         float64 `json:"engine_mean_ttft_ms"`
	UniqueTokens       int     `json:"unique_tokens"`
	MinUncached        float64 `json:"min_uncached_tokens"`
	// OK is false when there is nothing to divide by: no telemetry for the
	// span, or no request reached its first token in it.
	OK bool `json:"ok"`
	// Cached marks the side invalid for comparison: it prefilled less than
	// MinUncached per request, or the check could not be made.
	Cached bool   `json:"cached"`
	Note   string `json:"note,omitempty"`
}

// Covering returns the smallest pair of samples whose span contains
// [start, end]: the last at or before start and the first at or after end.
// Starting before the window keeps a request prefilled in the second the
// window opens; ending after it lets requests sent just before the window
// closes (the closed loop sends nothing after it) finish their prefill and
// be counted, instead of being prefilled in one sample and counted in the
// next. ok is false when the telemetry does not reach both edges.
func Covering(samples []Sample, start, end time.Time) (from, to Sample, ok bool) {
	fi, ti := -1, -1
	for i := range samples {
		if !samples[i].At.After(start) {
			fi = i
		}
		if ti < 0 && !samples[i].At.Before(end) {
			ti = i
		}
	}
	if fi < 0 || ti < 0 {
		return Sample{}, Sample{}, false
	}
	return samples[fi], samples[ti], true
}

// CheckEngine computes the engine-side figures between two samples and
// judges them against the prompt's unique part.
func CheckEngine(label, span string, from, to Sample, haveSpan bool, unique int, minFraction float64) Engine {
	e := Engine{Label: label, Span: span, UniqueTokens: unique, MinUncached: minFraction * float64(unique)}
	if !haveSpan {
		e.Cached = true
		e.Note = "telemetry does not cover the " + span
		return e
	}
	e.FromMs, e.ToMs = from.At.UnixMilli(), to.At.UnixMilli()
	e.TTFTCount = to.TTFTCount - from.TTFTCount
	if e.TTFTCount <= 0 {
		e.Cached = true
		e.Note = "no request reached its first token in the " + span
		return e
	}
	e.OK = true
	uncached := (to.PrefixQueries - to.PrefixHits) - (from.PrefixQueries - from.PrefixHits)
	e.UncachedPerRequest = uncached / e.TTFTCount
	e.MeanTTFTms = (to.TTFTSum - from.TTFTSum) / e.TTFTCount * 1000
	switch {
	case unique <= 0:
		e.Cached = true
		e.Note = "the prompt's unique length is unknown, so the cache cannot be ruled out"
	case e.UncachedPerRequest < e.MinUncached:
		e.Cached = true
		e.Note = fmt.Sprintf("prefilled %.1f uncached prompt tokens per request, under %.1f (%g × its %d-token unique part): its prompts were served from the prefix cache",
			e.UncachedPerRequest, e.MinUncached, minFraction, unique)
	}
	return e
}

// EngineOurs checks one of our runs over its measured window.
func EngineOurs(r *Run, concurrency int, minFraction float64) Engine {
	var start, end time.Time
	for i := range r.Summary.Levels {
		if l := &r.Summary.Levels[i]; l.Concurrency == concurrency {
			start, end = l.MeasureStart, l.End
		}
	}
	from, to, ok := Covering(r.Telemetry, start, end)
	if start.IsZero() {
		ok = false
	}
	return CheckEngine(r.Label, "measured window", from, to, ok, r.Config.Profile.UniqueWords, minFraction)
}

// EngineVLLM checks vLLM's side over its whole telemetry file: `bench
// sample` ran only while its client did, so the file is that run (its
// warmups included).
func EngineVLLM(v *VLLM, minFraction float64) Engine {
	n := len(v.Telemetry)
	if n == 0 {
		return CheckEngine("vLLM", "whole file", Sample{}, Sample{}, false, v.UniqueTokens, minFraction)
	}
	return CheckEngine("vLLM", "whole file", v.Telemetry[0], v.Telemetry[n-1], true, v.UniqueTokens, minFraction)
}
