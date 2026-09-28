package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/wrbooth/vllm-serve-bench/internal/sampler"
)

// sampleCmd runs the engine and GPU samplers on their own, until the duration
// elapses or the process is interrupted. `bench run` starts the same samplers
// itself; this form covers load that `bench run` does not drive, such as the
// `vllm bench serve` cross-check.
func sampleCmd(ctx context.Context, args []string, stderr io.Writer) int {
	if err := sample(ctx, args, stderr); err != nil {
		_, _ = fmt.Fprintln(stderr, "bench sample:", err)
		return 1
	}
	return 0
}

func sample(ctx context.Context, args []string, stderr io.Writer) error {
	fs := flag.NewFlagSet("bench sample", flag.ContinueOnError)
	fs.SetOutput(stderr)
	baseURL := fs.String("base-url", "http://127.0.0.1:8000", "engine base URL")
	out := fs.String("out", "", "directory for vllm_metrics.csv and gpu.csv (created)")
	interval := fs.Duration("interval", time.Second, "sampling interval")
	duration := fs.Duration("duration", 0, "stop after this long; 0 runs until interrupted")
	noGPU := fs.Bool("no-gpu", false, "skip the nvidia-smi sampler")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *out == "" {
		return errors.New("--out is required")
	}
	if err := os.MkdirAll(*out, 0o750); err != nil {
		return err
	}

	if *duration > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, *duration)
		defer cancel()
	}

	// A scrape can never take longer than the interval, or rows pile up.
	engine := &sampler.Engine{
		URL:    strings.TrimSuffix(*baseURL, "/") + "/metrics",
		Client: &http.Client{Timeout: *interval * 9 / 10},
	}
	if _, err := engine.Sample(ctx); err != nil {
		return fmt.Errorf("first scrape: %w", err)
	}
	if kv, ok := engine.Last.KVCacheTokens(); ok {
		_, _ = fmt.Fprintf(stderr, "bench sample: engine KV pool %d tokens\n", kv)
	}

	sources := map[string]sampler.Source{"vllm_metrics.csv": engine}
	if !*noGPU {
		sources["gpu.csv"] = &sampler.GPU{}
	}
	var wg sync.WaitGroup
	errs := make(chan error, len(sources))
	for name, src := range sources {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- sampleTo(ctx, filepath.Join(*out, name), src, *interval)
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
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	s := &sampler.Sampler{Source: src, Now: time.Now}
	runErr := s.Run(ctx, t.C, f)
	return errors.Join(runErr, f.Close())
}
