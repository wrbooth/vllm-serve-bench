package prompts

import (
	"bytes"
	"strings"
	"testing"
)

// The binary must carry a usable list and a fixed-token count for every
// profile, or `bench run` cannot predict prompt_tokens.
func TestEmbeddedVocabCoversEveryProfile(t *testing.T) {
	t.Parallel()
	v, err := Embedded()
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Words) < minWords {
		t.Errorf("embedded list has %d words, need at least %d", len(v.Words), minWords)
	}
	if v.Fixed.Model == "" || v.Fixed.Source == "" {
		t.Errorf("fixed_tokens.json lacks model or source: %+v", v.Fixed)
	}
	for _, name := range ProfileNames() {
		g, err := v.Generator(name, 1)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		p := g.Profile()
		if want := v.Fixed.Profiles[name] + p.BodyWords(); g.PromptTokens() != want {
			t.Errorf("%s: PromptTokens() = %d, want fixed %d + body %d", name, g.PromptTokens(), v.Fixed.Profiles[name], p.BodyWords())
		}
	}
}

func TestVocabGeneratorRejectsMissingProfileOrCount(t *testing.T) {
	t.Parallel()
	v := &Vocab{Words: testWords(t, 1000), Fixed: Fixed{Profiles: map[string]int{"interactive": 1}}}
	if _, err := v.Generator("throughput", 1); err == nil {
		t.Error("Generator accepted a profile with no fixed-token count")
	}
	if _, err := v.Generator("nope", 1); err == nil {
		t.Error("Generator accepted an unknown profile")
	}
}

func TestWordsSHA256HashesTheNewlineJoinedList(t *testing.T) {
	t.Parallel()
	// sha256("a\nb"), from `printf 'a\nb' | shasum -a 256`.
	const want = "7e18f737311b2dc3b2f269dd78396b0351f14fb66efa879f768cb23181883c78"
	if got := WordsSHA256([]string{"a", "b"}); got != want {
		t.Errorf("WordsSHA256 = %s, want %s", got, want)
	}
}

func TestParseWordsSkipsCommentsAndRejectsBadLines(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		in      string
		want    []string
		wantErr string
	}{
		{name: "CommentsAndBlanksSkipped", in: "# header\n\nalpha\n  beta  \n", want: []string{"alpha", "beta"}},
		{name: "UppercaseRejected", in: "alpha\nBeta\n", wantErr: "line 2"},
		{name: "PunctuationRejected", in: "don't\n", wantErr: "line 1"},
		{name: "DuplicateRejected", in: "alpha\nbeta\nalpha\n", wantErr: `line 3: duplicate word "alpha"`},
		{name: "EmptyRejected", in: "# only a header\n", wantErr: "empty"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseWords(strings.NewReader(tt.in))
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want one containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil || strings.Join(got, ",") != strings.Join(tt.want, ",") {
				t.Errorf("ParseWords = %v, %v; want %v", got, err, tt.want)
			}
		})
	}
}

func TestWriteWordsRoundTripsSortedWithHeader(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	if err := WriteWords(&buf, "line one\nline two", []string{"gamma", "alpha", "beta"}); err != nil {
		t.Fatal(err)
	}
	want := "# line one\n# line two\nalpha\nbeta\ngamma\n"
	if buf.String() != want {
		t.Fatalf("WriteWords wrote %q, want %q", buf.String(), want)
	}
	got, err := ParseWords(&buf)
	if err != nil || strings.Join(got, ",") != "alpha,beta,gamma" {
		t.Errorf("ParseWords(WriteWords) = %v, %v", got, err)
	}
}

func TestWriteFixedWritesIndentedJSON(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	f := &Fixed{Model: "m", Source: "s", Profiles: map[string]int{"b": 2, "a": 1}}
	if err := WriteFixed(&buf, f); err != nil {
		t.Fatal(err)
	}
	want := "{\n  \"model\": \"m\",\n  \"source\": \"s\",\n  \"profiles\": {\n    \"a\": 1,\n    \"b\": 2\n  }\n}\n"
	if buf.String() != want {
		t.Errorf("WriteFixed wrote %q, want %q", buf.String(), want)
	}
}
