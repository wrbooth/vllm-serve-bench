package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/wrbooth/vllm-serve-bench/internal/crosscheck"
	"github.com/wrbooth/vllm-serve-bench/internal/report"
)

// reportCmd is `bench report`: it rewrites the generated blocks of a
// results document (docs/03-results.md) from committed run directories.
// Only the text between `bench-report` marker lines is replaced; a
// document that does not exist yet is started from a skeleton with a
// block for every run given.
func reportCmd(args []string, stdout, stderr io.Writer) int {
	fset := flag.NewFlagSet("bench report", flag.ContinueOnError)
	fset.SetOutput(stderr)
	out := fset.String("out", "docs/03-results.md", "results document to rewrite (created from a skeleton if absent)")
	sloPath := fset.String("slo", "docs/slo.json", "committed SLO file")
	minUncached := fset.Float64("min-uncached-fraction", crosscheck.DefaultOptions.MinUncachedFraction,
		"flag a level that prefilled fewer uncached prompt tokens per request than this fraction of its unique part")
	fset.Usage = func() {
		_, _ = fmt.Fprintln(stderr, "usage: bench report [--out FILE] [--slo FILE] RUN_DIR...")
		fset.PrintDefaults()
	}
	if err := fset.Parse(args); err != nil {
		return 2
	}
	if fset.NArg() == 0 {
		fset.Usage()
		return 2
	}
	command := commandLine(append([]string{"bench", "report"}, args...))
	missing, err := writeReport(*out, *sloPath, *minUncached, fset.Args(), command)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "bench report:", err)
		return 1
	}
	_, _ = fmt.Fprintln(stdout, "wrote", *out)
	for _, id := range missing {
		_, _ = fmt.Fprintf(stderr, "note: run %s has no table block in %s; to show it, add\n  %s\n  %s\n",
			id, *out, report.BeginMarker("table:"+id), report.EndMarker("table:"+id))
	}
	return 0
}

// writeReport loads everything, renders every block of the document (or
// of a new skeleton) and writes it. It returns the runs with no table
// block. Nothing is written if any step fails.
func writeReport(out, sloPath string, minUncached float64, dirs []string, command string) ([]string, error) {
	slo, err := report.LoadSLO(sloPath)
	if err != nil {
		return nil, err
	}
	d := report.Doc{Command: command, SLOPath: sloPath, SLO: slo}
	seen := map[string]string{}
	for _, dir := range dirs {
		r, err := report.LoadRun(dir)
		if err != nil {
			return nil, err
		}
		if prev, dup := seen[r.ID()]; dup {
			return nil, fmt.Errorf("run %s is given twice (%s and %s)", r.ID(), prev, dir)
		}
		seen[r.ID()] = dir
		t, err := report.NewTable(&r, &slo, minUncached)
		if err != nil {
			return nil, err
		}
		d.Runs = append(d.Runs, r)
		d.Tables = append(d.Tables, t)
	}
	doc, err := os.ReadFile(out)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		doc = d.Skeleton()
	case err != nil:
		return nil, err
	}
	next, ids, err := report.Rewrite(doc, d.Render)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", out, err)
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil { //nolint:gosec // G301: docs are committed and meant to be readable
		return nil, err
	}
	if err := os.WriteFile(out, next, 0o644); err != nil { //nolint:gosec // G306: as above
		return nil, err
	}
	return d.MissingTables(ids), nil
}
