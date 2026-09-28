package prompts

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/wrbooth/vllm-serve-bench/internal/openai"
)

// Tokenizer counts tokens the way the engine does. The bench implements it
// with vLLM's /tokenize endpoint; tests use the fake server's.
type Tokenizer interface {
	// CountText counts plain text, with no special tokens added.
	CountText(ctx context.Context, text string) (int, error)
	// CountChat counts messages after the chat template is applied with
	// the generation prompt, as the engine counts prompt_tokens.
	CountChat(ctx context.Context, msgs []openai.Message) (int, error)
}

// CheckWords returns the words that are not exactly one token when written
// with a leading space, in list order. It counts batches of up to batch
// words joined as " w1 w2 ...": every word is at least one token, so a batch
// that counts exactly its length is all single tokens. A batch that counts
// more is split in half until the offenders are isolated, so a clean list
// costs len(words)/batch requests.
func CheckWords(ctx context.Context, tk Tokenizer, words []string, batch int) ([]string, error) {
	if batch < 1 {
		return nil, errors.New("prompts: batch must be at least 1")
	}
	var bad []string
	for lo := 0; lo < len(words); lo += batch {
		b, err := checkChunk(ctx, tk, words[lo:min(lo+batch, len(words))])
		if err != nil {
			return nil, err
		}
		bad = append(bad, b...)
	}
	return bad, nil
}

func checkChunk(ctx context.Context, tk Tokenizer, words []string) ([]string, error) {
	n, err := tk.CountText(ctx, " "+strings.Join(words, " "))
	if err != nil {
		return nil, err
	}
	switch {
	case n == len(words):
		return nil, nil
	case n < len(words):
		// Fewer tokens than words means words merged, which the design
		// rules out; no split can localise it, so it is an error.
		return nil, fmt.Errorf("prompts: %d words counted as %d tokens; words merged across spaces", len(words), n)
	case len(words) == 1:
		// A fresh slice, never the caller's: the append below would
		// otherwise write the right half's results into the caller's
		// word list through this subslice's spare capacity.
		return []string{words[0]}, nil
	}
	mid := len(words) / 2
	left, err := checkChunk(ctx, tk, words[:mid])
	if err != nil {
		return nil, err
	}
	right, err := checkChunk(ctx, tk, words[mid:])
	if err != nil {
		return nil, err
	}
	return append(left, right...), nil
}

// MeasureFixed measures a profile's fixed-token count: the engine's count of
// the reference prompt (request 0) minus its body words.
func MeasureFixed(ctx context.Context, tk Tokenizer, p *Profile, words []string, seed uint64) (int, error) {
	g, err := NewGenerator(p, words, 0, seed)
	if err != nil {
		return 0, err
	}
	n, err := tk.CountChat(ctx, g.Messages(0))
	if err != nil {
		return 0, err
	}
	return n - p.BodyWords(), nil
}

// Mismatch is a generated prompt whose engine count differs from the
// prediction.
type Mismatch struct {
	Profile   string
	Index     uint64
	Predicted int
	Counted   int
}

// CheckPrompts counts the prompts at indexes with the engine and returns
// every one whose count is not g.PromptTokens().
func CheckPrompts(ctx context.Context, tk Tokenizer, g *Generator, indexes []uint64) ([]Mismatch, error) {
	var out []Mismatch
	for _, i := range indexes {
		n, err := tk.CountChat(ctx, g.Messages(i))
		if err != nil {
			return nil, err
		}
		if n != g.PromptTokens() {
			out = append(out, Mismatch{Profile: g.profile.Name, Index: i, Predicted: g.PromptTokens(), Counted: n})
		}
	}
	return out, nil
}

// SampleIndexes returns n request indexes to check: the first half are
// 0, 1, 2, ... (what a run sends first), the rest are spread up to the
// capacity so that every index word position takes non-zero digits.
func SampleIndexes(g *Generator, n int) []uint64 {
	out := make([]uint64, 0, n)
	low := (n + 1) / 2
	for i := range low {
		out = append(out, uint64(i))
	}
	high := n - low
	for k := range high {
		// k+1 of high+1 evenly spaced steps below capacity.
		out = append(out, g.Capacity()/uint64(high+1)*uint64(k+1))
	}
	return out
}
