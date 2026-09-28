package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/wrbooth/vllm-serve-bench/internal/loadgen"
	"github.com/wrbooth/vllm-serve-bench/internal/openai"
	"github.com/wrbooth/vllm-serve-bench/internal/prompts"
	"github.com/wrbooth/vllm-serve-bench/internal/results"
)

// readyPoll is how often the readiness wait polls /v1/models.
const readyPoll = time.Second

type runFlags struct {
	profile, concurrency, baseURL, model, out string
	engineConfig, engineArgv, engineImage     string
	warmup, duration, requestTimeout, ready   time.Duration
	seed                                      uint64
	naturalStop                               bool
}

func (f *runFlags) register(fs *flag.FlagSet) {
	fs.StringVar(&f.profile, "profile", "", "workload profile: "+strings.Join(prompts.ProfileNames(), " | "))
	fs.StringVar(&f.concurrency, "concurrency", "", "comma-separated concurrency levels, run in order (default: the profile's sweep)")
	fs.DurationVar(&f.warmup, "warmup", 10*time.Second, "warmup before each level's measured window")
	fs.DurationVar(&f.duration, "duration", 60*time.Second, "measured window per level")
	fs.DurationVar(&f.requestTimeout, "request-timeout", 5*time.Minute, "per-request deadline; a request past it is an error row")
	fs.DurationVar(&f.ready, "ready-timeout", 10*time.Minute, "how long to wait for /v1/models before giving up")
	fs.StringVar(&f.baseURL, "base-url", "http://localhost:8000", "engine root URL")
	fs.StringVar(&f.model, "model", "Qwen/Qwen2.5-7B-Instruct", "served model name")
	fs.Uint64Var(&f.seed, "seed", 1, "prompt seed")
	fs.StringVar(&f.out, "out", "results", "parent directory of the run directory")
	fs.StringVar(&f.engineConfig, "engine-config", "", "engine config name (deploy/compose/engine/<name>.env); part of the run id")
	fs.StringVar(&f.engineArgv, "engine-argv", "", "the running engine's argv, from `deploy/compose/engine.sh argv`; recorded verbatim")
	fs.StringVar(&f.engineImage, "engine-image", "", "engine image as ref@digest, from the compose .env; recorded verbatim")
	fs.BoolVar(&f.naturalStop, "natural-stop", false, "let requests stop at EOS (ignore_eos off); output length then varies")
}

// check validates the flags and returns the concurrency levels.
func (f *runFlags) check() ([]int, error) {
	switch {
	case f.profile == "":
		return nil, errors.New("--profile is required")
	case f.engineConfig == "" || f.engineArgv == "" || f.engineImage == "":
		return nil, errors.New("--engine-config, --engine-argv and --engine-image are required: " +
			"a result without the engine's actual argv cannot be defended")
	case f.duration <= 0:
		return nil, errors.New("--duration must be positive")
	case f.warmup < 0:
		return nil, errors.New("--warmup must not be negative")
	}
	p, err := prompts.Lookup(f.profile)
	if err != nil {
		return nil, err
	}
	if f.concurrency == "" {
		return p.Sweep, nil
	}
	return parseLevels(f.concurrency)
}

// parseLevels parses "1,2,4" into positive integers, in the order given.
func parseLevels(s string) ([]int, error) {
	parts := strings.Split(s, ",")
	levels := make([]int, 0, len(parts))
	for _, part := range parts {
		n, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil || n < 1 {
			return nil, fmt.Errorf("--concurrency: %q is not a positive integer", part)
		}
		levels = append(levels, n)
	}
	return levels, nil
}

// runCmd is `bench run`: a closed-loop sweep over the concurrency levels,
// written to a new run directory. Exit 0 when every level completed (a
// prompt-token mismatch is a warning, recorded in config.json), 1 on
// failure or interruption, 2 on a usage error.
func runCmd(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("bench run", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var f runFlags
	f.register(fs)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	levels, err := f.check()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "bench run:", err)
		return 2
	}
	flags := map[string]string{}
	fs.VisitAll(func(fl *flag.Flag) { flags[fl.Name] = fl.Value.String() })

	if err := run(ctx, &f, levels, newConfig(&f, levels, args, flags), stdout, stderr); err != nil {
		_, _ = fmt.Fprintln(stderr, "bench run:", err)
		return 1
	}
	return 0
}

func newConfig(f *runFlags, levels []int, args []string, flags map[string]string) *results.Config {
	return &results.Config{
		Schema:       results.SchemaVersion,
		BenchVersion: version,
		Args:         args,
		Flags:        flags,
		BaseURL:      f.baseURL,
		Model:        f.model,
		Concurrency:  levels,
		WarmupS:      f.warmup.Seconds(),
		DurationS:    f.duration.Seconds(),
		Seed:         f.seed,
		Engine:       results.EngineConfig{Config: f.engineConfig, Image: f.engineImage, Argv: f.engineArgv},
		Warnings:     []string{},
	}
}

