package report

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/wrbooth/vllm-serve-bench/internal/crosscheck"
	"github.com/wrbooth/vllm-serve-bench/internal/results"
)

// LoadRun reads a run directory: config, summary and requests through
// crosscheck.LoadRun (the same reader `bench verify` uses), then the
// gauges of vllm_metrics.csv and gpu.csv.
func LoadRun(dir string) (Run, error) {
	cr, err := crosscheck.LoadRun(dir)
	if err != nil {
		return Run{}, err
	}
	r := Run{Dir: dir, Config: cr.Config, Summary: cr.Summary, Rows: cr.Rows, Counters: cr.Telemetry}
	if r.Config.RunID == "" {
		return Run{}, fmt.Errorf("%s: config.json has no run_id", dir)
	}
	if r.Engine, err = readPoints(filepath.Join(dir, results.VLLMMetricsFile), EngineColumns); err != nil {
		return Run{}, err
	}
	if r.GPU, err = readPoints(filepath.Join(dir, results.GPUFile), GPUColumns); err != nil {
		return Run{}, err
	}
	return r, nil
}

// LoadSLO reads the SLO file.
func LoadSLO(path string) (SLO, error) {
	f, err := os.Open(path)
	if err != nil {
		return SLO{}, err
	}
	defer f.Close() //nolint:errcheck // read-only file; a close error cannot lose data
	s, err := ParseSLO(f)
	if err != nil {
		return SLO{}, fmt.Errorf("%s: %w", path, err)
	}
	return s, nil
}

func readPoints(path string, cols []string) ([]Point, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close() //nolint:errcheck // read-only file; a close error cannot lose data
	p, err := ParsePoints(f, cols)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return p, nil
}

// ParsePoints reads a sampler CSV (internal/sampler): a t_unix_ms column,
// the value columns, and an error column. A row whose error is set
// measured nothing and is skipped, as in crosscheck.ParseTelemetry.
func ParsePoints(r io.Reader, cols []string) ([]Point, error) {
	cr := csv.NewReader(r)
	header, err := cr.Read()
	if err != nil {
		return nil, fmt.Errorf("header: %w", err)
	}
	idx := map[string]int{}
	for i, h := range header {
		idx[h] = i
	}
	want := append([]string{"t_unix_ms", "error"}, cols...)
	for _, name := range want {
		if _, ok := idx[name]; !ok {
			return nil, fmt.Errorf("no %s column", name)
		}
	}
	var out []Point
	for line := 2; ; line++ {
		rec, err := cr.Read()
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		if rec[idx["error"]] != "" {
			continue
		}
		ms, err := strconv.ParseInt(rec[idx["t_unix_ms"]], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("line %d: t_unix_ms: %w", line, err)
		}
		p := Point{At: time.UnixMilli(ms), V: make([]float64, len(cols))}
		for i, c := range cols {
			if p.V[i], err = strconv.ParseFloat(rec[idx[c]], 64); err != nil {
				return nil, fmt.Errorf("line %d: %s: %w", line, c, err)
			}
		}
		out = append(out, p)
	}
}
