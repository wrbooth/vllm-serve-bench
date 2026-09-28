package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"
)

// Paths of the non-streaming endpoints the bench uses besides ChatPath.
const (
	// ModelsPath lists the served models; its first 200 is the readiness
	// signal (docs/02-architecture.md, "Serving layer").
	ModelsPath = "/v1/models"
	// TokenizePath is vLLM's tokenizer endpoint (not part of the OpenAI
	// API). With messages it applies the chat template, so its count is
	// the prompt_tokens a chat request would report.
	TokenizePath = "/tokenize"
	// ResetPrefixCachePath empties vLLM's prefix cache. It exists only when
	// the engine runs with VLLM_SERVER_DEV_MODE=1, as vLLM's own benchmark
	// sweep (vllm/benchmarks/sweep/server.py) runs it.
	ResetPrefixCachePath = "/reset_prefix_cache"
)

// maxJSONBody bounds a non-streaming response body. /tokenize returns every
// token id, so a long prompt is a few tens of KB.
const maxJSONBody = 8 << 20

// TokenizeRequest is the body of POST /tokenize. Set exactly one of Prompt
// and Messages.
type TokenizeRequest struct {
	Model    string    `json:"model"`
	Prompt   string    `json:"prompt,omitempty"`
	Messages []Message `json:"messages,omitempty"`
	// AddSpecialTokens is sent when non-nil. vLLM defaults it to true for a
	// prompt and false for messages.
	AddSpecialTokens *bool `json:"add_special_tokens,omitempty"`
}

// TokenizeResponse is vLLM's answer to POST /tokenize.
type TokenizeResponse struct {
	Count       int   `json:"count"`
	MaxModelLen int   `json:"max_model_len"`
	Tokens      []int `json:"tokens"`
}

// Tokenize counts tokens with the engine's own tokenizer and chat template.
func (c *Client) Tokenize(ctx context.Context, req *TokenizeRequest) (TokenizeResponse, error) {
	var out TokenizeResponse
	body, err := json.Marshal(req)
	if err != nil {
		return out, fmt.Errorf("encode tokenize request: %w", err)
	}
	err = c.doJSON(ctx, http.MethodPost, TokenizePath, body, &out)
	return out, err
}

// ErrResetRefused means the engine kept answering success=false, which it
// does while running requests still hold cache blocks.
var ErrResetRefused = errors.New("prefix cache reset refused")

// ResetPrefixCache empties the engine's prefix cache, retrying every poll
// while the engine refuses (blocks still held) until ctx is done.
func (c *Client) ResetPrefixCache(ctx context.Context, poll time.Duration) error {
	refused := false
	for {
		var out struct {
			Success bool `json:"success"`
		}
		if err := c.doJSON(ctx, http.MethodPost, ResetPrefixCachePath, nil, &out); err != nil {
			// The deadline can land mid-request as easily as between
			// attempts; after a refusal, either way the engine refused.
			if refused && ctx.Err() != nil {
				return fmt.Errorf("%w: %w", ErrResetRefused, ctx.Err())
			}
			return err
		}
		refused = true
		if out.Success {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("%w: %w", ErrResetRefused, ctx.Err())
		case <-time.After(poll):
		}
	}
}

// Models returns the ids of the models the server is serving.
func (c *Client) Models(ctx context.Context) ([]string, error) {
	var out struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := c.doJSON(ctx, http.MethodGet, ModelsPath, nil, &out); err != nil {
		return nil, err
	}
	ids := make([]string, len(out.Data))
	for i := range out.Data {
		ids[i] = out.Data[i].ID
	}
	return ids, nil
}

// ErrModelNotServed means the server is up but does not serve the model
// asked for; waiting longer will not help.
var ErrModelNotServed = errors.New("model not served")

// WaitReady polls ModelsPath every interval until it answers 200 and, when
// model is non-empty, lists model. It returns ctx's error if ctx ends first,
// wrapped with the last failure seen.
func (c *Client) WaitReady(ctx context.Context, model string, interval time.Duration) error {
	tick := time.NewTicker(interval)
	defer tick.Stop()
	var last error // the last failure not caused by ctx ending
	for {
		ids, err := c.Models(ctx)
		if err == nil {
			if model == "" || slices.Contains(ids, model) {
				return nil
			}
			return fmt.Errorf("%w: %q (serving %s)", ErrModelNotServed, model, strings.Join(ids, ", "))
		}
		if ctx.Err() == nil || last == nil {
			last = err
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("server not ready (last error: %w): %w", last, ctx.Err())
		case <-tick.C:
		}
	}
}

// doJSON sends one non-streaming request and decodes a 200 JSON answer into
// out. Any other status is a *StatusError.
func (c *Client) doJSON(ctx context.Context, method, path string, body []byte, out any) error {
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimSuffix(c.BaseURL, "/")+path, rd)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return statusError(resp)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxJSONBody)).Decode(out); err != nil {
		return fmt.Errorf("decode %s response: %w", path, err)
	}
	return nil
}
