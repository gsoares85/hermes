// Command checkcommits fails when a commit or a branch credits an AI assistant.
// The rules live in internal/tooling/commitcheck, where they are covered by
// tests; this is only the Git plumbing around them.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/gsoares85/hermes/internal/tooling/commitcheck"
)

// Separators that cannot appear in a commit message.
const (
	fieldSeparator  = "\x1f"
	recordSeparator = "\x1e"
)

func main() {
	revisionRange := flag.String("range", "", "commit range to inspect, such as origin/main..HEAD")
	branch := flag.String("branch", "", "branch name to inspect")
	flag.Parse()

	var violations []commitcheck.Violation

	if *revisionRange != "" {
		commits, err := readCommits(*revisionRange)
		if err != nil {
			fmt.Fprintf(os.Stderr, "checkcommits: %v\n", err)
			os.Exit(1)
		}
		violations = append(violations, commitcheck.Commits(commits)...)
	}

	if *branch != "" {
		violations = append(violations, commitcheck.Branch(*branch)...)
	}

	if len(violations) == 0 {
		fmt.Println("checkcommits: no attribution found")
		return
	}

	for _, violation := range violations {
		fmt.Fprintln(os.Stderr, violation)
	}
	fmt.Fprintln(os.Stderr, "\nEvery commit is authored by a person. Rewrite the history and push again.")
	os.Exit(1)
}

func readCommits(revisionRange string) ([]commitcheck.Commit, error) {
	if err := commitcheck.ValidateRevisionRange(revisionRange); err != nil {
		return nil, fmt.Errorf("reading commits: %w", err)
	}

	format := fmt.Sprintf("--format=%%H%s%%B%s", fieldSeparator, recordSeparator)

	// The range was validated above against a strict allowlist, so it cannot
	// grow into another argument or another command.
	//nolint:gosec // G204: revisionRange is checked by ValidateRevisionRange.
	output, err := exec.Command("git", "log", format, revisionRange).Output()
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", revisionRange, err)
	}

	var commits []commitcheck.Commit
	for _, record := range strings.Split(string(output), recordSeparator) {
		hash, message, found := strings.Cut(strings.TrimSpace(record), fieldSeparator)
		if !found {
			continue
		}
		commits = append(commits, commitcheck.Commit{Hash: hash, Message: message})
	}

	return commits, nil
}
