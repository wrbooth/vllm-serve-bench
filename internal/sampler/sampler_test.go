package sampler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

// fixture is a real /metrics scrape from the pinned engine (vLLM 0.29.0,
// Qwen2.5-7B-Instruct, baseline flags) after one 8-token request, taken
// 2026-09-27. Its values are what the assertions below are derived from.
func fixture(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/vllm-0.29.0-metrics.txt")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestParseMetricsReadsRecordedFieldsFromRealScrape(t *testing.T) {
	t.Parallel()
	m, err := ParseMetrics(strings.NewReader(string(fixture(t))))
	if err != nil {
		t.Fatal(err)
	}
	// One request with a 30-token prompt (the chat template around "hi")
	// and max_tokens 8: prompt 30, generation 8, one TTFT observation. The
	// cache was queried for all 30 prompt tokens and hit none (first request).
	want := map[string]float64{
		"vllm:num_requests_running":              0,
		"vllm:num_requests_waiting":              0,
		"vllm:kv_cache_usage_perc":               0,
		"vllm:num_preemptions_total":             0,
		"vllm:prefix_cache_queries_total":        30,
		"vllm:prefix_cache_hits_total":           0,
		"vllm:prompt_tokens_total":               30,
		"vllm:generation_tokens_total":           8,
		"vllm:time_to_first_token_seconds_sum":   0.028446197509765625,
		"vllm:time_to_first_token_seconds_count": 1,
	}
	for _, f := range EngineFields {
		got, ok := m.Values[f]
		if !ok || got != want[f] {
			t.Errorf("%s = %v (present %v), want %v", f, got, ok, want[f])
		}
	}
	// request_success_total is split by finished_reason (stop, length,
	// abort, error, repetition): 0+1+0+0+0 = 1.
	if got := m.Values["vllm:request_success_total"]; got != 1 {
		t.Errorf("request_success_total summed over labels = %v, want 1", got)
	}
}

func TestKVCacheTokensComesFromCacheConfigInfo(t *testing.T) {
	t.Parallel()
	m, err := ParseMetrics(strings.NewReader(string(fixture(t))))
	if err != nil {
		t.Fatal(err)
	}
	// Same figure as the startup log's "GPU KV cache size: 241,680 tokens".
	if got, ok := m.KVCacheTokens(); !ok || got != 241680 {
		t.Fatalf("KVCacheTokens = %d, %v; want 241680, true", got, ok)
	}
	if _, ok := (Metrics{}).KVCacheTokens(); ok {
		t.Fatal("KVCacheTokens on an empty scrape reported ok")
	}
}

func TestSplitSampleHandlesQuotedLabelValues(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, line, wantName, wantRest string
		wantLabels                     map[string]string
		wantErr                        bool
	}{
		{name: "no labels", line: "up 1", wantName: "up", wantRest: " 1"},
		{
			name: "comma, brace and escaped quote inside a value",
			line: `m{a="x,y}",b="say \"hi\"\\n"} 2 1700000000`, wantName: "m", wantRest: " 2 1700000000",
			wantLabels: map[string]string{"a": "x,y}", "b": `say "hi"\n`},
		},
		{name: "escaped newline", line: `m{a="1\n2"} 3`, wantName: "m", wantRest: " 3", wantLabels: map[string]string{"a": "1\n2"}},
		{name: "unterminated value", line: `m{a="x} 1`, wantErr: true},
		{name: "label without value", line: `m{a} 1`, wantErr: true},
		{name: "no name", line: `{a="x"} 1`, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			name, labels, rest, err := splitSample(tc.line)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("want error, got name %q labels %v", name, labels)
				}
				return
			}
			if err != nil || name != tc.wantName || rest != tc.wantRest {
				t.Fatalf("got (%q, %q, %v), want (%q, %q)", name, rest, err, tc.wantName, tc.wantRest)
			}
			if tc.wantLabels != nil && !reflect.DeepEqual(labels, tc.wantLabels) {
				t.Fatalf("labels = %v, want %v", labels, tc.wantLabels)
			}
		})
	}
}

func TestParseMetricsRejectsBadValue(t *testing.T) {
	t.Parallel()
	if _, err := ParseMetrics(strings.NewReader("# HELP x\nx{a=\"b\"} notanumber\n")); err == nil {
		t.Fatal("want an error for a non-numeric value")
	}
}

