// Package report is what `bench report` computes from committed run
// directories: per-level tables judged against the SLO, the engine-side
// view of each level from the 1 Hz telemetry, comparisons of an experiment
// with its baseline, and the headline goodput per profile. Everything
// except load.go is pure; markdown.go and blocks.go render it into
// docs/03-results.md between marker comments.
//
// Two goodputs are reported (docs/02-architecture.md, "Metric definitions"):
//
//   - Max compliant goodput, the headline. A level meets the SLO when every
//     bound's percentile is at or under its limit and it had no errors. The
//     headline is the highest output tok/s among levels that meet it and
//     passed the engine-side check, and the concurrency where it occurs.
//   - Per-request goodput, per level: Σ completion_tokens of the successful
//     requests whose own TTFT / TPOT / E2E are each at or under the bound's
//     limit, over the level's measured window.
package report

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"

	"github.com/wrbooth/vllm-serve-bench/internal/results"
)

// Metric names a bound can use.
const (
	MetricTTFT = "ttft"
	MetricTPOT = "tpot"
	MetricE2E  = "e2e"
)

// Bound is one term of an SLO: the given percentile of a metric, per
// concurrency level, must be at or under MaxMs. Equal to the limit passes:
// the SLOs are written "p95 ≤ limit".
type Bound struct {
	Metric     string  `json:"metric"`     // ttft, tpot or e2e
	Percentile int     `json:"percentile"` // 50, 95 or 99: what summary.json holds
	MaxMs      float64 `json:"max_ms"`
}

// ProfileSLO is one profile's SLO: every bound must hold.
type ProfileSLO struct {
	// Decision says where the reasoning for the values is written down.
	Decision string  `json:"decision"`
	Bounds   []Bound `json:"bounds"`
}

// SLO is the committed SLO file (docs/slo.json), keyed by profile name.
type SLO struct {
	Comment  string                `json:"comment,omitempty"`
	Profiles map[string]ProfileSLO `json:"profiles"`
}

// ParseSLO reads and validates an SLO file.
func ParseSLO(r io.Reader) (SLO, error) {
	var s SLO
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&s); err != nil {
		return SLO{}, fmt.Errorf("slo: %w", err)
	}
	if len(s.Profiles) == 0 {
		return SLO{}, errors.New("slo: no profiles")
	}
	for name, p := range s.Profiles {
		if len(p.Bounds) == 0 {
			return SLO{}, fmt.Errorf("slo: profile %q has no bounds", name)
		}
		for _, b := range p.Bounds {
			if err := b.validate(); err != nil {
				return SLO{}, fmt.Errorf("slo: profile %q: %w", name, err)
			}
		}
	}
	return s, nil
}

func (b *Bound) validate() error {
	if !slices.Contains([]string{MetricTTFT, MetricTPOT, MetricE2E}, b.Metric) {
		return fmt.Errorf("unknown metric %q (want ttft, tpot or e2e)", b.Metric)
	}
	if !slices.Contains([]int{50, 95, 99}, b.Percentile) {
		return fmt.Errorf("%s: percentile %d is not one summary.json holds (50, 95, 99)", b.Metric, b.Percentile)
	}
	if b.MaxMs <= 0 {
		return fmt.Errorf("%s: max_ms %g must be positive", b.Metric, b.MaxMs)
	}
	return nil
}

// For returns the SLO of a profile.
func (s *SLO) For(profile string) (ProfileSLO, error) {
	p, ok := s.Profiles[profile]
	if !ok {
		return ProfileSLO{}, fmt.Errorf("slo: no SLO for profile %q", profile)
	}
	return p, nil
}

// BoundResult is one bound judged at one level.
type BoundResult struct {
	Bound Bound
	// Value is the level's percentile in ms; HasData is false when the
	// metric had no samples (every request failed), which fails the bound.
	Value   float64
	HasData bool
	Pass    bool
	// MarginPct is (Value − MaxMs) / MaxMs × 100: negative is headroom,
	// positive is how far over the limit.
	MarginPct float64
}

// dist picks the level's distribution for a metric.
func dist(l *results.Level, metric string) results.Dist {
	switch metric {
	case MetricTTFT:
		return l.TTFT
	case MetricTPOT:
		return l.TPOT
	default:
		return l.E2E
	}
}

// percentile picks one of a distribution's percentiles (validated to be
// 50, 95 or 99).
func percentile(d results.Dist, p int) float64 {
	switch p {
	case 50:
		return d.P50ms
	case 95:
		return d.P95ms
	default:
		return d.P99ms
	}
}

// Judge checks one bound against a level's summary.
func (b *Bound) Judge(l *results.Level) BoundResult {
	d := dist(l, b.Metric)
	r := BoundResult{Bound: *b, HasData: d.N > 0}
	if !r.HasData {
		return r
	}
	r.Value = percentile(d, b.Percentile)
	r.Pass = r.Value <= b.MaxMs
	r.MarginPct = (r.Value - b.MaxMs) / b.MaxMs * 100
	return r
}

// RequestMeets reports whether one request met every bound on its own:
// each of its metrics at or under the bound's limit. An error row never
// does. A request without a TPOT (completion_tokens < 2) has nothing to
// judge against a TPOT bound, as it is excluded from the TPOT percentile,
// so only its other metrics count.
func (p *ProfileSLO) RequestMeets(row *results.Row) bool {
	if !row.OK() {
		return false
	}
	for _, b := range p.Bounds {
		var v *float64
		switch b.Metric {
		case MetricTTFT:
			v = row.TTFTms
		case MetricTPOT:
			if row.TPOTms == nil {
				continue
			}
			v = row.TPOTms
		default:
			v = row.E2Ems
		}
		if v == nil || *v > b.MaxMs {
			return false
		}
	}
	return true
}
