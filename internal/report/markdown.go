package report

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// Formatting. Every function here depends only on its arguments, so the
// generated document is byte-identical for the same inputs.

// f formats with a fixed number of decimals.
func f(v float64, digits int) string { return strconv.FormatFloat(v, 'f', digits, 64) }

// Pct formats a signed percentage with one decimal: "+4.8%", "-27.0%",
// and "+0.0%" for a tiny negative that rounds to zero.
func Pct(v float64) string {
	s := f(v, 1)
	if s == "-0.0" {
		s = "0.0"
	}
	if !strings.HasPrefix(s, "-") {
		s = "+" + s
	}
	return s + "%"
}

// Latency formats a metric's value given in ms: TTFT and TPOT in ms with
// one decimal, E2E in s with two.
func Latency(metric string, ms float64) string {
	if metric == MetricE2E {
		return f(ms/1000, 2) + " s"
	}
	return f(ms, 1) + " ms"
}

// latencyBare is Latency without the unit, for table columns whose header
// carries it.
func latencyBare(metric string, ms float64) string {
	return strings.TrimSuffix(strings.TrimSuffix(Latency(metric, ms), " ms"), " s")
}

var metricLabel = map[string]string{MetricTTFT: "TTFT", MetricTPOT: "TPOT", MetricE2E: "E2E"}

// Limit formats a bound: "TTFT p95 ≤ 100 ms", "E2E p95 ≤ 15 s".
func (b *Bound) Limit() string {
	lim := strconv.FormatFloat(b.MaxMs, 'f', -1, 64) + " ms"
	if b.Metric == MetricE2E {
		lim = strconv.FormatFloat(b.MaxMs/1000, 'f', -1, 64) + " s"
	}
	return fmt.Sprintf("%s p%d ≤ %s", metricLabel[b.Metric], b.Percentile, lim)
}

// Cell formats a judged bound with its value and margin, so a knife-edge
// pass or fail stays visible: "pass 73.0 ms (-27.0%)",
// "**FAIL** 26.2 ms (+5.0%)".
func (r *BoundResult) Cell() string {
	if !r.HasData {
		return "**FAIL** no data"
	}
	verdict := "pass"
	if !r.Pass {
		verdict = "**FAIL**"
	}
	return fmt.Sprintf("%s %s (%s)", verdict, Latency(r.Bound.Metric, r.Value), Pct(r.MarginPct))
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// checkCell is the engine-side check's verdict.
func checkCell(e *EngineLevel) string {
	switch {
	case !e.Check.Cached:
		return "ok"
	case !e.Check.OK:
		return "**UNVERIFIED**"
	default:
		return "**CACHED**"
	}
}

// row is a table row. A "|" inside a cell is escaped so it cannot split
// the cell.
func row(cells ...string) string {
	esc := make([]string, len(cells))
	for i, c := range cells {
		esc[i] = strings.ReplaceAll(c, "|", `\|`)
	}
	return "| " + strings.Join(esc, " | ") + " |\n"
}

// header is a header row and its separator: the first `text` columns
// left-aligned, the rest right-aligned (numbers).
func header(text int, cells ...string) string {
	return row(cells...) + "|" + strings.Repeat("---|", text) + strings.Repeat("---:|", len(cells)-text) + "\n"
}

// SLOMarkdown is the SLO table, one row per profile, by name.
func SLOMarkdown(s *SLO) string {
	names := make([]string, 0, len(s.Profiles))
	for n := range s.Profiles {
		names = append(names, n)
	}
	slices.Sort(names)
	var b strings.Builder
	b.WriteString(header(3, "Profile", "A level meets the SLO when", "Decision"))
	for _, n := range names {
		p := s.Profiles[n]
		limits := make([]string, 0, len(p.Bounds))
		for i := range p.Bounds {
			limits = append(limits, p.Bounds[i].Limit())
		}
		b.WriteString(row("`"+n+"`", strings.Join(limits, " and ")+", with no errors", p.Decision))
	}
	return b.String()
}

// TableMarkdown is one run's per-level table and its SLO table.
func TableMarkdown(t *Table) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Run `%s`: profile `%s`, engine config `%s`, %s s warmup and %s s measured window per level.\n\n",
		t.RunID, t.Profile, t.EngineConfig, strconv.FormatFloat(t.WarmupS, 'f', -1, 64), strconv.FormatFloat(t.DurationS, 'f', -1, 64))
	b.WriteString(header(1, "c", "Requests", "Errors", "RPS", "Output tok/s",
		"TTFT p50", "TTFT p95", "TTFT p99", "TPOT p50", "TPOT p95", "TPOT p99", "E2E p50", "E2E p95", "E2E p99"))
	for i := range t.Levels {
		l := &t.Levels[i].Level
		cells := make([]string, 0, 14)
		cells = append(cells, strconv.Itoa(l.Concurrency), strconv.Itoa(l.Requests), strconv.Itoa(l.Errors), f(l.RPS, 2), f(l.OutputTokPerSec, 1))
		for _, m := range []string{MetricTTFT, MetricTPOT, MetricE2E} {
			d := dist(l, m)
			for _, p := range []int{50, 95, 99} {
				cells = append(cells, latencyBare(m, percentile(d, p)))
			}
		}
		b.WriteString(row(cells...))
	}
	b.WriteString("\nTTFT and TPOT in ms, E2E in s: nearest-rank percentiles over each level's successful requests (TPOT excludes completion_tokens < 2). RPS and output tok/s count successful requests over the measured window.\n\n")

	head := make([]string, 0, 1+len(t.SLO.Bounds)+5)
	head = append(head, "c")
	for i := range t.SLO.Bounds {
		head = append(head, t.SLO.Bounds[i].Limit())
	}
	b.WriteString(header(1, append(head, "Meets SLO", "Engine check", "Output tok/s", "Requests meeting SLO", "Per-request goodput (tok/s)")...))
	for i := range t.Levels {
		lr := &t.Levels[i]
		cells := []string{strconv.Itoa(lr.Level.Concurrency)}
		for j := range lr.Bounds {
			cells = append(cells, lr.Bounds[j].Cell())
		}
		g := &lr.Goodput
		cells = append(cells, yesNo(lr.Meets), checkCell(&lr.Engine), f(lr.Level.OutputTokPerSec, 1),
			fmt.Sprintf("%d / %d (%s%%)", g.Met, g.Requests, f(g.SharePct, 1)), f(g.TokPerS, 1))
		b.WriteString(row(cells...))
	}
	b.WriteString("\nA level meets the SLO when every bound holds (a value equal to the limit passes) and it had no errors; the margin is (value − limit) / limit. " +
		"Per-request goodput counts the output tokens of the requests that each met every limit on their own, per second of the measured window. " +
		"The engine check is the uncached-prompt guard in the engine table.\n")
	return b.String()
}