func TestEngineSampleReturnsFieldsInOrderAndErrorsWhenOneIsMissing(t *testing.T) {
	t.Parallel()
	body := fixture(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/metrics":
			_, _ = w.Write(body)
		case "/partial":
			_, _ = w.Write([]byte("vllm:num_requests_running{engine=\"0\"} 3\n"))
		default:
			http.Error(w, "no", http.StatusServiceUnavailable)
		}
	}))
	defer srv.Close()
	ctx := context.Background()

	e := &Engine{URL: srv.URL + "/metrics", Client: srv.Client()}
	got, err := e.Sample(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"0", "0", "0", "0", "30", "0", "30", "8", "0.028446197509765625", "1"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("row = %v, want %v", got, want)
	}
	if kv, _ := e.Last.KVCacheTokens(); kv != 241680 {
		t.Fatalf("Last not kept after a successful scrape: KV %d", kv)
	}

	// A missing metric must not read as zero.
	if _, err := (&Engine{URL: srv.URL + "/partial", Client: srv.Client()}).Sample(ctx); err == nil ||
		!strings.Contains(err.Error(), "vllm:num_requests_waiting missing") {
		t.Fatalf("partial scrape: err = %v, want the first missing metric named", err)
	}
	if _, err := (&Engine{URL: srv.URL + "/down", Client: srv.Client()}).Sample(ctx); err == nil {
		t.Fatal("503 from /metrics: want an error")
	}
}

func TestGPUSampleQueriesFieldsInOrderAndBlanksUnsupported(t *testing.T) {
	t.Parallel()
	var gotArgs []string
	g := &GPU{Run: func(_ context.Context, args ...string) ([]byte, error) {
		gotArgs = args
		return []byte("52, 29748, [N/A], 300, 34\n"), nil
	}}
	got, err := g.Sample(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	wantArgs := []string{
		"--query-gpu=utilization.gpu,memory.used,power.draw,clocks.sm,temperature.gpu",
		"--format=csv,noheader,nounits", "--id=0",
	}
	if !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Fatalf("args = %q, want %q", gotArgs, wantArgs)
	}
	// [N/A] is "not reported", kept empty rather than read as 0 W.
	if want := []string{"52", "29748", "", "300", "34"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("row = %q, want %q", got, want)
	}
}

func TestGPUSampleErrors(t *testing.T) {
	t.Parallel()
	cases := map[string]func(context.Context, ...string) ([]byte, error){
		"command fails":   func(context.Context, ...string) ([]byte, error) { return nil, errors.New("exit 9") },
		"too few fields":  func(context.Context, ...string) ([]byte, error) { return []byte("52, 29748\n"), nil },
		"too many fields": func(context.Context, ...string) ([]byte, error) { return []byte("1,2,3,4,5,6\n"), nil },
	}
	for name, run := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := (&GPU{Run: run}).Sample(context.Background()); err == nil {
				t.Fatal("want an error")
			}
		})
	}
}

// scripted is a Source that returns one canned result per call.
type scripted struct {
	fields []string
	calls  [][]string
	errs   []error
	i      int
}

func (s *scripted) Fields() []string { return s.fields }

func (s *scripted) Sample(context.Context) ([]string, error) {
	i := s.i
	s.i++
	return s.calls[i], s.errs[i]
}

func TestSamplerWritesOneRowPerTickAndKeepsFailedTicks(t *testing.T) {
	t.Parallel()
	src := &scripted{
		fields: []string{"a", "b"},
		calls:  [][]string{{"1", "2"}, nil, {"only-one"}},
		errs:   []error{nil, errors.New("scrape timed out"), nil},
	}
	clock := time.UnixMilli(1_790_000_000_000)
	s := &Sampler{Source: src, Now: func() time.Time { clock = clock.Add(time.Second); return clock }}
	ticks := make(chan time.Time, 3)
	for range 3 {
		ticks <- time.Time{}
	}
	close(ticks)
	var b strings.Builder
	if err := s.Run(context.Background(), ticks, &b); err != nil {
		t.Fatal(err)
	}
	// Three ticks, three rows, one second apart. The failed scrape and the
	// short row are kept with empty values and the reason, not dropped.
	want := "t_unix_ms,a,b,error\n" +
		"1790000001000,1,2,\n" +
		"1790000002000,,,scrape timed out\n" +
		"1790000003000,,,\"sample returned 1 values, want 2\"\n"
	if b.String() != want {
		t.Fatalf("got:\n%s\nwant:\n%s", b.String(), want)
	}
}

func TestSamplerStopsWhenContextIsDone(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s := &Sampler{Source: &scripted{fields: []string{"a"}}, Now: time.Now}
	var b strings.Builder
	// ticks is never written or closed: only ctx can end Run.
	if err := s.Run(ctx, make(chan time.Time), &b); err != nil {
		t.Fatal(err)
	}
	if b.String() != "t_unix_ms,a,error\n" {
		t.Fatalf("got %q, want the header only", b.String())
	}
}
