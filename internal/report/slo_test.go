package report

import (
	"math"
	"os"
	"strings"
	"testing"

	"github.com/wrbooth/vllm-serve-bench/internal/results"
)

func TestParseSLOAcceptsTheCommittedFile(t *testing.T) {
	t.Parallel()
	s, err := LoadSLO("../../docs/slo.json")
	if err != nil {
		t.Fatal(err)
	}
	in, err := s.For("interactive")
	if err != nil {
		t.Fatal(err)
	}
	// The committed values (wiki/log.md, the two SLO decisions).
	want := []Bound{{MetricTTFT, 95, 100}, {MetricTPOT, 95, 25}}
	if len(in.Bounds) != 2 || in.Bounds[0] != want[0] || in.Bounds[1] != want[1] {
		t.Errorf("interactive bounds = %+v, want %+v", in.Bounds, want)
	}
	tp, err := s.For("throughput")
	if err != nil || len(tp.Bounds) != 1 || tp.Bounds[0] != (Bound{MetricE2E, 95, 15000}) {
		t.Errorf("throughput bounds = %+v (%v), want E2E p95 ≤ 15000 ms", tp.Bounds, err)
	}
	if _, err := s.For("nope"); err == nil {
		t.Error("For(unknown profile): want an error")
	}
}

func TestParseSLORejectsWhatItCannotJudge(t *testing.T) {
	t.Parallel()
	tests := []struct{ name, json, want string }{
		{"Empty", `{}`, "no profiles"},
		{"NoBounds", `{"profiles":{"p":{"bounds":[]}}}`, "no bounds"},
		{"UnknownMetric", `{"profiles":{"p":{"bounds":[{"metric":"itl","percentile":95,"max_ms":1}]}}}`, "unknown metric"},
		{"Percentile", `{"profiles":{"p":{"bounds":[{"metric":"ttft","percentile":90,"max_ms":1}]}}}`, "percentile 90"},
		{"ZeroLimit", `{"profiles":{"p":{"bounds":[{"metric":"ttft","percentile":95,"max_ms":0}]}}}`, "must be positive"},
		{"UnknownField", `{"profiles":{},"extra":1}`, "unknown field"},
		{"NotJSON", `{`, "slo:"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := ParseSLO(strings.NewReader(tt.json))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("ParseSLO(%s) error = %v, want one containing %q", tt.json, err, tt.want)
			}
		})
	}
	if _, err := LoadSLO("testdata/absent.json"); !os.IsNotExist(err) {
		t.Errorf("LoadSLO(absent) = %v, want not-exist", err)
	}
}

// A bound is "p ≤ limit": equal passes, anything over fails, and the
// margin is (value − limit) / limit × 100.
func TestBoundJudgePassesAtTheLimitAndFailsJustOverIt(t *testing.T) {
	t.Parallel()
	b := Bound{Metric: MetricTPOT, Percentile: 95, MaxMs: 25}
	tests := []struct {
		name   string
		p95    float64
		pass   bool
		margin float64
		cell   string
	}{
		// 25 = 25: passes, margin 0.
		{"Equal", 25, true, 0, "pass 25.0 ms (+0.0%)"},
		// 25.0001 > 25: fails; (0.0001 / 25) × 100 = 0.0004%.
		{"JustOver", 25.0001, false, 0.0004, "**FAIL** 25.0 ms (+0.0%)"},
		// (26.3 − 25) / 25 = 1.3 / 25 = 0.052 → +5.2%.
		{"Over", 26.3, false, 5.2, "**FAIL** 26.3 ms (+5.2%)"},
		// (15.5 − 25) / 25 = −9.5 / 25 = −0.38 → −38.0%.
		{"Under", 15.5, true, -38, "pass 15.5 ms (-38.0%)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			l := results.Level{TPOT: results.Dist{N: 10, P50ms: 1, P95ms: tt.p95, P99ms: 99}}
			r := b.Judge(&l)
			if r.Pass != tt.pass || r.Value != tt.p95 || !r.HasData || math.Abs(r.MarginPct-tt.margin) > 1e-9 {
				t.Errorf("Judge(p95 %v) = %+v, want pass %v margin %v", tt.p95, r, tt.pass, tt.margin)
			}
			if got := r.Cell(); got != tt.cell {
				t.Errorf("Cell = %q, want %q", got, tt.cell)
			}
		})
	}
}

