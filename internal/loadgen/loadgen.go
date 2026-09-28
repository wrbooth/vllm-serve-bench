// Package loadgen is the closed-loop load generator: N workers, each holding
// exactly one request in flight and sending the next the moment the previous
// one completes, for a fixed wall duration after a warmup
// (docs/02-architecture.md, "Load model").
package loadgen

import (
	"context"
	"errors"
	"slices"
	"sync"
	"time"

	"github.com/wrbooth/vllm-serve-bench/internal/metrics"
	"github.com/wrbooth/vllm-serve-bench/internal/openai"
)

// Runner runs one concurrency level. Configure the fields, then call Run.
type Runner struct {
	// Client sends the requests. Its clock (Client.Now) is the only clock
	// the runner reads, so window boundaries and request timestamps agree.
	Client *openai.Client
	// Concurrency is the number of workers, i.e. requests in flight.
	Concurrency int
	// Warmup is excluded from the results; Duration is the measured window
	// that follows it.
	Warmup, Duration time.Duration
	// RequestTimeout bounds each request; zero means no per-request limit.
	RequestTimeout time.Duration
	// Build returns the request a worker sends as its seq-th request
	// (0-based). It is called from the worker goroutines concurrently.
	Build func(worker, seq int) *openai.Request
}

// Result is the outcome of one Run.
type Result struct {
	// Start is when the workers were released; MeasureStart = Start+Warmup;
	// End = MeasureStart+Duration.
	Start, MeasureStart, End time.Time
	// Records holds one row per request whose attempt started inside
	// [MeasureStart, End), errors included, ordered by Send. Requests still
	// in flight at End run to completion and are kept: cutting them off
	// would drop exactly the slowest requests and flatter the tail.
	Records []metrics.Record
	// WarmupRequests counts requests started before MeasureStart; they are
	// not in Records.
	WarmupRequests int
	// Summary aggregates Records over Duration.
	Summary metrics.Summary
}

// Run drives the load until End, waits for in-flight requests, and returns
// the measured records. If ctx is cancelled it stops early and returns what
// was collected together with ctx's error.
func (r *Runner) Run(ctx context.Context) (Result, error) {
	switch {
	case r.Client == nil:
		return Result{}, errors.New("loadgen: Client is nil")
	case r.Build == nil:
		return Result{}, errors.New("loadgen: Build is nil")
	case r.Concurrency < 1:
		return Result{}, errors.New("loadgen: Concurrency must be at least 1")
	case r.Duration <= 0:
		return Result{}, errors.New("loadgen: Duration must be positive")
	case r.Warmup < 0:
		return Result{}, errors.New("loadgen: Warmup must not be negative")
	}

	res := Result{Start: r.Client.Now()}
	res.MeasureStart = res.Start.Add(r.Warmup)
	res.End = res.MeasureStart.Add(r.Duration)

	// Each worker owns its slice; they are merged only after Wait, so no
	// record is shared between goroutines.
	perWorker := make([]workerOut, r.Concurrency)
	var wg sync.WaitGroup
	for w := range r.Concurrency {
		wg.Go(func() { perWorker[w] = r.work(ctx, w, &res) })
	}
	wg.Wait()

	for i := range perWorker {
		res.Records = append(res.Records, perWorker[i].records...)
		res.WarmupRequests += perWorker[i].warmup
	}
	slices.SortStableFunc(res.Records, func(a, b metrics.Record) int { return a.Send.Compare(b.Send) })
	res.Summary = metrics.Summarize(res.Records, r.Duration)
	return res, ctx.Err()
}

type workerOut struct {
	records []metrics.Record
	warmup  int
}

// work is one closed-loop worker. A request belongs to the window its
// attempt started in: the same instant decides whether to send at all, so
// no request can fall between the warmup and measured sets.
func (r *Runner) work(ctx context.Context, worker int, win *Result) workerOut {
	var out workerOut
	for seq := 0; ctx.Err() == nil; seq++ {
		started := r.Client.Now()
		if !started.Before(win.End) {
			break
		}
		sr := r.send(ctx, r.Build(worker, seq))
		if started.Before(win.MeasureStart) {
			out.warmup++
			continue
		}
		out.records = append(out.records, record(worker, &sr))
	}
	return out
}

func (r *Runner) send(ctx context.Context, req *openai.Request) openai.Result {
	if r.RequestTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, r.RequestTimeout)
		defer cancel()
	}
	return r.Client.Stream(ctx, req)
}

// record applies the metric definitions to one client result.
func record(worker int, sr *openai.Result) metrics.Record {
	return metrics.NewRecord(worker, &metrics.Timing{
		Send:             sr.Send,
		FirstContent:     sr.FirstContent,
		Done:             sr.Done,
		PromptTokens:     sr.PromptTokens,
		CompletionTokens: sr.CompletionTokens,
		FinishReason:     sr.FinishReason,
		Err:              sr.Err,
	})
}
