// Package results is the run directory (docs/02-architecture.md, "Run
// directory"): the schema of config.json, requests.jsonl and summary.json,
// and the writer that `bench run` fills them with. `bench report` will read
// the same types back.
//
// Durations are written as float milliseconds (seconds for window-level
// fields) and times as RFC 3339 with nanoseconds. A value that does not
// exist, such as the TTFT of an error row, is null, never 0.
package results

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/wrbooth/vllm-serve-bench/internal/loadgen"
	"github.com/wrbooth/vllm-serve-bench/internal/metrics"
)

// SchemaVersion is written into config.json and bumped on any change a
// reader must know about.
const SchemaVersion = 1

// File names inside a run directory.
const (
	ConfigFile   = "config.json"
	RequestsFile = "requests.jsonl"
	SummaryFile  = "summary.json"
	// VLLMMetricsFile and GPUFile are the 1 Hz telemetry (internal/sampler).
	VLLMMetricsFile = "vllm_metrics.csv"
	GPUFile         = "gpu.csv"
)

// Row is one line of requests.jsonl: one request of the measured window of
// one concurrency level. Error rows keep t_send and the error, and have
// null latencies.
type Row struct {
	Concurrency      int       `json:"concurrency"`
	Worker           int       `json:"worker"`
	TSend            time.Time `json:"t_send"`
	TTFTms           *float64  `json:"ttft_ms"`
	E2Ems            *float64  `json:"e2e_ms"`
	TPOTms           *float64  `json:"tpot_ms"` // null when completion_tokens < 2
	PromptTokens     int       `json:"prompt_tokens"`
	CompletionTokens int       `json:"completion_tokens"`
	FinishReason     string    `json:"finish_reason"`
	Error            string    `json:"error"` // "" on success
}

// OK reports whether the row is a successful request.
func (r *Row) OK() bool { return r.Error == "" }

// NewRow converts one measured record.
func NewRow(concurrency int, rec *metrics.Record) Row {
	row := Row{
		Concurrency:      concurrency,
		Worker:           rec.Worker,
		TSend:            rec.Send,
		PromptTokens:     rec.PromptTokens,
		CompletionTokens: rec.CompletionTokens,
		FinishReason:     rec.FinishReason,
		Error:            rec.Err,
	}
	if rec.OK() {
		row.TTFTms, row.E2Ems = ms(rec.TTFT), ms(rec.E2E)
		if rec.HasTPOT {
			row.TPOTms = ms(rec.TPOT)
		}
	}
	return row
}

// ms converts a duration to float milliseconds, exact to the nanosecond.
func ms(d time.Duration) *float64 {
	v := float64(d) / float64(time.Millisecond)
	return &v
}

// Dist is metrics.Dist in milliseconds.
type Dist struct {
	N     int     `json:"n"`
	P50ms float64 `json:"p50_ms"`
	P95ms float64 `json:"p95_ms"`
	P99ms float64 `json:"p99_ms"`
}

func newDist(d metrics.Dist) Dist {
	return Dist{N: d.N, P50ms: *ms(d.P50), P95ms: *ms(d.P95), P99ms: *ms(d.P99)}
}

// Level is one concurrency level's entry in summary.json: the window it
// ran in and the aggregates of its measured records.
type Level struct {
	Concurrency int `json:"concurrency"`
	// PrefixCacheReset is whether the engine's prefix cache was emptied
	// before this level's warmup, so no prompt from an earlier level or run
	// could be served from it.
	PrefixCacheReset bool      `json:"prefix_cache_reset"`
	Start            time.Time `json:"start"`
	MeasureStart     time.Time `json:"measure_start"`
	End              time.Time `json:"end"`
	WarmupRequests   int       `json:"warmup_requests"`
	Requests         int       `json:"requests"` // measured rows, errors included
	Errors           int       `json:"errors"`
	ErrorRate        float64   `json:"error_rate"`
	WindowS          float64   `json:"window_s"`
	RPS              float64   `json:"rps"`
	OutputTokPerSec  float64   `json:"output_tok_per_s"`
	TTFT             Dist      `json:"ttft"`
	E2E              Dist      `json:"e2e"`
	TPOT             Dist      `json:"tpot"`
}

