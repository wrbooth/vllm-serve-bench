package openai_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/wrbooth/vllm-serve-bench/internal/fakeserver"
	"github.com/wrbooth/vllm-serve-bench/internal/openai"
)

// These are the only wall-clock tests of the client (AGENTS.md, testing
// standard 6). They run the real net/http transport over loopback against
// the fake server and check that the timestamps land where the server's
// configured delays put them.
//
// Why the tolerances are safe:
//
//   - Lower bound on TTFT is exact: the client stamps Send when the body is
//     written, the server reads it and then sleeps at least FirstTokenDelay
//     (timers never fire early) before writing the first content chunk, so
//     TTFT >= FirstTokenDelay on any machine.
//   - Upper slack is 50 ms: loopback delivery is microseconds; what remains
//     is goroutine scheduling and timer overshoot, which under -race on a
//     loaded CI runner is single-digit milliseconds. 50 ms is far above that
//     and still well below the 150 ms delay, so a TTFT timed from the
//     role-only chunk (~0 ms) or from before the request was built cannot
//     pass.
//   - TPOT = (Done - FirstContent) / (tokens - 1). The server sleeps at
//     least InterTokenInterval between tokens, so the decode span is at
//     least (tokens-1)*interval minus however late the client stamped the
//     first token (at most the same 50 ms slack). Spread over tokens-1 = 5
//     intervals, that is 10 ms either side of the 30 ms interval.
const (
	firstTokenDelay = 150 * time.Millisecond
	interval        = 30 * time.Millisecond
	tokens          = 6
	slack           = 50 * time.Millisecond
	tpotSlack       = slack / (tokens - 1)
)

func TestStreamTimingMatchesServerDelaysOverLoopback(t *testing.T) {
	t.Parallel()
	for _, concurrency := range []int{1, 8} {
		t.Run(map[int]string{1: "Concurrency1", 8: "Concurrency8"}[concurrency], func(t *testing.T) {
			t.Parallel()
			srv := &fakeserver.Server{
				FirstTokenDelay:    firstTokenDelay,
				InterTokenInterval: interval,
				Tokens:             tokens,
				PromptTokens:       32,
				SplitWrites:        true,
			}
			ts := httptest.NewServer(srv)
			defer ts.Close()
			c := &openai.Client{BaseURL: ts.URL}

			results := make([]openai.Result, concurrency)
			var wg sync.WaitGroup
			for i := range concurrency {
				wg.Go(func() {
					results[i] = c.Stream(context.Background(), &openai.Request{Model: "fake", MaxTokens: tokens})
				})
			}
			wg.Wait()

			if got := srv.MaxInFlight(); got != concurrency {
				t.Errorf("server saw %d concurrent requests, want %d", got, concurrency)
			}
			for i := range results {
				checkTiming(t, &results[i])
			}
		})
	}
}

func checkTiming(t *testing.T, r *openai.Result) {
	t.Helper()
	if r.Err != nil {
		t.Fatalf("Stream: %v", r.Err)
	}
	if r.PromptTokens != 32 || r.CompletionTokens != tokens || r.ContentChunks != tokens || r.FinishReason != "length" {
		t.Errorf("tokens = prompt %d, completion %d, chunks %d, finish %q; want 32, %d, %d, \"length\"",
			r.PromptTokens, r.CompletionTokens, r.ContentChunks, r.FinishReason, tokens, tokens)
	}
	ttft := r.FirstContent.Sub(r.Send)
	if ttft < firstTokenDelay || ttft > firstTokenDelay+slack {
		t.Errorf("TTFT = %v, want in [%v, %v]", ttft, firstTokenDelay, firstTokenDelay+slack)
	}
	tpot := r.Done.Sub(r.FirstContent) / (tokens - 1)
	if tpot < interval-tpotSlack || tpot > interval+tpotSlack {
		t.Errorf("TPOT = %v, want in [%v, %v]", tpot, interval-tpotSlack, interval+tpotSlack)
	}
}

func TestStreamReportsServerFailureModes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		srv        *fakeserver.Server
		wantStatus int
		wantErr    func(error) bool
		wantChunks int
	}{
		{
			name:       "Non200StatusIsAStatusError",
			srv:        &fakeserver.Server{Status: 503},
			wantStatus: 503,
			wantErr: func(err error) bool {
				var se *openai.StatusError
				return errors.As(err, &se) && se.Code == 503 && se.Message == "configured failure"
			},
		},
		{
			// The connection dies after 2 tokens: the partial stream is kept
			// (2 content chunks seen) but the request is an error.
			name:       "MidStreamAbortIsAnErrorWithPartialStream",
			srv:        &fakeserver.Server{Tokens: 5, AbortAfter: 2},
			wantStatus: 200,
			wantErr:    func(err error) bool { return err != nil && !errors.Is(err, openai.ErrNoDone) },
			wantChunks: 2,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ts := httptest.NewServer(tt.srv)
			defer ts.Close()
			r := (&openai.Client{BaseURL: ts.URL}).Stream(context.Background(), &openai.Request{Model: "fake"})
			if !tt.wantErr(r.Err) {
				t.Errorf("Err = %v (%T), not the expected error", r.Err, r.Err)
			}
			if r.Status != tt.wantStatus || r.ContentChunks != tt.wantChunks || r.Send.IsZero() || !r.Done.IsZero() {
				t.Errorf("Result = %+v; want status %d, %d chunks, Send set, Done zero", r, tt.wantStatus, tt.wantChunks)
			}
		})
	}
}

// A client whose deadline passes mid-stream gets an error, and the server
// stops streaming to it instead of sleeping through the remaining tokens.
func TestStreamDeadlineAbortsARequestInFlight(t *testing.T) {
	t.Parallel()
	srv := &fakeserver.Server{FirstTokenDelay: 10 * time.Second}
	ts := httptest.NewServer(srv)
	defer ts.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	r := (&openai.Client{BaseURL: ts.URL}).Stream(ctx, &openai.Request{Model: "fake"})
	if !errors.Is(r.Err, context.DeadlineExceeded) {
		t.Errorf("Err = %v, want context.DeadlineExceeded", r.Err)
	}
	if took := time.Since(start); took > 5*time.Second {
		t.Errorf("Stream took %v; the deadline did not abort it", took)
	}
}