func TestBoundJudgeReadsTheNamedMetricAndPercentile(t *testing.T) {
	t.Parallel()
	l := results.Level{
		TTFT: results.Dist{N: 1, P50ms: 1, P95ms: 2, P99ms: 3},
		TPOT: results.Dist{N: 1, P50ms: 4, P95ms: 5, P99ms: 6},
		E2E:  results.Dist{N: 1, P50ms: 7, P95ms: 8, P99ms: 9},
	}
	tests := []struct {
		metric string
		p      int
		want   float64
	}{
		{MetricTTFT, 50, 1},
		{MetricTTFT, 95, 2},
		{MetricTTFT, 99, 3},
		{MetricTPOT, 50, 4},
		{MetricTPOT, 95, 5},
		{MetricTPOT, 99, 6},
		{MetricE2E, 50, 7},
		{MetricE2E, 95, 8},
		{MetricE2E, 99, 9},
	}
	for _, tt := range tests {
		b := Bound{Metric: tt.metric, Percentile: tt.p, MaxMs: 100}
		if got := b.Judge(&l).Value; got != tt.want {
			t.Errorf("%s p%d = %v, want %v", tt.metric, tt.p, got, tt.want)
		}
	}
}

func TestBoundJudgeFailsALevelWithNoSamples(t *testing.T) {
	t.Parallel()
	b := Bound{Metric: MetricTTFT, Percentile: 95, MaxMs: 100}
	r := b.Judge(&results.Level{}) // every request failed: N = 0, P95 = 0
	if r.Pass || r.HasData || r.Cell() != "**FAIL** no data" {
		t.Errorf("Judge(no samples) = %+v, cell %q; want a failure with no data", r, r.Cell())
	}
}

func TestRequestMeetsJudgesEachRequestOnItsOwnValues(t *testing.T) {
	t.Parallel()
	p := interactiveSLO // TTFT ≤ 100, TPOT ≤ 25
	tests := []struct {
		name string
		row  results.Row
		want bool
	}{
		{"BothAtTheLimit", results.Row{TTFTms: fp(100), TPOTms: fp(25), E2Ems: fp(1)}, true},
		{"TTFTOver", results.Row{TTFTms: fp(100.001), TPOTms: fp(10), E2Ems: fp(1)}, false},
		{"TPOTOver", results.Row{TTFTms: fp(50), TPOTms: fp(25.001), E2Ems: fp(1)}, false},
		// completion_tokens < 2: no TPOT, so only TTFT is judged.
		{"NoTPOT", results.Row{TTFTms: fp(50), E2Ems: fp(1), CompletionTokens: 1}, true},
		{"ErrorRow", results.Row{Error: "HTTP 500"}, false},
		// A successful row missing a value it must have cannot be shown to meet it.
		{"NoTTFT", results.Row{TPOTms: fp(10), E2Ems: fp(1)}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := p.RequestMeets(&tt.row); got != tt.want {
				t.Errorf("RequestMeets = %v, want %v", got, tt.want)
			}
		})
	}
	e2e := testSLO.Profiles["throughput"] // E2E ≤ 15000
	if !e2e.RequestMeets(&results.Row{TTFTms: fp(1e6), E2Ems: fp(15000)}) || e2e.RequestMeets(&results.Row{TTFTms: fp(1), E2Ems: fp(15000.5)}) {
		t.Error("E2E bound: want 15000 ms to pass and 15000.5 ms to fail, TTFT ignored")
	}
}
