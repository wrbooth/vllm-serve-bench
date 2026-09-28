package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/wrbooth/vllm-serve-bench/internal/openai"
	"github.com/wrbooth/vllm-serve-bench/internal/prompts"
)

// engineTokenizer counts with the engine's /tokenize endpoint.
type engineTokenizer struct {
	c     *openai.Client
	model string
}

func (e engineTokenizer) CountText(ctx context.Context, text string) (int, error) {
	no := false
	r, err := e.c.Tokenize(ctx, &openai.TokenizeRequest{Model: e.model, Prompt: text, AddSpecialTokens: &no})
	return r.Count, err
}

func (e engineTokenizer) CountChat(ctx context.Context, msgs []openai.Message) (int, error) {
	r, err := e.c.Tokenize(ctx, &openai.TokenizeRequest{Model: e.model, Messages: msgs})
	return r.Count, err
}

type verifyFlags struct {
	baseURL, model, write string
	seed                  uint64
	samples, batch        int
}

// verifyCmd is `bench prompts verify`: it checks, against a live engine, the
// two facts the prompt-token prediction rests on (every word is one token;
// each profile's fixed-token count is right) and exits 1 on any mismatch.
// With --write DIR it instead prunes the words that fail, measures the fixed
// counts, confirms them on the samples, and writes words.txt and
// fixed_tokens.json into DIR (internal/prompts, to be committed).
func verifyCmd(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("bench prompts verify", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var f verifyFlags
	fs.StringVar(&f.baseURL, "base-url", "http://localhost:8000", "engine root URL")
	fs.StringVar(&f.model, "model", "Qwen/Qwen2.5-7B-Instruct", "served model name")
	fs.Uint64Var(&f.seed, "seed", 1, "seed for the sampled prompts")
	fs.IntVar(&f.samples, "samples", 20, "prompts checked per profile")
	fs.IntVar(&f.batch, "batch", 256, "words per /tokenize request")
	fs.StringVar(&f.write, "write", "", "prune failing words, measure fixed counts, and write both into this directory")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if err := verify(ctx, &f, stdout, stderr); err != nil {
		_, _ = fmt.Fprintln(stderr, "bench prompts verify:", err)
		return 1
	}
	return 0
}

// errVerifyFailed is returned after the mismatches have been printed.
var errVerifyFailed = errors.New("verification failed")

func verify(ctx context.Context, f *verifyFlags, stdout, stderr io.Writer) error {
	v, err := prompts.Embedded()
	if err != nil {
		return err
	}
	tk := engineTokenizer{c: &openai.Client{BaseURL: f.baseURL}, model: f.model}

	bad, err := prompts.CheckWords(ctx, tk, v.Words, f.batch)
	if err != nil {
		return fmt.Errorf("check words: %w", err)
	}
	_, _ = fmt.Fprintf(stdout, "words: %d checked, %d not a single token\n", len(v.Words), len(bad))
	if len(bad) > 0 {
		_, _ = fmt.Fprintf(stdout, "  not single-token: %s\n", strings.Join(bad, " "))
	}
	words := slices.DeleteFunc(slices.Clone(v.Words), func(w string) bool { return slices.Contains(bad, w) })
	_, _ = fmt.Fprintf(stdout, "words: %d kept, sha256 %s\n", len(words), prompts.WordsSHA256(words))

	fixed := v.Fixed
	fixed.Profiles = map[string]int{}
	for name, n := range v.Fixed.Profiles {
		fixed.Profiles[name] = n
	}
	if f.write != "" {
		for _, name := range prompts.ProfileNames() {
			p, _ := prompts.Lookup(name) // names come from the table
			if fixed.Profiles[name], err = prompts.MeasureFixed(ctx, tk, &p, words, f.seed); err != nil {
				return fmt.Errorf("measure %s: %w", name, err)
			}
		}
		fixed.Model = f.model
		fixed.Source = fmt.Sprintf("measured with bench prompts verify --write (bench %s) against the engine's /tokenize on %s",
			version, time.Now().UTC().Format(time.DateOnly))
	}

	mismatches, err := checkProfiles(ctx, tk, words, &fixed, f, stdout)
	if err != nil {
		return err
	}
	switch {
	case mismatches > 0:
		// In --write mode a mismatch means the fixed count is not constant
		// across prompts, so the design is wrong; nothing is written.
		return fmt.Errorf("%w: %d prompts counted differently from the prediction", errVerifyFailed, mismatches)
	case f.write != "":
		return writeVocab(f.write, words, &fixed, stdout)
	case len(bad) > 0:
		return fmt.Errorf("%w: %d words are not a single token; rerun with --write internal/prompts to prune them", errVerifyFailed, len(bad))
	}
	_, _ = fmt.Fprintln(stderr, "ok")
	return nil
}

func checkProfiles(ctx context.Context, tk prompts.Tokenizer, words []string, fixed *prompts.Fixed, f *verifyFlags, stdout io.Writer) (int, error) {
	total := 0
	for _, name := range prompts.ProfileNames() {
		p, _ := prompts.Lookup(name) // names come from the table
		g, err := prompts.NewGenerator(&p, words, fixed.Profiles[name], f.seed)
		if err != nil {
			return 0, err
		}
		mm, err := prompts.CheckPrompts(ctx, tk, g, prompts.SampleIndexes(g, f.samples))
		if err != nil {
			return 0, fmt.Errorf("check %s prompts: %w", name, err)
		}
		_, _ = fmt.Fprintf(stdout, "%s: %d prompts, predicted %d tokens (fixed %d), %d mismatches\n",
			name, f.samples, g.PromptTokens(), fixed.Profiles[name], len(mm))
		for _, m := range mm {
			_, _ = fmt.Fprintf(stdout, "  index %d: engine counted %d, predicted %d\n", m.Index, m.Counted, m.Predicted)
		}
		total += len(mm)
	}
	return total, nil
}

func writeVocab(dir string, words []string, fixed *prompts.Fixed, stdout io.Writer) error {
	header := fmt.Sprintf("Words that are exactly one token with a leading space for %s,\n"+
		"confirmed against the engine's /tokenize by `bench prompts verify --write`.\n"+
		"Regenerate with that command; do not edit by hand.", fixed.Model)
	var wb, fb strings.Builder
	if err := prompts.WriteWords(&wb, header, words); err != nil {
		return err
	}
	if err := prompts.WriteFixed(&fb, fixed); err != nil {
		return err
	}
	for name, body := range map[string]string{"words.txt": wb.String(), "fixed_tokens.json": fb.String()} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil { //nolint:gosec // G306: a committed source file, meant to be world-readable
			return err
		}
		_, _ = fmt.Fprintln(stdout, "wrote", path)
	}
	return nil
}
