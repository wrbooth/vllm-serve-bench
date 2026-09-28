package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// committedRound is a cross-check round under results/verify/ and the
// exit code its committed comparison was generated with.
type committedRound struct {
	dir  string
	exit int
}

var committedRounds = []committedRound{
	{"20260927-interactive-c8-varied-output", 0},
	{"20260927-interactive-c8-varied-output-cache-contaminated", 1},
}

// copyInputs copies a round's inputs (never its outputs) from the repo
// into root at the same relative path.
func copyInputs(t *testing.T, root, round string) {
	t.Helper()
	src := filepath.Join("..", "..", "results", "verify", round)
	err := filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		switch d.Name() {
		case "vllm-bench.json", "vllm_metrics.csv", "command.txt", "config.json", "summary.json", "requests.jsonl":
		default:
			return nil
		}
		rel, err := filepath.Rel(filepath.Join("..", ".."), path)
		if err != nil {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		dst := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		return os.WriteFile(dst, b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// commandIn returns the `bench verify` command a comparison.md records.
func commandIn(t *testing.T, md []byte) []string {
	t.Helper()
	for line := range strings.SplitSeq(string(md), "\n") {
		if strings.HasPrefix(line, "bench verify ") {
			return strings.Fields(line)
		}
	}
	t.Fatal("comparison.md records no `bench verify` command")
	return nil
}

// The committed comparison.md and comparison.json are what the command
// in their header produces from the committed inputs, byte for byte. The
// inputs are copied to a scratch root so the default --out (the round's
// directory) writes there, not into the repo. Not parallel: t.Chdir.
func TestVerifyRegeneratesTheCommittedComparisons(t *testing.T) {
	for _, r := range committedRounds {
		t.Run(r.dir, func(t *testing.T) {
			committed := filepath.Join("..", "..", "results", "verify", r.dir)
			wantMD, err := os.ReadFile(filepath.Join(committed, comparisonMD))
			if err != nil {
				t.Fatal(err)
			}
			wantJSON, err := os.ReadFile(filepath.Join(committed, comparisonJSON))
			if err != nil {
				t.Fatal(err)
			}
			argv := commandIn(t, wantMD)
			root := t.TempDir()
			copyInputs(t, root, r.dir)
			t.Chdir(root)

			code, out, errs := runBench(t, argv[1:]...)
			if code != r.exit {
				t.Fatalf("exit %d, want %d; stdout %q stderr %q", code, r.exit, out, errs)
			}
			for name, want := range map[string][]byte{comparisonMD: wantMD, comparisonJSON: wantJSON} {
				got, err := os.ReadFile(filepath.Join("results", "verify", r.dir, name))
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(got, want) {
					t.Errorf("regenerated %s differs from the committed one; rerun the command in its header and commit the result", name)
				}
			}
		})
	}
}

func TestVerifyWritesTheFlaggedComparisonAndExitsZeroOnlyWithAllowCached(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	round := committedRounds[1].dir
	copyInputs(t, root, round)
	dir := filepath.Join(root, "results", "verify", round)
	args := []string{
		"verify", "--vllm", filepath.Join(dir, "vllm"), "--out", filepath.Join(root, "out"),
		filepath.Join(dir, "a", "interactive-baseline-20260928-010408"),
	}

	code, _, errs := runBench(t, args...)
	if code != 1 || !strings.Contains(errs, "WARNING: ours (a): prefilled") || !strings.Contains(errs, "not valid") {
		t.Errorf("without --allow-cached: exit %d, stderr %q; want 1 with a warning", code, errs)
	}
	md, err := os.ReadFile(filepath.Join(root, "out", comparisonMD))
	if err != nil || !bytes.Contains(md, []byte("**NOT VALID:**")) {
		t.Errorf("comparison.md not written or not flagged (%v)", err)
	}

	// Flags go before the run directories (Go's flag package stops at the
	// first positional argument).
	code, _, errs = runBench(t, append([]string{"verify", "--allow-cached"}, args[1:]...)...)
	if code != 0 || !strings.Contains(errs, "WARNING: ours (a)") {
		t.Errorf("with --allow-cached: exit %d, stderr %q; want 0, still warning", code, errs)
	}
}

func TestVerifyRejectsBadUsageAndMissingInputs(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	tests := []struct {
		name string
		args []string
		code int
	}{
		{"NoVLLM", []string{"verify", "run"}, 2},
		{"NoRuns", []string{"verify", "--vllm", tmp}, 2},
		{"UnknownFlag", []string{"verify", "--nope"}, 2},
		{"MissingVLLMFiles", []string{"verify", "--vllm", tmp, "--out", tmp, tmp}, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if code, _, errs := runBench(t, tt.args...); code != tt.code {
				t.Errorf("exit %d, want %d; stderr %q", code, tt.code, errs)
			}
		})
	}
	// A missing run directory fails after vLLM's side loads.
	root := t.TempDir()
	copyInputs(t, root, committedRounds[0].dir)
	vllm := filepath.Join(root, "results", "verify", committedRounds[0].dir, "vllm")
	if code, _, errs := runBench(t, "verify", "--vllm", vllm, "--out", root, filepath.Join(root, "absent")); code != 1 {
		t.Errorf("missing run dir: exit %d, want 1; stderr %q", code, errs)
	}
}

func TestCommandLineQuotesOnlyWhatAShellWouldSplit(t *testing.T) {
	t.Parallel()
	got := commandLine([]string{"bench", "verify", "--trim=10s", "results/a b", "it's", ""})
	want := `bench verify --trim=10s 'results/a b' 'it'\''s' ''`
	if got != want {
		t.Errorf("commandLine = %s, want %s", got, want)
	}
}
