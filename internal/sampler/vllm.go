package sampler

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// EngineFields are the vLLM metrics recorded each tick (docs/02, "Telemetry
// samplers"), named as vLLM 0.29.0 exposes them. `gpu_cache_usage_perc`, the
// older name in the design, no longer exists; `kv_cache_usage_perc` replaced
// it. The TTFT histogram's sum and count are kept so the engine's own mean
// TTFT per interval can be set against the client's.
var EngineFields = []string{
	"vllm:num_requests_running",
	"vllm:num_requests_waiting",
	"vllm:kv_cache_usage_perc",
	"vllm:num_preemptions_total",
	"vllm:prefix_cache_queries_total",
	"vllm:prefix_cache_hits_total",
	"vllm:prompt_tokens_total",
	"vllm:generation_tokens_total",
	"vllm:time_to_first_token_seconds_sum",
	"vllm:time_to_first_token_seconds_count",
}

// Metrics is one scrape of a Prometheus text exposition.
type Metrics struct {
	// Values maps a sample name to its value summed over every label set, so
	// a counter split by label (request_success_total by finished_reason) is
	// read as its total. Histogram buckets keep their _bucket name and are
	// never confused with _sum or _count.
	Values map[string]float64
	// Info holds the labels of each *_info metric (the value is always 1).
	Info map[string]map[string]string
}

// KVCacheTokens is the KV pool size the engine allocated, from
// vllm:cache_config_info. The startup log's "GPU KV cache size" line and
// this label are the same number; this one can be read from a running engine.
func (m Metrics) KVCacheTokens() (int, bool) {
	v, ok := m.Info["vllm:cache_config_info"]["kv_cache_size_tokens"]
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(v)
	return n, err == nil
}

// ParseMetrics reads the Prometheus text format: comment lines, then
// `name{label="value",...} value [timestamp]` or `name value`.
func ParseMetrics(r io.Reader) (Metrics, error) {
	m := Metrics{Values: map[string]float64{}, Info: map[string]map[string]string{}}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || line[0] == '#' {
			continue
		}
		name, labels, rest, err := splitSample(line)
		if err != nil {
			return Metrics{}, fmt.Errorf("metrics line %d: %w", n, err)
		}
		valStr, _, _ := strings.Cut(strings.TrimSpace(rest), " ") // drop an optional timestamp
		v, err := strconv.ParseFloat(valStr, 64)
		if err != nil {
			return Metrics{}, fmt.Errorf("metrics line %d: value %q: %w", n, valStr, err)
		}
		m.Values[name] += v
		if strings.HasSuffix(name, "_info") {
			m.Info[name] = labels
		}
	}
	return m, sc.Err()
}

var errMalformed = errors.New("malformed sample")

// splitSample splits a sample line into its name, labels and the text after
// the closing brace. Label values are quoted and may contain `,`, `}` and the
// escapes \" \\ \n, so they are scanned rather than split.
func splitSample(line string) (name string, labels map[string]string, rest string, err error) {
	i := strings.IndexAny(line, "{ ")
	if i <= 0 {
		return "", nil, "", errMalformed
	}
	name = line[:i]
	if line[i] == ' ' {
		return name, nil, line[i:], nil
	}
	labels = map[string]string{}
	s := line[i+1:]
	for {
		s = strings.TrimLeft(s, ", ")
		if strings.HasPrefix(s, "}") {
			return name, labels, s[1:], nil
		}
		key, after, ok := strings.Cut(s, `="`)
		if !ok || key == "" {
			return "", nil, "", errMalformed
		}
		var val strings.Builder
		j := 0
		for ; j < len(after) && after[j] != '"'; j++ {
			c := after[j]
			if c == '\\' && j+1 < len(after) {
				j++
				switch after[j] {
				case 'n':
					c = '\n'
				default:
					c = after[j]
				}
			}
			val.WriteByte(c)
		}
		if j == len(after) {
			return "", nil, "", errMalformed // unterminated value
		}
		labels[key] = val.String()
		s = after[j+1:]
	}
}

// Engine samples a vLLM server's /metrics.
type Engine struct {
	URL    string // e.g. http://127.0.0.1:8000/metrics
	Client *http.Client
	// Last is the most recent successful scrape, for callers that want more
	// than the recorded fields (the KV pool size, for one).
	Last Metrics
}

// Fields implements Source.
func (e *Engine) Fields() []string { return EngineFields }

// Sample implements Source. A metric missing from the scrape is an error for
// the whole row: a silent zero would read as "no preemptions" rather than
// "not measured".
func (e *Engine) Sample(ctx context.Context) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, e.URL, http.NoBody)
	if err != nil {
		return nil, err
	}
	resp, err := e.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close() //nolint:errcheck // read-only body; a close error cannot lose data
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", e.URL, resp.Status)
	}
	m, err := ParseMetrics(resp.Body)
	if err != nil {
		return nil, err
	}
	e.Last = m
	out := make([]string, len(EngineFields))
	for i, f := range EngineFields {
		v, ok := m.Values[f]
		if !ok {
			return nil, fmt.Errorf("metric %s missing from scrape", f)
		}
		out[i] = strconv.FormatFloat(v, 'g', -1, 64)
	}
	return out, nil
}
