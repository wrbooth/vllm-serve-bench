package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptrace"
	"strings"
	"sync"
	"testing"
	"time"
)

var t0 = time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)

// stepClock returns t0, t0+10ms, t0+20ms, ... on successive calls, so every
// clock read in Stream lands on a known instant. Safe for concurrent use
// because Stream reads it from the transport's write goroutine too.
type stepClock struct {
	mu sync.Mutex
	n  int
}

func (c *stepClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := t0.Add(time.Duration(c.n) * 10 * time.Millisecond)
	c.n++
	return t
}

func at(ms int) time.Time { return t0.Add(time.Duration(ms) * time.Millisecond) }

// stubTransport answers every request with a canned status and body, without
// a network. Like net/http's transport, it reports the request body as
// written (httptrace WroteRequest) before returning the response.
type stubTransport struct {
	status int
	body   string
	err    error // returned instead of a response, after WroteRequest

	mu      sync.Mutex
	gotReq  *http.Request
	gotBody []byte
}

func (s *stubTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	b, _ := io.ReadAll(req.Body)
	s.mu.Lock()
	s.gotReq, s.gotBody = req, b
	s.mu.Unlock()
	if tr := httptrace.ContextClientTrace(req.Context()); tr != nil && tr.WroteRequest != nil {
		tr.WroteRequest(httptrace.WroteRequestInfo{})
	}
	if s.err != nil {
		return nil, s.err
	}
	return &http.Response{
		StatusCode: s.status,
		Body:       io.NopCloser(strings.NewReader(s.body)),
		Header:     http.Header{"Content-Type": {"text/event-stream"}},
		Request:    req,
	}, nil
}

// Chunk shapes captured from vLLM 0.29.0 (Qwen2.5-0.5B-Instruct), including
// the fields the client does not read, which decoding must ignore.
const (
	roleChunk     = `{"id":"chatcmpl-1","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{"role":"assistant","content":""},"logprobs":null,"finish_reason":null}],"prompt_token_ids":null,"prompt_text":null}`
	hiChunk       = `{"id":"chatcmpl-1","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{"content":"Hi"},"logprobs":null,"finish_reason":null,"token_ids":null}]}`
	emptyChunk    = `{"id":"chatcmpl-1","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{"content":""},"logprobs":null,"finish_reason":null}]}`
	bangLastChunk = `{"id":"chatcmpl-1","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{"content":"!"},"logprobs":null,"finish_reason":"length","stop_reason":null,"token_ids":null}]}`
	usageChunk    = `{"id":"chatcmpl-1","object":"chat.completion.chunk","created":1,"model":"m","choices":[],"usage":{"prompt_tokens":32,"total_tokens":35,"completion_tokens":3},"system_fingerprint":"vllm-0.29.0"}`
)

func sse(events ...string) string {
	var b strings.Builder
	for _, e := range events {
		b.WriteString("data: " + e + "\n\n")
	}
	return b.String()
}

func newStubClient(st *stubTransport) *Client {
	c := &stepClock{}
	return &Client{BaseURL: "http://stub/", HTTP: &http.Client{Transport: st}, Clock: c.now}
}

// Clock reads in a successful Stream, with stepClock: #0 attempt start (0 ms),
// #1 WroteRequest (10 ms), then one per event in order.
func TestStreamRecordsTimingFacts(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		body string
		want Result
	}{
		{
			// Events: role 20, "Hi" 30, "" 40, "!" 50, usage 60, [DONE] 70.
			// The role chunk arrives first but carries no content: timing it
			// as the first token would put FirstContent at 20 instead of 30.
			name: "FirstContentSkipsRoleAndEmptyDeltas",
			body: sse(roleChunk, hiChunk, emptyChunk, bangLastChunk, usageChunk, "[DONE]"),
			want: Result{
				Send: at(10), FirstContent: at(30), LastContent: at(50), Done: at(70),
				ContentChunks: 2, PromptTokens: 32, CompletionTokens: 3, FinishReason: "length", Status: 200,
			},
		},
		{
			// Keep-alive comments are not events, so they take no clock read:
			// role 20, "Hi" 30, usage 40, [DONE] 50.
			name: "CommentsTakeNoClockRead",
			body: ": ping\n\n" + sse(roleChunk) + ": ping\n\n" + sse(hiChunk, usageChunk, "[DONE]"),
			want: Result{
				Send: at(10), FirstContent: at(30), LastContent: at(30), Done: at(50),
				ContentChunks: 1, PromptTokens: 32, CompletionTokens: 3, Status: 200,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			st := &stubTransport{status: 200, body: tt.body}
			got := newStubClient(st).Stream(context.Background(), &Request{Model: "m"})
			if got != tt.want {
				t.Errorf("Stream =\n  %+v\nwant\n  %+v", got, tt.want)
			}
		})
	}
}