// EngineMarkdown is one run's engine-side table.
func EngineMarkdown(t *Table) string {
	var b strings.Builder
	b.WriteString(header(1, "c", "Uncached prompt tokens / request", "Check", "Preemptions", "Running mean / max",
		"Waiting mean / max", "KV cache mean / max", "GPU power mean / max (W)", "Samples (engine / GPU)"))
	for i := range t.Levels {
		e := &t.Levels[i].Engine
		uncached, preempt := "–", "–"
		if e.Check.OK {
			uncached = f(e.Check.UncachedPerRequest, 1)
		}
		if e.HasPreemptions {
			preempt = f(e.Preemptions, 0)
		}
		b.WriteString(row(strconv.Itoa(t.Levels[i].Level.Concurrency), uncached, checkCell(e), preempt,
			f(e.Running.Mean, 1)+" / "+f(e.Running.Max, 0),
			f(e.Waiting.Mean, 1)+" / "+f(e.Waiting.Max, 0),
			f(e.KV.Mean*100, 1)+"% / "+f(e.KV.Max*100, 1)+"%",
			f(e.PowerW.Mean, 0)+" / "+f(e.PowerW.Max, 0),
			fmt.Sprintf("%d / %d", e.Samples, e.GPUCount)))
	}
	if len(t.Levels) > 0 {
		c := &t.Levels[0].Engine.Check
		fmt.Fprintf(&b, "\nFrom `vllm_metrics.csv` and `gpu.csv`, restricted to each level's measured window. "+
			"Counters (uncached prompt tokens = Δ(prefix_cache_queries − prefix_cache_hits) / Δ ttft_count, or Δprompt_tokens / Δ ttft_count when the engine made no cache lookups because prefix caching is off; preemptions) are deltas between the samples covering the window; "+
			"gauges are the mean and max of the 1 Hz samples inside it. "+
			"A level is **CACHED**, and not counted in the headline, under %s uncached tokens per request: %s × the prompt's %d-token unique part.\n",
			f(c.MinUncached, 1), strconv.FormatFloat(t.MinUncachedFraction, 'f', -1, 64), c.UniqueTokens)
	}
	return b.String()
}

// pairCell is "base → exp (Δ%)", with "–" for a side that did not run
// the level.
func pairCell(base, exp *LevelRow, get func(*LevelRow) float64, format func(float64) string) string {
	switch {
	case base == nil && exp == nil:
		return "–"
	case base == nil:
		return "– → " + format(get(exp))
	case exp == nil:
		return format(get(base)) + " → –"
	}
	s := format(get(base)) + " → " + format(get(exp))
	if d := DeltaPct(get(base), get(exp)); d != nil {
		s += " (" + Pct(*d) + ")"
	}
	return s
}

func sideCell(base, exp *LevelRow, get func(*LevelRow) string) string {
	side := func(l *LevelRow) string {
		if l == nil {
			return "–"
		}
		return get(l)
	}
	return side(base) + " → " + side(exp)
}

