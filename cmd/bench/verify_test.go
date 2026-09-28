package main

import (
	"bytes"
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/wrbooth/vllm-serve-bench/internal/fakeserver"
	"github.com/wrbooth/vllm-serve-bench/internal/prompts"
)

// leadFields is what the fake's toy tokenizer counts for a profile's fixed
// text: the fields of its leads.
func leadFields(t *testing.T, name string) int {
	t.Helper()
	p, err := prompts.Lookup(name)
	if err != nil {
		t.Fatal(err)
	}
	return len(strings.Fields(p.SystemLead)) + len(strings.Fields(p.UserLead))
}

// calibratedFake returns a fake whose toy tokenizer agrees with the
// embedded fixed-token counts, whatever they are, so these tests do not
// change when the engine re-measures them:
//
//	interactive fixed = ChatOverhead + lead fields
//	throughput  fixed = ChatOverhead + fields(DefaultSystem) + lead fields
func calibratedFake(t *testing.T) *fakeserver.Server {
	t.Helper()
	v, err := prompts.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	overhead := v.Fixed.Profiles["interactive"] - leadFields(t, "interactive")
	defaults := v.Fixed.Profiles["throughput"] - overhead - leadFields(t, "throughput")
	if overhead < 0 || defaults < 0 {
		t.Fatalf("embedded fixed counts %v cannot be modelled by the toy tokenizer", v.Fixed.Profiles)
	}
	metrics, err := os.ReadFile("../../internal/sampler/testdata/vllm-0.29.0-metrics.txt")
	if err != nil {
		t.Fatal(err)
	}
	return &fakeserver.Server{
		Metrics:       string(metrics), // a real scrape: KV pool 241680, prefix caching on
		Models:        []string{"fake"},
		ChatOverhead:  overhead,
		DefaultSystem: strings.Repeat("sys ", defaults),
		CountPrompt:   true,
	}
}

func runBench(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errb bytes.Buffer
	code = dispatch(context.Background(), args, &out, &errb)
	return code, out.String(), errb.String()
}

func serve(t *testing.T, srv *fakeserver.Server) string {
	t.Helper()
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	return ts.URL
}

func TestVerifyPassesWhenTheEngineAgreesWithThePrediction(t *testing.T) {
	t.Parallel()
	url := serve(t, calibratedFake(t))
	code, out, errs := runBench(t, "prompts", "verify", "--base-url", url, "--model", "fake", "--samples", "4")
	if code != 0 {
		t.Fatalf("exit %d, stdout %q stderr %q; want 0", code, out, errs)
	}
	for _, want := range []string{"0 not a single token", "interactive: 4 prompts", "throughput: 4 prompts", "0 mismatches"} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout %q lacks %q", out, want)
		}
	}
}

func TestVerifyFailsOnAMultiTokenWordOrAWrongFixedCount(t *testing.T) {
	t.Parallel()
	v, err := prompts.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		mutate func(*fakeserver.Server)
		want   string
	}{
		{
			name:   "MultiTokenWord",
			mutate: func(s *fakeserver.Server) { s.MultiToken = map[string]int{v.Words[3]: 2} },
			want:   "not single-token: " + v.Words[3] + "\n",
		},
		{
			// One more template token than the embedded count predicts.
			name:   "FixedCountOffByOne",
			mutate: func(s *fakeserver.Server) { s.ChatOverhead++ },
			want:   "engine counted",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			srv := calibratedFake(t)
			tt.mutate(srv)
			code, out, errs := runBench(t, "prompts", "verify", "--base-url", serve(t, srv), "--model", "fake", "--samples", "2")
			if code != 1 || !strings.Contains(out, tt.want) || !strings.Contains(errs, "verification failed") {
				t.Errorf("exit %d, stdout %q, stderr %q; want 1 with %q", code, out, errs, tt.want)
			}
		})
	}
}

// --write drops the words the engine splits and re-measures the fixed
// counts. With ChatOverhead 5 and no default system prompt the toy
// tokenizer gives interactive 5 + 16 lead fields = 21 and throughput
// 5 + 0 + 8 = 13 (lead fields spelled out in internal/prompts' tests).
func TestVerifyWritePrunesWordsAndWritesMeasuredCounts(t *testing.T) {
	t.Parallel()
	v, err := prompts.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	dropped := []string{v.Words[0], v.Words[len(v.Words)-1]}
	srv := &fakeserver.Server{ChatOverhead: 5, MultiToken: map[string]int{dropped[0]: 2, dropped[1]: 3}}
	dir := t.TempDir()
	code, out, errs := runBench(t, "prompts", "verify", "--base-url", serve(t, srv), "--model", "fake", "--samples", "3", "--write", dir)
	if code != 0 {
		t.Fatalf("exit %d, stdout %q stderr %q; want 0", code, out, errs)
	}

	f, err := os.Open(filepath.Join(dir, "words.txt"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	got, err := prompts.ParseWords(f)
	if err != nil {
		t.Fatal(err)
	}
	want := slices.DeleteFunc(slices.Clone(v.Words), func(w string) bool { return slices.Contains(dropped, w) })
	if !slices.Equal(got, want) {
		t.Errorf("words.txt has %d words, want the %d embedded minus %v", len(got), len(want), dropped)
	}

	fixed, err := os.ReadFile(filepath.Join(dir, "fixed_tokens.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"interactive": 21`, `"throughput": 13`, `"model": "fake"`, "verify --write"} {
		if !strings.Contains(string(fixed), want) {
			t.Errorf("fixed_tokens.json %s lacks %q", fixed, want)
		}
	}
}

func TestVerifyReportsAnUnreachableEngine(t *testing.T) {
	t.Parallel()
	code, _, errs := runBench(t, "prompts", "verify", "--base-url", "http://127.0.0.1:1")
	if code != 1 || !strings.Contains(errs, "check words") {
		t.Errorf("exit %d stderr %q; want 1 naming the word check", code, errs)
	}
}

func TestDispatchUsageErrorsExitTwo(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		args []string
		code int
		out  string
	}{
		{name: "NoArgs", args: nil, code: 2},
		{name: "UnknownSubcommand", args: []string{"nope"}, code: 2},
		{name: "PromptsWithoutVerify", args: []string{"prompts"}, code: 2},
		{name: "VerifyBadFlag", args: []string{"prompts", "verify", "--nope"}, code: 2},
		{name: "Version", args: []string{"version"}, code: 0, out: "dev\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			code, out, _ := runBench(t, tt.args...)
			if code != tt.code || out != tt.out {
				t.Errorf("exit %d stdout %q; want %d %q", code, out, tt.code, tt.out)
			}
		})
	}
}
