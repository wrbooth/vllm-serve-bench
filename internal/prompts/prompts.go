// Package prompts generates the benchmark's prompts from a seed, with a
// prompt-token count that is known in advance (docs/02-architecture.md,
// "Prompt generation" and "Workload profiles").
//
// The trick is the word list. Every word in words.txt, written with a leading
// space, is exactly one token for the Qwen2.5 tokenizer, and it cannot merge
// with its neighbours: Qwen2's pre-tokenizer splits text on a regex before
// BPE runs, and " word" (an optional space followed by letters) is always its
// own piece. A body of K words is therefore exactly K tokens, so any number of
// unique prompts can be built from (seed, profile, request index) with no
// tokenizer at run time. What the chat template and the profile's fixed
// lead-in text add is a per-profile constant, measured once against the
// engine and embedded as fixed_tokens.json. `bench prompts verify` checks
// both facts against a live engine.
//
// The engine runs with prefix caching on, so reusing prompt text would hit
// the cache and shorten TTFT. Every request index therefore gets a different
// unique part, and the difference starts at its first word: the index itself
// is written there in base len(words).
package prompts

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"

	"github.com/wrbooth/vllm-serve-bench/internal/openai"
)

// Profile is one workload shape. The prompt is laid out as
//
//	[system: SystemLead + SharedWords words]   omitted when SystemLead is ""
//	user:    UserLead + UniqueWords words
//
// A lead must end in a non-letter (":" here), so the first body word, which
// is written with a leading space, starts a new pre-token.
type Profile struct {
	Name string
	// SystemLead opens the system message. Empty means no system message:
	// the chat template then inserts the model's default one.
	SystemLead string
	// SharedWords follow SystemLead and are the same for every request
	// under one seed: the prefix that prefix caching acts on.
	SharedWords int
	// UserLead opens the user message.
	UserLead string
	// UniqueWords follow UserLead and differ for every request index.
	UniqueWords int
	// MaxTokens is the output length (held fixed with ignore_eos unless the
	// run uses natural stop).
	MaxTokens int
	// Sweep is the default concurrency list for `bench run`.
	Sweep []int
}

// profiles are the two workloads in docs/02-architecture.md.
var profiles = []Profile{
	{
		Name:        "interactive",
		SystemLead:  "You are a helpful assistant. Use the reference notes below to answer the question.\n\nNotes:",
		SharedWords: 300,
		UserLead:    "Question:",
		UniqueWords: 100,
		MaxTokens:   128,
		Sweep:       []int{1, 2, 4, 8, 16, 32},
	},
	{
		Name:        "throughput",
		UserLead:    "Summarize the following document in one paragraph.\n\nDocument:",
		UniqueWords: 1500,
		MaxTokens:   256,
		Sweep:       []int{8, 16, 32, 64, 128, 192, 256},
	},
}

// ProfileNames lists the profiles in a fixed order.
func ProfileNames() []string {
	names := make([]string, len(profiles))
	for i := range profiles {
		names[i] = profiles[i].Name
	}
	return names
}

// Lookup returns the named profile. The returned value is a copy.
func Lookup(name string) (Profile, error) {
	for i := range profiles {
		if profiles[i].Name == name {
			p := profiles[i]
			p.Sweep = slices.Clone(p.Sweep)
			return p, nil
		}
	}
	return Profile{}, fmt.Errorf("prompts: unknown profile %q (have %s)", name, strings.Join(ProfileNames(), ", "))
}

// BodyWords is the number of words, and so tokens, a prompt carries besides
// its fixed tokens.
func (p *Profile) BodyWords() int { return p.SharedWords + p.UniqueWords }

// Sampling is fixed rather than left to the server: vLLM's defaults come from
// the checkpoint's generation_config.json (Qwen2.5 ships temperature 0.7,
// top_k 20, repetition_penalty 1.1). Greedy decoding with no repetition
// penalty makes a run independent of the checkpoint's file, and at
// temperature 0 vLLM skips top-k/top-p, so those need not be sent.
const (
	Temperature       = 0.0
	RepetitionPenalty = 1.0
)

// indexWords is how many leading words of the unique part encode the request
// index. minWords keeps the capacity, len(words)^indexWords, at least 10^9.
const (
	indexWords = 3
	minWords   = 1000
)

// Stream tags for the PRNG, so the shared part, the digit permutation and the
// unique parts never draw from the same sequence.
const (
	partShared uint64 = iota + 1
	partDigits
	partUnique
)

// Generator builds the prompts of one profile under one seed. It is
// immutable after NewGenerator and safe for concurrent use.
type Generator struct {
	profile Profile
	words   []string
	fixed   int
	seed    uint64
	digits  []string // words[perm[d]] spells digit d of the index
	system  string   // the shared system message, built once
}

