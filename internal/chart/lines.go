package chart

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
)

// Point is one level of one series.
type Point struct {
	X, Y float64
	// Filled draws a filled marker (the level meets its SLO); otherwise the
	// marker is hollow.
	Filled bool
	Tip    string // native tooltip
}

// Series is one run's line in a panel. Slot picks its color (see Slots).
type Series struct {
	Name   string
	Slot   int
	Points []Point
}

// Limit is a horizontal SLO line with its label.
type Limit struct {
	Value float64
	Label string
}

// Callout labels one point with a value, in ink, not the series color.
type Callout struct {
	X, Y float64
	Text string
}

// Panel is one y-axis over the shared x-axis.
type Panel struct {
	Title string
	// Max fixes the y-axis maximum (a percentage panel stops at 100); 0
	// sizes the axis to the data.
	Max      float64
	Series   []Series
	Limit    *Limit
	Callouts []Callout
}

// Lines is a stack of panels sharing a log₂ x-axis: one y-axis per panel,
// never two in one.
type Lines struct {
	Title    string
	Subtitle []string
	Source   string // recorded in <metadata>: how to regenerate the file
	XTitle   string
	XTicks   []float64
	Key      bool // add the filled/hollow marker key to the legend
	Panels   []Panel
	Notes    []string
}

const (
	width      = 760.0
	plotLeft   = 76.0
	plotRight  = width - 36.0
	plotHeight = 132.0
	charW      = 6.6 // mean glyph advance of 12px system-ui, for layout only
	lineH      = 17.0
)

// SVG renders the chart.
func (c *Lines) SVG() ([]byte, error) {
	if err := c.validate(); err != nil {
		return nil, err
	}
	legend := c.legendItems()
	top := 30 + lineH*float64(len(c.Subtitle)) + 14
	legendRows := layoutLegend(legend, c.Key)
	panelsTop := top + lineH*float64(len(legendRows)) + 18
	panelSpan := 22 + plotHeight + 26
	bottom := panelsTop + panelSpan*float64(len(c.Panels)) - 26
	h := bottom + 44 + lineH*float64(len(c.Notes)) + 8

	d := newDoc(width, h, c.Title, strings.Join(c.Subtitle, " "), c.Source)
	d.header(c.Title, c.Subtitle)
	d.legend(top, legendRows)

	xs := newLog2Axis(slices.Min(c.XTicks), slices.Max(c.XTicks), plotLeft, plotRight)
	for i := range c.Panels {
		y := panelsTop + panelSpan*float64(i)
		d.panel(&c.Panels[i], xs, c.XTicks, y+22, y+22+plotHeight, y)
	}
	for _, t := range c.XTicks {
		d.text(xs.at(t), bottom+16, "tick", "middle", Commas(t, 0))
	}
	d.text((plotLeft+plotRight)/2, bottom+34, "", "middle", c.XTitle)
	for i, n := range c.Notes {
		d.text(plotLeft, bottom+58+lineH*float64(i), "", "", n)
	}
	return d.bytes(), nil
}

func (c *Lines) validate() error {
	if len(c.Panels) == 0 {
		return errors.New("chart: no panels")
	}
	if len(c.XTicks) == 0 {
		return errors.New("chart: no x ticks")
	}
	for _, t := range c.XTicks {
		if t <= 0 {
			return fmt.Errorf("chart: x tick %g is not positive (log scale)", t)
		}
	}
	for i := range c.Panels {
		for j := range c.Panels[i].Series {
			s := &c.Panels[i].Series[j]
			if err := checkSlot(s.Name, s.Slot); err != nil {
				return err
			}
			for _, p := range s.Points {
				if p.X <= 0 || math.IsNaN(p.Y) || math.IsInf(p.Y, 0) {
					return fmt.Errorf("series %q: point (%g, %g) cannot be drawn", s.Name, p.X, p.Y)
				}
			}
		}
	}
	return nil
}

// legendItems lists every series once, in first-appearance order.
func (c *Lines) legendItems() []Series {
	var out []Series
	for i := range c.Panels {
		for _, s := range c.Panels[i].Series {
			if !slices.ContainsFunc(out, func(o Series) bool { return o.Name == s.Name }) {
				out = append(out, Series{Name: s.Name, Slot: s.Slot})
			}
		}
	}
	return out
}

// legendEntry is one legend item placed on a row.
type legendEntry struct {
	x      float64
	name   string
	slot   int
	marker int // 0 series key, 1 filled key, 2 hollow key
}

const (
	keySeries = iota
	keyFilled
	keyHollow
)

// layoutLegend flows the series keys, then the marker key, into rows that
// fit the plot width.
func layoutLegend(items []Series, markerKey bool) [][]legendEntry {
	var entries []legendEntry
	for _, s := range items {
		entries = append(entries, legendEntry{name: s.Name, slot: s.Slot, marker: keySeries})
	}
	if markerKey {
		entries = append(entries,
			legendEntry{name: "meets SLO", marker: keyFilled},
			legendEntry{name: "misses SLO", marker: keyHollow})
	}
	var rows [][]legendEntry
	x := plotLeft - 60
	row := make([]legendEntry, 0, len(entries))
	for _, e := range entries {
		w := 30 + charW*float64(len([]rune(e.name))) + 18
		if e.marker != keySeries {
			w = 14 + charW*float64(len([]rune(e.name))) + 18
		}
		if len(row) > 0 && x+w > plotRight {
			rows, row, x = append(rows, row), make([]legendEntry, 0, len(entries)), plotLeft-60
		}
		e.x = x
		row = append(row, e)
		x += w
	}
	return append(rows, row)
}

