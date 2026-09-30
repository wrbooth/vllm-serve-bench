package chart

import (
	"errors"
	"fmt"
	"math"
	"strings"
)

// Bar is one horizontal bar. Every bar carries its value label, so the
// groups need no value axis.
type Bar struct {
	Name  string
	Slot  int
	Value float64
	Label string
	// Missing draws no bar, only the label (e.g. "no compliant level").
	Missing bool
}

// BarGroup is one scale: its bars share a baseline and a maximum.
type BarGroup struct {
	Title string
	Bars  []Bar
}

// Bars is a stack of bar groups, each on its own scale.
type Bars struct {
	Title    string
	Subtitle []string
	Source   string
	Groups   []BarGroup
	Notes    []string
}

const (
	barThick = 20.0
	barPitch = 30.0
)

// SVG renders the chart.
func (c *Bars) SVG() ([]byte, error) {
	if err := c.validate(); err != nil {
		return nil, err
	}
	names, labels := 0, 0
	for _, g := range c.Groups {
		for _, b := range g.Bars {
			names = max(names, len([]rune(b.Name)))
			labels = max(labels, len([]rune(b.Label)))
		}
	}
	x0 := plotLeft - 60 + charW*float64(names) + 16
	// Every group's longest bar ends at the same x, leaving room for the
	// longest label of all.
	span := plotRight - x0 - charW*float64(labels) - 12
	top := 30 + lineH*float64(len(c.Subtitle)) + 20
	h := top
	for _, g := range c.Groups {
		h += 26 + barPitch*float64(len(g.Bars)) + 16
	}
	h += lineH*float64(len(c.Notes)) + 12

	d := newDoc(width, h, c.Title, strings.Join(c.Subtitle, " "), c.Source)
	d.header(c.Title, c.Subtitle)
	y := top
	for i := range c.Groups {
		y = d.barGroup(&c.Groups[i], x0, span, y)
	}
	for i, n := range c.Notes {
		d.text(plotLeft-60, y+lineH*float64(i)+4, "", "", n)
	}
	return d.bytes(), nil
}

func (c *Bars) validate() error {
	if len(c.Groups) == 0 {
		return errors.New("chart: no bar groups")
	}
	for _, g := range c.Groups {
		if len(g.Bars) == 0 {
			return fmt.Errorf("chart: bar group %q is empty", g.Title)
		}
		for _, b := range g.Bars {
			if err := checkSlot(b.Name, b.Slot); err != nil {
				return err
			}
			if !b.Missing && (b.Value < 0 || math.IsNaN(b.Value) || math.IsInf(b.Value, 0)) {
				return fmt.Errorf("bar %q: value %g cannot be drawn", b.Name, b.Value)
			}
		}
	}
	return nil
}

// barGroup draws one group from y, its longest bar span px long, and
// returns the y below it.
func (d *doc) barGroup(g *BarGroup, x0, span, y float64) float64 {
	d.text(plotLeft-60, y+12, "pt", "", g.Title)
	y += 26
	top := 0.0
	for _, b := range g.Bars {
		top = math.Max(top, b.Value)
	}
	if top <= 0 {
		top = 1
	}
	for i, b := range g.Bars {
		cy := y + barPitch*float64(i) + barPitch/2
		d.text(x0-12, cy+4, "", "end", b.Name)
		end := x0
		if !b.Missing {
			end = x0 + b.Value/top*span
			d.bar(x0, end, cy-barThick/2, b)
		}
		d.text(end+8, cy+4, "v", "", b.Label)
	}
	bottom := y + barPitch*float64(len(g.Bars))
	d.line(x0, y, x0, bottom, "axis")
	return bottom + 16
}

// bar draws a bar with a 4px rounded data end, square at the baseline.
func (d *doc) bar(x0, x1, y float64, b Bar) {
	r := math.Min(4, (x1-x0)/2)
	d.printf(`<path d="M%s %sH%sA%s %s 0 0 1 %s %sV%sA%s %s 0 0 1 %s %sH%sZ" class="b%d"><title>%s</title></path>`+"\n",
		num(x0), num(y), num(x1-r), num(r), num(r), num(x1), num(y+r), num(y+barThick-r),
		num(r), num(r), num(x1-r), num(y+barThick), num(x0), b.Slot, esc(b.Name+": "+b.Label))
}
