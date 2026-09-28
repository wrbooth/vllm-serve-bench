package crosscheck

import (
	"bufio"
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/wrbooth/vllm-serve-bench/internal/results"
)

// Files in the vLLM side's directory (scripts/cross-check.sh).
const (
	VLLMBenchFile   = "vllm-bench.json"
	VLLMCommandFile = "command.txt"
)

// LoadRun reads a `bench run` directory. Its label is "ours (<parent>)",
// the name of the directory holding it (a/ and b/ in the cross-check).
func LoadRun(dir string) (Run, error) {
	r := Run{Dir: dir, Label: "ours (" + filepath.Base(filepath.Dir(filepath.Clean(dir))) + ")"}
	if err := readJSON(filepath.Join(dir, results.ConfigFile), &r.Config); err != nil {
		return Run{}, err
	}
	if err := readJSON(filepath.Join(dir, results.SummaryFile), &r.Summary); err != nil {
		return Run{}, err
	}
	var err error
	if r.Rows, err = readRows(filepath.Join(dir, results.RequestsFile)); err != nil {
		return Run{}, err
	}
	if r.Telemetry, err = readTelemetry(filepath.Join(dir, results.VLLMMetricsFile)); err != nil {
		return Run{}, err
	}
	return r, nil
}

// LoadVLLM reads the vLLM side's directory. uniqueTokens, when positive,
// is the length of its prompts' unique part; otherwise it is read from
// --random-input-len in command.txt.
func LoadVLLM(dir string, uniqueTokens int) (VLLM, error) {
	v := VLLM{Dir: dir, UniqueTokens: uniqueTokens}
	if err := readJSON(filepath.Join(dir, VLLMBenchFile), &v.Bench); err != nil {
		return VLLM{}, err
	}
	var err error
	if v.Telemetry, err = readTelemetry(filepath.Join(dir, results.VLLMMetricsFile)); err != nil {
		return VLLM{}, err
	}
	if v.UniqueTokens <= 0 {
		cmd, err := os.ReadFile(filepath.Join(dir, VLLMCommandFile))
		if err != nil {
			return VLLM{}, fmt.Errorf("%w (or pass the unique length explicitly)", err)
		}
		if v.UniqueTokens, err = RandomInputLen(string(cmd)); err != nil {
			return VLLM{}, fmt.Errorf("%s: %w", VLLMCommandFile, err)
		}
	}
	return v, nil
}

// RandomInputLen finds --random-input-len in a `vllm bench serve` command
// line: with --random-prefix-len, the length of each prompt's unique part.
func RandomInputLen(cmd string) (int, error) {
	fields := strings.Fields(cmd)
	for i, fl := range fields {
		val, ok := "", false
		switch {
		case fl == "--random-input-len" && i+1 < len(fields):
			val, ok = fields[i+1], true
		case strings.HasPrefix(fl, "--random-input-len="):
			val, ok = strings.TrimPrefix(fl, "--random-input-len="), true
		}
		if ok {
			n, err := strconv.Atoi(val)
			if err != nil || n <= 0 {
				return 0, fmt.Errorf("--random-input-len %q is not a positive integer", val)
			}
			return n, nil
		}
	}
	return 0, errors.New("no --random-input-len in the command")
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

func readRows(path string) ([]results.Row, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var rows []results.Row
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for n := 1; sc.Scan(); n++ {
		if len(bytes.TrimSpace(sc.Bytes())) == 0 {
			continue
		}
		var r results.Row
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			return nil, fmt.Errorf("%s line %d: %w", path, n, err)
		}
		rows = append(rows, r)
	}
	return rows, sc.Err()
}

func readTelemetry(path string) ([]Sample, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close() //nolint:errcheck // read-only file; a close error cannot lose data
	s, err := ParseTelemetry(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return s, nil
}

// telemetryColumns are the vllm_metrics.csv columns the check reads
// (internal/sampler writes them).
var telemetryColumns = []string{
	"t_unix_ms",
	"vllm:prefix_cache_queries_total",
	"vllm:prefix_cache_hits_total",
	"vllm:time_to_first_token_seconds_sum",
	"vllm:time_to_first_token_seconds_count",
	"error",
}

// ParseTelemetry reads vllm_metrics.csv. A row whose scrape failed (its
// error column is set, its values empty) is skipped: it measured nothing,
// and the counters are cumulative, so the next good row loses no data.
func ParseTelemetry(r io.Reader) ([]Sample, error) {
	cr := csv.NewReader(r)
	header, err := cr.Read()
	if err != nil {
		return nil, fmt.Errorf("header: %w", err)
	}
	col := make([]int, len(telemetryColumns))
	for i, name := range telemetryColumns {
		col[i] = -1
		for j, h := range header {
			if h == name {
				col[i] = j
			}
		}
		if col[i] < 0 {
			return nil, fmt.Errorf("no %s column", name)
		}
	}
	var out []Sample
	for line := 2; ; line++ {
		rec, err := cr.Read()
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		if rec[col[5]] != "" {
			continue
		}
		ms, err := strconv.ParseInt(rec[col[0]], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("line %d: t_unix_ms: %w", line, err)
		}
		var v [4]float64
		for i := range v {
			if v[i], err = strconv.ParseFloat(rec[col[i+1]], 64); err != nil {
				return nil, fmt.Errorf("line %d: %s: %w", line, telemetryColumns[i+1], err)
			}
		}
		out = append(out, Sample{At: time.UnixMilli(ms), PrefixQueries: v[0], PrefixHits: v[1], TTFTSum: v[2], TTFTCount: v[3]})
	}
}
