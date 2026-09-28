package report

import (
	"math"
	"strings"
	"testing"
)

// Baseline runs c=1, 2; the experiment runs c=2, 4. The union is 1, 2, 4,
// each level run on one side only kept with the other side nil.
//
//	c=2 output 200 → 250: (250 − 200) / 200 = +25%
//	headline: baseline best compliant is c=2 at 200, experiment c=4 at 400
//	          → (400 − 200) / 200 = +100%
func TestCompareKeepsLevelsRunOnOneSideOnly(t *testing.T) {
	t.Parallel()
	base := table(lr(1, 100, true, true), lr(2, 200, true, true))
	base.Profile = "interactive"
	exp := table(lr(2, 250, true, true), lr(4, 400, true, true))
	exp.Profile = "interactive"
	c, err := Compare(&exp, &base)
	if err != nil {
		t.Fatal(err)
	}
	type side struct{ base, exp bool }
	want := map[int]side{1: {true, false}, 2: {true, true}, 4: {false, true}}
	if len(c.Levels) != 3 {
		t.Fatalf("levels = %+v, want c=1, 2, 4", c.Levels)
	}
	for i, n := range []int{1, 2, 4} {
		p := c.Levels[i]
		if p.Concurrency != n || (p.Base != nil) != want[n].base || (p.Exp != nil) != want[n].exp {
			t.Errorf("level %d = c=%d base %v exp %v, want c=%d %+v", i, p.Concurrency, p.Base != nil, p.Exp != nil, n, want[n])
		}
	}
	if p := c.Levels[1]; p.Base.Level.OutputTokPerSec != 200 || p.Exp.Level.OutputTokPerSec != 250 {
		t.Errorf("c=2 paired wrong: base %v exp %v", p.Base.Level.OutputTokPerSec, p.Exp.Level.OutputTokPerSec)
	}
	if c.GoodputDeltaPct == nil || math.Abs(*c.GoodputDeltaPct-100) > 1e-12 {
		t.Errorf("goodput delta = %v, want +100%%", c.GoodputDeltaPct)
	}
	md := CompareMarkdown(&c)
	for _, s := range []string{
		"| 1 | 100.0 → – |",
		"| 2 | 200.0 → 250.0 (+25.0%) |",
		"| 4 | – → 400.0 |",
		"baseline 200.0 tok/s at c=2 → experiment 400.0 tok/s at c=4 (+100.0%)",
	} {
		if !strings.Contains(md, s) {
			t.Errorf("comparison markdown lacks %q:\n%s", s, md)
		}
	}
}

func TestCompareWithoutAHeadlineOnOneSideHasNoGoodputDelta(t *testing.T) {
	t.Parallel()
	base := table(lr(1, 100, false, true))
	exp := table(lr(1, 200, true, true))
	c, err := Compare(&exp, &base)
	if err != nil {
		t.Fatal(err)
	}
	if c.GoodputDeltaPct != nil || c.BaseHeadline.Found || !c.ExpHeadline.Found {
		t.Errorf("delta %v base found %v exp found %v; want no delta", c.GoodputDeltaPct, c.BaseHeadline.Found, c.ExpHeadline.Found)
	}
	if md := CompareMarkdown(&c); !strings.Contains(md, "baseline none (no level meets the SLO) → experiment 200.0 tok/s at c=1.") {
		t.Errorf("markdown:\n%s", md)
	}
}

func TestCompareRejectsDifferentProfiles(t *testing.T) {
	t.Parallel()
	a, b := table(), table()
	a.Profile, b.Profile = "interactive", "throughput"
	if _, err := Compare(&a, &b); err == nil {
		t.Error("want an error comparing two profiles")
	}
}

func TestDeltaPctGuardsAZeroBase(t *testing.T) {
	t.Parallel()
	if DeltaPct(0, 5) != nil {
		t.Error("DeltaPct(0, x): want nil")
	}
	// (90 − 120) / 120 = −0.25 → −25%.
	if d := DeltaPct(120, 90); d == nil || *d != -25 {
		t.Errorf("DeltaPct(120, 90) = %v, want -25", d)
	}
	zero := &LevelRow{}
	if got := pairCell(zero, zero, func(*LevelRow) float64 { return 0 }, func(v float64) string { return f(v, 0) }); got != "0 → 0" {
		t.Errorf("pairCell with a zero base = %q, want no delta", got)
	}
	if got := pairCell(nil, nil, nil, nil); got != "–" {
		t.Errorf("pairCell(nil, nil) = %q", got)
	}
}
