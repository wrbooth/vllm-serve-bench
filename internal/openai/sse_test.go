package openai

import (
	"errors"
	"io"
	"strings"
	"testing"
	"testing/iotest"
)

// readAll drains an sseReader, returning the events and the terminal error.
func readAll(t *testing.T, r io.Reader) (events []string, err error) {
	t.Helper()
	s := newSSEReader(r)
	for {
		ev, err := s.next()
		if err != nil {
			return events, err
		}
		events = append(events, ev)
	}
}

func TestSSEReaderParsesEvents(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("x", 200_000) // larger than bufio's 4 KiB buffer
	tests := []struct {
		name    string
		stream  string
		want    []string
		wantErr error
	}{
		{
			name:    "SingleEventThenCleanEOF",
			stream:  "data: {\"a\":1}\n\n",
			want:    []string{`{"a":1}`},
			wantErr: io.EOF,
		},
		{
			name:    "EmptyStreamIsCleanEOF",
			stream:  "",
			wantErr: io.EOF,
		},
		{
			name:    "MultipleEventsIncludingDone",
			stream:  "data: one\n\ndata: two\n\ndata: [DONE]\n\n",
			want:    []string{"one", "two", "[DONE]"},
			wantErr: io.EOF,
		},
		{
			name:    "MultiLineDataJoinedWithNewline",
			stream:  "data: line1\ndata: line2\ndata:line3\n\n",
			want:    []string{"line1\nline2\nline3"},
			wantErr: io.EOF,
		},
		{
			name:    "CRLFLineEndings",
			stream:  "data: a\r\n\r\ndata: b\r\n\r\n",
			want:    []string{"a", "b"},
			wantErr: io.EOF,
		},
		{
			name:    "OnlyOneLeadingSpaceIsStripped",
			stream:  "data:  two spaces\n\n",
			want:    []string{" two spaces"},
			wantErr: io.EOF,
		},
		{
			name:    "CommentsAreIgnored",
			stream:  ": keep-alive\ndata: x\n: another\n\n",
			want:    []string{"x"},
			wantErr: io.EOF,
		},
		{
			name:    "OtherFieldsAreIgnored",
			stream:  "event: message\nid: 7\nretry: 100\ndata: x\n\n",
			want:    []string{"x"},
			wantErr: io.EOF,
		},
		{
			name:    "BareDataFieldIsEmptyValue",
			stream:  "data\n\n",
			want:    []string{""},
			wantErr: io.EOF,
		},
		{
			name:    "BlankLineRunsDoNotProduceEmptyEvents",
			stream:  "\n\n\ndata: x\n\n\n\n: c\n\n",
			want:    []string{"x"},
			wantErr: io.EOF,
		},
		{
			name:    "LineLongerThanTheReadBuffer",
			stream:  "data: " + long + "\n\n",
			want:    []string{long},
			wantErr: io.EOF,
		},
		{
			name:    "EOFInsideAnEventIsUnexpected",
			stream:  "data: a\n\ndata: partial\n",
			want:    []string{"a"},
			wantErr: io.ErrUnexpectedEOF,
		},
		{
			name:    "EOFMidLineIsUnexpected",
			stream:  "data: a\n\ndata: parti",
			want:    []string{"a"},
			wantErr: io.ErrUnexpectedEOF,
		},
		{
			name:    "EOFMidCommentIsUnexpected",
			stream:  ": trunc",
			wantErr: io.ErrUnexpectedEOF,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := readAll(t, strings.NewReader(tt.stream))
			assertEvents(t, got, err, tt.want, tt.wantErr)
		})
	}
}

// A TCP read can end anywhere: mid-field-name, between "\r" and "\n", or
// between the two newlines of an event boundary. Feeding the stream one byte
// per Read is the worst case of that, and must give the same events as one
// big Read.
func TestSSEReaderEventsDoNotDependOnReadBoundaries(t *testing.T) {
	t.Parallel()
	const stream = "data: {\"choices\":[{\"delta\":{\"role\":\"assistant\",\"content\":\"\"}}]}\r\n\r\n" +
		": ping\n\n" +
		"data: {\"choices\":[{\"delta\":{\"content\":\"Hi\"}}]}\n\n" +
		"data: a\ndata: b\n\n" +
		"data: [DONE]\n\n"
	want := []string{
		`{"choices":[{"delta":{"role":"assistant","content":""}}]}`,
		`{"choices":[{"delta":{"content":"Hi"}}]}`,
		"a\nb",
		"[DONE]",
	}
	readers := map[string]func() io.Reader{
		"OneByteReads":  func() io.Reader { return iotest.OneByteReader(strings.NewReader(stream)) },
		"HalfReads":     func() io.Reader { return iotest.HalfReader(strings.NewReader(stream)) },
		"DataWithEOF":   func() io.Reader { return iotest.DataErrReader(strings.NewReader(stream)) },
		"SplitAtEveryN": func() io.Reader { return &chunkedReader{s: stream, n: 7} },
	}
	for name, mk := range readers {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got, err := readAll(t, mk())
			assertEvents(t, got, err, want, io.EOF)
		})
	}
}

func TestSSEReaderPropagatesReadErrors(t *testing.T) {
	t.Parallel()
	boom := errors.New("connection reset")
	r := io.MultiReader(strings.NewReader("data: a\n\ndata: b"), iotest.ErrReader(boom))
	got, err := readAll(t, r)
	assertEvents(t, got, err, []string{"a"}, boom)
}

// chunkedReader returns at most n bytes per Read, so reads end at fixed
// offsets that fall mid-line.
type chunkedReader struct {
	s string
	n int
}

func (c *chunkedReader) Read(p []byte) (int, error) {
	if c.s == "" {
		return 0, io.EOF
	}
	k := min(c.n, len(p), len(c.s))
	copy(p, c.s[:k])
	c.s = c.s[k:]
	return k, nil
}

func assertEvents(t *testing.T, got []string, err error, want []string, wantErr error) {
	t.Helper()
	if !errors.Is(err, wantErr) {
		t.Errorf("terminal error = %v, want %v", err, wantErr)
	}
	if len(got) != len(want) {
		t.Fatalf("got %d events %q, want %d %q", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("event %d = %q, want %q", i, got[i], want[i])
		}
	}
}
