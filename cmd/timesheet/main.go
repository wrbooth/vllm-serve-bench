// Command timesheet prints the worklog's session table, reconstructed from the
// Claude Code transcripts of this repo (primary checkout and its worktrees) and
// the commit history. See internal/timesheet for how sessions are cut and
// docs/worklog.md for how the table is used. Run it as `make timesheet`.
package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/wrbooth/vllm-serve-bench/internal/timesheet"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "timesheet:", err)
		os.Exit(1)
	}
}

func run() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	gap := flag.Duration("gap", 5*time.Minute, "silence longer than this ends a session")
	projects := flag.String("projects", filepath.Join(home, ".claude", "projects"), "Claude Code transcripts root")
	flag.Parse()

	ctx := context.Background()
	commonDir, err := git(ctx, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return err
	}
	primary := filepath.Dir(strings.TrimSpace(string(commonDir)))

	var events []time.Time
	pattern := filepath.Join(*projects, timesheet.ProjectDirName(primary)+"*", "*.jsonl")
	files, err := filepath.Glob(pattern)
	if err != nil {
		return err
	}
	for _, f := range files {
		ts, skipped, err := transcriptTimes(f)
		if err != nil {
			return fmt.Errorf("%s: %w", f, err)
		}
		if skipped > 0 {
			fmt.Fprintf(os.Stderr, "timesheet: %s: skipped %d unparseable lines\n", filepath.Base(f), skipped)
		}
		events = append(events, ts...)
	}

	log, err := git(ctx, "log", "--all", "--format=%aI")
	if err != nil {
		return err
	}
	commits, err := timesheet.GitTimes(bytes.NewReader(log))
	if err != nil {
		return err
	}
	events = append(events, commits...)

	fmt.Fprintf(os.Stderr, "timesheet: %d transcripts, %d commits, gap %s\n", len(files), len(commits), *gap)
	return timesheet.WriteMarkdown(os.Stdout, timesheet.Sessions(events, *gap), time.Local)
}

func transcriptTimes(path string) ([]time.Time, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close() //nolint:errcheck // read-only file; a close error cannot lose data
	return timesheet.TranscriptTimes(f)
}

func git(ctx context.Context, args ...string) ([]byte, error) {
	out, err := exec.CommandContext(ctx, "git", args...).Output()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return out, nil
}
