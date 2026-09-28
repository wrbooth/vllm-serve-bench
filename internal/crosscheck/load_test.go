package crosscheck

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const header = "t_unix_ms,vllm:num_requests_running,vllm:prefix_cache_queries_total,vllm:prefix_cache_hits_total,vllm:time_to_first_token_seconds_sum,vllm:time_to_first_token_seconds_count,error\n"

func TestParseTelemetryReadsColumnsByNameAndSkipsFailedTicks(t *testing.T) {
	t.Parallel()
	in := header +
		"1000,8,100,40,0.5,2,\n" +
		"2000,,,,,,scrape failed: timeout\n" +
		"3000,8,300,90,1.25,6,\n"
	got, err := ParseTelemetry(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	want := []Sample{
		{At: time.UnixMilli(1000), PrefixQueries: 100, PrefixHits: 40, TTFTSum: 0.5, TTFTCount: 2},
		{At: time.UnixMilli(3000), PrefixQueries: 300, PrefixHits: 90, TTFTSum: 1.25, TTFTCount: 6},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d samples, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if !got[i].At.Equal(want[i].At) || got[i].PrefixQueries != want[i].PrefixQueries || got[i].PrefixHits != want[i].PrefixHits ||
			got[i].TTFTSum != want[i].TTFTSum || got[i].TTFTCount != want[i].TTFTCount {
			t.Errorf("sample %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestParseTelemetryRejectsMalformedFiles(t *testing.T) {
	t.Parallel()
	tests := map[string]string{
		"Empty":         "",
		"MissingColumn": "t_unix_ms,error\n1000,\n",
		"BadTime":       header + "soon,8,1,1,1,1,\n",
		"BadValue":      header + "1000,8,many,1,1,1,\n",
		"Ragged":        header + "1000,8\n",
	}
	for name, in := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := ParseTelemetry(strings.NewReader(in)); err == nil {
				t.Errorf("ParseTelemetry(%q): want an error", in)
			}
		})
	}
}

func TestRandomInputLenReadsTheUniqueLengthFromVLLMsCommand(t *testing.T) {
	t.Parallel()
	tests := []struct {
		cmd  string
		want int
		err  bool
	}{
		{"vllm bench serve --random-prefix-len 300 --random-input-len 100 --seed 1", 100, false},
		{"vllm bench serve --random-input-len=64", 64, false},
		{"vllm bench serve --random-prefix-len 300", 0, true},
		{"vllm bench serve --random-input-len lots", 0, true},
		{"vllm bench serve --random-input-len 0", 0, true},
		{"vllm bench serve --random-input-len", 0, true},
	}
	for _, tt := range tests {
		got, err := RandomInputLen(tt.cmd)
		if got != tt.want || (err != nil) != tt.err {
			t.Errorf("RandomInputLen(%q) = %d, %v; want %d, error %v", tt.cmd, got, err, tt.want, tt.err)
		}
	}
}

// The committed valid round: the figures in wiki/log.md ("Cross-check
// passes"), which were first computed by hand, come out of the code.
func TestCommittedRoundReproducesTheWikiFigures(t *testing.T) {
	t.Parallel()
	round := filepath.Join("..", "..", "results", "verify", "20260927-interactive-c8-varied-output")
	a, err := LoadRun(filepath.Join(round, "a", "interactive-baseline-20260928-012148"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := LoadRun(filepath.Join(round, "b", "interactive-baseline-20260928-012415"))
	if err != nil {
		t.Fatal(err)
	}
	v, err := LoadVLLM(filepath.Join(round, "vllm"), 0)
	if err != nil {
		t.Fatal(err)
	}
	c, err := Compare([]Run{a, b}, &v, DefaultOptions)
	if err != nil {
		t.Fatal(err)
	}
	checks := []struct {
		what string
		got  float64
		dig  int
		want string
	}{
		{"ours (a) TTFT p50", c.Ours[0].TTFT.P50, 1, "31.7"},
		{"ours (a) TTFT p95", c.Ours[0].TTFT.P95, 1, "36.6"},
		{"ours (b) TTFT p95", c.Ours[1].TTFT.P95, 1, "36.7"},
		{"vLLM TTFT p50", c.VLLMReported.TTFT.P50, 2, "31.83"},
		{"vLLM TTFT p95", c.VLLMReported.TTFT.P95, 2, "36.46"},
		{"ours (a) TPOT p50", c.Ours[0].TPOT.P50, 2, "9.78"},
		{"ours (b) TPOT p50", c.Ours[1].TPOT.P50, 2, "9.79"},
		{"vLLM TPOT p50", c.VLLMReported.TPOT.P50, 2, "9.80"},
		{"ours (a) E2E p99", c.Ours[0].E2E.P99, 0, "1590"},
		{"ours (b) E2E p99", c.Ours[1].E2E.P99, 0, "1588"},
		{"vLLM E2E p99", c.VLLMReported.E2E.P99, 0, "1587"},
		{"ours RPS", c.Ours[0].RPS, 3, "6.317"},
		{"vLLM RPS", c.VLLMReported.RPS, 3, "6.171"},
		{"ours (a) TTFT p99", c.Ours[0].TTFT.P99, 1, "40.5"},
		{"ours (b) TTFT p99", c.Ours[1].TTFT.P99, 1, "38.3"},
		{"vLLM TTFT p99", c.VLLMReported.TTFT.P99, 1, "67.8"},
		{"burst-free p50", c.Burst.TTFT.P50, 1, "31.8"},
		{"burst-free p95", c.Burst.TTFT.P95, 1, "35.7"},
		{"burst-free p99", c.Burst.TTFT.P99, 1, "37.0"},
		{"overall RPS gap", *c.OverallRPSDeltaPct[0], 1, "2.4"},
		{"steady RPS", c.Steady.RPS, 3, "6.215"},
		{"steady RPS gap", *c.Rows[7].DeltaPct[0], 1, "1.6"},
		{"vLLM mean output", c.VLLMRecomputed.MeanOutputTokens, 2, "129.21"},
		{"ours mean output", c.Ours[0].MeanOutputTokens, 2, "127.47"},
		{"ours (a) uncached", c.Engine[0].UncachedPerRequest, 1, "113.0"},
		{"ours (b) uncached", c.Engine[1].UncachedPerRequest, 1, "113.0"},
		{"vLLM uncached", c.Engine[2].UncachedPerRequest, 1, "106.1"},
	}
	for _, ck := range checks {
		if got := f(ck.got, ck.dig); got != ck.want {
			t.Errorf("%s = %s, wiki says %s", ck.what, got, ck.want)
		}
	}
	if !c.Valid || c.Burst.Excluded != 8 {
		t.Errorf("valid %v, burst %d; want valid with 8 excluded", c.Valid, c.Burst.Excluded)
	}
}

// The round run before `bench run` reset the prefix cache must be flagged.
func TestCommittedContaminatedRoundIsFlagged(t *testing.T) {
	t.Parallel()
	round := filepath.Join("..", "..", "results", "verify", "20260927-interactive-c8-varied-output-cache-contaminated")
	var runs []Run
	for _, d := range []string{"a/interactive-baseline-20260928-010408", "b/interactive-baseline-20260928-010635"} {
		r, err := LoadRun(filepath.Join(round, d))
		if err != nil {
			t.Fatal(err)
		}
		runs = append(runs, r)
	}
	v, err := LoadVLLM(filepath.Join(round, "vllm"), 0)
	if err != nil {
		t.Fatal(err)
	}
	c, err := Compare(runs, &v, DefaultOptions)
	if err != nil {
		t.Fatal(err)
	}
	if c.Valid || !c.Engine[0].Cached || !c.Engine[1].Cached || c.Engine[2].Cached {
		t.Errorf("valid %v, engine %+v; want ours (a) and (b) flagged, vLLM not", c.Valid, c.Engine)
	}
}

func TestLoadFailsOnMissingOrBrokenFiles(t *testing.T) {
	t.Parallel()
	round := filepath.Join("..", "..", "results", "verify", "20260927-interactive-c8-varied-output")
	copyFile := func(t *testing.T, from, to string) {
		t.Helper()
		b, err := os.ReadFile(from)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(to, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run := filepath.Join(round, "a", "interactive-baseline-20260928-012148")
	files := []string{"config.json", "summary.json", "requests.jsonl", "vllm_metrics.csv"}
	for i, broken := range files {
		t.Run("run/"+broken, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			for _, name := range files[:i] {
				copyFile(t, filepath.Join(run, name), filepath.Join(dir, name))
			}
			if _, err := LoadRun(dir); err == nil {
				t.Errorf("LoadRun without %s: want an error", broken)
			}
			if err := os.WriteFile(filepath.Join(dir, broken), []byte("{not json\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadRun(dir); err == nil {
				t.Errorf("LoadRun with a broken %s: want an error", broken)
			}
		})
	}
	vfiles := []string{VLLMBenchFile, "vllm_metrics.csv", VLLMCommandFile}
	for i, broken := range vfiles {
		t.Run("vllm/"+broken, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			for _, name := range vfiles[:i] {
				copyFile(t, filepath.Join(round, "vllm", name), filepath.Join(dir, name))
			}
			if _, err := LoadVLLM(dir, 0); err == nil {
				t.Errorf("LoadVLLM without %s: want an error", broken)
			}
			if err := os.WriteFile(filepath.Join(dir, broken), []byte("{not json\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadVLLM(dir, 0); err == nil {
				t.Errorf("LoadVLLM with a broken %s: want an error", broken)
			}
		})
	}
	// An explicit unique length makes command.txt unnecessary.
	t.Run("vllm/explicit", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		for _, name := range vfiles[:2] {
			copyFile(t, filepath.Join(round, "vllm", name), filepath.Join(dir, name))
		}
		v, err := LoadVLLM(dir, 64)
		if err != nil || v.UniqueTokens != 64 {
			t.Errorf("LoadVLLM(dir, 64) = %d, %v; want 64", v.UniqueTokens, err)
		}
	})
}

func TestLoadRunLabelsARunByItsParentDirectory(t *testing.T) {
	t.Parallel()
	r, err := LoadRun(filepath.Join("..", "..", "results", "verify", "20260927-interactive-c8-varied-output", "b", "interactive-baseline-20260928-012415") + "/")
	if err != nil {
		t.Fatal(err)
	}
	if r.Label != "ours (b)" {
		t.Errorf("label %q, want %q", r.Label, "ours (b)")
	}
}
