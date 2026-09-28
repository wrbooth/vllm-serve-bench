package results

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/wrbooth/vllm-serve-bench/internal/loadgen"
	"github.com/wrbooth/vllm-serve-bench/internal/metrics"
)

var t0 = time.Date(2026, 9, 27, 17, 4, 5, 123456789, time.UTC)

func at(msec int) time.Time { return t0.Add(time.Duration(msec) * time.Millisecond) }

func f64(v float64) *float64 { return &v }

func TestNewRowConvertsDurationsToMillisecondsAndNullsWhatIsUndefined(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		rec  metrics.Record
		want Row
	}{
		{
			// 120 ms TTFT, 520 ms E2E, (520-120)/(5-1) = 100 ms TPOT.
			name: "SuccessWithTPOT",
			rec: metrics.NewRecord(3, &metrics.Timing{
				Send: at(0), FirstContent: at(120), Done: at(520), PromptTokens: 433, CompletionTokens: 5, FinishReason: "length",
			}),
			want: Row{
				Concurrency: 8, Worker: 3, TSend: at(0), TTFTms: f64(120), E2Ems: f64(520), TPOTms: f64(100),
				PromptTokens: 433, CompletionTokens: 5, FinishReason: "length",
			},
		},
		{
			// 1.5 ms is kept exactly: float ms, not rounded to integers.
			name: "SingleTokenHasNoTPOT",
			rec: metrics.NewRecord(0, &metrics.Timing{
				Send: at(0), FirstContent: t0.Add(1500 * time.Microsecond), Done: at(2), PromptTokens: 9, CompletionTokens: 1, FinishReason: "stop",
			}),
			want: Row{
				Concurrency: 8, TSend: at(0), TTFTms: f64(1.5), E2Ems: f64(2),
				PromptTokens: 9, CompletionTokens: 1, FinishReason: "stop",
			},
		},
		{
			name: "ErrorRowKeepsSendAndErrorOnly",
			rec:  metrics.NewRecord(1, &metrics.Timing{Send: at(7), Err: errors.New("http 500: boom")}),
			want: Row{Concurrency: 8, Worker: 1, TSend: at(7), Error: "http 500: boom"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := NewRow(8, &tt.rec)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("NewRow = %s\nwant    %s", mustJSON(t, got), mustJSON(t, tt.want))
			}
		})
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// An error row serialises its missing latencies as null, not 0, so a
// reader cannot average a failure in as a zero-latency success.
func TestErrorRowSerialisesLatenciesAsNull(t *testing.T) {
	t.Parallel()
	got := mustJSON(t, Row{Concurrency: 1, TSend: t0, Error: "x"})
	want := `{"concurrency":1,"worker":0,"t_send":"2026-09-27T17:04:05.123456789Z","ttft_ms":null,"e2e_ms":null,"tpot_ms":null,` +
		`"prompt_tokens":0,"completion_tokens":0,"finish_reason":"","error":"x"}`
	if got != want {
		t.Errorf("row JSON\n got %s\nwant %s", got, want)
	}
}

// Three successes and one error over a 2 s window:
//
//	TTFT 100, 200, 300 ms -> nearest-rank p50 = rank ceil(1.5) = 2 -> 200;
//	p95, p99 = rank 3 -> 300.
//	E2E 500, 600, 900 ms -> p50 600, p95/p99 900.
//	TPOT (E2E-TTFT)/(4-1): 400/3, 400/3, 600/3 ms -> p50 133.333333 ms, p99 200 ms
//	(integer nanoseconds: 133333333 ns).
//	RPS = 3 / 2 = 1.5; tok/s = 12 / 2 = 6; error rate 1/4 = 0.25.
func TestNewLevelCarriesTheWindowAndSummaryInMilliseconds(t *testing.T) {
	t.Parallel()
	var recs []metrics.Record
	for _, v := range [][2]int{{100, 500}, {200, 600}, {300, 900}} {
		recs = append(recs, metrics.NewRecord(0, &metrics.Timing{
			Send: at(0), FirstContent: at(v[0]), Done: at(v[1]), CompletionTokens: 4,
		}))
	}
	recs = append(recs, metrics.NewRecord(1, &metrics.Timing{Send: at(0), Err: errors.New("x")}))
	res := loadgen.Result{
		Start: at(0), MeasureStart: at(1000), End: at(3000), WarmupRequests: 7, Records: recs,
		Summary: metrics.Summarize(recs, 2*time.Second),
	}
	got := NewLevel(4, &res)
	want := Level{
		Concurrency: 4, Start: at(0), MeasureStart: at(1000), End: at(3000), WarmupRequests: 7,
		Requests: 4, Errors: 1, ErrorRate: 0.25, WindowS: 2, RPS: 1.5, OutputTokPerSec: 6,
		TTFT: Dist{N: 3, P50ms: 200, P95ms: 300, P99ms: 300},
		E2E:  Dist{N: 3, P50ms: 600, P95ms: 900, P99ms: 900},
		TPOT: Dist{N: 3, P50ms: 133.333333, P95ms: 200, P99ms: 200},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("NewLevel =\n %s\nwant\n %s", mustJSON(t, got), mustJSON(t, want))
	}
}

