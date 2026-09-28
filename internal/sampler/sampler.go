// Package sampler records engine and GPU telemetry at a fixed interval (1 Hz
// in practice) as CSV, one row per tick, so the rows line up with the request
// rows of the same run by wall-clock time (docs/02-architecture.md, "Telemetry
// samplers").
//
// A tick is never dropped: a sample that fails still writes a row, with empty
// values and the error in the last column, so a gap in the telemetry is
// visible in the data instead of silently narrowing it.
package sampler

import (
	"context"
	"encoding/csv"
	"io"
	"strconv"
	"time"
)

// Source takes one sample. Fields returns the column names, in the order
// Sample returns values.
type Source interface {
	Fields() []string
	Sample(ctx context.Context) ([]string, error)
}

// Sampler writes one CSV row per tick from a Source.
type Sampler struct {
	Source Source
	Now    func() time.Time // injected so tests do not depend on the wall clock
}

// Run writes the header, then one row per tick until ctx is done or ticks is
// closed. Columns: t_unix_ms, the source's fields, error.
func (s *Sampler) Run(ctx context.Context, ticks <-chan time.Time, w io.Writer) error {
	cw := csv.NewWriter(w)
	fields := s.Source.Fields()
	header := append(append([]string{"t_unix_ms"}, fields...), "error")
	if err := cw.Write(header); err != nil {
		return err
	}
	cw.Flush()
	for {
		select {
		case <-ctx.Done():
			return cw.Error()
		case _, ok := <-ticks:
			if !ok {
				return cw.Error()
			}
			if err := cw.Write(s.row(ctx, len(fields))); err != nil {
				return err
			}
			// Flush every row: a run that dies mid-level still leaves its
			// telemetry on disk up to that second.
			cw.Flush()
			if err := cw.Error(); err != nil {
				return err
			}
		}
	}
}

func (s *Sampler) row(ctx context.Context, n int) []string {
	t := strconv.FormatInt(s.Now().UnixMilli(), 10)
	vals, err := s.Source.Sample(ctx)
	if err == nil && len(vals) != n {
		err = fieldCountError{got: len(vals), want: n}
	}
	if err != nil {
		vals = make([]string, n)
	}
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	return append(append([]string{t}, vals...), msg)
}

type fieldCountError struct{ got, want int }

func (e fieldCountError) Error() string {
	return "sample returned " + strconv.Itoa(e.got) + " values, want " + strconv.Itoa(e.want)
}
