package prompts

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	_ "embed" // the word list and fixed-token counts ship inside the binary
)

// The word list and the fixed-token counts are embedded so the binary is
// self-contained: a run on the GPU host needs no files besides the binary.
// `bench prompts verify --write` regenerates both from a live engine.
var (
	//go:embed words.txt
	wordsFile []byte
	//go:embed fixed_tokens.json
	fixedFile []byte
)

// Fixed is fixed_tokens.json: per profile, the prompt tokens that are not
// body words (the chat template plus the profile's lead text), and where
// the numbers came from.
type Fixed struct {
	// Model is the model whose tokenizer and chat template were measured.
	Model string `json:"model"`
	// Source says how the counts were obtained, so a run on unconfirmed
	// counts is visible in its config.json.
	Source   string         `json:"source"`
	Profiles map[string]int `json:"profiles"`
}

// Vocab is the embedded word list with its fixed-token counts.
type Vocab struct {
	Words []string
	Fixed Fixed
}

// Embedded parses the word list and fixed-token counts built into the binary.
func Embedded() (*Vocab, error) {
	words, err := ParseWords(bytes.NewReader(wordsFile))
	if err != nil {
		return nil, fmt.Errorf("embedded words.txt: %w", err)
	}
	var f Fixed
	if err := json.Unmarshal(fixedFile, &f); err != nil {
		return nil, fmt.Errorf("embedded fixed_tokens.json: %w", err)
	}
	return &Vocab{Words: words, Fixed: f}, nil
}

// Generator returns the generator for the named profile and seed, using the
// embedded list and that profile's embedded fixed-token count.
func (v *Vocab) Generator(profile string, seed uint64) (*Generator, error) {
	p, err := Lookup(profile)
	if err != nil {
		return nil, err
	}
	fixed, ok := v.Fixed.Profiles[profile]
	if !ok {
		return nil, fmt.Errorf("prompts: no fixed-token count for profile %q", profile)
	}
	return NewGenerator(&p, v.Words, fixed, seed)
}

// WordsSHA256 identifies the word list: the prompts of a seed depend on it,
// so config.json records it.
func WordsSHA256(words []string) string {
	sum := sha256.Sum256([]byte(strings.Join(words, "\n")))
	return hex.EncodeToString(sum[:])
}

// ParseWords reads a word list: one word per line, lowercase ASCII letters
// only, no duplicates. Blank lines and lines starting with '#' are skipped.
func ParseWords(r io.Reader) ([]string, error) {
	var words []string
	seen := map[string]bool{}
	sc := bufio.NewScanner(r)
	for line := 1; sc.Scan(); line++ {
		w := strings.TrimSpace(sc.Text())
		if w == "" || strings.HasPrefix(w, "#") {
			continue
		}
		if !isLowerWord(w) {
			return nil, fmt.Errorf("line %d: %q is not a lowercase ASCII word", line, w)
		}
		if seen[w] {
			return nil, fmt.Errorf("line %d: duplicate word %q", line, w)
		}
		seen[w] = true
		words = append(words, w)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(words) == 0 {
		return nil, errors.New("empty word list")
	}
	return words, nil
}

// WriteWords writes a word list that ParseWords reads back unchanged: a
// comment header, then the words sorted, one per line.
func WriteWords(w io.Writer, header string, words []string) error {
	sorted := slices.Clone(words)
	slices.Sort(sorted)
	var b strings.Builder
	for line := range strings.SplitSeq(strings.TrimSpace(header), "\n") {
		b.WriteString("# " + line + "\n")
	}
	for _, word := range sorted {
		b.WriteString(word + "\n")
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// WriteFixed writes fixed_tokens.json.
func WriteFixed(w io.Writer, f *Fixed) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(f)
}

func isLowerWord(w string) bool {
	for i := range len(w) {
		if w[i] < 'a' || w[i] > 'z' {
			return false
		}
	}
	return w != ""
}
