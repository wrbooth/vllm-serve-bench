package loadgen

import (
	"context"
	"errors"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/wrbooth/vllm-serve-bench/internal/fakeserver"
	"github.com/wrbooth/vllm-serve-bench/internal/metrics"
	"github.com/wrbooth/vllm-serve-bench/internal/openai"
)

// newRunner points a Runner at a fresh fake server on loopback.
func newRunner(t *testing.T, srv *fakeserver.Server, concurrency int, warmup, duration time.Duration) *Runner {
	t.Helper()
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	return &Runner{
		Client:      &openai.Client{BaseURL: ts.URL},
		Concurrency: concurrency,
		Warmup:      warmup,
		Duration:    duration,
		Build: func(_, _ int) *openai.Request {
			return &openai.Request{Model: "fake", MaxTokens: 4}
		},
	}
}

func TestRunIsClosedLoopAndExcludesWarmup(t *testing.T) {
	t.Parallel()
	srv := &fakeserver.Server{FirstTokenDelay: 5 * time.Millisecond, InterTokenInterval: time.Millisecond, Tokens: 4, PromptTokens: 9}
	r := newRunner(t, srv, 4, 100*time.Millisecond, 300*time.Millisecond)

	// Record the seq numbers each worker asked for, to show every worker
	// builds its requests in order 0, 1, 2, ...
	var mu sync.Mutex
	seqs := map[int][]int{}
	r.Build = func(w, seq int) *openai.Request {
		mu.Lock()
		seqs[w] = append(seqs[w], seq)
		mu.Unlock()
		return &openai.Request{Model: "fake", MaxTokens: 4}
	}

	res, err := r.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Closed loop: exactly Concurrency requests in flight at the peak, never more.
	if got := srv.MaxInFlight(); got != 4 {
		t.Errorf("server peak concurrency = %d, want 4", got)
	}
	// Nothing is lost: every request the server saw is either warmup or a record.
	if got := res.WarmupRequests + len(res.Records); got != srv.Requests() {
		t.Errorf("warmup %d + records %d = %d, server saw %d", res.WarmupRequests, len(res.Records), got, srv.Requests())
	}
	if res.WarmupRequests == 0 || len(res.Records) == 0 {
		t.Fatalf("warmup %d, records %d; want both non-zero", res.WarmupRequests, len(res.Records))
	}
	if !res.MeasureStart.Equal(res.Start.Add(100*time.Millisecond)) || !res.End.Equal(res.MeasureStart.Add(300*time.Millisecond)) {
		t.Errorf("window = %v / %v / %v, not Start+warmup+duration", res.Start, res.MeasureStart, res.End)
	}

	lastDone := map[int]time.Time{}
	for i := range res.Records {
		rec := &res.Records[i]
		if !rec.OK() {
			t.Fatalf("record %d failed: %s", i, rec.Err)
		}
		// Send follows the attempt start, which was inside the window.
		if rec.Send.Before(res.MeasureStart) {
			t.Errorf("record %d sent at %v, before the window opened at %v", i, rec.Send, res.MeasureStart)
		}
		if i > 0 && rec.Send.Before(res.Records[i-1].Send) {
			t.Errorf("records not ordered by Send at %d", i)
		}
		// One in flight per worker: each request starts after the
		// worker's previous one finished.
		if prev, ok := lastDone[rec.Worker]; ok && rec.Send.Before(prev) {
			t.Errorf("worker %d sent at %v before its previous request finished at %v", rec.Worker, rec.Send, prev)
		}
		lastDone[rec.Worker] = rec.Send.Add(rec.E2E)
		if rec.PromptTokens != 9 || rec.CompletionTokens != 4 || !rec.HasTPOT {
			t.Errorf("record %d = %+v, want 9 prompt / 4 completion tokens with TPOT", i, rec)
		}
	}
	if len(lastDone) != 4 {
		t.Errorf("%d workers produced measured records, want 4", len(lastDone))
	}
	for w, s := range seqs {
		for i, seq := range s {
			if seq != i {
				t.Fatalf("worker %d built seqs %v, want 0, 1, 2, ...", w, s)
			}
		}
	}

	want := metrics.Summarize(res.Records, 300*time.Millisecond)
	if res.Summary != want {
		t.Errorf("Summary = %+v, want Summarize(records, duration) = %+v", res.Summary, want)
	}
}

