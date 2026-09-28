package fakeserver

import (
	"bufio"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestServerRejectsRequestsItCannotStream(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		method string
		path   string
		body   string
		want   int
	}{
		{name: "WrongPathIs404", method: http.MethodPost, path: "/v1/completions", body: `{"stream":true}`, want: 404},
		{name: "GetIs404", method: http.MethodGet, path: "/v1/chat/completions", want: 404},
		{name: "MalformedJSONIs400", method: http.MethodPost, path: "/v1/chat/completions", body: `{`, want: 400},
		{name: "NonStreamingIs400", method: http.MethodPost, path: "/v1/chat/completions", body: `{"stream":false}`, want: 400},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rec := httptest.NewRecorder()
			(&Server{}).ServeHTTP(rec, httptest.NewRequest(tt.method, tt.path, strings.NewReader(tt.body)))
			if rec.Code != tt.want || !strings.Contains(rec.Body.String(), `"error"`) {
				t.Errorf("status %d body %q; want %d with an error object", rec.Code, rec.Body.String(), tt.want)
			}
		})
	}
}

// Every FailEvery-th request fails: with FailEvery 3, requests 3 and 6 of 6.
func TestServerFailEveryFailsEveryNthRequest(t *testing.T) {
	t.Parallel()
	s := &Server{FailEvery: 3, Tokens: 1}
	var codes []int
	for range 6 {
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"stream":true}`)))
		codes = append(codes, rec.Code)
	}
	want := []int{200, 200, 500, 200, 200, 500}
	for i := range want {
		if codes[i] != want[i] {
			t.Fatalf("status codes %v, want %v", codes, want)
		}
	}
	if s.Requests() != 6 {
		t.Errorf("Requests() = %d, want 6", s.Requests())
	}
}

// Without an explicit Tokens, the stream length follows max_tokens; the
// usage chunk is sent only when include_usage is requested.
func TestServerStreamShapeFollowsTheRequest(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		body       string
		wantTokens int
		wantUsage  bool
	}{
		{name: "MaxTokensSetsLength", body: `{"stream":true,"max_tokens":3,"stream_options":{"include_usage":true}}`, wantTokens: 3, wantUsage: true},
		{name: "DefaultLengthIs16", body: `{"stream":true}`, wantTokens: 16, wantUsage: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rec := httptest.NewRecorder()
			(&Server{}).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(tt.body)))
			events := dataLines(t, rec.Body)
			// role chunk + content chunks + optional usage chunk + [DONE]
			want := 1 + tt.wantTokens + 1
			if tt.wantUsage {
				want++
			}
			if len(events) != want || events[len(events)-1] != "[DONE]" {
				t.Fatalf("got %d events ending %q, want %d ending [DONE]", len(events), events[len(events)-1], want)
			}
			if !strings.Contains(events[0], `"role":"assistant","content":""`) {
				t.Errorf("first event %q is not the role-only chunk", events[0])
			}
			if hasUsage := strings.Contains(events[len(events)-2], `"choices":[],"usage"`); hasUsage != tt.wantUsage {
				t.Errorf("usage chunk present = %v, want %v", hasUsage, tt.wantUsage)
			}
		})
	}
}

func dataLines(t *testing.T, r io.Reader) []string {
	t.Helper()
	var out []string
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		if d, ok := strings.CutPrefix(sc.Text(), "data: "); ok {
			out = append(out, d)
		}
	}
	return out
}

// The toy tokenizer: one token per field, MultiToken words count their
// value, and a chat adds ChatOverhead.
func TestServerTokenizeCountsWithTheToyTokenizer(t *testing.T) {
	t.Parallel()
	s := &Server{ChatOverhead: 10, MultiToken: map[string]int{"supercalifragilistic": 4}}
	tests := []struct {
		name string
		body string
		want string
		code int
	}{
		// " alpha beta gamma" = 3 fields
		{name: "PromptCountsFields", body: `{"model":"m","prompt":" alpha beta gamma"}`, want: `"count":3`, code: 200},
		// 1 + 4 = 5
		{name: "MultiTokenWordsCountTheirValue", body: `{"prompt":"a supercalifragilistic"}`, want: `"count":5`, code: 200},
		// 10 + 2 ("be" "terse.") + 2 ("Say" "hi.") = 14
		{name: "ChatAddsOverhead", body: `{"messages":[{"role":"system","content":"be terse."},{"role":"user","content":"Say hi."}]}`, want: `"count":14`, code: 200},
		{name: "NeitherIs400", body: `{"model":"m"}`, code: 400},
		{name: "BothIs400", body: `{"prompt":"a","messages":[]}`, code: 400},
		{name: "MalformedIs400", body: `{`, code: 400},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rec := httptest.NewRecorder()
			s.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/tokenize", strings.NewReader(tt.body)))
			if rec.Code != tt.code || !strings.Contains(rec.Body.String(), tt.want) {
				t.Errorf("status %d body %q; want %d containing %q", rec.Code, rec.Body.String(), tt.code, tt.want)
			}
		})
	}
	if s.Requests() != 0 {
		t.Errorf("Requests() = %d; /tokenize must not count as a chat request", s.Requests())
	}
}

func TestServerModelsIsUnavailableForUnreadyForCalls(t *testing.T) {
	t.Parallel()
	s := &Server{UnreadyFor: 2, Models: []string{"a", "b"}}
	var codes []int
	var last string
	for range 3 {
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/models", http.NoBody))
		codes = append(codes, rec.Code)
		last = rec.Body.String()
	}
	if codes[0] != 503 || codes[1] != 503 || codes[2] != 200 {
		t.Errorf("status codes %v, want [503 503 200]", codes)
	}
	if !strings.Contains(last, `"id":"a"`) || !strings.Contains(last, `"id":"b"`) {
		t.Errorf("models body %q does not list a and b", last)
	}
	if s.Requests() != 0 {
		t.Errorf("Requests() = %d; /v1/models must not count as a chat request", s.Requests())
	}
}

// With CountPrompt, usage.prompt_tokens is the toy count of the messages:
// 3 overhead + 2 fields = 5.
func TestServerCountPromptReportsTheToyCountInUsage(t *testing.T) {
	t.Parallel()
	s := &Server{ChatOverhead: 3, CountPrompt: true, PromptTokens: 99, Tokens: 1}
	rec := httptest.NewRecorder()
	body := `{"stream":true,"stream_options":{"include_usage":true},"messages":[{"role":"user","content":"two words"}]}`
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body)))
	if !strings.Contains(rec.Body.String(), `"prompt_tokens":5,`) {
		t.Errorf("stream %q does not report prompt_tokens 5", rec.Body.String())
	}
}
