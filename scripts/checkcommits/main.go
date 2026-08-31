// Command checkcommits fails when a commit or a branch credits an AI assistant.
// The rules live in internal/tooling/commitcheck and the record format in
// internal/tooling/gitlog, both covered by tests; this is only the Git plumbing
// around them.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/gsoares85/hermes/internal/tooling/commitcheck"
	"github.com/gsoares85/hermes/internal/tooling/gitlog"
)

// A history read cannot take longer than this. Without a deadline a hung git
// holds the job until the timeout of the whole workflow.
const readTimeout = 2 * time.Minute

// The fields read for every commit, in the order commitcheck expects them.
var logFields = []string{"%H", "%P", "%an", "%ae", "%cn", "%ce", "%B"}

func main() {
	// The exit lives here so that run can own a cancellable context: os.Exit
	// runs no deferred call, and a deferred cancel is the whole point of one.
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "checkcommits: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	revisionRange := flag.String("range", "", "commit range to inspect, such as origin/main..HEAD")
	branch := flag.String("branch", "", "branch name to inspect")
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), readTimeout)
	defer cancel()

	var violations []commitcheck.Violation

	if *revisionRange != "" {
		commits, err := readCommits(ctx, *revisionRange)
		if err != nil {
			return err
		}
		violations = append(violations, commitcheck.Commits(commits)...)
	}

	if *branch != "" {
		violations = append(violations, commitcheck.Branch(*branch)...)
	}

	if len(violations) == 0 {
		fmt.Println("checkcommits: no violation found")
		return nil
	}

	for _, violation := range violations {
		fmt.Fprintln(os.Stderr, violation)
	}
	fmt.Fprintln(os.Stderr, "\nEvery commit is authored by a person and follows Conventional Commits.")

	return fmt.Errorf("%d violations found; rewrite the history and push again", len(violations))
}

func readCommits(ctx context.Context, revisionRange string) ([]commitcheck.Commit, error) {
	if err := commitcheck.ValidateRevisionRange(revisionRange); err != nil {
		return nil, fmt.Errorf("reading commits: %w", err)
	}

	// Resolved to an absolute path rather than left to the PATH, so that the
	// binary this runs is the one the machine installed.
	git, err := exec.LookPath("git")
	if err != nil {
		return nil, fmt.Errorf("looking for git: %w", err)
	}

	// The range was validated above against a strict allowlist, so it cannot
	// grow into another argument or another command.
	//nolint:gosec // G204: revisionRange is checked by ValidateRevisionRange.
	output, err := exec.CommandContext(ctx, git, "log", gitlog.Format(logFields...), revisionRange).Output()
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", revisionRange, err)
	}

	records, err := gitlog.Parse(string(output), len(logFields))
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", revisionRange, err)
	}

	commits := make([]commitcheck.Commit, 0, len(records))
	for _, record := range records {
		commits = append(commits, commitcheck.Commit{
			Hash:           record[0],
			Merge:          gitlog.IsMerge(record[1]),
			AuthorName:     record[2],
			AuthorEmail:    record[3],
			CommitterName:  record[4],
			CommitterEmail: record[5],
			Message:        record[6],
		})
	}

	return commits, nil
}
