package main

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wrbooth/vllm-serve-bench/internal/fakeserver"
	"github.com/wrbooth/vllm-serve-bench/internal/metrics"
	"github.com/wrbooth/vllm-serve-bench/internal/prompts"
	"github.com/wrbooth/vllm-serve-bench/internal/results"
)

const (
	testArgv  = "vllm serve fake --gpu-memory-utilization 0.90 --max-model-len 8192"
	testImage = "vllm/vllm-openai:v0.29.0@sha256:0123abcd"
)

// runArgs is a short two-level sweep against url, writing under out.
func runArgs(url, out string, extra ...string) []string {
	return append([]string{
		"run", "--profile", "interactive", "--concurrency", "1,2",
		"--warmup", "50ms", "--duration", "250ms",
		"--base-url", url, "--model", "fake", "--seed", "3", "--out", out,
		"--engine-config", "test", "--engine-argv", testArgv, "--engine-image", testImage,
	}, extra...)
}

// runDir is what one `bench run` wrote.
type runDir struct {
	path    string
	config  results.Config
	rows    []results.Row
	summary results.Summary
}

func readRunDir(t *testing.T, out, stdout string) *runDir {
	t.Helper()
	entries, err := os.ReadDir(out)
	if err != nil || len(entries) != 1 {
		t.Fatalf("out holds %v (%v), want exactly one run directory", entries, err)
	}
	d := &runDir{path: filepath.Join(out, entries[0].Name())}
	if strings.TrimSpace(stdout) != d.path {
		t.Errorf("stdout %q, want the run directory %q", stdout, d.path)
	}
	for name, v := range map[string]any{results.ConfigFile: &d.config, results.SummaryFile: &d.summary} {
		b, err := os.ReadFile(filepath.Join(d.path, name))
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(b, v); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	f, err := os.Open(filepath.Join(d.path, results.RequestsFile))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var r results.Row
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			t.Fatalf("requests.jsonl line %q: %v", sc.Text(), err)
		}
		d.rows = append(d.rows, r)
	}
	return d
}