// Every third request fails with HTTP 500. The failures are rows in the
// result, counted in the summary, never dropped.
func TestRunCountsErrorRows(t *testing.T) {
	t.Parallel()
	srv := &fakeserver.Server{FailEvery: 3, Tokens: 2, InterTokenInterval: time.Millisecond}
	r := newRunner(t, srv, 2, 0, 150*time.Millisecond)

	res, err := r.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	errs := 0
	for i := range res.Records {
		if !res.Records[i].OK() {
			errs++
		}
	}
	if errs == 0 {
		t.Fatal("no error rows; FailEvery should have produced some")
	}
	if res.WarmupRequests != 0 || len(res.Records) != srv.Requests() {
		t.Errorf("warmup %d, records %d, server saw %d; with no warmup every request is a record",
			res.WarmupRequests, len(res.Records), srv.Requests())
	}
	// Requests 3, 6, 9, ... failed: floor(n/3) of n.
	if want := srv.Requests() / 3; errs != want || res.Summary.Errors != want || res.Summary.Requests != len(res.Records) {
		t.Errorf("error rows %d, Summary.Errors %d, Summary.Requests %d; want %d, %d, %d",
			errs, res.Summary.Errors, res.Summary.Requests, want, want, len(res.Records))
	}
}

// End to end through the runner and the metric definitions at concurrency 8
// (the architecture doc's integration case). The tolerance reasoning is the
// same as in internal/openai's loopback test: TTFT cannot be below the
// server's first-token delay, and 50 ms covers scheduling jitter under
// -race with room to spare; spread over 5 decode intervals that is 10 ms of
// TPOT slack either side.
func TestRunMetricsMatchServerDelaysAtConcurrency8(t *testing.T) {
	t.Parallel()
	const (
		delay    = 100 * time.Millisecond
		interval = 20 * time.Millisecond
		tokens   = 6
		slack    = 50 * time.Millisecond
	)
	srv := &fakeserver.Server{FirstTokenDelay: delay, InterTokenInterval: interval, Tokens: tokens}
	r := newRunner(t, srv, 8, 0, 300*time.Millisecond)
	res, err := r.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Summary.TTFT.N < 8 || res.Summary.Errors != 0 {
		t.Fatalf("Summary = %+v; want at least 8 successful requests", res.Summary)
	}
	for i := range res.Records {
		rec := &res.Records[i]
		if rec.TTFT < delay || rec.TTFT > delay+slack {
			t.Errorf("record %d TTFT = %v, want in [%v, %v]", i, rec.TTFT, delay, delay+slack)
		}
		if lo, hi := interval-slack/(tokens-1), interval+slack/(tokens-1); rec.TPOT < lo || rec.TPOT > hi {
			t.Errorf("record %d TPOT = %v, want in [%v, %v]", i, rec.TPOT, lo, hi)
		}
	}
}

// A per-request timeout turns a stuck request into an error row, and the
// worker moves on instead of hanging the run.
func TestRunRequestTimeoutMakesErrorRows(t *testing.T) {
	t.Parallel()
	srv := &fakeserver.Server{FirstTokenDelay: 10 * time.Second}
	r := newRunner(t, srv, 1, 0, 100*time.Millisecond)
	r.RequestTimeout = 30 * time.Millisecond
	res, err := r.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.Records) == 0 || res.Summary.Errors != len(res.Records) {
		t.Errorf("records %d, errors %d; want every request timed out", len(res.Records), res.Summary.Errors)
	}
}

func TestRunStopsWhenContextIsCancelled(t *testing.T) {
	t.Parallel()
	srv := &fakeserver.Server{FirstTokenDelay: 10 * time.Second}
	r := newRunner(t, srv, 2, 0, time.Hour)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	res, err := r.Run(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Run error = %v, want context.DeadlineExceeded", err)
	}
	if took := time.Since(start); took > 5*time.Second {
		t.Errorf("Run took %v after cancellation", took)
	}
	// The two in-flight requests were aborted: rows, not silence.
	if res.Summary.Errors != 2 || len(res.Records) != 2 {
		t.Errorf("records %d, errors %d; want the 2 aborted requests as error rows", len(res.Records), res.Summary.Errors)
	}
}

func TestRunRejectsInvalidConfig(t *testing.T) {
	t.Parallel()
	ok := func() *Runner {
		return &Runner{
			Client: &openai.Client{BaseURL: "http://127.0.0.1:1"}, Concurrency: 1, Duration: time.Second,
			Build: func(_, _ int) *openai.Request { return &openai.Request{} },
		}
	}
	tests := []struct {
		name   string
		mutate func(*Runner)
	}{
		{name: "NilClient", mutate: func(r *Runner) { r.Client = nil }},
		{name: "NilBuild", mutate: func(r *Runner) { r.Build = nil }},
		{name: "ZeroConcurrency", mutate: func(r *Runner) { r.Concurrency = 0 }},
		{name: "ZeroDuration", mutate: func(r *Runner) { r.Duration = 0 }},
		{name: "NegativeWarmup", mutate: func(r *Runner) { r.Warmup = -time.Second }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r := ok()
			tt.mutate(r)
			if _, err := r.Run(context.Background()); err == nil {
				t.Error("Run accepted an invalid config")
			}
		})
	}
}