func TestStreamFailuresAreErrorsWithPartialFacts(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		st      *stubTransport
		wantErr func(error) bool
		want    Result // compared with Err cleared
	}{
		{
			name:    "BodyEndsBeforeDone",
			st:      &stubTransport{status: 200, body: sse(roleChunk, hiChunk, usageChunk)},
			wantErr: func(err error) bool { return errors.Is(err, ErrNoDone) },
			// role 20, "Hi" 30, usage 40; no Done.
			want: Result{Send: at(10), FirstContent: at(30), LastContent: at(30), ContentChunks: 1, PromptTokens: 32, CompletionTokens: 3, Status: 200},
		},
		{
			name:    "BodyTruncatedMidEvent",
			st:      &stubTransport{status: 200, body: sse(roleChunk, hiChunk) + "data: {\"choi"},
			wantErr: func(err error) bool { return errors.Is(err, io.ErrUnexpectedEOF) },
			want:    Result{Send: at(10), FirstContent: at(30), LastContent: at(30), ContentChunks: 1, Status: 200},
		},
		{
			name:    "DoneWithoutUsage",
			st:      &stubTransport{status: 200, body: sse(roleChunk, hiChunk, "[DONE]")},
			wantErr: func(err error) bool { return errors.Is(err, ErrNoUsage) },
			want:    Result{Send: at(10), FirstContent: at(30), LastContent: at(30), Done: at(40), ContentChunks: 1, Status: 200},
		},
		{
			name:    "DoneWithoutContent",
			st:      &stubTransport{status: 200, body: sse(roleChunk, emptyChunk, usageChunk, "[DONE]")},
			wantErr: func(err error) bool { return errors.Is(err, ErrNoContent) },
			want:    Result{Send: at(10), Done: at(50), PromptTokens: 32, CompletionTokens: 3, Status: 200},
		},
		{
			name:    "MalformedChunk",
			st:      &stubTransport{status: 200, body: sse(roleChunk, "{not json", "[DONE]")},
			wantErr: func(err error) bool { var se *json.SyntaxError; return errors.As(err, &se) },
			want:    Result{Send: at(10), Status: 200},
		},
		{
			name:    "ErrorObjectInsideStream",
			st:      &stubTransport{status: 200, body: sse(roleChunk, hiChunk, `{"error":{"message":"engine dead","type":"InternalServerError","code":500}}`)},
			wantErr: func(err error) bool { var se *StreamError; return errors.As(err, &se) && se.Message == "engine dead" },
			want:    Result{Send: at(10), FirstContent: at(30), LastContent: at(30), ContentChunks: 1, Status: 200},
		},
		{
			// Captured from vLLM 0.29.0 for an unknown model with stream=true:
			// a plain JSON body, not SSE.
			name: "NotFoundCarriesStatusAndServerMessage",
			st: &stubTransport{status: 404, body: `{"error":{"message":"The model ` + "`nope`" +
				` does not exist.","type":"NotFoundError","param":"model","code":404}}`},
			wantErr: func(err error) bool {
				var se *StatusError
				return errors.As(err, &se) && se.Code == 404 && se.Message == "The model `nope` does not exist."
			},
			want: Result{Send: at(10), Status: 404},
		},
		{
			name: "FlatVLLMErrorBody",
			st:   &stubTransport{status: 400, body: `{"object":"error","message":"max_tokens too large","type":"BadRequestError","code":400}`},
			wantErr: func(err error) bool {
				var se *StatusError
				return errors.As(err, &se) && se.Code == 400 && se.Message == "max_tokens too large"
			},
			want: Result{Send: at(10), Status: 400},
		},
		{
			name: "NonJSONErrorBodyIsKeptVerbatim",
			st:   &stubTransport{status: 502, body: "  bad gateway\n"},
			wantErr: func(err error) bool {
				var se *StatusError
				return errors.As(err, &se) && se.Code == 502 && se.Message == "bad gateway" && err.Error() == "http 502: bad gateway"
			},
			want: Result{Send: at(10), Status: 502},
		},
		{
			name: "HugeErrorBodyIsTruncated",
			st:   &stubTransport{status: 500, body: strings.Repeat("e", 10_000)},
			wantErr: func(err error) bool {
				var se *StatusError
				return errors.As(err, &se) && se.Message == strings.Repeat("e", 200)+"..."
			},
			want: Result{Send: at(10), Status: 500},
		},
		{
			// The write completed (WroteRequest at 10 ms) but no response came.
			name:    "TransportErrorAfterWrite",
			st:      &stubTransport{err: errors.New("connection refused")},
			wantErr: func(err error) bool { return err != nil && strings.Contains(err.Error(), "connection refused") },
			want:    Result{Send: at(10)},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := newStubClient(tt.st).Stream(context.Background(), &Request{Model: "m"})
			if !tt.wantErr(got.Err) {
				t.Errorf("Err = %v (%T), not the expected error", got.Err, got.Err)
			}
			got.Err = nil
			if got != tt.want {
				t.Errorf("Stream =\n  %+v\nwant\n  %+v", got, tt.want)
			}
		})
	}
}