func TestCheckPromptTokensCountsMismatchesAmongSuccessesOnly(t *testing.T) {
	t.Parallel()
	rows := []Row{
		{PromptTokens: 433},
		{PromptTokens: 433},
		{PromptTokens: 434},
		{PromptTokens: 434},
		{PromptTokens: 400},
		{PromptTokens: 0, Error: "http 500"}, // error rows carry no usage and are not checked
	}
	got := CheckPromptTokens(rows, 433)
	want := PromptTokenCheck{Predicted: 433, Checked: 5, Mismatches: 3, Observed: map[int]int{434: 2, 400: 1}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("CheckPromptTokens = %+v, want %+v", got, want)
	}
	clean := CheckPromptTokens(rows[:2], 433)
	if clean.Mismatches != 0 || clean.Observed != nil || clean.Checked != 2 {
		t.Errorf("clean check = %+v, want 2 checked, no mismatches, no observed map", clean)
	}
}

func TestRunIDIsProfileConfigAndUTCTime(t *testing.T) {
	t.Parallel()
	pacific := time.FixedZone("PDT", -7*3600)
	tests := []struct {
		profile, config string
		want            string
		wantErr         bool
	}{
		// 17:04:05 UTC regardless of the zone the time is expressed in.
		{profile: "interactive", config: "baseline", want: "interactive-baseline-20260927-170405"},
		{profile: "throughput", config: "fp8-online", want: "throughput-fp8-online-20260927-170405"},
		{profile: "interactive", config: "../escape", wantErr: true},
		{profile: "interactive", config: "", wantErr: true},
		{profile: "Interactive", config: "baseline", wantErr: true},
	}
	for _, tt := range tests {
		got, err := RunID(tt.profile, tt.config, t0.In(pacific))
		if (err != nil) != tt.wantErr || got != tt.want {
			t.Errorf("RunID(%q, %q) = %q, %v; want %q, error %v", tt.profile, tt.config, got, err, tt.want, tt.wantErr)
		}
	}
}

func TestCreateRefusesAnExistingRunDirectory(t *testing.T) {
	t.Parallel()
	out := filepath.Join(t.TempDir(), "results") // parent is created on demand
	if _, err := Create(out, "run"); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(out, "run"); err == nil {
		t.Error("Create reused an existing run directory; raw results must never be overwritten")
	}
}

func TestDirAppendsRowsAndReplacesJSONFiles(t *testing.T) {
	t.Parallel()
	d, err := Create(t.TempDir(), "run")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []int{1, 2} {
		if err := d.AppendRows([]Row{{Concurrency: c, TSend: t0}, {Concurrency: c, TSend: t0}}); err != nil {
			t.Fatal(err)
		}
	}
	f, err := os.Open(filepath.Join(d.Path, RequestsFile))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var levels []int
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var r Row
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			t.Fatalf("line %q: %v", sc.Text(), err)
		}
		levels = append(levels, r.Concurrency)
	}
	if !reflect.DeepEqual(levels, []int{1, 1, 2, 2}) {
		t.Errorf("requests.jsonl concurrency column = %v, want [1 1 2 2]", levels)
	}

	for _, id := range []string{"first", "second"} {
		if err := d.WriteSummary(&Summary{RunID: id}); err != nil {
			t.Fatal(err)
		}
		if err := d.WriteConfig(&Config{RunID: id}); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{SummaryFile, ConfigFile} {
		b, err := os.ReadFile(filepath.Join(d.Path, name))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), `"run_id": "second"`) || strings.Contains(string(b), "first") {
			t.Errorf("%s = %s, want only the second write", name, b)
		}
	}
	entries, _ := os.ReadDir(d.Path)
	if len(entries) != 3 {
		t.Errorf("run directory holds %d entries, want exactly the 3 files (no temporaries)", len(entries))
	}
}

func TestDirWritesFailInAMissingDirectory(t *testing.T) {
	t.Parallel()
	d := &Dir{Path: filepath.Join(t.TempDir(), "gone")}
	if err := d.AppendRows([]Row{{}}); err == nil {
		t.Error("AppendRows succeeded in a missing directory")
	}
	if err := d.WriteConfig(&Config{}); err == nil {
		t.Error("WriteConfig succeeded in a missing directory")
	}
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(filepath.Join(file, "results"), "run"); err == nil {
		t.Error("Create succeeded under a regular file")
	}
}
