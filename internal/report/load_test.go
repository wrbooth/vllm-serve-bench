package report

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A committed run directory loads with its gauges; the file reads are
// the only I/O in the package.
func TestLoadRunReadsACommittedRunDirectory(t *testing.T) {
	t.Parallel()
	dir := filepath.Join("..", "..", "results", "baseline", "throughput-baseline-20260928-014014")
	r, err := LoadRun(dir)
	if err != nil {
		t.Fatal(err)
	}
	if r.ID() != "throughput-baseline-20260928-014014" || r.Profile() != "throughput" || r.Dir != dir {
		t.Errorf("identity = %q %q %q", r.ID(), r.Profile(), r.Dir)
	}
	if len(r.Engine) == 0 || len(r.Engine[0].V) != len(EngineColumns) || len(r.GPU) == 0 || len(r.Counters) == 0 || len(r.Rows) == 0 {
		t.Errorf("loaded %d engine, %d gpu, %d counter samples and %d rows; want all non-empty", len(r.Engine), len(r.GPU), len(r.Counters), len(r.Rows))
	}
}

func TestLoadRunFailsOnAMissingOrBrokenFile(t *testing.T) {
	t.Parallel()
	src := filepath.Join("..", "..", "results", "baseline", "throughput-baseline-20260928-014014")
	copyRun := func(t *testing.T, skip string) string {
		t.Helper()
		dst := t.TempDir()
		for _, name := range []string{"config.json", "summary.json", "requests.jsonl", "vllm_metrics.csv", "gpu.csv"} {
			if name == skip {
				continue
			}
			b, err := os.ReadFile(filepath.Join(src, name))
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dst, name), b, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return dst
	}
	if _, err := LoadRun(copyRun(t, "gpu.csv")); err == nil || !strings.Contains(err.Error(), "gpu.csv") {
		t.Errorf("no gpu.csv: %v", err)
	}
	if _, err := LoadRun(copyRun(t, "config.json")); err == nil {
		t.Error("no config.json: want an error")
	}
	bad := copyRun(t, "")
	if err := os.WriteFile(filepath.Join(bad, "gpu.csv"), []byte("t_unix_ms,error\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadRun(bad); err == nil || !strings.Contains(err.Error(), "no power.draw column") {
		t.Errorf("gpu.csv without power: %v", err)
	}
	noID := copyRun(t, "")
	if err := os.WriteFile(filepath.Join(noID, "config.json"), []byte(`{"profile":{"name":"throughput"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadRun(noID); err == nil || !strings.Contains(err.Error(), "no run_id") {
		t.Errorf("config without run_id: %v", err)
	}
}
