package crosscheck

import (
	"slices"
	"testing"
	"time"
)

// Nearest-rank: the value at 1-based rank ceil(p·n/100). numpy's default
// would interpolate instead (p50 of {10, 20} is 15 there, 10 here).
func TestNearestRankPicksTheSampleAtRankCeilPN(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		sample []float64
		want   Dist
	}{
		// Empty: N 0, all zero.
		{"Empty", nil, Dist{}},
		// n=1: every rank clamps to 1.
		{"One", []float64{7.5}, Dist{N: 1, P50: 7.5, P95: 7.5, P99: 7.5}},
		// n=2: p50 rank ceil(1) = 1 → 10; p95 rank ceil(1.9) = 2 → 20.
		{"TwoIsNotInterpolated", []float64{20, 10}, Dist{N: 2, P50: 10, P95: 20, P99: 20}},
		// n=5 unsorted {5,1,3,2,4}: p50 rank ceil(2.5) = 3 → 3;
		// p95 rank ceil(4.75) = 5 → 5; p99 rank ceil(4.95) = 5 → 5.
		{"FiveUnsorted", []float64{5, 1, 3, 2, 4}, Dist{N: 5, P50: 3, P95: 5, P99: 5}},
		// Ties: sorted {1,7,7,7}; p50 rank 2 → 7.
		{"Ties", []float64{7, 1, 7, 7}, Dist{N: 4, P50: 7, P95: 7, P99: 7}},
		// n=20, values 1..20: p50 rank 10 → 10; p95 rank 19 → 19;
		// p99 rank ceil(19.8) = 20 → 20.
		{"Twenty", seq(1, 20), Dist{N: 20, P50: 10, P95: 19, P99: 20}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			in := slices.Clone(tt.sample)
			if got := NearestRank(tt.sample); got != tt.want {
				t.Errorf("NearestRank(%v) = %+v, want %+v", in, got, tt.want)
			}
			if !slices.Equal(in, tt.sample) {
				t.Errorf("NearestRank sorted its input in place: %v", tt.sample)
			}
		})
	}
}

func seq(from, to int) []float64 {
	var s []float64
	for i := from; i <= to; i++ {
		s = append(s, float64(i))
	}
	return s
}

// benchFixture is four requests, with dyadic times so every derived value
// is exact in float64:
//
//	#0 ttft 1/64 s,  itls {1/1024, 1/128} s, output 3
//	   TTFT = 15.625 ms
//	   Σ itls = 0.0009765625 + 0.0078125 = 0.0087890625 s
//	   E2E  = (0.015625 + 0.0087890625) × 1000 = 24.4140625 ms
//	   TPOT = 8.7890625 / (3 − 1) = 4.39453125 ms
//	#1 ttft 1/32 s, itls {}, output 1: TTFT 31.25, E2E 31.25, no TPOT (< 2)
//	#2 ttft 1/128 s, itls {1/256, 1/64} s, output 2
//	   TTFT = 7.8125; Σ itls = 0.01953125 s; E2E = 27.34375
//	   TPOT = 19.53125 / (2 − 1) = 19.53125
//	#3 error "boom": no metrics
func benchFixture() Bench {
	return Bench{
		MaxConcurrency: 2,
		Duration:       2,
		Completed:      3,
		Failed:         1,
		TTFTs:          []float64{1.0 / 64, 1.0 / 32, 1.0 / 128, 0},
		ITLs:           [][]float64{{1.0 / 1024, 1.0 / 128}, {}, {1.0 / 256, 1.0 / 64}, {}},
		OutputLens:     []int{3, 1, 2, 0},
		StartTimes:     []float64{100, 100, 101, 101.5},
		Errors:         []string{"", "", "", "boom"},
	}
}

func TestRequestsDeriveE2EAndTPOTFromITLsAndExcludeShortOutputs(t *testing.T) {
	t.Parallel()
	b := benchFixture()
	got, err := Requests(&b)
	if err != nil {
		t.Fatal(err)
	}
	want := []Request{
		{Start: 100, OK: true, TTFT: 15.625, E2E: 24.4140625, TPOT: 4.39453125, HasTPOT: true, OutputLen: 3},
		{Start: 100, OK: true, TTFT: 31.25, E2E: 31.25, OutputLen: 1},
		{Start: 101, OK: true, TTFT: 7.8125, E2E: 27.34375, TPOT: 19.53125, HasTPOT: true, OutputLen: 2},
		{Start: 101.5, OutputLen: 0},
	}
	if !slices.Equal(got, want) {
		t.Errorf("Requests =\n%+v\nwant\n%+v", got, want)
	}
}

