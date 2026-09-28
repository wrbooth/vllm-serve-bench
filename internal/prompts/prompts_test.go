package prompts

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Regenerate the golden files with `go test ./internal/prompts -update` and
// review the diff: a change there changes every prompt a seed produces.
var update = flag.Bool("update", false, "rewrite testdata/*.golden")

// testWords is a synthetic list of n distinct lowercase words ("aaa",
// "aab", ...), so the golden files pin the generator's algorithm and do not
// change when the engine prunes the embedded list.
func testWords(t *testing.T, n int) []string {
	t.Helper()
	words := make([]string, n)
	for i := range n {
		words[i] = string([]byte{'a' + byte(i/676%26), 'a' + byte(i/26%26), 'a' + byte(i%26)})
	}
	return words
}

func testGenerator(t *testing.T, profile string, fixed int, seed uint64) *Generator {
	t.Helper()
	p, err := Lookup(profile)
	if err != nil {
		t.Fatal(err)
	}
	g, err := NewGenerator(&p, testWords(t, 1000), fixed, seed)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func render(t *testing.T, g *Generator, indexes ...uint64) []byte {
	t.Helper()
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	for _, i := range indexes {
		if err := enc.Encode(g.Messages(i)); err != nil {
			t.Fatal(err)
		}
	}
	return buf.Bytes()
}

func TestGeneratedPromptsMatchGoldenFilesForASeed(t *testing.T) {
	t.Parallel()
	for _, profile := range ProfileNames() {
		t.Run(profile, func(t *testing.T) {
			t.Parallel()
			got := render(t, testGenerator(t, profile, 0, 1), 0, 1, 2, 999_999)
			path := filepath.Join("testdata", profile+"-seed1.golden")
			if *update {
				if err := os.WriteFile(path, got, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("%v (run `go test ./internal/prompts -update` to create it)", err)
			}
			if !bytes.Equal(got, want) {
				t.Errorf("prompts for seed 1 differ from %s; if the change is intended, rerun with -update and review the diff", path)
			}
		})
	}
}

func TestSameSeedIsByteIdenticalAndDifferentSeedDiffers(t *testing.T) {
	t.Parallel()
	for _, profile := range ProfileNames() {
		t.Run(profile, func(t *testing.T) {
			t.Parallel()
			a := render(t, testGenerator(t, profile, 0, 7), 0, 1, 2)
			b := render(t, testGenerator(t, profile, 0, 7), 0, 1, 2)
			c := render(t, testGenerator(t, profile, 0, 8), 0, 1, 2)
			if !bytes.Equal(a, b) {
				t.Error("two generators with seed 7 produced different prompts")
			}
			if bytes.Equal(a, c) {
				t.Error("seeds 7 and 8 produced the same prompts")
			}
		})
	}
}

// body returns the words of a message after its lead.
func body(t *testing.T, content, lead string) []string {
	t.Helper()
	rest, ok := strings.CutPrefix(content, lead)
	if !ok {
		t.Fatalf("message %.40q does not start with lead %q", content, lead)
	}
	if !strings.HasPrefix(rest, " ") || strings.Contains(rest, "  ") {
		t.Fatalf("body must be ' w1 w2 ...' with single spaces, got %.40q", rest)
	}
	return strings.Fields(rest)
}

// The token prediction rests on the layout: the lead text, then exactly
// SharedWords / UniqueWords list words, each after one space.
func TestMessagesHaveTheProfileLayoutAndPredictedTokenCount(t *testing.T) {
	t.Parallel()
	tests := []struct {
		profile     string
		fixed       int
		wantSystem  int // body words in the system message; -1 = no system message
		wantUser    int
		wantPredict int
	}{
		// 33 fixed + 300 shared + 100 unique = 433
		{profile: "interactive", fixed: 33, wantSystem: 300, wantUser: 100, wantPredict: 433},
		// 41 fixed + 1500 unique = 1541
		{profile: "throughput", fixed: 41, wantSystem: -1, wantUser: 1500, wantPredict: 1541},
	}
	for _, tt := range tests {
		t.Run(tt.profile, func(t *testing.T) {
			t.Parallel()
			g := testGenerator(t, tt.profile, tt.fixed, 3)
			p := g.Profile()
			inList := map[string]bool{}
			for _, w := range testWords(t, 1000) {
				inList[w] = true
			}
			msgs := g.Messages(5)
			user := msgs[len(msgs)-1]
			if tt.wantSystem < 0 {
				if len(msgs) != 1 {
					t.Fatalf("got %d messages, want only the user message", len(msgs))
				}
			} else {
				if len(msgs) != 2 || msgs[0].Role != "system" {
					t.Fatalf("got %+v, want system then user", msgs)
				}
				if got := body(t, msgs[0].Content, p.SystemLead); len(got) != tt.wantSystem {
					t.Errorf("system body has %d words, want %d", len(got), tt.wantSystem)
				}
			}
			words := body(t, user.Content, p.UserLead)
			if user.Role != "user" || len(words) != tt.wantUser {
				t.Errorf("user message role %q with %d words, want user with %d", user.Role, len(words), tt.wantUser)
			}
			for _, w := range words {
				if !inList[w] {
					t.Fatalf("word %q is not from the list", w)
				}
			}
			if got := g.PromptTokens(); got != tt.wantPredict {
				t.Errorf("PromptTokens() = %d, want %d", got, tt.wantPredict)
			}
		})
	}
}

// Prefix caching is on in the baseline, so a repeated prompt would be served
// from cache. Every index must give a different user message, differing at
// its first body word, while the system prompt stays shared.
func TestEveryRequestIndexHasADistinctUserMessageFromItsFirstWord(t *testing.T) {
	t.Parallel()
	g := testGenerator(t, "interactive", 0, 1)
	p := g.Profile()
	seen := map[string]uint64{}
	shared := g.Messages(0)[0].Content
	for i := range uint64(3000) { // 3000 > len(words): the second digit turns over
		msgs := g.Messages(i)
		if msgs[0].Content != shared {
			t.Fatalf("index %d has a different system prompt", i)
		}
		key := strings.Join(body(t, msgs[1].Content, p.UserLead)[:indexWords], " ")
		if prev, dup := seen[key]; dup {
			t.Fatalf("indexes %d and %d start with the same words %q", prev, i, key)
		}
		seen[key] = i
		if i > 0 {
			prevFirst := body(t, g.Messages(i - 1)[1].Content, p.UserLead)[0]
			if first := body(t, msgs[1].Content, p.UserLead)[0]; first == prevFirst {
				t.Fatalf("indexes %d and %d share their first body word %q", i-1, i, first)
			}
		}
	}
}

// The first three body words spell the index in base len(words), least
// significant digit first, through the seed's digit permutation.
func TestIndexWordsSpellTheIndexInBaseLenWords(t *testing.T) {
	t.Parallel()
	g := testGenerator(t, "throughput", 0, 5)
	p := g.Profile()
	digitOf := map[string]uint64{}
	for d, w := range g.digits {
		digitOf[w] = uint64(d)
	}
	// 1000 words: 123_456_789 = 789 + 456·1000 + 123·1000²
	words := body(t, g.Messages(123_456_789)[0].Content, p.UserLead)
	got := []uint64{digitOf[words[0]], digitOf[words[1]], digitOf[words[2]]}
	if got[0] != 789 || got[1] != 456 || got[2] != 123 {
		t.Errorf("index words decode to digits %v, want [789 456 123]", got)
	}
}

func TestMessagesPanicsPastCapacity(t *testing.T) {
	t.Parallel()
	g := testGenerator(t, "interactive", 0, 1)
	if g.Capacity() != 1_000_000_000 { // 1000³
		t.Fatalf("Capacity() = %d, want 1000³", g.Capacity())
	}
	g.Messages(g.Capacity() - 1)
	defer func() {
		if recover() == nil {
			t.Error("Messages(Capacity()) did not panic; indexes would wrap and repeat")
		}
	}()
	g.Messages(g.Capacity())
}

func TestNewGeneratorRejectsInvalidInput(t *testing.T) {
	t.Parallel()
	good, err := Lookup("interactive")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		words  int
		fixed  int
		mutate func(*Profile)
	}{
		{name: "TooFewWords", words: 999, mutate: func(*Profile) {}},
		{name: "TooFewUniqueWordsForTheIndex", words: 1000, mutate: func(p *Profile) { p.UniqueWords = 2 }},
		{name: "SharedWordsWithoutSystemMessage", words: 1000, mutate: func(p *Profile) { p.SystemLead = "" }},
		{name: "NegativeFixed", words: 1000, fixed: -1, mutate: func(*Profile) {}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p := good
			tt.mutate(&p)
			if _, err := NewGenerator(&p, testWords(t, tt.words), tt.fixed, 1); err == nil {
				t.Error("NewGenerator accepted invalid input")
			}
		})
	}
}