// NewLevel summarises one loadgen run at the given concurrency.
func NewLevel(concurrency int, res *loadgen.Result) Level {
	s := &res.Summary
	return Level{
		Concurrency:     concurrency,
		Start:           res.Start,
		MeasureStart:    res.MeasureStart,
		End:             res.End,
		WarmupRequests:  res.WarmupRequests,
		Requests:        s.Requests,
		Errors:          s.Errors,
		ErrorRate:       s.ErrorRate,
		WindowS:         s.Window.Seconds(),
		RPS:             s.RPS,
		OutputTokPerSec: s.OutputTokPerSec,
		TTFT:            newDist(s.TTFT),
		E2E:             newDist(s.E2E),
		TPOT:            newDist(s.TPOT),
	}
}

// Summary is summary.json: one entry per completed level, in run order.
type Summary struct {
	RunID  string  `json:"run_id"`
	Levels []Level `json:"levels"`
}

// PromptTokenCheck compares the generator's predicted prompt_tokens with
// what the server reported for every successful request. A mismatch means
// the word list or the fixed-token count is wrong for this engine, so the
// run's input lengths are not what the profile says.
type PromptTokenCheck struct {
	Predicted  int `json:"predicted"`
	Checked    int `json:"checked"` // successful rows; error rows have no usage
	Mismatches int `json:"mismatches"`
	// Observed counts the reported values that differed from Predicted,
	// keyed by value. Empty when everything matched.
	Observed map[int]int `json:"observed_mismatches,omitempty"`
}

// CheckPromptTokens runs the check over rows.
func CheckPromptTokens(rows []Row, predicted int) PromptTokenCheck {
	c := PromptTokenCheck{Predicted: predicted}
	for i := range rows {
		if !rows[i].OK() {
			continue
		}
		c.Checked++
		if got := rows[i].PromptTokens; got != predicted {
			c.Mismatches++
			if c.Observed == nil {
				c.Observed = map[int]int{}
			}
			c.Observed[got]++
		}
	}
	return c
}

// runIDPart is what the profile and engine-config parts of a run id may
// contain, so the id is always one safe path element.
var runIDPart = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

// RunID is "<profile>-<engine-config>-<yyyymmdd-hhmmss>", the time in UTC.
func RunID(profile, engineConfig string, at time.Time) (string, error) {
	for _, part := range []string{profile, engineConfig} {
		if !runIDPart.MatchString(part) {
			return "", fmt.Errorf("results: %q is not a valid run-id part (lowercase letters, digits, '.', '_', '-')", part)
		}
	}
	return profile + "-" + engineConfig + "-" + at.UTC().Format("20060102-150405"), nil
}

// Dir is an open run directory.
type Dir struct {
	Path string
}

// Create makes the run directory out/runID. It fails if the directory
// already exists: raw results are never overwritten.
func Create(out, runID string) (*Dir, error) {
	if err := os.MkdirAll(out, 0o755); err != nil { //nolint:gosec // G301: results are committed and meant to be readable
		return nil, err
	}
	path := filepath.Join(out, runID)
	if err := os.Mkdir(path, 0o755); err != nil { //nolint:gosec // G301: as above
		return nil, fmt.Errorf("results: create run directory: %w", err)
	}
	return &Dir{Path: path}, nil
}

// AppendRows appends rows to requests.jsonl, one JSON object per line.
func (d *Dir) AppendRows(rows []Row) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	for i := range rows {
		if err := enc.Encode(&rows[i]); err != nil {
			return err
		}
	}
	f, err := os.OpenFile(filepath.Join(d.Path, RequestsFile), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644) //nolint:gosec // G302: as above
	if err != nil {
		return err
	}
	if _, err := f.Write(buf.Bytes()); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// WriteConfig writes config.json, replacing any earlier version.
func (d *Dir) WriteConfig(c *Config) error { return d.writeJSON(ConfigFile, c) }

// WriteSummary writes summary.json, replacing any earlier version.
func (d *Dir) WriteSummary(s *Summary) error { return d.writeJSON(SummaryFile, s) }

// writeJSON writes v indented, via a temporary file and a rename, so a
// crash never leaves a half-written file.
func (d *Dir) writeJSON(name string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(d.Path, "."+name+".tmp")
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil { //nolint:gosec // G306: as above
		return err
	}
	return os.Rename(tmp, filepath.Join(d.Path, name))
}
