package chart

import (
	"math"
)

// linear maps [0, max] onto a pixel range that runs upward: y0 is the
// baseline pixel, y1 the top.
type linear struct {
	max    float64
	y0, y1 float64
}

func (s linear) at(v float64) float64 { return s.y0 - v/s.max*(s.y0-s.y1) }

// log2Axis maps concurrency onto x with a log₂ scale, padded by half a
// doubling on each side so the end markers do not sit on the frame.
type log2Axis struct {
	lo, hi float64 // log₂ of the padded domain
	x0, x1 float64
}

func newLog2Axis(minX, maxX, x0, x1 float64) log2Axis {
	lo, hi := math.Log2(minX), math.Log2(maxX)
	if hi == lo {
		lo, hi = lo-1, hi+1
	}
	return log2Axis{lo: lo - 0.3, hi: hi + 0.3, x0: x0, x1: x1}
}

func (a log2Axis) at(v float64) float64 {
	return a.x0 + (math.Log2(v)-a.lo)/(a.hi-a.lo)*(a.x1-a.x0)
}

// niceTicks returns evenly spaced ticks from 0 that cover top, about four of
// them, stepped 1, 2, 2.5 or 5 × a power of ten. The last tick is the axis
// maximum. top ≤ 0 gives a unit axis.
func niceTicks(top float64) (ticks []float64, step float64) {
	if top <= 0 || math.IsNaN(top) || math.IsInf(top, 0) {
		return []float64{0, 1}, 1
	}
	raw := top / 4
	mag := math.Pow(10, math.Floor(math.Log10(raw)))
	step = 10 * mag
	for _, m := range []float64{1, 2, 2.5, 5} {
		if m*mag >= raw {
			step = m * mag
			break
		}
	}
	n := int(math.Ceil(top/step - 1e-9))
	for i := 0; i <= n; i++ {
		ticks = append(ticks, float64(i)*step)
	}
	return ticks, step
}

// tickLabel formats a tick with as many decimals as its step needs: none
// for a whole step, one for 2.5 or 0.5, two below that.
func tickLabel(v, step float64) string {
	for d := range 2 {
		scaled := step * math.Pow(10, float64(d))
		if math.Abs(scaled-math.Round(scaled)) < 1e-9 {
			return Commas(v, d)
		}
	}
	return Commas(v, 2)
}