func TestRequestsRejectsMissingOrRaggedPerRequestData(t *testing.T) {
	t.Parallel()
	empty := Bench{}
	if _, err := Requests(&empty); err == nil {
		t.Error("no per-request data: want an error")
	}
	ragged := benchFixture()
	ragged.StartTimes = ragged.StartTimes[:3]
	if _, err := Requests(&ragged); err == nil {
		t.Error("start_times shorter than ttfts: want an error")
	}
}

// A request past the end of a short errors array counts as successful.
func TestRequestsTreatAMissingErrorEntryAsSuccess(t *testing.T) {
	t.Parallel()
	b := benchFixture()
	b.Errors = nil
	reqs, err := Requests(&b)
	if err != nil {
		t.Fatal(err)
	}
	if !reqs[3].OK {
		t.Error("request 3 with no errors entry: want OK")
	}
}

func TestRecomputedCountsFailuresAndUsesOurPercentiles(t *testing.T) {
	t.Parallel()
	b := benchFixture()
	reqs, err := Requests(&b)
	if err != nil {
		t.Fatal(err)
	}
	got := Recomputed(reqs, b.Duration)
	// Successful: #0..#2. TTFT {15.625, 31.25, 7.8125} sorted {7.8125,
	// 15.625, 31.25}: p50 rank ceil(1.5) = 2 → 15.625; p95, p99 rank 3.
	// TPOT only #0 and #2 (#1 has 1 output token): {4.39453125, 19.53125},
	// p50 rank 1, p95 rank 2. E2E {24.4140625, 31.25, 27.34375}.
	// RPS = 3 / 2 s = 1.5; output tokens 3 + 1 + 2 = 6 → 3 tok/s, mean 2.
	want := Side{
		Label:            "vLLM recomputed",
		N:                3,
		Failed:           1,
		TTFT:             Dist{N: 3, P50: 15.625, P95: 31.25, P99: 31.25},
		TPOT:             Dist{N: 2, P50: 4.39453125, P95: 19.53125, P99: 19.53125},
		E2E:              Dist{N: 3, P50: 27.34375, P95: 31.25, P99: 31.25},
		RPS:              1.5,
		OutputTokPerSec:  3,
		MeanOutputTokens: 2,
	}
	if got != want {
		t.Errorf("Recomputed =\n%+v\nwant\n%+v", got, want)
	}
	// No duration and no successes: rates and mean stay 0, not NaN.
	if z := Recomputed(reqs[3:], 0); z.RPS != 0 || z.MeanOutputTokens != 0 || z.N != 0 {
		t.Errorf("Recomputed of one failure over 0 s = %+v, want zeros", z)
	}
}

func TestMeanChecksSetRecomputedMeansAgainstReportedOnes(t *testing.T) {
	t.Parallel()
	b := benchFixture()
	// TTFT mean (15.625 + 31.25 + 7.8125) / 3 = 18.229166...; report
	// exactly that sum / 3 so the delta is 0. TPOT mean (4.39453125 +
	// 19.53125) / 2 = 11.962890625; reported 12.5 → Δ = (11.962890625 −
	// 12.5) / 12.5 = −4.296875%. E2E reported 0 → no delta.
	b.MeanTTFTms = (15.625 + 31.25 + 7.8125) / 3
	b.MeanTPOTms = 12.5
	reqs, err := Requests(&b)
	if err != nil {
		t.Fatal(err)
	}
	got := MeanChecks(reqs, &b)
	want := []MeanCheck{
		{Metric: "TTFT", Reported: b.MeanTTFTms, Recomputed: b.MeanTTFTms, DeltaPct: 0},
		{Metric: "TPOT", Reported: 12.5, Recomputed: 11.962890625, DeltaPct: -4.296875},
		{Metric: "E2E", Reported: 0, Recomputed: (24.4140625 + 31.25 + 27.34375) / 3, DeltaPct: 0},
	}
	if !slices.Equal(got, want) {
		t.Errorf("MeanChecks =\n%+v\nwant\n%+v", got, want)
	}
}

