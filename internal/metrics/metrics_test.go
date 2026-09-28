package metrics

import (
	"errors"
	"testing"
	"time"
)

// t0 is an arbitrary fixed instant; every fixture is an offset from it.
var t0 = time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)

func at(ms int) time.Time { return t0.Add(time.Duration(ms) * time.Millisecond) }

func TestNewRecordAppliesMetricDefinitions(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		timing Timing
		want   Record
	}{
		{
			name: "TPOTDividesDecodeTimeByTokensAfterTheFirst",
			// send 0, first 120, done 520, 5 tokens.
			// TTFT = 120 − 0 = 120 ms; E2E = 520 − 0 = 520 ms;
			// TPOT = (520 − 120) / (5 − 1) = 400 / 4 = 100 ms.
			timing: Timing{Send: at(0), FirstContent: at(120), Done: at(520), PromptTokens: 32, CompletionTokens: 5, FinishReason: "length"},
			want: Record{
				Send: at(0), TTFT: 120 * time.Millisecond, E2E: 520 * time.Millisecond,
				TPOT: 100 * time.Millisecond, HasTPOT: true, PromptTokens: 32, CompletionTokens: 5, FinishReason: "length",
			},
		},
		{
			name: "TPOTWithTwoTokensIsTheSingleInterTokenGap",
			// send 10, first 40, done 65, 2 tokens.
			// TTFT = 40 − 10 = 30; E2E = 65 − 10 = 55; TPOT = (55 − 30) / (2 − 1) = 25 ms.
			timing: Timing{Send: at(10), FirstContent: at(40), Done: at(65), CompletionTokens: 2},
			want:   Record{Send: at(10), TTFT: 30 * time.Millisecond, E2E: 55 * time.Millisecond, TPOT: 25 * time.Millisecond, HasTPOT: true, CompletionTokens: 2},
		},
		{
			name: "TPOTTruncatesToWholeNanoseconds",
			// E2E − TTFT = 100 ms = 100,000,000 ns over (4 − 1) = 3 intervals:
			// 100,000,000 / 3 = 33,333,333.3… → 33,333,333 ns (truncated).
			timing: Timing{Send: at(0), FirstContent: at(0), Done: at(100), CompletionTokens: 4},
			want:   Record{Send: at(0), E2E: 100 * time.Millisecond, TPOT: 33333333 * time.Nanosecond, HasTPOT: true, CompletionTokens: 4},
		},
		{
			name: "TPOTExcludedForSingleTokenCompletion",
			// completion_tokens = 1 → TPOT undefined; TTFT = 50, E2E = 60 still defined.
			timing: Timing{Send: at(0), FirstContent: at(50), Done: at(60), CompletionTokens: 1},
			want:   Record{Send: at(0), TTFT: 50 * time.Millisecond, E2E: 60 * time.Millisecond, CompletionTokens: 1},
		},
		{
			name:   "TPOTExcludedForZeroTokenCompletion",
			timing: Timing{Send: at(0), FirstContent: at(50), Done: at(60), CompletionTokens: 0},
			want:   Record{Send: at(0), TTFT: 50 * time.Millisecond, E2E: 60 * time.Millisecond},
		},
		{
			name: "ErrorRowKeepsSendAndCarriesTheMessage",
			// A partial stream's timestamps are not turned into latencies.
			timing: Timing{Send: at(7), FirstContent: at(20), CompletionTokens: 3, Err: errors.New("http 500: boom")},
			want:   Record{Send: at(7), CompletionTokens: 3, Err: "http 500: boom"},
		},
		{
			name:   "MissingDoneWithoutErrorIsAnErrorRow",
			timing: Timing{Send: at(0), FirstContent: at(5), CompletionTokens: 3},
			want:   Record{Send: at(0), CompletionTokens: 3, Err: "incomplete timing"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tt.want.Worker = 3
			got := NewRecord(3, &tt.timing)
			if got != tt.want {
				t.Errorf("NewRecord =\n  %+v\nwant\n  %+v", got, tt.want)
			}
		})
	}
}

