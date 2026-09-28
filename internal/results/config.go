package results

import "time"

// Config is config.json: everything needed to know what a run measured and
// to rerun it. It is written when the run starts and rewritten when it
// ends, so an interrupted run still says what it was.
type Config struct {
	Schema       int    `json:"schema"`
	RunID        string `json:"run_id"`
	BenchVersion string `json:"bench_version"`
	// Complete is true only when every level ran to the end.
	Complete   bool       `json:"complete"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at"`

	// Args is the bench's command line after the subcommand, verbatim;
	// Flags is every flag's effective value, defaults included.
	Args  []string          `json:"args"`
	Flags map[string]string `json:"flags"`

	BaseURL     string  `json:"base_url"`
	Model       string  `json:"model"`
	Concurrency []int   `json:"concurrency"`
	WarmupS     float64 `json:"warmup_s"`
	DurationS   float64 `json:"duration_s"`
	Seed        uint64  `json:"seed"`

	Profile ProfileConfig `json:"profile"`
	Engine  EngineConfig  `json:"engine"`
	// Host is filled by the GPU sampler once it is wired in; absent until then.
	Host *HostFacts `json:"host,omitempty"`

	// ReadyWaitS is how long the bench waited for /v1/models before the
	// first level.
	ReadyWaitS float64 `json:"ready_wait_s"`
	// PromptTokens is filled at the end of the run.
	PromptTokens *PromptTokenCheck `json:"prompt_token_check,omitempty"`
	// Warnings are things a reader of the results must know, such as a
	// prompt-token mismatch. Empty on a clean run.
	Warnings []string `json:"warnings"`
}

// ProfileConfig is the workload as generated, including what its prompt
// token count rests on.
type ProfileConfig struct {
	Name        string `json:"name"`
	SharedWords int    `json:"shared_words"`
	UniqueWords int    `json:"unique_words"`
	// FixedTokens is the embedded template-plus-lead count, and
	// FixedSource says whether it was measured on the engine.
	FixedTokens int    `json:"fixed_tokens"`
	FixedSource string `json:"fixed_source"`
	FixedModel  string `json:"fixed_model"`
	// PredictedPromptTokens = FixedTokens + SharedWords + UniqueWords.
	PredictedPromptTokens int    `json:"predicted_prompt_tokens"`
	WordListSHA256        string `json:"word_list_sha256"`
	WordListSize          int    `json:"word_list_size"`
	// MaxTokens is the mean output length; each request's own is drawn
	// uniformly from OutputTokensMin..OutputTokensMax (inclusive).
	MaxTokens         int     `json:"max_tokens"`
	OutputRangeRatio  float64 `json:"output_range_ratio"`
	OutputTokensMin   int     `json:"output_tokens_min"`
	OutputTokensMax   int     `json:"output_tokens_max"`
	IgnoreEOS         bool    `json:"ignore_eos"`
	Temperature       float64 `json:"temperature"`
	RepetitionPenalty float64 `json:"repetition_penalty"`
}

// EngineConfig identifies the engine under test. Image and Argv are
// recorded verbatim from the caller (`deploy/compose/engine.sh argv` and
// the compose .env), because vLLM takes the last of a repeated flag and
// only the running process's argv says what was used.
type EngineConfig struct {
	Config string `json:"config"`
	Image  string `json:"image"`
	Argv   string `json:"argv"`
	// Facts is filled by the vLLM sampler once it is wired in.
	Facts *EngineFacts `json:"facts,omitempty"`
}

// EngineFacts are read from the running engine, not from its flags. All
// fields are optional; a nil pointer means "not captured".
type EngineFacts struct {
	// KVCacheTokens is the KV pool size, from /metrics
	// cache_config_info{kv_cache_size_tokens=...}. It varies with the
	// compile-cache state (wiki/gpu-host.md), so it is recorded per run.
	KVCacheTokens *int `json:"kv_cache_tokens,omitempty"`
	// MaxNumSeqs and MaxNumBatchedTokens are the resolved scheduler
	// limits (docs/02, "Workload profiles").
	MaxNumSeqs          *int `json:"max_num_seqs,omitempty"`
	MaxNumBatchedTokens *int `json:"max_num_batched_tokens,omitempty"`
	// PrefixCaching is whether prefix caching was on.
	PrefixCaching *bool `json:"prefix_caching,omitempty"`
}

// HostFacts describe the GPU, from nvidia-smi.
type HostFacts struct {
	GPUName      string `json:"gpu_name,omitempty"`
	Driver       string `json:"driver,omitempty"`
	GPUMemoryMiB int    `json:"gpu_memory_mib,omitempty"`
}