// Requests listed out of send order; ties (#1 and #5 at t=1) keep file
// order. By start: #1(1) #5(1) #2(2) #0(3) #4(4) #3(5).
func burstFixture() []Request {
	starts := []float64{3, 1, 2, 5, 4, 1}
	ttfts := []float64{30, 100, 20, 50, 40, 90}
	reqs := make([]Request, len(starts))
	for i := range reqs {
		reqs[i] = Request{Start: starts[i], OK: true, TTFT: ttfts[i]}
	}
	return reqs
}

func TestExcludeBurstDropsTheFirstRequestsBySendTimeNotFileOrder(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		burst int
		fail  int // index to mark failed, -1 for none
		want  Burst
	}{
		// Drop #1 and #5 (TTFT 100, 90). Left {20, 30, 50, 40} sorted
		// {20, 30, 40, 50}: p50 rank 2 → 30, p95/p99 rank 4 → 50. Dropping
		// by file order instead (#0, #1) would leave a 90 as the max.
		{"TwoOfSix", 2, -1, Burst{Excluded: 2, TTFT: Dist{N: 4, P50: 30, P95: 50, P99: 50}}},
		// A failed request after the burst is not in the distribution:
		// drop #1, #5; #2 failed; left {30, 50, 40}: p50 rank 2 → 40.
		{"FailedAfterBurst", 2, 2, Burst{Excluded: 2, TTFT: Dist{N: 3, P50: 40, P95: 50, P99: 50}}},
		// Burst 0 keeps all six: sorted {20,30,40,50,90,100}, p50 rank 3.
		{"None", 0, -1, Burst{Excluded: 0, TTFT: Dist{N: 6, P50: 40, P95: 100, P99: 100}}},
		// A burst larger than the run leaves nothing.
		{"Everything", 9, -1, Burst{Excluded: 6}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			reqs := burstFixture()
			if tt.fail >= 0 {
				reqs[tt.fail].OK = false
			}
			if got := ExcludeBurst(reqs, tt.burst); got != tt.want {
				t.Errorf("ExcludeBurst(%d) = %+v, want %+v", tt.burst, got, tt.want)
			}
		})
	}
}

func starts(s ...float64) []Request {
	reqs := make([]Request, len(s))
	for i, v := range s {
		reqs[i] = Request{Start: v, OK: true}
	}
	return reqs
}

func TestSteadyStateCountsSendsInsideTheTrimmedWindowEdgesIncluded(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		reqs []Request
		trim time.Duration
		want Steady
	}{
		// First 0, last 14, trim 3 s → window [3, 11]. Inside, edges
		// included: 3, 4, 6, 9, 11 → n = 5 over span 11 − 3 = 8 s →
		// (5 − 1) / 8 = 0.5. Excluding the edges would give 2 / 5; n / span
		// would give 0.625. Input deliberately unsorted.
		{
			"Edges", starts(14, 0, 3, 1, 4, 12, 6, 9, 11), 3 * time.Second,
			Steady{TrimS: 3, FromS: 3, ToS: 11, Sends: 5, SpanS: 8, RPS: 0.5, OK: true},
		},
		// Offsets are measured from the first send, not from 0.
		{
			"Offset", starts(100, 102, 104, 106, 108), 2 * time.Second,
			Steady{TrimS: 2, FromS: 2, ToS: 6, Sends: 3, SpanS: 4, RPS: 0.5, OK: true},
		},
		// One send inside: no rate.
		{"OneSend", starts(0, 5, 10), 4 * time.Second, Steady{TrimS: 4, FromS: 4, ToS: 6, Sends: 1}},
		// Two sends at the same instant: zero span, no rate.
		{"ZeroSpan", starts(0, 5, 5, 10), 4 * time.Second, Steady{TrimS: 4, FromS: 4, ToS: 6, Sends: 2}},
		// No requests at all.
		{"Empty", nil, time.Second, Steady{TrimS: 1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := SteadyState(tt.reqs, tt.trim); got != tt.want {
				t.Errorf("SteadyState = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestDeltaPctIsUndefinedAgainstZero(t *testing.T) {
	t.Parallel()
	// (6.3 − 6.0) / 6.0 is not exact in binary; (5 − 4) / 4 × 100 = 25 is.
	if d, ok := deltaPct(5, 4); !ok || d != 25 {
		t.Errorf("deltaPct(5, 4) = %v, %v; want 25, true", d, ok)
	}
	if _, ok := deltaPct(5, 0); ok {
		t.Error("deltaPct against 0: want ok false")
	}
}