func TestOutputBoundsMatchVLLMRandomRangeRatio(t *testing.T) {
	t.Parallel()
	// vllm/benchmarks/datasets/utils.py: floor(L*(1-r)) .. ceil(L*(1+r)),
	// each at least 1.
	cases := []struct {
		maxTokens int
		ratio     float64
		lo, hi    int
	}{
		{128, 0.25, 96, 160},  // 128*0.75 = 96, 128*1.25 = 160
		{256, 0.25, 192, 320}, // 256*0.75 = 192, 256*1.25 = 320
		{10, 0.25, 7, 13},     // floor(7.5) = 7, ceil(12.5) = 13
		{128, 0, 128, 128},    // fixed length
		{2, 1, 1, 4},          // floor(0) = 0 -> 1, ceil(4) = 4
	}
	for _, tc := range cases {
		p := Profile{MaxTokens: tc.maxTokens, OutputRangeRatio: tc.ratio}
		if lo, hi := p.OutputBounds(); lo != tc.lo || hi != tc.hi {
			t.Errorf("OutputBounds(%d, %v) = %d..%d, want %d..%d", tc.maxTokens, tc.ratio, lo, hi, tc.lo, tc.hi)
		}
	}
}

func TestOutputTokensAreSeededUniformAndCoverBothBounds(t *testing.T) {
	t.Parallel()
	g := testGenerator(t, "interactive", 0, 1)
	same := testGenerator(t, "interactive", 0, 1)
	other := testGenerator(t, "interactive", 0, 2)
	prof := g.Profile()
	lo, hi := prof.OutputBounds()
	// 20,000 draws over 65 values: each value's expected count is ~308, so
	// both endpoints appearing is certain in practice, and a mean within 1
	// token of 128 is ~20 standard errors of slack (sd of U{96..160} is
	// ~18.8; its standard error over 20,000 draws is ~0.13).
	const n = 20000
	seen := map[int]bool{}
	sum, differ := 0, 0
	for i := range uint64(n) {
		k := g.OutputTokens(i)
		if k < lo || k > hi {
			t.Fatalf("OutputTokens(%d) = %d, outside %d..%d", i, k, lo, hi)
		}
		if same.OutputTokens(i) != k {
			t.Fatalf("OutputTokens(%d) not reproducible under the same seed", i)
		}
		if other.OutputTokens(i) != k {
			differ++
		}
		seen[k] = true
		sum += k
	}
	if !seen[lo] || !seen[hi] || len(seen) != hi-lo+1 {
		t.Errorf("drew %d distinct lengths (lo seen %v, hi seen %v), want all %d", len(seen), seen[lo], seen[hi], hi-lo+1)
	}
	if mean := float64(sum) / n; mean < 127 || mean > 129 {
		t.Errorf("mean output length %.2f, want 128 +- 1", mean)
	}
	// A different seed agrees by chance 1 time in 65: ~308 of 20,000.
	if differ < n*9/10 {
		t.Errorf("seed 2 matched seed 1 on %d of %d lengths; want independent streams", n-differ, n)
	}
}

