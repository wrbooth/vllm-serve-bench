package report

import (
	"time"

	"github.com/wrbooth/vllm-serve-bench/internal/crosscheck"
	"github.com/wrbooth/vllm-serve-bench/internal/results"
)

// Point is one successful row of a telemetry CSV: its time and the
// columns asked for, in the order asked for.
type Point struct {
	At time.Time
	V  []float64
}

// Columns read from the telemetry, in Point.V order.
var (
	EngineColumns = []string{
		"vllm:num_requests_running",
		"vllm:num_requests_waiting",
		"vllm:kv_cache_usage_perc", // a fraction, 0–1, despite the name
		"vllm:num_preemptions_total",
	}
	GPUColumns = []string{"power.draw"}
)

const (
	colRunning = iota
	colWaiting
	colKV
	colPreemptions
)

// Gauge is the mean and max of a gauge over the samples in a window.
type Gauge struct {
	Mean, Max float64
}

// EngineLevel is what the engine and the GPU did during one level's
// measured window [measure_start, end].
//
// Counters are deltas between the samples covering the window, as in the
// cross-check (crosscheck.Covering): the last at or before it opens and the
// first at or after it closes. Gauges are over the samples inside the
// window only, so the warmup and the gap before the next level do not
// dilute them.
type EngineLevel struct {
	// Check is the cross-check's uncached-prompt guard over this window.
	Check crosscheck.Engine
	// Preemptions is Δ num_preemptions_total; HasPreemptions is false when
	// the telemetry does not cover the window.
	Preemptions    float64
	HasPreemptions bool
	// Samples is the number of engine samples inside the window; the
	// gauges are zero when it is 0.
	Samples  int
	Running  Gauge
	Waiting  Gauge
	KV       Gauge // fraction, 0–1
	GPUCount int   // GPU samples inside the window
	PowerW   Gauge
}

// EngineAt computes a level's engine-side figures.
func EngineAt(r *Run, l *results.Level, minUncachedFraction float64) EngineLevel {
	start, end := l.MeasureStart, l.End
	from, to, ok := crosscheck.Covering(r.Counters, start, end)
	e := EngineLevel{
		Check: crosscheck.CheckEngine("ours", "measured window", from, to, ok && !start.IsZero(), r.Config.Profile.UniqueWords, minUncachedFraction),
	}
	if fi, ti, ok := crosscheck.CoveringIndex(len(r.Engine), func(i int) time.Time { return r.Engine[i].At }, start, end); ok {
		e.Preemptions = r.Engine[ti].V[colPreemptions] - r.Engine[fi].V[colPreemptions]
		e.HasPreemptions = true
	}
	in := Within(r.Engine, start, end)
	e.Samples = len(in)
	e.Running = GaugeOf(in, colRunning)
	e.Waiting = GaugeOf(in, colWaiting)
	e.KV = GaugeOf(in, colKV)
	gpu := Within(r.GPU, start, end)
	e.GPUCount = len(gpu)
	e.PowerW = GaugeOf(gpu, 0)
	return e
}

// Within returns the points at or after start and at or before end.
func Within(points []Point, start, end time.Time) []Point {
	var out []Point
	for i := range points {
		if at := points[i].At; !at.Before(start) && !at.After(end) {
			out = append(out, points[i])
		}
	}
	return out
}

// GaugeOf is the mean and max of column col; zero for no points.
func GaugeOf(points []Point, col int) Gauge {
	if len(points) == 0 {
		return Gauge{}
	}
	g := Gauge{Max: points[0].V[col]}
	sum := 0.0
	for i := range points {
		v := points[i].V[col]
		sum += v
		g.Max = max(g.Max, v)
	}
	g.Mean = sum / float64(len(points))
	return g
}
