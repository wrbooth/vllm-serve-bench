// Package openai is a streaming client for the OpenAI-compatible
// /v1/chat/completions endpoint served by vLLM. It records the raw timing
// facts that the metrics in docs/02-architecture.md are computed from; it does
// no metric arithmetic itself.
package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptrace"
	"strings"
	"sync"
	"time"
)

// ChatPath is the endpoint the client streams from, relative to BaseURL.
const ChatPath = "/v1/chat/completions"

// Sentinel errors for streams that returned 200 but did not deliver what a
// measurement needs. Each makes the request an error row, counted and never
// dropped.
var (
	// ErrNoDone means the body ended without the terminating "data: [DONE]".
	ErrNoDone = errors.New("stream ended before [DONE]")
	// ErrNoUsage means [DONE] arrived without a usage chunk, so the
	// server-authoritative token counts are unknown.
	ErrNoUsage = errors.New("stream had no usage chunk")
	// ErrNoContent means no chunk carried non-empty delta.content, so TTFT
	// is undefined.
	ErrNoContent = errors.New("stream had no content token")
)

// maxErrorBody bounds how much of a non-200 body is read into an error.
const maxErrorBody = 4 << 10

// StatusError is a non-200 response. Message is the server's error message
// when the body parses as an OpenAI or vLLM error object, else the raw body
// (truncated).
type StatusError struct {
	Code    int
	Message string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("http %d: %s", e.Code, e.Message)
}

// StreamError is an error object the server sent inside a 200 stream.
type StreamError struct {
	Message string
}

func (e *StreamError) Error() string {
	return "server error in stream: " + e.Message
}

// Message is one chat message.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Request is the caller-controlled part of a chat completion request. The
// client always adds stream=true and stream_options.include_usage=true.
type Request struct {
	Model     string    `json:"model"`
	Messages  []Message `json:"messages"`
	MaxTokens int       `json:"max_tokens,omitempty"`
	// IgnoreEOS is vLLM's extension that holds output length at MaxTokens.
	IgnoreEOS bool `json:"ignore_eos,omitempty"`

	// Sampling. nil means "not sent", and then the server's default applies;
	// for vLLM that default comes from the model's generation_config.json
	// (Qwen2.5 ships temperature 0.7, top_k 20, repetition_penalty 1.1), not
	// from the OpenAI spec. A run that must not depend on the checkpoint's
	// defaults sets these explicitly. TopK and RepetitionPenalty are vLLM
	// extensions.
	Temperature       *float64 `json:"temperature,omitempty"`
	TopP              *float64 `json:"top_p,omitempty"`
	TopK              *int     `json:"top_k,omitempty"`
	RepetitionPenalty *float64 `json:"repetition_penalty,omitempty"`
}

type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type wireRequest struct {
	Request
	Stream        bool          `json:"stream"`
	StreamOptions streamOptions `json:"stream_options"`
}

// Result holds the raw facts of one streamed request. Times come from the
// client's clock; zero means "did not happen". On error, the fields that were
// reached before the failure are still set, so a partial stream is visible.
type Result struct {
	// Send is when the request body was fully written (httptrace
	// WroteRequest). If the write never completed, it is when the attempt
	// started, so every row can be placed in or out of the warmup window.
	Send time.Time
	// FirstContent is receipt of the first chunk with non-empty
	// delta.content. Role-only and empty-delta chunks do not count.
	FirstContent time.Time
	// LastContent is receipt of the last chunk with non-empty delta.content.
	LastContent time.Time
	// Done is receipt of "data: [DONE]"; zero unless the stream completed.
	Done time.Time
	// ContentChunks counts chunks with non-empty delta.content.
	ContentChunks int
	// PromptTokens and CompletionTokens come from the final usage chunk.
	PromptTokens     int
	CompletionTokens int
	// FinishReason is the last non-null choices[].finish_reason: "length"
	// when max_tokens was reached, "stop" on EOS or a stop string.
	FinishReason string
	// Status is the HTTP status code, 0 if no response arrived.
	Status int
	Err    error
}

// Client streams chat completions. The zero value is not usable: BaseURL is
// required. A Client is safe for concurrent use.
type Client struct {
	// BaseURL is the server root, e.g. "http://localhost:8000".
	BaseURL string
	// HTTP is the client used for requests; nil means a shared client whose
	// transport keeps enough idle connections for high concurrency.
	HTTP *http.Client
	// Clock returns the current time; nil means time.Now. It may be called
	// from the transport's goroutine, so it must be safe for concurrent use.
	Clock func() time.Time
}

// defaultHTTP keeps up to 1024 idle connections per host. net/http's default
// of 2 would make a closed-loop run at concurrency N close and redial N-2
// connections after every request, and the redial lands inside TTFT.
var defaultHTTP = func() *http.Client {
	// DefaultTransport is documented to be an *http.Transport.
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.MaxIdleConns = 0
	t.MaxIdleConnsPerHost = 1024
	return &http.Client{Transport: t}
}()