func (d *doc) header(title string, subtitle []string) {
	d.text(plotLeft-60, 30, "t", "", title)
	for i, s := range subtitle {
		d.text(plotLeft-60, 30+lineH*float64(i+1), "st", "", s)
	}
}

func (d *doc) legend(top float64, rows [][]legendEntry) {
	for r, row := range rows {
		y := top + lineH*float64(r) + 4
		for _, e := range row {
			switch e.marker {
			case keySeries:
				d.printf(`<path d="M%s %sH%s" class="ln l%d"/>`+"\n", num(e.x), num(y), num(e.x+22), e.slot)
				d.marker(e.x+11, y, e.slot, true, e.name)
				d.text(e.x+30, y+4, "", "", e.name)
			default:
				d.marker(e.x+4, y, 0, e.marker == keyFilled, e.name)
				d.text(e.x+14, y+4, "", "", e.name)
			}
		}
	}
}

// panel draws one panel: title, grid, SLO line, series, callouts. y0 is the
// baseline pixel, y1 the top of the plot, ty the title's baseline.
func (d *doc) panel(p *Panel, xs log2Axis, xticks []float64, y1, y0, ty float64) {
	d.text(plotLeft-60, ty+12, "pt", "", p.Title)
	top := 0.0
	for _, s := range p.Series {
		for _, pt := range s.Points {
			top = math.Max(top, pt.Y)
		}
	}
	if p.Limit != nil {
		top = math.Max(top, p.Limit.Value)
	}
	ticks, step := niceTicks(top * 1.08)
	if p.Max > 0 {
		ticks, step = niceTicks(p.Max)
	}
	ys := linear{max: ticks[len(ticks)-1], y0: y0, y1: y1}
	for _, t := range xticks {
		d.line(xs.at(t), y1, xs.at(t), y0, "grid")
	}
	for _, t := range ticks[1:] {
		d.line(plotLeft, ys.at(t), plotRight, ys.at(t), "grid")
	}
	for _, t := range ticks {
		d.text(plotLeft-8, ys.at(t)+4, "tick", "end", tickLabel(t, step))
	}
	d.line(plotLeft, y0, plotRight, y0, "axis")
	if p.Limit != nil {
		ly := ys.at(p.Limit.Value)
		d.line(plotLeft, ly, plotRight, ly, "lim")
		d.text(plotLeft+6, ly-5, "v halo", "", p.Limit.Label)
	}
	for _, s := range p.Series {
		d.series(&s, xs, ys, xticks)
	}
	d.callouts(p.Callouts, xs, ys)
}

// series draws a line through the points and their markers. The line
// breaks where the series skips a level the chart has (an experiment run
// only around the knee), so no segment implies a level that was not run.
func (d *doc) series(s *Series, xs log2Axis, ys linear, xticks []float64) {
	pts := slices.Clone(s.Points)
	slices.SortFunc(pts, func(a, b Point) int { return cmpFloat(a.X, b.X) })
	var path strings.Builder
	for i, p := range pts {
		cmd := "L"
		if i == 0 || skipsTick(pts[i-1].X, p.X, xticks) {
			cmd = "M"
		}
		fmt.Fprintf(&path, "%s%s %s", cmd, num(xs.at(p.X)), num(ys.at(p.Y)))
	}
	if strings.Contains(path.String(), "L") {
		d.printf(`<path d="%s" class="ln l%d"/>`+"\n", path.String(), s.Slot)
	}
	for _, p := range pts {
		d.marker(xs.at(p.X), ys.at(p.Y), s.Slot, p.Filled, p.Tip)
	}
}

// callouts writes value labels above their points, or below when that would
// collide with a label already placed.
func (d *doc) callouts(cs []Callout, xs log2Axis, ys linear) {
	type box struct{ x0, x1, y0, y1 float64 }
	placed := make([]box, 0, len(cs))
	for _, c := range cs {
		x, y := xs.at(c.X), ys.at(c.Y)
		w := charW * float64(len([]rune(c.Text)))
		anchor, bx := "middle", x-w/2
		if x+w/2 > plotRight {
			anchor, bx = "end", x-w
		}
		b := box{bx, bx + w, y - 24, y - 10}
		if slices.ContainsFunc(placed, func(o box) bool {
			return b.x0 < o.x1 && o.x0 < b.x1 && b.y0 < o.y1 && o.y0 < b.y1
		}) {
			b.y0, b.y1 = y+10, y+24
		}
		placed = append(placed, b)
		d.text(x, b.y1-2, "v halo", anchor, c.Text)
	}
}

// skipsTick reports whether a tick lies strictly between a and b.
func skipsTick(a, b float64, ticks []float64) bool {
	return slices.ContainsFunc(ticks, func(t float64) bool { return t > a && t < b })
}

func cmpFloat(a, b float64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}