// NewGenerator returns a generator for p. fixed is the profile's measured
// fixed-token count (template plus leads); words must be the verified
// single-token list.
func NewGenerator(p *Profile, words []string, fixed int, seed uint64) (*Generator, error) {
	switch {
	case len(words) < minWords:
		return nil, fmt.Errorf("prompts: %d words, need at least %d", len(words), minWords)
	case p.UniqueWords < indexWords:
		return nil, fmt.Errorf("prompts: profile %q has %d unique words, need at least %d", p.Name, p.UniqueWords, indexWords)
	case p.SystemLead == "" && p.SharedWords > 0:
		return nil, fmt.Errorf("prompts: profile %q has shared words but no system message", p.Name)
	case fixed < 0:
		return nil, fmt.Errorf("prompts: negative fixed-token count %d", fixed)
	}
	g := &Generator{profile: *p, words: words, fixed: fixed, seed: seed}
	perm := rngFor(seed, partDigits, 0).Perm(len(words))
	g.digits = make([]string, len(words))
	for d, i := range perm {
		g.digits[d] = words[i]
	}
	if p.SystemLead != "" {
		var b strings.Builder
		b.WriteString(p.SystemLead)
		g.appendRandom(&b, rngFor(seed, partShared, 0), p.SharedWords)
		g.system = b.String()
	}
	return g, nil
}

// Profile returns the generator's profile.
func (g *Generator) Profile() Profile { return g.profile }

// PromptTokens is the predicted prompt_tokens of every request: the fixed
// tokens plus one token per body word.
func (g *Generator) PromptTokens() int { return g.fixed + g.profile.BodyWords() }

// Capacity is the number of distinct request indexes: len(words)^indexWords.
func (g *Generator) Capacity() uint64 {
	n := uint64(len(g.words))
	return n * n * n
}

// Messages returns the chat messages for request index. Different indexes
// give different user messages, differing from the first word after the
// lead. It panics if index >= Capacity(), which is at least 10^9.
func (g *Generator) Messages(index uint64) []openai.Message {
	if index >= g.Capacity() {
		panic(fmt.Sprintf("prompts: request index %d exceeds capacity %d", index, g.Capacity()))
	}
	var b strings.Builder
	b.WriteString(g.profile.UserLead)
	base, rest := uint64(len(g.digits)), index
	for range indexWords { // least significant digit first
		b.WriteByte(' ')
		b.WriteString(g.digits[rest%base])
		rest /= base
	}
	g.appendRandom(&b, rngFor(g.seed, partUnique, index), g.profile.UniqueWords-indexWords)

	msgs := make([]openai.Message, 0, 2)
	if g.system != "" {
		msgs = append(msgs, openai.Message{Role: "system", Content: g.system})
	}
	return append(msgs, openai.Message{Role: "user", Content: b.String()})
}

// Request returns the chat request for index: the profile's output length
// held fixed with ignore_eos unless naturalStop, and explicit greedy
// sampling.
func (g *Generator) Request(model string, index uint64, naturalStop bool) *openai.Request {
	temp, rep := Temperature, RepetitionPenalty
	return &openai.Request{
		Model:             model,
		Messages:          g.Messages(index),
		MaxTokens:         g.profile.MaxTokens,
		IgnoreEOS:         !naturalStop,
		Temperature:       &temp,
		RepetitionPenalty: &rep,
	}
}

func (g *Generator) appendRandom(b *strings.Builder, rng *rand.Rand, n int) {
	for range n {
		b.WriteByte(' ')
		b.WriteString(g.words[rng.IntN(len(g.words))])
	}
}

// rngFor derives an independent, reproducible stream for (seed, part, i).
// PCG is specified by math/rand/v2 and does not change between Go releases,
// so the same seed gives the same prompts on any build.
func rngFor(seed, part, i uint64) *rand.Rand {
	return rand.New(rand.NewPCG(splitmix64(seed^splitmix64(part)), splitmix64(i))) //nolint:gosec // G404: reproducible benchmark text needs a seeded PRNG, not a secret
}

// splitmix64 is the SplitMix64 finaliser: a bijective mix that spreads
// nearby inputs (seed 1 vs 2, index 7 vs 8) across all 64 bits.
func splitmix64(x uint64) uint64 {
	x += 0x9e3779b97f4a7c15
	x = (x ^ (x >> 30)) * 0xbf58476d1ce4e5b9
	x = (x ^ (x >> 27)) * 0x94d049bb133111eb
	return x ^ (x >> 31)
}
