package report

import (
	"fmt"
	"slices"
)

// Comparison is an experiment run against a baseline run of the same
// profile, level by level.
type Comparison struct {
	Baseline, Experiment *Table
	// Levels is the union of both runs' concurrencies, ascending. A level
	// run on one side only is kept with the other side nil: it is shown,
	// not dropped.
	Levels []LevelPair
	// Headlines of each side, and the change in max compliant goodput.
	BaseHeadline, ExpHeadline Headline
	GoodputDeltaPct           *float64
}

// LevelPair is one concurrency on both sides.
type LevelPair struct {
	Concurrency int
	Base, Exp   *LevelRow
}

// Compare pairs the levels of two tables of the same profile.
func Compare(exp, base *Table) (Comparison, error) {
	if exp.Profile != base.Profile {
		return Comparison{}, fmt.Errorf("compare: %s is profile %q, %s is %q", exp.RunID, exp.Profile, base.RunID, base.Profile)
	}
	c := Comparison{Baseline: base, Experiment: exp, BaseHeadline: MaxCompliant(base), ExpHeadline: MaxCompliant(exp)}
	var levels []int
	for _, t := range []*Table{base, exp} {
		for i := range t.Levels {
			levels = append(levels, t.Levels[i].Level.Concurrency)
		}
	}
	slices.Sort(levels)
	for _, n := range slices.Compact(levels) {
		c.Levels = append(c.Levels, LevelPair{Concurrency: n, Base: base.At(n), Exp: exp.At(n)})
	}
	if c.BaseHeadline.Found && c.ExpHeadline.Found {
		c.GoodputDeltaPct = DeltaPct(c.BaseHeadline.Best.Level.OutputTokPerSec, c.ExpHeadline.Best.Level.OutputTokPerSec)
	}
	return c, nil
}

// At is the level at concurrency n, or nil.
func (t *Table) At(n int) *LevelRow {
	for i := range t.Levels {
		if t.Levels[i].Level.Concurrency == n {
			return &t.Levels[i]
		}
	}
	return nil
}

// DeltaPct is (exp − base) / base × 100, nil when base is 0.
func DeltaPct(base, exp float64) *float64 {
	if base == 0 {
		return nil
	}
	d := (exp - base) / base * 100
	return &d
}
