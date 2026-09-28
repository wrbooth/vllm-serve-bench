package openai_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/wrbooth/vllm-serve-bench/internal/fakeserver"
	"github.com/wrbooth/vllm-serve-bench/internal/openai"
)

func newFake(t *testing.T, srv *fakeserver.Server) *openai.Client {
	t.Helper()
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	return &openai.Client{BaseURL: ts.URL + "/"} // trailing slash is trimmed
}

// The fake's toy tokenizer: 3 fields = 3 tokens; a chat of 2 fields plus
// overhead 5 = 7.
func TestTokenizeReturnsTheServersCount(t *testing.T) {
	t.Parallel()
	c := newFake(t, &fakeserver.Server{ChatOverhead: 5})
	no := false
	got, err := c.Tokenize(context.Background(), &openai.TokenizeRequest{Model: "m", Prompt: " a b c", AddSpecialTokens: &no})
	if err != nil || got.Count != 3 || len(got.Tokens) != 3 || got.MaxModelLen != 8192 {
		t.Errorf("Tokenize(prompt) = %+v, %v; want count 3 with 3 tokens, max_model_len 8192", got, err)
	}
	got, err = c.Tokenize(context.Background(), &openai.TokenizeRequest{Model: "m", Messages: []openai.Message{{Role: "user", Content: "Say hi."}}})
	if err != nil || got.Count != 7 {
		t.Errorf("Tokenize(messages) = %+v, %v; want count 7", got, err)
	}
	// Neither prompt nor messages: the server's 400 comes back as a StatusError.
	_, err = c.Tokenize(context.Background(), &openai.TokenizeRequest{Model: "m"})
	var se *openai.StatusError
	if !errors.As(err, &se) || se.Code != http.StatusBadRequest {
		t.Errorf("Tokenize(empty) error = %v, want a 400 StatusError", err)
	}
}

func TestModelsListsServedIDs(t *testing.T) {
	t.Parallel()
	c := newFake(t, &fakeserver.Server{Models: []string{"x", "y"}})
	ids, err := c.Models(context.Background())
	if err != nil || strings.Join(ids, ",") != "x,y" {
		t.Errorf("Models = %v, %v; want [x y]", ids, err)
	}
}

func TestJSONEndpointsReportTransportAndDecodeErrors(t *testing.T) {
	t.Parallel()
	garbage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("not json"))
	}))
	t.Cleanup(garbage.Close)
	if _, err := (&openai.Client{BaseURL: garbage.URL}).Models(context.Background()); err == nil || !strings.Contains(err.Error(), "decode") {
		t.Errorf("Models on a non-JSON body: error = %v, want a decode error", err)
	}
	if _, err := (&openai.Client{BaseURL: "http://127.0.0.1:1"}).Tokenize(context.Background(), &openai.TokenizeRequest{}); err == nil {
		t.Error("Tokenize against a closed port returned no error")
	}
	if _, err := (&openai.Client{BaseURL: "http://bad url"}).Models(context.Background()); err == nil {
		t.Error("Models with an invalid URL returned no error")
	}
}

func TestWaitReadyPollsUntilTheModelIsServed(t *testing.T) {
	t.Parallel()
	srv := &fakeserver.Server{UnreadyFor: 3, Models: []string{"m"}}
	c := newFake(t, srv)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.WaitReady(ctx, "m", time.Millisecond); err != nil {
		t.Fatalf("WaitReady = %v, want nil after three 503s", err)
	}
}

func TestWaitReadyFailsFastWhenTheModelIsNotServed(t *testing.T) {
	t.Parallel()
	c := newFake(t, &fakeserver.Server{Models: []string{"other"}})
	err := c.WaitReady(context.Background(), "m", time.Hour)
	if !errors.Is(err, openai.ErrModelNotServed) || !strings.Contains(err.Error(), "other") {
		t.Errorf("WaitReady = %v, want ErrModelNotServed naming the served model", err)
	}
	// An empty model accepts whatever is served.
	if err := c.WaitReady(context.Background(), "", time.Hour); err != nil {
		t.Errorf("WaitReady(\"\") = %v, want nil", err)
	}
}

func TestWaitReadyGivesUpWhenTheContextEnds(t *testing.T) {
	t.Parallel()
	c := newFake(t, &fakeserver.Server{UnreadyFor: 1 << 30})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	err := c.WaitReady(ctx, "m", 5*time.Millisecond)
	var se *openai.StatusError
	if !errors.Is(err, context.DeadlineExceeded) || !errors.As(err, &se) || se.Code != 503 {
		t.Errorf("WaitReady = %v, want DeadlineExceeded wrapping the last 503", err)
	}
}

func TestResetPrefixCacheRetriesWhileTheEngineRefuses(t *testing.T) {
	t.Parallel()
	// vLLM answers success=false while running requests hold blocks.
	srv := &fakeserver.Server{ResetRefusals: 3}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := newFake(t, srv).ResetPrefixCache(ctx, time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if srv.Resets() != 1 {
		t.Fatalf("resets = %d, want 1 after 3 refusals", srv.Resets())
	}
}

func TestResetPrefixCacheFailsWithoutDevModeOrWhenRefusedToTheEnd(t *testing.T) {
	t.Parallel()
	var se *openai.StatusError
	err := newFake(t, &fakeserver.Server{NoDevMode: true}).ResetPrefixCache(context.Background(), time.Millisecond)
	if !errors.As(err, &se) || se.Code != http.StatusNotFound {
		t.Errorf("without dev mode: %v, want a 404 StatusError", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	err = newFake(t, &fakeserver.Server{ResetRefusals: 1 << 30}).ResetPrefixCache(ctx, time.Millisecond)
	if !errors.Is(err, openai.ErrResetRefused) {
		t.Errorf("always refused: %v, want ErrResetRefused", err)
	}
}
