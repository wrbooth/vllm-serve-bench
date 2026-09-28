package report

import (
	"strings"
	"testing"
	"time"

	"github.com/wrbooth/vllm-serve-bench/internal/crosscheck"
	"github.com/wrbooth/vllm-serve-bench/internal/results"
)

// windowRun has one level measured over [t0 + 2.5 s, t0 + 10.5 s] and 1 Hz
// samples at t0 + 0 … 12 s. Inside the window are the samples at 3 … 10 s
// (8 of them). Outside it (warmup, drain) every gauge reads 1000, so a
// leak of any of them into a mean or max shows.
//
//	running  = i inside          → mean (3+…+10)/8 = 52/8 = 6.5, max 10
//	waiting  = 2i inside         → mean 13, max 20
//	KV       = i/100 inside      → mean 0.065, max 0.10
//	power    = 600 inside        → mean 600, max 600
//	preempt  = 10i (a counter)   → covering samples 2 (≤ 2.5 s) and 11 (≥ 10.5 s): 110 − 20 = 90
//	counters = uncached 113·i, count i → (113·11 − 113·2) / (11 − 2) = 113 per request
func windowRun(hitsPerSec float64) (Run, results.Level) {
	l := results.Level{Concurrency: 8, MeasureStart: t0.Add(2500 * time.Millisecond), End: t0.Add(10500 * time.Millisecond)}
	r := Run{Config: results.Config{Profile: results.ProfileConfig{UniqueWords: 100}}}
	for i := range 13 {
		at, v := t0.Add(time.Duration(i)*time.Second), float64(i)
		in := i >= 3 && i <= 10
		gauge := func(x float64) float64 {
			if in {
				return x
			}
			return 1000
		}
		r.Engine = append(r.Engine, Point{At: at, V: []float64{gauge(v), gauge(2 * v), gauge(v / 100), 10 * v}})
		r.GPU = append(r.GPU, Point{At: at, V: []float64{gauge(600)}})
		r.Counters = append(r.Counters, crosscheck.Sample{At: at, PrefixQueries: 1000 * v, PrefixHits: hitsPerSec * v, TTFTSum: 0.05 * v, TTFTCount: v})
	}
	return r, l
}

func TestEngineAtRestrictsGaugesToTheMeasuredWindow(t *testing.T) {
	t.Parallel()
	r, l := windowRun(887)
	e := EngineAt(&r, &l, 0.9)
	want := struct {
		samples, gpu       int
		running, waiting   Gauge
		power              Gauge
		preempt, uncached  float64
		cached, hasPreempt bool
	}{8, 8, Gauge{6.5, 10}, Gauge{13, 20}, Gauge{600, 600}, 90, 113, false, true}
	if e.Samples != want.samples || e.GPUCount != want.gpu || e.Running != want.running || e.Waiting != want.waiting || e.PowerW != want.power {
		t.Errorf("gauges: samples %d/%d running %+v waiting %+v power %+v; want %+v", e.Samples, e.GPUCount, e.Running, e.Waiting, e.PowerW, want)
	}
	if d := e.KV.Mean - 0.065; d > 1e-12 || d < -1e-12 || e.KV.Max != 0.10 {
		t.Errorf("KV = %+v, want mean 0.065 max 0.10", e.KV)
	}
	if e.Preemptions != want.preempt || !e.HasPreemptions {
		t.Errorf("preemptions = %v (%v), want %v", e.Preemptions, e.HasPreemptions, want.preempt)
	}
	if e.Check.UncachedPerRequest != want.uncached || e.Check.Cached || !e.Check.OK {
		t.Errorf("check = %+v, want %v uncached per request, not cached", e.Check, want.uncached)
	}
}

// hits 999·i: uncached (1000 − 999)·9 / 9 = 1 per request, under
// 0.9 × 100 = 90: the level was served from the cache.
func TestEngineAtFlagsALevelServedFromTheCache(t *testing.T) {
	t.Parallel()
	r, l := windowRun(999)
	e := EngineAt(&r, &l, 0.9)
	if !e.Check.Cached || e.Check.UncachedPerRequest != 1 {
		t.Errorf("check = %+v, want cached at 1 token per request", e.Check)
	}
	lr := LevelRow{Meets: true, Engine: e}
	if lr.Valid() || lr.Compliant() {
		t.Error("a cached level must be neither valid nor compliant")
	}
}

func TestEngineAtWithoutTelemetryMarksTheLevelUnverified(t *testing.T) {
	t.Parallel()
	_, l := windowRun(887)
	r := Run{Config: results.Config{Profile: results.ProfileConfig{UniqueWords: 100}}}
	e := EngineAt(&r, &l, 0.9)
	if !e.Check.Cached || e.Check.OK || e.HasPreemptions || e.Samples != 0 || e.Running != (Gauge{}) {
		t.Errorf("no telemetry: %+v; want unverified, no preemptions, zero gauges", e)
	}
	if got := checkCell(&e); got != "**UNVERIFIED**" {
		t.Errorf("checkCell = %q", got)
	}
}

func TestWithinIncludesBothEdges(t *testing.T) {
	t.Parallel()
	pts := []Point{{At: t0}, {At: t0.Add(time.Second)}, {At: t0.Add(2 * time.Second)}, {At: t0.Add(3 * time.Second)}}
	got := Within(pts, t0.Add(time.Second), t0.Add(2*time.Second))
	if len(got) != 2 || !got[0].At.Equal(pts[1].At) || !got[1].At.Equal(pts[2].At) {
		t.Errorf("Within = %v, want the samples at 1 s and 2 s", got)
	}
	if got := GaugeOf(nil, 0); got != (Gauge{}) {
		t.Errorf("GaugeOf(no points) = %+v, want zero", got)
	}
	// One point: mean = max = its value, even when negative.
	if got := GaugeOf([]Point{{V: []float64{-3}}}, 0); got != (Gauge{-3, -3}) {
		t.Errorf("GaugeOf(-3) = %+v", got)
	}
}

func TestParsePointsSkipsFailedScrapesAndRejectsBadFiles(t *testing.T) {
	t.Parallel()
	csv := "t_unix_ms,power.draw,other,error\n" +
		"1000,500.5,x,\n" +
		"2000,,,scrape failed\n" +
		"3000,600,y,\n"
	pts, err := ParsePoints(strings.NewReader(csv), GPUColumns)
	if err != nil {
		t.Fatal(err)
	}
	if len(pts) != 2 || pts[0].V[0] != 500.5 || pts[1].V[0] != 600 || !pts[1].At.Equal(time.UnixMilli(3000)) {
		t.Errorf("ParsePoints = %+v", pts)
	}
	bad := []struct{ name, csv, want string }{
		{"Empty", "", "header"},
		{"NoColumn", "t_unix_ms,error\n", "no power.draw column"},
		{"NoError", "t_unix_ms,power.draw\n", "no error column"},
		{"BadTime", "t_unix_ms,power.draw,error\nx,1,\n", "t_unix_ms"},
		{"BadValue", "t_unix_ms,power.draw,error\n1,watts,\n", "power.draw"},
		{"Ragged", "t_unix_ms,power.draw,error\n1,2\n", "wrong number of fields"},
	}
	for _, tt := range bad {
		if _, err := ParsePoints(strings.NewReader(tt.csv), GPUColumns); err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: error %v, want one containing %q", tt.name, err, tt.want)
		}
	}
}
