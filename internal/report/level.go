package report

import (
	"fmt"
	"slices"

	"github.com/wrbooth/vllm-serve-bench/internal/crosscheck"
	"github.com/wrbooth/vllm-serve-bench/internal/results"
)

// Run is one run directory as the report sees it.
type Run struct {
	Dir     string // as given on the command line
	Config  results.Config
	Summary results.Summary
	Rows    []results.Row
	// Counters is the vllm_metrics.csv view the engine-side check reads
	// (internal/crosscheck); Engine and GPU are the gauges read here.
	Counters []crosscheck.Sample
	Engine   []Point
	GPU      []Point
}

// ID is the run id from config.json.
func (r *Run) ID() string { return r.Config.RunID }

// Profile is the workload profile name.
func (r *Run) Profile() string { return r.Config.Profile.Name }

// Goodput is per-request goodput at one level.
type Goodput struct {
	Met      int     // successful requests that met every bound on their own
	Requests int     // all measured rows, errors included
	Tokens   int     // Σ completion_tokens of the Met requests
	TokPerS  float64 // Tokens / window; 0 when the window is 0
	SharePct float64 // Met / Requests × 100; 0 when there are no requests
}

// LevelRow is one concurrency level judged against its profile's SLO.
type LevelRow struct {
	Level  results.Level
	Bounds []BoundResult
	// Meets: every bound passes and the level had no errors. An error row
	// has no latency, so it is not in the percentiles; a level that lost
	// requests does not meet the SLO however fast the rest were.
	Meets   bool
	Goodput Goodput
	Engine  EngineLevel
}

// Valid is whether the engine-side check passed: the level's prompts
// were prefilled, not served from an earlier level's or run's cache.
func (l *LevelRow) Valid() bool { return !l.Engine.Check.Cached }

// Compliant is whether the level counts toward the headline: it meets
// the SLO and its measurement is valid.
func (l *LevelRow) Compliant() bool { return l.Meets && l.Valid() }

// Table is one run's levels, in ascending concurrency.
type Table struct {
	RunID        string
	Profile      string
	EngineConfig string
	WarmupS      float64
	DurationS    float64
	SLO          ProfileSLO
	// MinUncachedFraction is the engine check's threshold.
	MinUncachedFraction float64
	Levels              []LevelRow
}

// NewTable judges every level of a run. minUncachedFraction is the
// engine-side check's threshold (crosscheck.Options).
func NewTable(r *Run, slo *SLO, minUncachedFraction float64) (Table, error) {
	p, err := slo.For(r.Profile())
	if err != nil {
		return Table{}, err
	}
	t := Table{
		RunID: r.ID(), Profile: r.Profile(), EngineConfig: r.Config.Engine.Config,
		WarmupS: r.Config.WarmupS, DurationS: r.Config.DurationS, SLO: p, MinUncachedFraction: minUncachedFraction,
	}
	levels := slices.Clone(r.Summary.Levels)
	slices.SortStableFunc(levels, func(a, b results.Level) int { return a.Concurrency - b.Concurrency })
	for i := 1; i < len(levels); i++ {
		if levels[i-1].Concurrency == levels[i].Concurrency {
			return Table{}, fmt.Errorf("%s: concurrency %d appears twice in summary.json", r.Dir, levels[i].Concurrency)
		}
	}
	for i := range levels {
		l := &levels[i]
		row, err := judgeLevel(r, l, &p)
		if err != nil {
			return Table{}, err
		}
		row.Engine = EngineAt(r, l, minUncachedFraction)
		t.Levels = append(t.Levels, row)
	}
	return t, nil
}

// judgeLevel applies the SLO to one level's summary and to its requests.
func judgeLevel(r *Run, l *results.Level, p *ProfileSLO) (LevelRow, error) {
	row := LevelRow{Level: *l, Meets: l.Errors == 0}
	for _, b := range p.Bounds {
		br := b.Judge(l)
		row.Bounds = append(row.Bounds, br)
		row.Meets = row.Meets && br.Pass
	}
	g, err := PerRequestGoodput(r.Rows, l, p)
	if err != nil {
		return LevelRow{}, fmt.Errorf("%s: %w", r.Dir, err)
	}
	row.Goodput = g
	return row, nil
}

// PerRequestGoodput counts the level's requests that met every bound on
// their own (ProfileSLO.RequestMeets) and their output tokens per second
// of the measured window. The rows at the level must number
// Level.Requests, or the files disagree and nothing is reported.
func PerRequestGoodput(rows []results.Row, l *results.Level, p *ProfileSLO) (Goodput, error) {
	g := Goodput{}
	for i := range rows {
		if rows[i].Concurrency != l.Concurrency {
			continue
		}
		g.Requests++
		if p.RequestMeets(&rows[i]) {
			g.Met++
			g.Tokens += rows[i].CompletionTokens
		}
	}
	if g.Requests != l.Requests {
		return Goodput{}, fmt.Errorf("requests.jsonl has %d rows at concurrency %d, summary.json says %d", g.Requests, l.Concurrency, l.Requests)
	}
	if l.WindowS > 0 {
		g.TokPerS = float64(g.Tokens) / l.WindowS
	}
	if g.Requests > 0 {
		g.SharePct = float64(g.Met) / float64(g.Requests) * 100
	}
	return g, nil
}

// Headline is a run's max compliant goodput.
type Headline struct {
	RunID        string
	EngineConfig string
	Found        bool
	// Best is the compliant level with the highest output tok/s (the
	// lower concurrency on a tie); Next is the level just above it in the
	// sweep, nil when Best is the top level or nothing was found.
	Best *LevelRow
	Next *LevelRow
}

// MaxCompliant finds the headline of a table.
func MaxCompliant(t *Table) Headline {
	h := Headline{RunID: t.RunID, EngineConfig: t.EngineConfig}
	best := -1
	for i := range t.Levels {
		l := &t.Levels[i]
		if !l.Compliant() {
			continue
		}
		if best < 0 || l.Level.OutputTokPerSec > t.Levels[best].Level.OutputTokPerSec {
			best = i
		}
	}
	if best < 0 {
		return h
	}
	h.Found, h.Best = true, &t.Levels[best]
	if best+1 < len(t.Levels) {
		h.Next = &t.Levels[best+1]
	}
	return h
}