func TestPercentileIsNearestRank(t *testing.T) {
	t.Parallel()
	oneTo := func(n int) []int {
		s := make([]int, n)
		for i := range s {
			s[i] = i + 1
		}
		return s
	}
	tests := []struct {
		name   string
		sorted []int
		p      float64
		want   int
		wantOK bool
	}{
		{name: "EmptySampleIsUndefined", sorted: nil, p: 50, want: 0, wantOK: false},
		// n=1: rank = ceil(p·1/100) = 1 for any 0 < p ≤ 100 → the only value.
		{name: "SingleValueIsEveryPercentileP50", sorted: []int{7}, p: 50, want: 7, wantOK: true},
		{name: "SingleValueIsEveryPercentileP99", sorted: []int{7}, p: 99, want: 7, wantOK: true},
		// n=2: p50 → ceil(50·2/100) = ceil(1.0) = 1 → 10; p51 → ceil(1.02) = 2 → 20.
		{name: "TwoValuesP50IsTheLower", sorted: []int{10, 20}, p: 50, want: 10, wantOK: true},
		{name: "TwoValuesP51IsTheUpper", sorted: []int{10, 20}, p: 51, want: 20, wantOK: true},
		// n=2: p95 → ceil(1.9) = 2 → 20.
		{name: "TwoValuesP95IsTheUpper", sorted: []int{10, 20}, p: 95, want: 20, wantOK: true},
		// n=10, values 1..10: p50 → ceil(5.0) = 5 → 5; p95 → ceil(9.5) = 10 → 10.
		{name: "TenValuesP50", sorted: oneTo(10), p: 50, want: 5, wantOK: true},
		{name: "TenValuesP95", sorted: oneTo(10), p: 95, want: 10, wantOK: true},
		// n=100, values 1..100: p99 → ceil(99.0) = 99 → 99 (not interpolated).
		{name: "HundredValuesP99IsRank99", sorted: oneTo(100), p: 99, want: 99, wantOK: true},
		// n=25, values 1..25: p28 → ceil(28·25/100) = ceil(700/100) = 7 → 7.
		// (p/100)·n = 0.28·25 = 7.000000000000001 in float64 would give rank 8.
		{name: "RankMultipliesBeforeDividingToAvoidFloatError", sorted: oneTo(25), p: 28, want: 7, wantOK: true},
		// Ties: [5 5 5 9], p50 → ceil(2.0) = 2 → 5; p75 → ceil(3.0) = 3 → 5; p76 → ceil(3.04) = 4 → 9.
		{name: "TiesP50", sorted: []int{5, 5, 5, 9}, p: 50, want: 5, wantOK: true},
		{name: "TiesP75", sorted: []int{5, 5, 5, 9}, p: 75, want: 5, wantOK: true},
		{name: "TiesP76", sorted: []int{5, 5, 5, 9}, p: 76, want: 9, wantOK: true},
		// Bounds: p ≤ 0 → rank clamps to 1 (min); p ≥ 100 → rank n (max).
		{name: "ZeroPercentileIsTheMinimum", sorted: []int{1, 2, 3}, p: 0, want: 1, wantOK: true},
		{name: "HundredthPercentileIsTheMaximum", sorted: []int{1, 2, 3}, p: 100, want: 3, wantOK: true},
		{name: "AboveHundredClampsToTheMaximum", sorted: []int{1, 2, 3}, p: 150, want: 3, wantOK: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, ok := Percentile(tt.sorted, tt.p)
			if got != tt.want || ok != tt.wantOK {
				t.Errorf("Percentile(%v, %v) = %v, %v; want %v, %v", tt.sorted, tt.p, got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

func ms(n int) time.Duration { return time.Duration(n) * time.Millisecond }

func TestSummarizeCountsErrorsAndExcludesThemFromLatency(t *testing.T) {
	t.Parallel()
	// Five rows over a 2 s window: three successes, one single-token success
	// (no TPOT), one error.
	records := []Record{
		{TTFT: ms(30), E2E: ms(300), TPOT: ms(10), HasTPOT: true, CompletionTokens: 28},
		{TTFT: ms(10), E2E: ms(100), TPOT: ms(20), HasTPOT: true, CompletionTokens: 10},
		{TTFT: ms(20), E2E: ms(200), TPOT: ms(30), HasTPOT: true, CompletionTokens: 7},
		{TTFT: ms(40), E2E: ms(40), CompletionTokens: 1},
		{Err: "http 500: boom", TTFT: ms(999), E2E: ms(999), CompletionTokens: 1000},
	}
	got := Summarize(records, 2*time.Second)
	want := Summary{
		Requests:  5,       // every row, error included
		Errors:    1,       //
		ErrorRate: 1.0 / 5, // 0.2
		Window:    2 * time.Second,
		// 4 completed / 2 s = 2.0 rps.
		RPS: 2.0,
		// (28 + 10 + 7 + 1) = 46 tokens / 2 s = 23.0; the error row's 1000 is ignored.
		OutputTokPerSec: 23.0,
		// TTFT sorted: [10 20 30 40], n=4.
		// p50 → ceil(2.0) = 2 → 20; p95 → ceil(3.8) = 4 → 40; p99 → ceil(3.96) = 4 → 40.
		TTFT: Dist{N: 4, P50: ms(20), P95: ms(40), P99: ms(40)},
		// E2E sorted: [40 100 200 300], n=4. p50 → rank 2 → 100; p95, p99 → rank 4 → 300.
		E2E: Dist{N: 4, P50: ms(100), P95: ms(300), P99: ms(300)},
		// TPOT excludes the single-token row: [10 20 30], n=3.
		// p50 → ceil(1.5) = 2 → 20; p95 → ceil(2.85) = 3 → 30; p99 → ceil(2.97) = 3 → 30.
		TPOT: Dist{N: 3, P50: ms(20), P95: ms(30), P99: ms(30)},
	}
	if got != want {
		t.Errorf("Summarize =\n  %+v\nwant\n  %+v", got, want)
	}
}

func TestSummarizeEdgeCases(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		records []Record
		window  time.Duration
		want    Summary
	}{
		{
			name:    "EmptyInputIsAllZero",
			records: nil,
			window:  time.Second,
			want:    Summary{Window: time.Second},
		},
		{
			name:    "OnlyErrorsGiveFullErrorRateAndNoLatency",
			records: []Record{{Err: "a"}, {Err: "b"}},
			window:  time.Second,
			// 2 of 2 failed → rate 1.0; nothing completed → 0 rps, 0 tok/s.
			want: Summary{Requests: 2, Errors: 2, ErrorRate: 1, Window: time.Second},
		},
		{
			name:    "SingleRequestIsEveryPercentile",
			records: []Record{{TTFT: ms(5), E2E: ms(50), TPOT: ms(3), HasTPOT: true, CompletionTokens: 16}},
			window:  4 * time.Second,
			// 1 / 4 s = 0.25 rps; 16 / 4 s = 4 tok/s; n=1 → p50 = p95 = p99 = the value.
			want: Summary{
				Requests: 1, Window: 4 * time.Second, RPS: 0.25, OutputTokPerSec: 4,
				TTFT: Dist{N: 1, P50: ms(5), P95: ms(5), P99: ms(5)},
				E2E:  Dist{N: 1, P50: ms(50), P95: ms(50), P99: ms(50)},
				TPOT: Dist{N: 1, P50: ms(3), P95: ms(3), P99: ms(3)},
			},
		},
		{
			name:    "ZeroWindowGivesZeroRatesNotInfinity",
			records: []Record{{TTFT: ms(1), E2E: ms(2), CompletionTokens: 1}},
			window:  0,
			want: Summary{
				Requests: 1,
				TTFT:     Dist{N: 1, P50: ms(1), P95: ms(1), P99: ms(1)},
				E2E:      Dist{N: 1, P50: ms(2), P95: ms(2), P99: ms(2)},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := Summarize(tt.records, tt.window); got != tt.want {
				t.Errorf("Summarize =\n  %+v\nwant\n  %+v", got, tt.want)
			}
		})
	}
}
