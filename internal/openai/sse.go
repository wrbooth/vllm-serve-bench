package openai

import (
	"bufio"
	"errors"
	"io"
	"strings"
)

// sseReader decodes a text/event-stream body into the data payloads of its
// events, following the WHATWG server-sent events parsing rules that matter
// for an OpenAI-compatible stream:
//
//   - lines end in LF or CRLF; a lone CR is not treated as a line end (the
//     spec allows it, no OpenAI-compatible server emits it, and JSON payloads
//     escape their own CRs, so this is a deliberate simplification);
//   - "data:" lines accumulate; several in one event are joined with "\n";
//   - a single space after the colon is stripped, nothing more;
//   - lines starting with ":" are comments (keep-alives) and are ignored;
//   - other fields (event, id, retry) are ignored;
//   - a blank line ends an event; an event with no data lines is not
//     dispatched, so runs of blank lines never produce empty events.
//
// A network read may end anywhere, including mid-line; bufio assembles whole
// lines before any of the above is applied, so the event boundaries never
// depend on how the bytes were chunked on the wire.
type sseReader struct {
	br *bufio.Reader
}

func newSSEReader(r io.Reader) *sseReader {
	return &sseReader{br: bufio.NewReader(r)}
}

// next returns the data of the next event. At a clean end of stream (no
// partial event pending) it returns io.EOF. If the stream ends inside an
// event, or mid-line, it returns io.ErrUnexpectedEOF: an event that was not
// terminated by a blank line was not completely received and must not be
// acted on.
func (s *sseReader) next() (string, error) {
	var data strings.Builder
	pending := false // at least one data line seen in this event
	for {
		line, err := s.br.ReadString('\n')
		if err != nil {
			if errors.Is(err, io.EOF) {
				if pending || line != "" {
					return "", io.ErrUnexpectedEOF
				}
				return "", io.EOF
			}
			return "", err
		}
		line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")

		if line == "" {
			if pending {
				return data.String(), nil
			}
			continue
		}
		if line[0] == ':' {
			continue
		}
		field, value, found := strings.Cut(line, ":")
		if found {
			value = strings.TrimPrefix(value, " ")
		}
		if field != "data" {
			continue
		}
		if pending {
			data.WriteByte('\n')
		}
		data.WriteString(value)
		pending = true
	}
}