func run(ctx context.Context, f *runFlags, levels []int, cfg *results.Config, stdout, stderr io.Writer) error {
	v, err := prompts.Embedded()
	if err != nil {
		return err
	}
	g, err := v.Generator(f.profile, f.seed)
	if err != nil {
		return err
	}
	cfg.Profile = profileConfig(g, v, f.naturalStop)
	if !strings.Contains(f.engineImage, "@sha256:") {
		cfg.Warnings = append(cfg.Warnings, "engine image is not pinned by digest: "+f.engineImage)
	}

	client := &openai.Client{BaseURL: f.baseURL}
	readyStart := time.Now()
	rctx, cancel := context.WithTimeout(ctx, f.ready)
	err = client.WaitReady(rctx, f.model, readyPoll)
	cancel()
	if err != nil {
		return err
	}
	cfg.ReadyWaitS = time.Since(readyStart).Seconds()

	cfg.StartedAt = time.Now()
	if cfg.RunID, err = results.RunID(f.profile, f.engineConfig, cfg.StartedAt); err != nil {
		return err
	}
	dir, err := results.Create(f.out, cfg.RunID)
	if err != nil {
		return err
	}
	if err := dir.WriteConfig(cfg); err != nil {
		return err
	}

	rows, runErr := sweep(ctx, f, levels, g, client, dir, cfg.RunID, stderr)

	check := results.CheckPromptTokens(rows, g.PromptTokens())
	cfg.PromptTokens = &check
	if w := promptWarning(&check); w != "" {
		cfg.Warnings = append(cfg.Warnings, w)
		_, _ = fmt.Fprintln(stderr, "WARNING:", w)
	}
	finished := time.Now()
	cfg.FinishedAt = &finished
	cfg.Complete = runErr == nil
	if err := dir.WriteConfig(cfg); err != nil {
		return err
	}
	_, _ = fmt.Fprintln(stdout, dir.Path)
	if runErr != nil {
		return fmt.Errorf("interrupted; partial results in %s: %w", dir.Path, runErr)
	}
	return nil
}

func profileConfig(g *prompts.Generator, v *prompts.Vocab, naturalStop bool) results.ProfileConfig {
	p := g.Profile()
	return results.ProfileConfig{
		Name:                  p.Name,
		SharedWords:           p.SharedWords,
		UniqueWords:           p.UniqueWords,
		FixedTokens:           v.Fixed.Profiles[p.Name],
		FixedSource:           v.Fixed.Source,
		FixedModel:            v.Fixed.Model,
		PredictedPromptTokens: g.PromptTokens(),
		WordListSHA256:        prompts.WordsSHA256(v.Words),
		WordListSize:          len(v.Words),
		MaxTokens:             p.MaxTokens,
		IgnoreEOS:             !naturalStop,
		Temperature:           prompts.Temperature,
		RepetitionPenalty:     prompts.RepetitionPenalty,
	}
}

// sweep runs the levels in order, each with its own warmup, appending each
// level's rows and rewriting summary.json as soon as the level ends, so an
// interrupted sweep keeps every finished level. Request indexes run on
// across levels and warmups: no prompt is sent twice in a run.
func sweep(ctx context.Context, f *runFlags, levels []int, g *prompts.Generator, client *openai.Client,
	dir *results.Dir, runID string, stderr io.Writer,
) ([]results.Row, error) {
	var next atomic.Uint64
	build := func(_, _ int) *openai.Request { return g.Request(f.model, next.Add(1)-1, f.naturalStop) }
	summary := results.Summary{RunID: runID}
	var all []results.Row
	for _, c := range levels {
		r := &loadgen.Runner{
			Client: client, Concurrency: c, Warmup: f.warmup, Duration: f.duration,
			RequestTimeout: f.requestTimeout, Build: build,
		}
		res, runErr := r.Run(ctx)
		rows := make([]results.Row, len(res.Records))
		for i := range res.Records {
			rows[i] = results.NewRow(c, &res.Records[i])
		}
		if err := dir.AppendRows(rows); err != nil {
			return all, err
		}
		all = append(all, rows...)
		lvl := results.NewLevel(c, &res)
		summary.Levels = append(summary.Levels, lvl)
		if err := dir.WriteSummary(&summary); err != nil {
			return all, err
		}
		_, _ = fmt.Fprintf(stderr, "c=%d: %d requests (%d errors, %d warmup), %.2f req/s, %.1f tok/s, TTFT p50 %.1f ms p95 %.1f ms, TPOT p50 %.2f ms\n",
			c, lvl.Requests, lvl.Errors, lvl.WarmupRequests, lvl.RPS, lvl.OutputTokPerSec, lvl.TTFT.P50ms, lvl.TTFT.P95ms, lvl.TPOT.P50ms)
		if runErr != nil {
			return all, runErr
		}
	}
	return all, nil
}

// promptWarning explains a failed or empty prompt-token check; "" when it
// passed.
func promptWarning(c *results.PromptTokenCheck) string {
	switch {
	case c.Checked == 0:
		return "no successful requests, so the predicted prompt_tokens was never checked"
	case c.Mismatches > 0:
		return fmt.Sprintf("prompt_tokens: %d of %d successful requests differ from the predicted %d (observed %v); "+
			"input lengths are not what the profile says; run `bench prompts verify`",
			c.Mismatches, c.Checked, c.Predicted, c.Observed)
	}
	return ""
}
