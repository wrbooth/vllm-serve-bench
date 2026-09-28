package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/wrbooth/vllm-serve-bench/internal/crosscheck"
)

// Output files of `bench verify`.
const (
	comparisonMD   = "comparison.md"
	comparisonJSON = "comparison.json"
)

// errNotValid is returned after the comparison is written when a side's
// engine-side check failed and --allow-cached was not given.
var errNotValid = errors.New("the comparison is not valid (engine-side check failed); rerun with --allow-cached to accept it")

// crossCheckCmd is `bench verify`: it compares one or more `bench run`
// directories with a `vllm bench serve` result (docs/02, "Cross-check") and
// writes comparison.md and comparison.json. It exits 1 when a side's
// prompts were served from the prefix cache, after writing both files, so
// the flagged comparison can still be read and kept.
func crossCheckCmd(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("bench verify", flag.ContinueOnError)
	fs.SetOutput(stderr)
	opt := crosscheck.DefaultOptions
	vllmDir := fs.String("vllm", "", "directory with vLLM's "+crosscheck.VLLMBenchFile+", vllm_metrics.csv and "+crosscheck.VLLMCommandFile+" (required)")
	out := fs.String("out", "", "directory for "+comparisonMD+" and "+comparisonJSON+" (default: the parent of --vllm)")
	fs.DurationVar(&opt.Trim, "trim", opt.Trim, "cut from each end of vLLM's sends for its steady-state RPS")
	fs.Float64Var(&opt.MinUncachedFraction, "min-uncached-fraction", opt.MinUncachedFraction,
		"flag a side that prefilled fewer uncached prompt tokens per request than this fraction of its unique part")
	unique := fs.Int("vllm-unique-tokens", 0, "length of vLLM's prompts' unique part (default: --random-input-len in "+crosscheck.VLLMCommandFile+")")
	allowCached := fs.Bool("allow-cached", false, "exit 0 even when the engine-side check fails")
	fs.Usage = func() {
		_, _ = fmt.Fprintln(stderr, "usage: bench verify --vllm DIR [flags] RUN_DIR...")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *vllmDir == "" || fs.NArg() == 0 {
		fs.Usage()
		return 2
	}
	if *out == "" {
		*out = filepath.Dir(filepath.Clean(*vllmDir))
	}
	cmp, err := crossCheck(*vllmDir, *unique, fs.Args(), opt)
	if err == nil {
		err = writeComparison(*out, &cmp, commandLine(append([]string{"bench", "verify"}, args...)))
	}
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "bench verify:", err)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "wrote %s and %s in %s\n", comparisonMD, comparisonJSON, *out)
	for _, w := range cmp.Warnings {
		_, _ = fmt.Fprintln(stderr, "WARNING:", w)
	}
	if !cmp.Valid && !*allowCached {
		_, _ = fmt.Fprintln(stderr, "bench verify:", errNotValid)
		return 1
	}
	return 0
}

func crossCheck(vllmDir string, unique int, runDirs []string, opt crosscheck.Options) (crosscheck.Comparison, error) {
	v, err := crosscheck.LoadVLLM(vllmDir, unique)
	if err != nil {
		return crosscheck.Comparison{}, err
	}
	runs := make([]crosscheck.Run, 0, len(runDirs))
	for _, d := range runDirs {
		r, err := crosscheck.LoadRun(d)
		if err != nil {
			return crosscheck.Comparison{}, err
		}
		runs = append(runs, r)
	}
	return crosscheck.Compare(runs, &v, opt)
}

// writeComparison writes both files. The inputs are never touched: only
// these two names are created or replaced in out.
func writeComparison(out string, c *crosscheck.Comparison, command string) error {
	js, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(out, 0o755); err != nil { //nolint:gosec // G301: results are committed and meant to be readable
		return err
	}
	if err := os.WriteFile(filepath.Join(out, comparisonJSON), append(js, '\n'), 0o644); err != nil { //nolint:gosec // G306: as above
		return err
	}
	return os.WriteFile(filepath.Join(out, comparisonMD), c.Markdown(command), 0o644) //nolint:gosec // G306: as above
}

// commandLine renders argv for a shell, single-quoting any argument that
// is not made only of characters a shell leaves alone.
func commandLine(argv []string) string {
	out := make([]string, len(argv))
	for i, a := range argv {
		out[i] = a
		if a == "" || strings.IndexFunc(a, unsafeInShell) >= 0 {
			out[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
		}
	}
	return strings.Join(out, " ")
}

func unsafeInShell(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return false
	}
	return !strings.ContainsRune("-_./=:,+@%", r)
}
