package sampler

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// GPUFields are nvidia-smi's query fields, in query order (docs/02,
// "Telemetry samplers"). DCGM is not used: consumer cards do not expose its
// profiling counters.
var GPUFields = []string{"utilization.gpu", "memory.used", "power.draw", "clocks.sm", "temperature.gpu"}

// GPU samples the first GPU with nvidia-smi.
type GPU struct {
	// Run executes nvidia-smi with args and returns stdout. nil means the
	// real binary; tests inject canned output.
	Run func(ctx context.Context, args ...string) ([]byte, error)
}

// Fields implements Source.
func (g *GPU) Fields() []string { return GPUFields }

// Sample implements Source.
func (g *GPU) Sample(ctx context.Context) ([]string, error) {
	run := g.Run
	if run == nil {
		run = nvidiaSMI
	}
	out, err := run(ctx, "--query-gpu="+strings.Join(GPUFields, ","), "--format=csv,noheader,nounits", "--id=0")
	if err != nil {
		return nil, fmt.Errorf("nvidia-smi: %w", err)
	}
	return parseGPU(string(out))
}

// parseGPU reads one line of `--format=csv,noheader,nounits`, e.g.
// "52, 29748, 59.15, 300, 34". nvidia-smi writes "[N/A]" for a field the
// card does not report; that is kept as an empty value, not a zero.
func parseGPU(out string) ([]string, error) {
	line, _, _ := strings.Cut(strings.TrimSpace(out), "\n")
	parts := strings.Split(line, ",")
	if len(parts) != len(GPUFields) {
		return nil, fmt.Errorf("nvidia-smi: got %d fields in %q, want %d", len(parts), line, len(GPUFields))
	}
	for i, p := range parts {
		p = strings.TrimSpace(p)
		if strings.HasPrefix(p, "[") { // [N/A], [Not Supported]
			p = ""
		}
		parts[i] = p
	}
	return parts, nil
}

func nvidiaSMI(ctx context.Context, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, "nvidia-smi", args...).Output()
}