// CompareMarkdown is the level-by-level comparison and the change in max
// compliant goodput.
func CompareMarkdown(c *Comparison) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Experiment `%s` (engine config `%s`) against baseline `%s` (engine config `%s`), profile `%s`. Each cell is baseline → experiment (Δ); – marks a level run on one side only. Compliant: the level meets the SLO and passes the engine check.\n\n",
		c.Experiment.RunID, c.Experiment.EngineConfig, c.Baseline.RunID, c.Baseline.EngineConfig, c.Experiment.Profile)
	b.WriteString(header(1, "c", "Output tok/s", "TTFT p50 (ms)", "TTFT p95 (ms)", "TPOT p50 (ms)", "TPOT p95 (ms)",
		"E2E p50 (s)", "E2E p95 (s)", "Per-request goodput (tok/s)", "Preemptions", "Compliant"))
	tok := func(v float64) string { return f(v, 1) }
	lat := func(m string, p int) func(*LevelRow) float64 {
		return func(l *LevelRow) float64 { return percentile(dist(&l.Level, m), p) }
	}
	ms := func(v float64) string { return f(v, 1) }
	secs := func(v float64) string { return f(v/1000, 2) }
	for i := range c.Levels {
		p := &c.Levels[i]
		b.WriteString(row(strconv.Itoa(p.Concurrency),
			pairCell(p.Base, p.Exp, func(l *LevelRow) float64 { return l.Level.OutputTokPerSec }, tok),
			pairCell(p.Base, p.Exp, lat(MetricTTFT, 50), ms),
			pairCell(p.Base, p.Exp, lat(MetricTTFT, 95), ms),
			pairCell(p.Base, p.Exp, lat(MetricTPOT, 50), ms),
			pairCell(p.Base, p.Exp, lat(MetricTPOT, 95), ms),
			pairCell(p.Base, p.Exp, lat(MetricE2E, 50), secs),
			pairCell(p.Base, p.Exp, lat(MetricE2E, 95), secs),
			pairCell(p.Base, p.Exp, func(l *LevelRow) float64 { return l.Goodput.TokPerS }, tok),
			sideCell(p.Base, p.Exp, func(l *LevelRow) string {
				if !l.Engine.HasPreemptions {
					return "–"
				}
				return f(l.Engine.Preemptions, 0)
			}),
			sideCell(p.Base, p.Exp, func(l *LevelRow) string { return yesNo(l.Compliant()) })))
	}
	fmt.Fprintf(&b, "\n**Max compliant goodput:** baseline %s → experiment %s", headlineShort(&c.BaseHeadline), headlineShort(&c.ExpHeadline))
	if c.GoodputDeltaPct != nil {
		fmt.Fprintf(&b, " (%s)", Pct(*c.GoodputDeltaPct))
	}
	b.WriteString(".\n")
	return b.String()
}

func headlineShort(h *Headline) string {
	if !h.Found {
		return "none (no level meets the SLO)"
	}
	return fmt.Sprintf("%s tok/s at c=%d", f(h.Best.Level.OutputTokPerSec, 1), h.Best.Level.Concurrency)
}

// nextCell says why the level above the headline does not carry it.
func nextCell(h *Headline) string {
	switch {
	case !h.Found:
		return "–"
	case h.Next == nil:
		return "none: the headline is the top of the sweep"
	}
	n := h.Next
	s := fmt.Sprintf("c=%d, %s tok/s: ", n.Level.Concurrency, f(n.Level.OutputTokPerSec, 1))
	if n.Compliant() {
		return s + "meets the SLO with no more output"
	}
	var why []string
	for i := range n.Bounds {
		if br := &n.Bounds[i]; !br.Pass {
			why = append(why, "fails "+metricLabel[br.Bound.Metric]+" p"+strconv.Itoa(br.Bound.Percentile)+" "+strings.TrimPrefix(br.Cell(), "**FAIL** "))
		}
	}
	if n.Level.Errors > 0 {
		why = append(why, fmt.Sprintf("%d errors", n.Level.Errors))
	}
	if !n.Valid() {
		why = append(why, "engine check "+strings.Trim(checkCell(&n.Engine), "*"))
	}
	return s + strings.Join(why, ", ")
}

// HeadlineMarkdown is the headline table of one profile: a row per run.
func HeadlineMarkdown(tables []*Table, hs []Headline) string {
	var b strings.Builder
	b.WriteString(header(3, "Run", "Engine config", "SLO", "Max compliant goodput (tok/s)", "At c", "Next level up"))
	for i := range hs {
		h := &hs[i]
		limits := make([]string, 0, len(tables[i].SLO.Bounds))
		for j := range tables[i].SLO.Bounds {
			limits = append(limits, tables[i].SLO.Bounds[j].Limit())
		}
		good, at := "none", "–"
		if h.Found {
			good, at = f(h.Best.Level.OutputTokPerSec, 1), strconv.Itoa(h.Best.Level.Concurrency)
		}
		b.WriteString(row("`"+h.RunID+"`", "`"+h.EngineConfig+"`", strings.Join(limits, " and "), good, at, nextCell(h)))
	}
	b.WriteString("\nMax compliant goodput is the highest output tok/s among the levels that meet the SLO and pass the engine check; a tie goes to the lower concurrency.\n")
	return b.String()
}