// The acceptance test for `bench run`: a two-level sweep against the fake
// server writes a run directory whose three files parse and agree with
// each other and with what the server saw.
func TestRunWritesAConsistentRunDirectory(t *testing.T) {
	t.Parallel()
	srv := calibratedFake(t)
	srv.Tokens = 4 // short completions keep the test fast; max_tokens is still 128 on the wire
	srv.FirstTokenDelay, srv.InterTokenInterval = 2*time.Millisecond, time.Millisecond
	srv.UnreadyFor = 1 // the readiness wait must poll past a 503
	out := t.TempDir()

	code, stdout, stderr := runBench(t, runArgs(serve(t, srv), out)...)
	if code != 0 {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	d := readRunDir(t, out, stdout)
	cfg := &d.config

	if !strings.HasPrefix(filepath.Base(d.path), "interactive-test-") || cfg.RunID != filepath.Base(d.path) || d.summary.RunID != cfg.RunID {
		t.Errorf("run id %q / %q in directory %q", cfg.RunID, d.summary.RunID, d.path)
	}
	if !cfg.Complete || cfg.FinishedAt == nil || len(cfg.Warnings) != 0 || cfg.ReadyWaitS <= 0 {
		t.Errorf("config complete=%v finished=%v warnings=%v ready_wait_s=%v; want a clean finished run that waited for readiness",
			cfg.Complete, cfg.FinishedAt, cfg.Warnings, cfg.ReadyWaitS)
	}
	// The engine facts come from the caller, verbatim.
	if cfg.Engine.Argv != testArgv || cfg.Engine.Image != testImage || cfg.Engine.Config != "test" {
		t.Errorf("engine = %+v, want the flags verbatim", cfg.Engine)
	}
	if cfg.Flags["duration"] != "250ms" || cfg.Flags["natural-stop"] != "false" || cfg.Flags["request-timeout"] != "5m0s" {
		t.Errorf("flags = %v; want every flag, defaults included", cfg.Flags)
	}
	if !slices.Equal(cfg.Concurrency, []int{1, 2}) || cfg.Seed != 3 || cfg.Schema != results.SchemaVersion {
		t.Errorf("concurrency %v seed %d schema %d", cfg.Concurrency, cfg.Seed, cfg.Schema)
	}
	v, _ := prompts.Embedded()
	// interactive: fixed + 300 shared + 100 unique words.
	predicted := v.Fixed.Profiles["interactive"] + 400
	if p := cfg.Profile; p.PredictedPromptTokens != predicted || !p.IgnoreEOS || p.MaxTokens != 128 || p.WordListSHA256 != prompts.WordsSHA256(v.Words) {
		t.Errorf("profile = %+v, want predicted %d, ignore_eos, 128 max tokens, the embedded list's hash", p, predicted)
	}

	// Rows and summary agree level by level, and nothing the server saw
	// is unaccounted for.
	if len(d.summary.Levels) != 2 {
		t.Fatalf("summary has %d levels, want 2", len(d.summary.Levels))
	}
	seen := 0
	for i, lvl := range d.summary.Levels {
		c := []int{1, 2}[i]
		var level []results.Row
		for _, r := range d.rows {
			if r.Concurrency == c {
				level = append(level, r)
			}
		}
		checkLevelAgainstRows(t, &lvl, c, level)
		seen += lvl.WarmupRequests + lvl.Requests
		if i > 0 && lvl.Start.Before(d.summary.Levels[i-1].End) {
			t.Errorf("level c=%d started before level c=%d's window ended: levels must run in sequence", c, d.summary.Levels[i-1].Concurrency)
		}
	}
	if len(d.rows) != d.summary.Levels[0].Requests+d.summary.Levels[1].Requests {
		t.Errorf("requests.jsonl has %d rows, summary counts %d", len(d.rows), d.summary.Levels[0].Requests+d.summary.Levels[1].Requests)
	}
	if seen != srv.Requests() {
		t.Errorf("warmup + measured = %d, server saw %d chat requests", seen, srv.Requests())
	}
	if srv.RepeatedPrompts() != 0 {
		t.Errorf("%d requests repeated an earlier prompt; with prefix caching on, every prompt must be new", srv.RepeatedPrompts())
	}
	if pc := cfg.PromptTokens; pc == nil || pc.Checked != len(d.rows) || pc.Mismatches != 0 || pc.Predicted != predicted {
		t.Errorf("prompt_token_check = %+v, want %d rows checked against %d, no mismatches", pc, len(d.rows), predicted)
	}
}

// checkLevelAgainstRows recomputes a level's counts and TTFT percentiles
// from its rows, so the summary cannot drift from the raw data.
func checkLevelAgainstRows(t *testing.T, lvl *results.Level, c int, rows []results.Row) {
	t.Helper()
	if lvl.Concurrency != c || lvl.Requests != len(rows) || lvl.Requests == 0 || lvl.WarmupRequests == 0 {
		t.Fatalf("level %+v, want concurrency %d with %d requests and some warmup", lvl, c, len(rows))
	}
	var ttft []float64
	workers := map[int]bool{}
	for _, r := range rows {
		if !r.OK() || r.TTFTms == nil || r.TPOTms == nil || r.CompletionTokens != 4 {
			t.Fatalf("row %+v: want a success with TTFT and TPOT", r)
		}
		if r.TSend.Before(lvl.MeasureStart) {
			t.Errorf("row sent at %v, before the level's window opened at %v", r.TSend, lvl.MeasureStart)
		}
		ttft = append(ttft, *r.TTFTms)
		workers[r.Worker] = true
	}
	if len(workers) != c {
		t.Errorf("c=%d: rows from %d workers", c, len(workers))
	}
	slices.Sort(ttft)
	p50, _ := metrics.Percentile(ttft, 50)
	p95, _ := metrics.Percentile(ttft, 95)
	if lvl.TTFT.N != len(rows) || lvl.TTFT.P50ms != p50 || lvl.TTFT.P95ms != p95 || lvl.Errors != 0 {
		t.Errorf("c=%d summary TTFT %+v errors %d; rows give n=%d p50=%v p95=%v", c, lvl.TTFT, lvl.Errors, len(rows), p50, p95)
	}
	// RPS = rows / window.
	if want := float64(len(rows)) / lvl.WindowS; lvl.RPS != want || lvl.WindowS != 0.25 {
		t.Errorf("c=%d rps %v window %v; want %v over 0.25 s", c, lvl.RPS, lvl.WindowS, want)
	}
}

// A server whose prompt_tokens disagree with the prediction does not fail
// the run, but the mismatch is counted in config.json and printed.
func TestRunRecordsAndWarnsAboutPromptTokenMismatch(t *testing.T) {
	t.Parallel()
	srv := calibratedFake(t)
	srv.Tokens = 2
	srv.ChatOverhead += 2 // the engine counts 2 more tokens than predicted
	out := t.TempDir()
	code, stdout, stderr := runBench(t, runArgs(serve(t, srv), out, "--concurrency", "2", "--natural-stop")...)
	if code != 0 {
		t.Fatalf("exit %d, stderr %q; a mismatch is a warning, not a failure", code, stderr)
	}
	d := readRunDir(t, out, stdout)
	pc := d.config.PromptTokens
	if pc == nil || pc.Checked == 0 || pc.Mismatches != pc.Checked || pc.Observed[pc.Predicted+2] != pc.Checked {
		t.Errorf("prompt_token_check = %+v; want every request mismatched at predicted+2", pc)
	}
	if len(d.config.Warnings) != 1 || !strings.Contains(stderr, "WARNING: prompt_tokens:") {
		t.Errorf("warnings %v, stderr %q; want the mismatch in both", d.config.Warnings, stderr)
	}
	if d.config.Profile.IgnoreEOS || d.config.Flags["natural-stop"] != "true" {
		t.Errorf("--natural-stop not recorded: ignore_eos %v", d.config.Profile.IgnoreEOS)
	}
}

// Every request failing leaves the prediction unchecked, which is also a
// warning; an image without a digest is flagged too.
func TestRunWarnsWhenNothingSucceededAndWhenTheImageIsUnpinned(t *testing.T) {
	t.Parallel()
	srv := &fakeserver.Server{Models: []string{"fake"}, Status: 500}
	out := t.TempDir()
	// Go's flag package keeps the last occurrence, so this overrides the pinned image.
	code, stdout, stderr := runBench(t, runArgs(serve(t, srv), out, "--concurrency", "1", "--engine-image", "vllm/vllm-openai:latest")...)
	if code != 0 {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	d := readRunDir(t, out, stdout)
	if len(d.rows) == 0 || d.summary.Levels[0].Errors != len(d.rows) {
		t.Errorf("%d rows, %d errors; want every row an error row", len(d.rows), d.summary.Levels[0].Errors)
	}
	joined := strings.Join(d.config.Warnings, "\n")
	if !strings.Contains(joined, "not pinned by digest") || !strings.Contains(joined, "never checked") {
		t.Errorf("warnings %v; want the unpinned image and the unchecked prediction", d.config.Warnings)
	}
}

func TestRunFailsWithoutWritingWhenTheEngineNeverBecomesReady(t *testing.T) {
	t.Parallel()
	srv := &fakeserver.Server{UnreadyFor: 1 << 30}
	out := t.TempDir()
	code, _, stderr := runBench(t, runArgs(serve(t, srv), out, "--ready-timeout", "50ms")...)
	if code != 1 || !strings.Contains(stderr, "server not ready") {
		t.Errorf("exit %d stderr %q; want 1 with the readiness error", code, stderr)
	}
	if entries, _ := os.ReadDir(out); len(entries) != 0 {
		t.Errorf("out holds %v; an engine that never came up must leave no run directory", entries)
	}
}

func TestRunRejectsBadFlags(t *testing.T) {
	t.Parallel()
	base := runArgs("http://127.0.0.1:1", "unused")
	without := func(flag string) []string {
		i := slices.Index(base, flag)
		return slices.Delete(slices.Clone(base), i, i+2)
	}
	tests := []struct {
		name string
		args []string
	}{
		{name: "NoProfile", args: without("--profile")},
		{name: "UnknownProfile", args: append(slices.Clone(base), "--profile", "nope")},
		{name: "NoEngineArgv", args: without("--engine-argv")},
		{name: "NoEngineImage", args: without("--engine-image")},
		{name: "NoEngineConfig", args: without("--engine-config")},
		{name: "ConcurrencyNotANumber", args: append(slices.Clone(base), "--concurrency", "1,x")},
		{name: "ConcurrencyZero", args: append(slices.Clone(base), "--concurrency", "0")},
		{name: "ZeroDuration", args: append(slices.Clone(base), "--duration", "0s")},
		{name: "NegativeWarmup", args: append(slices.Clone(base), "--warmup", "-1s")},
		{name: "UnknownFlag", args: append(slices.Clone(base), "--nope")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if code, _, stderr := runBench(t, tt.args...); code != 2 {
				t.Errorf("exit %d stderr %q; want usage error 2", code, stderr)
			}
		})
	}
}

func TestParseLevelsKeepsOrderAndDefaultsToTheProfileSweep(t *testing.T) {
	t.Parallel()
	got, err := parseLevels(" 4, 1,16")
	if err != nil || !slices.Equal(got, []int{4, 1, 16}) {
		t.Errorf("parseLevels = %v, %v; want [4 1 16]", got, err)
	}
	f := runFlags{profile: "throughput", engineConfig: "x", engineArgv: "x", engineImage: "x", duration: time.Second}
	levels, err := f.check()
	if err != nil || !slices.Equal(levels, []int{8, 16, 32, 64, 128, 192, 256}) {
		t.Errorf("default levels = %v, %v; want the throughput sweep", levels, err)
	}
}
