// Command bench is the load generator, telemetry sampler and report tool for
// benchmarking an OpenAI-compatible LLM inference server (vLLM).
//
// Subcommands (see docs/02-architecture.md): run, prompts verify, version.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := dispatch(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

// dispatch runs the subcommand named by args[0] and returns the exit code:
// 0 success, 1 failure, 2 usage error.
func dispatch(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return 2
	}
	switch args[0] {
	case "version":
		_, _ = fmt.Fprintln(stdout, version)
		return 0
	case "prompts":
		if len(args) < 2 || args[1] != "verify" {
			_, _ = fmt.Fprintln(stderr, "usage: bench prompts verify [flags]")
			return 2
		}
		return verifyCmd(ctx, args[2:], stdout, stderr)
	default:
		_, _ = fmt.Fprintf(stderr, "bench: unknown subcommand %q\n\n", args[0])
		usage(stderr)
		return 2
	}
}

func usage(w io.Writer) {
	_, _ = fmt.Fprintln(w, "usage: bench <run|prompts verify|version> [flags]")
}
