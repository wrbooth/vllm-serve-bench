// Package fakeserver is an in-process OpenAI-compatible streaming server for
// tests. It serves POST /v1/chat/completions with stream=true the way vLLM
// 0.29.0 does (role-only chunk first, one chunk per content token,
// finish_reason on the last content chunk, a usage-only chunk with empty
// choices, then [DONE]) with configurable timing and failure modes, so the
// client and load generator can be tested on loopback without a GPU.
//
// It also serves GET /v1/models (readiness) and vLLM's POST /tokenize with a
// toy tokenizer: one token per whitespace-separated field, except the words
// in MultiToken, plus ChatOverhead for a chat. That is enough to test the
// prompt checks without the real tokenizer.
//
// Use it with httptest: srv := &fakeserver.Server{...};
// ts := httptest.NewServer(srv).
package fakeserver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

// Server is an http.Handler. Configure the exported fields before serving and
// do not change them afterwards; the counters are safe for concurrent use.
// A Server must not be copied after first use.
type Server struct {
	// FirstTokenDelay is the time between reading the request and writing
	// the first content chunk (the role chunk goes out immediately, as in
	// vLLM, so a client that times the first chunk measures ~0).
	FirstTokenDelay time.Duration
	// InterTokenInterval is the time between consecutive content chunks.
	InterTokenInterval time.Duration
	// Tokens is the number of content chunks (= completion_tokens). Zero
	// means the request's max_tokens, or 16 if that is unset.
	Tokens int
	// PromptTokens is reported in the usage chunk.
	PromptTokens int

	// Status, if non-zero and not 200, is returned for every request with a
	// vLLM-style JSON error body and no stream.
	Status int
	// FailEvery, if > 0, answers every FailEvery-th request (1-based) with
	// HTTP 500 instead of a stream.
	FailEvery int
	// AbortAfter, if > 0, aborts the connection after that many content
	// chunks: no usage chunk, no [DONE], no clean end of body.
	AbortAfter int
	// SplitWrites writes each event in two flushed halves, cut mid-line, so
	// the client sees events split across reads over real TCP.
	SplitWrites bool

	// Models is what GET /v1/models lists; empty means ["fake"].
	Models []string
	// UnreadyFor answers the first UnreadyFor requests to /v1/models with
	// 503, like an engine that is still loading.
	UnreadyFor int

	// ChatOverhead is what the toy tokenizer adds to a chat (the template).
	ChatOverhead int
	// MultiToken maps words that the toy tokenizer counts as more than one
	// token to their count.
	MultiToken map[string]int
	// CountPrompt reports usage.prompt_tokens as the toy tokenizer's count
	// of the request's messages instead of PromptTokens.
	CountPrompt bool

	requests    atomic.Int64
	inFlight    atomic.Int64
	maxInFlight atomic.Int64
	modelsCalls atomic.Int64
}

// Requests returns the number of chat requests received so far (every
// request except those to /v1/models and /tokenize).
func (s *Server) Requests() int { return int(s.requests.Load()) }

// MaxInFlight returns the highest number of requests served concurrently.
func (s *Server) MaxInFlight() int { return int(s.maxInFlight.Load()) }

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type request struct {
	Model         string    `json:"model"`
	Messages      []message `json:"messages"`
	MaxTokens     int       `json:"max_tokens"`
	Stream        bool      `json:"stream"`
	StreamOptions struct {
		IncludeUsage bool `json:"include_usage"`
	} `json:"stream_options"`
}

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/v1/models":
		s.models(w)
		return
	case r.Method == http.MethodPost && r.URL.Path == "/tokenize":
		s.tokenize(w, r)
		return
	}
	s.chat(w, r)
}

// chat serves every request that is not /v1/models or /tokenize, so a
// request to a wrong path is counted and answered 404.
func (s *Server) chat(w http.ResponseWriter, r *http.Request) {
	n := s.requests.Add(1)
	cur := s.inFlight.Add(1)
	defer s.inFlight.Add(-1)
	for {
		prev := s.maxInFlight.Load()
		if cur <= prev || s.maxInFlight.CompareAndSwap(prev, cur) {
			break
		}
	}

	if r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" {
		writeError(w, http.StatusNotFound, "not found: "+r.Method+" "+r.URL.Path)
		return
	}
	var req request
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad json: "+err.Error())
		return
	}
	switch {
	case !req.Stream:
		writeError(w, http.StatusBadRequest, "fakeserver only streams")
		return
	case s.Status != 0 && s.Status != http.StatusOK:
		writeError(w, s.Status, "configured failure")
		return
	case s.FailEvery > 0 && n%int64(s.FailEvery) == 0:
		writeError(w, http.StatusInternalServerError, "configured periodic failure")
		return
	}
	s.stream(w, r, &req)
}