// If the request never reaches the transport, WroteRequest never fires and
// Send falls back to the attempt start, so the row can still be windowed.
func TestStreamSendFallsBackToAttemptStartWhenNothingWasWritten(t *testing.T) {
	t.Parallel()
	c := &Client{BaseURL: "://bad url", Clock: (&stepClock{}).now}
	got := c.Stream(context.Background(), &Request{Model: "m"})
	if got.Err == nil || !got.Send.Equal(at(0)) {
		t.Errorf("Stream = %+v; want an error and Send at the attempt start", got)
	}
}

func TestStreamHonoursContextCancellation(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c := &Client{BaseURL: "http://127.0.0.1:1", Clock: (&stepClock{}).now}
	got := c.Stream(ctx, &Request{Model: "m"})
	if !errors.Is(got.Err, context.Canceled) {
		t.Errorf("Err = %v, want context.Canceled", got.Err)
	}
}

func TestStreamSendsStreamingRequestWithUsage(t *testing.T) {
	t.Parallel()
	temp, topK := 0.0, 1
	st := &stubTransport{status: 200, body: sse(hiChunk, usageChunk, "[DONE]")}
	req := Request{
		Model:       "Qwen/Qwen2.5-0.5B-Instruct",
		Messages:    []Message{{Role: "user", Content: "Say hi."}},
		MaxTokens:   128,
		IgnoreEOS:   true,
		Temperature: &temp,
		TopK:        &topK,
	}
	if res := newStubClient(st).Stream(context.Background(), &req); res.Err != nil {
		t.Fatalf("Stream: %v", res.Err)
	}

	if st.gotReq.Method != http.MethodPost || st.gotReq.URL.String() != "http://stub/v1/chat/completions" {
		t.Errorf("request = %s %s, want POST http://stub/v1/chat/completions", st.gotReq.Method, st.gotReq.URL)
	}
	if ct := st.gotReq.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q", ct)
	}
	var got map[string]any
	if err := json.Unmarshal(st.gotBody, &got); err != nil {
		t.Fatal(err)
	}
	// Exact wire body: stream and include_usage always on; temperature 0 is
	// sent (a pointer, so an explicit zero is not omitted); unset top_p and
	// repetition_penalty are absent so the server default applies.
	want := map[string]any{
		"model":          "Qwen/Qwen2.5-0.5B-Instruct",
		"messages":       []any{map[string]any{"role": "user", "content": "Say hi."}},
		"max_tokens":     float64(128),
		"ignore_eos":     true,
		"temperature":    float64(0),
		"top_k":          float64(1),
		"stream":         true,
		"stream_options": map[string]any{"include_usage": true},
	}
	gotJSON, _ := json.Marshal(got)
	wantJSON, _ := json.Marshal(want)
	if !bytes.Equal(gotJSON, wantJSON) {
		t.Errorf("request body\n  %s\nwant\n  %s", gotJSON, wantJSON)
	}
}

func TestClientNowDefaultsToWallClock(t *testing.T) {
	t.Parallel()
	before := time.Now()
	got := (&Client{}).Now()
	if got.Before(before) || time.Since(got) > time.Minute {
		t.Errorf("Now() = %v, want about %v", got, before)
	}
}
