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
	also := flag.String("also", "", "comma-separated globs of transcripts started elsewhere; only records naming this repo count")
	flag.Parse()

	ctx := context.Background()
	commonDir, err := git(ctx, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return err
	}
	primary := filepath.Dir(strings.TrimSpace(string(commonDir)))

	own, err := filepath.Glob(filepath.Join(*projects, timesheet.ProjectDirName(primary)+"*", "*.jsonl"))
	if err != nil {
		return err
	}
	events, err := collect(nil, own, "")
	if err != nil {
		return err
	}
	var foreign []string
	for g := range strings.SplitSeq(*also, ",") {
		if g = strings.TrimSpace(g); g == "" {
			continue
		}
		m, err := filepath.Glob(g)
		if err != nil {
			return err
		}
		foreign = append(foreign, m...)
	}
	if events, err = collect(events, foreign, filepath.Base(primary)); err != nil {
		return err
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

	fmt.Fprintf(os.Stderr, "timesheet: %d own transcripts, %d others (filtered to %q), %d commits, gap %s\n",
		len(own), len(foreign), filepath.Base(primary), len(commits), *gap)
	return timesheet.WriteMarkdown(os.Stdout, timesheet.Sessions(events, *gap), time.Local)
}

// collect appends the activity times from each transcript; see
// timesheet.TranscriptTimes for what mention filters.
func collect(events []time.Time, files []string, mention string) ([]time.Time, error) {
	for _, f := range files {
		ts, skipped, err := transcriptTimes(f, mention)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", f, err)
		}
		if skipped > 0 {
			fmt.Fprintf(os.Stderr, "timesheet: %s: skipped %d unparseable lines\n", filepath.Base(f), skipped)
		}
		events = append(events, ts...)
	}
	return events, nil
}

func transcriptTimes(path, mention string) ([]time.Time, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close() //nolint:errcheck // read-only file; a close error cannot lose data
	return timesheet.TranscriptTimes(f, mention)
}

func git(ctx context.Context, args ...string) ([]byte, error) {
	out, err := exec.CommandContext(ctx, "git", args...).Output()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return out, nil
}
