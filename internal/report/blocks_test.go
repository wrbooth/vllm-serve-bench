package report

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

// renderID renders every block as "<id> body\n".
func renderID(id string) ([]byte, error) { return []byte(id + " body\n"), nil }

func TestRewriteReplacesOnlyBlockBodiesAndKeepsProseByteForByte(t *testing.T) {
	t.Parallel()
	// Prose with CRLF, trailing spaces, a marker-like line that is not a
	// marker (extra text), and no final newline.
	prose := []string{
		"# Title\r\n\r\nSome prose.  \n",
		"<!-- bench-report:begin a --> not a marker\n\n",
		"between\n",
		"\ttabbed tail",
	}
	doc := prose[0] +
		BeginMarker("a") + "\n" + "old a\nold a 2\n" + EndMarker("a") + "\n" +
		prose[1] +
		BeginMarker("b:x") + "\r\n" + EndMarker("b:x") + "\r\n" +
		prose[2] +
		BeginMarker("a") + "\n" + "stale\n" + EndMarker("a") + "\n" +
		prose[3]
	got, ids, err := Rewrite([]byte(doc), renderID)
	if err != nil {
		t.Fatal(err)
	}
	want := prose[0] +
		BeginMarker("a") + "\n" + "a body\n" + EndMarker("a") + "\n" +
		prose[1] +
		BeginMarker("b:x") + "\r\n" + "b:x body\n" + EndMarker("b:x") + "\r\n" +
		prose[2] +
		BeginMarker("a") + "\n" + "a body\n" + EndMarker("a") + "\n" +
		prose[3]
	if string(got) != want {
		t.Errorf("Rewrite =\n%q\nwant\n%q", got, want)
	}
	if strings.Join(ids, ",") != "a,b:x,a" {
		t.Errorf("ids = %v", ids)
	}
	// Idempotent: a second pass changes nothing.
	again, _, err := Rewrite(got, renderID)
	if err != nil || !bytes.Equal(again, got) {
		t.Errorf("second Rewrite differs (%v):\n%q", err, again)
	}
	// A document without markers is returned unchanged.
	if plain, ids, err := Rewrite([]byte(prose[0]+prose[3]), renderID); err != nil || string(plain) != prose[0]+prose[3] || len(ids) != 0 {
		t.Errorf("no markers: %q %v %v", plain, ids, err)
	}
}

func TestRewriteRejectsBrokenMarkers(t *testing.T) {
	t.Parallel()
	b, e := BeginMarker, EndMarker
	tests := []struct{ name, doc, want string }{
		{"Unclosed", b("a") + "\nx\n", "block a has no end marker"},
		{"BeginAtEOF", "p\n" + b("a"), "line 2: block a has no end marker"},
		{"Nested", b("a") + "\n" + b("b") + "\n" + e("b") + "\n" + e("a") + "\n", "line 2: begin b inside block a"},
		{"EndWithoutBegin", "p\n" + e("a") + "\n", "line 2: end a without a begin"},
		{"Mismatched", b("a") + "\n" + e("b") + "\n", "line 2: end b closes block a"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			out, _, err := Rewrite([]byte(tt.doc), renderID)
			if err == nil || !strings.Contains(err.Error(), tt.want) || out != nil {
				t.Errorf("error %v (out %q), want one containing %q and no output", err, out, tt.want)
			}
		})
	}
	boom := errors.New("boom")
	_, _, err := Rewrite([]byte(b("a")+"\n"+e("a")+"\n"), func(string) ([]byte, error) { return nil, boom })
	if !errors.Is(err, boom) {
		t.Errorf("render error = %v, want it wrapped", err)
	}
}

func TestParseMarkerAcceptsOnlyWholeWellFormedLines(t *testing.T) {
	t.Parallel()
	tests := []struct {
		line, kind, id string
		ok             bool
	}{
		{"<!-- bench-report:begin table:r -->", "begin", "table:r", true},
		{"<!-- bench-report:end table:r -->", "end", "table:r", true},
		{"<!-- bench-report:begin -->", "", "", false},
		{"<!-- bench-report:begin a b -->", "", "", false},
		{"<!-- bench-report:open a -->", "", "", false},
		{" <!-- bench-report:begin a -->", "", "", false},
		{"<!-- bench-report:begin a", "", "", false},
		{"<!-- other -->", "", "", false},
	}
	for _, tt := range tests {
		kind, id, ok := parseMarker(tt.line)
		if kind != tt.kind || id != tt.id || ok != tt.ok {
			t.Errorf("parseMarker(%q) = %q %q %v, want %q %q %v", tt.line, kind, id, ok, tt.kind, tt.id, tt.ok)
		}
	}
}

func TestRenderRejectsUnknownBlocksAndRunsNotGiven(t *testing.T) {
	t.Parallel()
	d := testDoc(t)
	tests := []struct{ id, want string }{
		{"chart:x", "unknown block"},
		{"table", "unknown block"},
		{"table:absent", "run absent is not among"},
		{"engine:absent", "run absent is not among"},
		{"compare:absent:" + baseID, "run absent is not among"},
		{"compare:" + expID + ":absent", "run absent is not among"},
		{"headline:batch", `no run of profile "batch"`},
		{"compare:" + expID + ":" + tpID, "compare:"},
	}
	for _, tt := range tests {
		if _, err := d.Render(tt.id); err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("Render(%s) error = %v, want one containing %q", tt.id, err, tt.want)
		}
	}
}

func TestMissingTablesNamesRunsWithNoTableBlock(t *testing.T) {
	t.Parallel()
	d := testDoc(t)
	got := d.MissingTables([]string{"table:" + baseID, "engine:" + expID})
	if strings.Join(got, ",") != expID+","+tpID {
		t.Errorf("MissingTables = %v, want %s and %s", got, expID, tpID)
	}
}
