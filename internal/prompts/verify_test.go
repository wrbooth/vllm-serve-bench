package prompts

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/wrbooth/vllm-serve-bench/internal/openai"
)

// fakeTokenizer counts one token per whitespace-separated field, except the
// words in split, which count as their mapped value; a chat adds overhead.
// merge, when set, makes CountText report one token fewer than that, the
// signature of words merging across spaces.
type fakeTokenizer struct {
	overhead int
	split    map[string]int
	merge    bool
	err      error
	calls    int
}

func (f *fakeTokenizer) count(text string) int {
	n := 0
	for _, w := range strings.Fields(text) {
		if k, ok := f.split[w]; ok {
			n += k
		} else {
			n++
		}
	}
	return n
}

func (f *fakeTokenizer) CountText(_ context.Context, text string) (int, error) {
	f.calls++
	if f.err != nil {
		return 0, f.err
	}
	n := f.count(text)
	if f.merge {
		n--
	}
	return n, nil
}

func (f *fakeTokenizer) CountChat(_ context.Context, msgs []openai.Message) (int, error) {
	f.calls++
	if f.err != nil {
		return 0, f.err
	}
	n := f.overhead
	for _, m := range msgs {
		n += f.count(m.Content)
	}
	return n, nil
}

func TestCheckWordsDoesNotModifyTheCallersList(t *testing.T) {
	t.Parallel()
	// Bad words in both halves of one batch: the left half returns [aa] and
	// the right [ii], and the two are appended. When [aa] was a subslice of
	// the input, that append wrote "ii" over words[1] ("bb"), corrupting the
	// list `verify --write` then prunes. Found by -race, pinned here without it.
	words := []string{"aa", "bb", "cc", "dd", "ee", "ff", "gg", "hh", "ii"}
	orig := slices.Clone(words)
	bad, err := CheckWords(context.Background(), &fakeTokenizer{split: map[string]int{"aa": 3, "ii": 2}}, words, 9)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(bad, []string{"aa", "ii"}) {
		t.Fatalf("bad = %v, want [aa ii]", bad)
	}
	if !slices.Equal(words, orig) {
		t.Fatalf("CheckWords modified its input: %v, was %v", words, orig)
	}
}

func TestCheckWordsFindsMultiTokenWordsByBisection(t *testing.T) {
	t.Parallel()
	words := []string{"aa", "bb", "cc", "dd", "ee", "ff", "gg", "hh", "ii"}
	tests := []struct {
		name      string
		split     map[string]int
		batch     int
		wantBad   []string
		wantCalls int
	}{
		// Batches [aa..dd] [ee..hh] [ii]: three clean counts.
		{name: "CleanListCostsOneCallPerBatch", batch: 4, wantCalls: 3},
		// [aa..dd] clean (1). [ee ff gg hh] = 5 (1) -> [ee ff] clean (1),
		// [gg hh] = 3 (1) -> [gg] clean (1), [hh] = 2 bad (1). [ii] (1). 7 calls.
		{name: "OneBadWordIsIsolated", split: map[string]int{"hh": 2}, batch: 4, wantBad: []string{"hh"}, wantCalls: 7},
		// Batch 9: whole list = 11 (1) -> [aa bb cc dd] = 5 (1) -> [aa bb]
		// = 3 (1) -> aa bad (1), bb clean (1); [cc dd] clean (1);
		// [ee ff gg hh ii] = 6 (1) -> [ee ff] clean (1), [gg hh ii] = 4 (1)
		// -> [gg] clean (1), [hh ii] = 3 (1) -> hh clean (1), ii bad (1). 13 calls.
		{name: "BadWordsInBothHalvesKeepListOrder", split: map[string]int{"ii": 2, "aa": 3}, batch: 9, wantBad: []string{"aa", "ii"}, wantCalls: 13},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tk := &fakeTokenizer{split: tt.split}
			bad, err := CheckWords(context.Background(), tk, words, tt.batch)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(bad, tt.wantBad) || tk.calls != tt.wantCalls {
				t.Errorf("bad %v in %d calls, want %v in %d", bad, tk.calls, tt.wantBad, tt.wantCalls)
			}
		})
	}
}

// failAfter answers ok calls, then fails every call after.
type failAfter struct {
	fakeTokenizer
	ok int
}

func (f *failAfter) CountText(ctx context.Context, text string) (int, error) {
	if f.calls >= f.ok {
		return 0, errors.New("tokenizer down")
	}
	return f.fakeTokenizer.CountText(ctx, text)
}

func (f *failAfter) CountChat(ctx context.Context, msgs []openai.Message) (int, error) {
	if f.calls >= f.ok {
		return 0, errors.New("tokenizer down")
	}
	return f.fakeTokenizer.CountChat(ctx, msgs)
}