// Now returns the client's current time. Callers that compare against Result
// timestamps (the load generator's warmup window) must use this clock.
func (c *Client) Now() time.Time {
	if c.Clock != nil {
		return c.Clock()
	}
	return time.Now()
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return defaultHTTP
}

// Stream sends req with streaming enabled and reads the response to [DONE].
// It never returns a nil-error Result without both token counts and a first
// content token. Cancel ctx to abort; the per-request deadline belongs on ctx.
func (c *Client) Stream(ctx context.Context, req *Request) Result {
	res := Result{Send: c.Now()}

	body, err := json.Marshal(wireRequest{
		Request:       *req,
		Stream:        true,
		StreamOptions: streamOptions{IncludeUsage: true},
	})
	if err != nil {
		res.Err = fmt.Errorf("encode request: %w", err)
		return res
	}

	// WroteRequest runs on the transport's write goroutine, so the time it
	// records is handed over under a lock.
	var (
		mu    sync.Mutex
		wrote time.Time
	)
	trace := &httptrace.ClientTrace{
		WroteRequest: func(info httptrace.WroteRequestInfo) {
			if info.Err != nil {
				return
			}
			t := c.Now()
			mu.Lock()
			wrote = t
			mu.Unlock()
		},
	}
	ctx = httptrace.WithClientTrace(ctx, trace)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimSuffix(c.BaseURL, "/")+ChatPath, bytes.NewReader(body))
	if err != nil {
		res.Err = fmt.Errorf("build request: %w", err)
		return res
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")

	resp, err := c.httpClient().Do(httpReq)
	mu.Lock()
	if !wrote.IsZero() {
		res.Send = wrote
	}
	mu.Unlock()
	if err != nil {
		res.Err = fmt.Errorf("send: %w", err)
		return res
	}
	defer func() {
		// Drain what is left after [DONE] (the chunked terminator) so the
		// connection goes back to the pool instead of being closed.
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxErrorBody))
		_ = resp.Body.Close()
	}()
	res.Status = resp.StatusCode

	if resp.StatusCode != http.StatusOK {
		res.Err = statusError(resp)
		return res
	}
	res.Err = c.readStream(resp.Body, &res)
	return res
}

// chunk is the subset of a chat.completion.chunk the client reads.
type chunk struct {
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
	Error *errorBody `json:"error"`
}

// readStream consumes SSE events until [DONE], filling res. The clock is read
// once per event, as soon as the event is complete.
func (c *Client) readStream(body io.Reader, res *Result) error {
	sse := newSSEReader(body)
	gotUsage := false
	for {
		data, err := sse.next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return ErrNoDone
			}
			return fmt.Errorf("read stream: %w", err)
		}
		now := c.Now()
		if data == "[DONE]" {
			res.Done = now
			break
		}
		var ch chunk
		if err := json.Unmarshal([]byte(data), &ch); err != nil {
			return fmt.Errorf("decode chunk %q: %w", truncate(data), err)
		}
		if ch.Error != nil {
			return &StreamError{Message: ch.Error.Message}
		}
		res.apply(&ch, now)
		gotUsage = gotUsage || ch.Usage != nil
	}
	if !gotUsage {
		return ErrNoUsage
	}
	if res.FirstContent.IsZero() {
		return ErrNoContent
	}
	return nil
}

// apply folds one chunk, received at now, into res.
func (res *Result) apply(ch *chunk, now time.Time) {
	content := false
	for i := range ch.Choices {
		content = content || ch.Choices[i].Delta.Content != ""
		if fr := ch.Choices[i].FinishReason; fr != nil && *fr != "" {
			res.FinishReason = *fr
		}
	}
	if content { // one chunk is one receipt, however many choices it holds
		if res.FirstContent.IsZero() {
			res.FirstContent = now
		}
		res.LastContent = now
		res.ContentChunks++
	}
	if ch.Usage != nil {
		res.PromptTokens = ch.Usage.PromptTokens
		res.CompletionTokens = ch.Usage.CompletionTokens
	}
}

// errorBody covers both error shapes vLLM has used: the OpenAI style
// {"error": {"message": ...}} and the older flat {"object": "error",
// "message": ...}.
type errorBody struct {
	Message string `json:"message"`
}

func statusError(resp *http.Response) error {
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
	if err != nil {
		return &StatusError{Code: resp.StatusCode, Message: "(body unreadable: " + err.Error() + ")"}
	}
	var wrapped struct {
		Error *errorBody `json:"error"`
		errorBody
	}
	msg := strings.TrimSpace(string(raw))
	if json.Unmarshal(raw, &wrapped) == nil {
		switch {
		case wrapped.Error != nil && wrapped.Error.Message != "":
			msg = wrapped.Error.Message
		case wrapped.Message != "":
			msg = wrapped.Message
		}
	}
	return &StatusError{Code: resp.StatusCode, Message: truncate(msg)}
}

// truncate keeps error strings bounded; a garbage body can be large.
func truncate(s string) string {
	const limit = 200
	if len(s) <= limit {
		return s
	}
	return s[:limit] + "..."
}
