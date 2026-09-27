// Command bench is the load generator, telemetry sampler and report tool for
// benchmarking an OpenAI-compatible LLM inference server (vLLM).
//
// Subcommands (see docs/02-architecture.md): run, report, verify, version.
package main

import (
	"fmt"
	"os"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "version":
		fmt.Println(version)
	default:
		fmt.Fprintf(os.Stderr, "bench: unknown subcommand %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: bench <run|report|verify|version> [flags]")
}
