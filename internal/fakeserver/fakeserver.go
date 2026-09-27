// Package fakeserver is an in-process OpenAI-compatible streaming server for
// tests. It serves POST /v1/chat/completions with stream=true the way vLLM
// 0.29.0 does (role-only chunk first, one chunk per content token,
// finish_reason on the last content chunk, a usage-only chunk with empty
// choices, then [DONE]) with configurable timing and failure modes, so the
// client and load generator can be tested on loopback without a GPU.
//
// Use it with httptest: srv := &fakeserver.Server{...};
// ts := httptest.NewServer(srv).
package fakeserver

import (
	"encoding/json"
	"fmt"
	"net/http"
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

	requests    atomic.Int64
	inFlight    atomic.Int64
	maxInFlight atomic.Int64
}

// Requests returns the number of requests received so far.
func (s *Server) Requests() int { return int(s.requests.Load()) }

// MaxInFlight returns the highest number of requests served concurrently.
func (s *Server) MaxInFlight() int { return int(s.maxInFlight.Load()) }

type request struct {
	Model         string `json:"model"`
	MaxTokens     int    `json:"max_tokens"`
	Stream        bool   `json:"stream"`
	StreamOptions struct {
		IncludeUsage bool `json:"include_usage"`
	} `json:"stream_options"`
}

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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
		send(fmt.Sprintf(`{"id":"chatcmpl-fake","object":"chat.completion.chunk","created":0,"model":%q,`+
			`"choices":[],"usage":{"prompt_tokens":%d,"total_tokens":%d,"completion_tokens":%d}}`,
			req.Model, s.PromptTokens, s.PromptTokens+tokens, tokens))
	}
	send("[DONE]")
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
