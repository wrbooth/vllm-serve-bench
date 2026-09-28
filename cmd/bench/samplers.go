package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wrbooth/vllm-serve-bench/internal/results"
	"github.com/wrbooth/vllm-serve-bench/internal/sampler"
)

// runSamplers writes one CSV per source into dir, one row per interval,
// until ctx is done. It returns only write errors: a failed sample is a row
// in the CSV, not an error here.
func runSamplers(ctx context.Context, dir string, interval time.Duration, sources map[string]sampler.Source) error {
	var wg sync.WaitGroup
	errs := make(chan error, len(sources))
	for name, src := range sources {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- sampleTo(ctx, filepath.Join(dir, name), src, interval)
		}()
	}
	wg.Wait()
	close(errs)
	var all []error
	for err := range errs {
		all = append(all, err)
	}
	return errors.Join(all...)
}

func sampleTo(ctx context.Context, path string, src sampler.Source, interval time.Duration) error {
	f, err := os.Create(path) //nolint:gosec // G703: path is the run directory or the operator's own --out, joined with a fixed file name
	if err != nil {
		return err
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	s := &sampler.Sampler{Source: src, Now: time.Now}
	runErr := s.Run(ctx, t.C, f)
	return errors.Join(runErr, f.Close())
}

// engineFacts reads what the running engine reports about itself (KV pool,
// prefix caching) from one /metrics scrape, and the scheduler budgets from
// its recorded argv: vLLM exposes neither budget at run time, and the argv is
// what the contract trusts.
func engineFacts(scrape sampler.Metrics, argv string) *results.EngineFacts {
	var f results.EngineFacts
	if kv, ok := scrape.KVCacheTokens(); ok {
		f.KVCacheTokens = &kv
	}
	if on, ok := scrape.PrefixCaching(); ok {
		f.PrefixCaching = &on
	}
	f.MaxNumSeqs = argvInt(argv, "--max-num-seqs")
	f.MaxNumBatchedTokens = argvInt(argv, "--max-num-batched-tokens")
	return &f
}

// argvInt returns the value of an integer flag in argv, as `--flag N` or
// `--flag=N`. vLLM keeps the last occurrence of a repeated flag, so this
// does too. nil when the flag is absent or not an integer.
func argvInt(argv, flag string) *int {
	var val *int
	fields := strings.Fields(argv)
	for i, a := range fields {
		var s string
		switch {
		case a == flag && i+1 < len(fields):
			s = fields[i+1]
		case strings.HasPrefix(a, flag+"="):
			s = strings.TrimPrefix(a, flag+"=")
		default:
			continue
		}
		if n, err := strconv.Atoi(s); err == nil {
			val = &n
		}
	}
	return val
}