func (s *Server) stream(w http.ResponseWriter, r *http.Request, req *request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "response writer cannot flush")
		return
	}
	tokens := s.Tokens
	if tokens == 0 {
		tokens = req.MaxTokens
	}
	if tokens == 0 {
		tokens = 16
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	send := func(payload string) {
		ev := "data: " + payload + "\n\n"
		if s.SplitWrites {
			half := len(ev) / 2
			_, _ = w.Write([]byte(ev[:half]))
			flusher.Flush()
			ev = ev[half:]
		}
		_, _ = w.Write([]byte(ev))
		flusher.Flush()
	}

	send(chunkJSON(req.Model, `{"role":"assistant","content":""}`, "null"))
	for i := range tokens {
		wait := s.InterTokenInterval
		if i == 0 {
			wait = s.FirstTokenDelay
		}
		if !sleep(r, wait) {
			return
		}
		if s.AbortAfter > 0 && i == s.AbortAfter {
			// Documented way to abort the connection without a clean end.
			panic(http.ErrAbortHandler)
		}
		finish := "null"
		if i == tokens-1 {
			finish = `"length"`
		}
		send(chunkJSON(req.Model, `{"content":"tok"}`, finish))
	}
	if req.StreamOptions.IncludeUsage {
		prompt := s.PromptTokens
		if s.CountPrompt {
			prompt = s.countChat(req.Messages)
		}
		send(fmt.Sprintf(`{"id":"chatcmpl-fake","object":"chat.completion.chunk","created":0,"model":%q,`+
			`"choices":[],"usage":{"prompt_tokens":%d,"total_tokens":%d,"completion_tokens":%d}}`,
			req.Model, prompt, prompt+tokens, tokens))
	}
	send("[DONE]")
}

func (s *Server) models(w http.ResponseWriter) {
	if s.modelsCalls.Add(1) <= int64(s.UnreadyFor) {
		writeError(w, http.StatusServiceUnavailable, "engine loading")
		return
	}
	ids := s.Models
	if len(ids) == 0 {
		ids = []string{"fake"}
	}
	data := make([]map[string]any, len(ids))
	for i, id := range ids {
		data[i] = map[string]any{"id": id, "object": "model", "owned_by": "fakeserver"}
	}
	writeJSON(w, map[string]any{"object": "list", "data": data})
}

// tokenizeRequest is vLLM's /tokenize body: a prompt or chat messages.
type tokenizeRequest struct {
	Prompt   *string   `json:"prompt"`
	Messages []message `json:"messages"`
}

func (s *Server) tokenize(w http.ResponseWriter, r *http.Request) {
	var req tokenizeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad json: "+err.Error())
		return
	}
	var n int
	switch {
	case req.Prompt != nil && req.Messages == nil:
		n = s.countText(*req.Prompt)
	case req.Prompt == nil && req.Messages != nil:
		n = s.countChat(req.Messages)
	default:
		writeError(w, http.StatusBadRequest, "set exactly one of prompt and messages")
		return
	}
	writeJSON(w, map[string]any{"count": n, "max_model_len": 8192, "tokens": make([]int, n), "token_strs": nil})
}

func (s *Server) countText(text string) int {
	n := 0
	for _, word := range strings.Fields(text) {
		if k, ok := s.MultiToken[word]; ok {
			n += k
		} else {
			n++
		}
	}
	return n
}

func (s *Server) countChat(msgs []message) int {
	n := s.ChatOverhead
	for i := range msgs {
		n += s.countText(msgs[i].Content)
	}
	return n
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func chunkJSON(model, delta, finish string) string {
	return fmt.Sprintf(`{"id":"chatcmpl-fake","object":"chat.completion.chunk","created":0,"model":%q,`+
		`"choices":[{"index":0,"delta":%s,"logprobs":null,"finish_reason":%s}]}`, model, delta, finish)
}

// sleep waits d, returning false if the client went away first.
func sleep(r *http.Request, d time.Duration) bool {
	if d <= 0 {
		return true
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-r.Context().Done():
		return false
	}
}

// writeError answers with vLLM's current error shape.
func writeError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	body, _ := json.Marshal(map[string]any{
		"error": map[string]any{"message": msg, "type": http.StatusText(code), "code": code},
	})
	_, _ = w.Write(body)
}