func TestCheckWordsFailsOnMergeBadBatchOrTokenizerError(t *testing.T) {
	t.Parallel()
	words := []string{"aa", "bb", "cc", "dd"}
	split := map[string]int{"dd": 2} // forces bisection: [aa bb] then [cc dd] then [cc], [dd]
	tests := []struct {
		name  string
		tk    Tokenizer
		batch int
	}{
		{name: "FewerTokensThanWordsMeansMerging", tk: &fakeTokenizer{merge: true}, batch: 4},
		{name: "ZeroBatch", tk: &fakeTokenizer{}, batch: 0},
		{name: "ErrorOnFirstBatch", tk: &failAfter{}, batch: 4},
		{name: "ErrorInLeftHalf", tk: &failAfter{fakeTokenizer: fakeTokenizer{split: split}, ok: 1}, batch: 4},
		{name: "ErrorInRightHalf", tk: &failAfter{fakeTokenizer: fakeTokenizer{split: split}, ok: 2}, batch: 4},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if bad, err := CheckWords(context.Background(), tt.tk, words, tt.batch); err == nil {
				t.Errorf("CheckWords = %v, nil; want an error", bad)
			}
		})
	}
}

// The fake adds overhead 7 to a chat and counts one token per field, so the
// fixed count is 7 plus the fields of the leads:
//
//	interactive: SystemLead "You are a helpful assistant. Use the reference
//	notes below to answer the question.\n\nNotes:" is 15 fields, UserLead
//	"Question:" is 1, so 7 + 15 + 1 = 23.
//	throughput: UserLead "Summarize the following document in one
//	paragraph.\n\nDocument:" is 8 fields, so 7 + 8 = 15.
func TestMeasureFixedIsTheChatCountMinusBodyWords(t *testing.T) {
	t.Parallel()
	for name, want := range map[string]int{"interactive": 23, "throughput": 15} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			p, _ := Lookup(name)
			got, err := MeasureFixed(context.Background(), &fakeTokenizer{overhead: 7}, &p, testWords(t, 1000), 1)
			if err != nil || got != want {
				t.Errorf("MeasureFixed = %d, %v; want %d", got, err, want)
			}
		})
	}
}

func TestMeasureFixedPropagatesErrors(t *testing.T) {
	t.Parallel()
	p, _ := Lookup("interactive")
	if _, err := MeasureFixed(context.Background(), &failAfter{}, &p, testWords(t, 1000), 1); err == nil {
		t.Error("MeasureFixed swallowed a tokenizer error")
	}
	if _, err := MeasureFixed(context.Background(), &fakeTokenizer{}, &p, testWords(t, 10), 1); err == nil {
		t.Error("MeasureFixed accepted a 10-word list")
	}
}

// With the fake's fixed count of 23 for interactive, the prediction is
// 23 + 300 + 100 = 423. A generator told 24 predicts 424 for every prompt,
// so every sampled prompt is a mismatch counted as 423.
func TestCheckPromptsReportsEveryPromptWhoseCountDiffers(t *testing.T) {
	t.Parallel()
	p, _ := Lookup("interactive")
	words := testWords(t, 1000)
	tk := &fakeTokenizer{overhead: 7}
	for _, tt := range []struct {
		fixed int
		want  int
	}{{fixed: 23, want: 0}, {fixed: 24, want: 3}} {
		g, err := NewGenerator(&p, words, tt.fixed, 1)
		if err != nil {
			t.Fatal(err)
		}
		got, err := CheckPrompts(context.Background(), tk, g, []uint64{0, 5, 999_999})
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != tt.want {
			t.Fatalf("fixed %d: %d mismatches %+v, want %d", tt.fixed, len(got), got, tt.want)
		}
		if tt.want > 0 && got[2] != (Mismatch{Profile: "interactive", Index: 999_999, Predicted: 424, Counted: 423}) {
			t.Errorf("mismatch = %+v, want index 999999 predicted 424 counted 423", got[2])
		}
	}
	g, _ := NewGenerator(&p, words, 23, 1)
	if _, err := CheckPrompts(context.Background(), &failAfter{}, g, []uint64{0}); err == nil {
		t.Error("CheckPrompts swallowed a tokenizer error")
	}
}

// 1000 words: capacity 10^9. n = 5 gives low = ceil(5/2) = 3 indexes 0, 1, 2
// and high = 2 at capacity/3 · 1 and · 2 = 333333333, 666666666.
func TestSampleIndexesCoverLowAndHighIndexes(t *testing.T) {
	t.Parallel()
	g := testGenerator(t, "interactive", 0, 1)
	tests := []struct {
		n    int
		want []uint64
	}{
		{n: 0, want: []uint64{}},
		{n: 1, want: []uint64{0}},
		{n: 5, want: []uint64{0, 1, 2, 333_333_333, 666_666_666}},
	}
	for _, tt := range tests {
		if got := SampleIndexes(g, tt.n); !slices.Equal(got, tt.want) {
			t.Errorf("SampleIndexes(%d) = %v, want %v", tt.n, got, tt.want)
		}
	}
}