func TestRequestHoldsOutputLengthAndSetsGreedySampling(t *testing.T) {
	t.Parallel()
	g := testGenerator(t, "throughput", 0, 1)
	for _, natural := range []bool{false, true} {
		r := g.Request("m", 4, natural)
		if r.Model != "m" || r.MaxTokens != g.OutputTokens(4) || r.IgnoreEOS == natural {
			t.Errorf("naturalStop=%v: model %q max_tokens %d ignore_eos %v; want m, %d, %v",
				natural, r.Model, r.MaxTokens, r.IgnoreEOS, g.OutputTokens(4), !natural)
		}
		// Every request carries its own drawn length, not the mean.
		lengths := map[int]bool{}
		for i := range uint64(50) {
			if got := g.Request("m", i, natural).MaxTokens; got != g.OutputTokens(i) {
				t.Fatalf("request %d max_tokens %d, want its drawn length %d", i, got, g.OutputTokens(i))
			}
			lengths[g.OutputTokens(i)] = true
		}
		if len(lengths) < 10 {
			t.Errorf("50 requests used %d distinct max_tokens; want the drawn spread", len(lengths))
		}
		if r.Temperature == nil || *r.Temperature != 0 || r.RepetitionPenalty == nil || *r.RepetitionPenalty != 1 {
			t.Errorf("sampling temperature %v repetition_penalty %v; want explicit 0 and 1", r.Temperature, r.RepetitionPenalty)
		}
		if got, want := r.Messages, g.Messages(4); len(got) != 1 || got[0] != want[0] {
			t.Errorf("request messages are not Messages(4)")
		}
	}
}

func TestLookupReturnsACopyAndRejectsUnknownProfiles(t *testing.T) {
	t.Parallel()
	p, err := Lookup("throughput")
	if err != nil {
		t.Fatal(err)
	}
	p.Sweep[0] = -1
	again, _ := Lookup("throughput")
	if again.Sweep[0] != 8 {
		t.Errorf("mutating a looked-up Sweep changed the profile table: %v", again.Sweep)
	}
	if _, err := Lookup("nope"); err == nil || !strings.Contains(err.Error(), "interactive, throughput") {
		t.Errorf("Lookup(nope) error = %v, want one listing the profiles", err)
	}
}

func TestSplitmix64MatchesReferenceValues(t *testing.T) {
	t.Parallel()
	// Reference: the SplitMix64 finaliser applied to 0 (with the golden-ratio
	// increment) is 0xe220a8397b1dcdaf, the first output of the reference
	// generator seeded with 0.
	if got := splitmix64(0); got != 0xe220a8397b1dcdaf {
		t.Errorf("splitmix64(0) = %#x, want 0xe220a8397b1dcdaf", got)
	}
}
