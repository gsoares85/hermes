// Command nextversion prints the version that follows the last released tag.
//
// The release workflow calls it with the previous tag, the labels of the merged
// pull request and, as a fallback, the commit messages of the range. Every
// decision lives in internal/version and every bit of parsing in
// internal/tooling/gitlog, both covered by tests; this is only the command line
// around them.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/gsoares85/hermes/internal/tooling/gitlog"
	"github.com/gsoares85/hermes/internal/version"
)

func main() {
	last := flag.String("last", "", "last released tag, empty when there is none yet")
	bump := flag.String("bump", "", "increment to apply, bypassing labels and commits")
	labels := flag.String("labels", "", "comma-separated labels of the merged pull request")
	messagesFile := flag.String("messages-file", "", "file with the commit messages of the range")
	flag.Parse()

	resolved, err := resolveBump(*bump, *labels, *messagesFile)
	if err != nil {
		fail(err)
	}

	next, err := version.Next(*last, resolved)
	if err != nil {
		fail(err)
	}

	fmt.Println(next)
}

func resolveBump(bump, labels, messagesFile string) (version.Bump, error) {
	if strings.TrimSpace(bump) != "" {
		return version.ParseBump(bump)
	}

	messages, err := readMessages(messagesFile)
	if err != nil {
		return "", err
	}

	return version.ResolveBump(version.SplitLabels(labels), messages)
}

func readMessages(path string) ([]string, error) {
	if path == "" {
		return nil, nil
	}

	// The path comes from the workflow that runs this tool, not from user
	// input, and the tool reads nothing else.
	//nolint:gosec // G304: the caller of this command chooses the file.
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading commit messages: %w", err)
	}

	messages, err := gitlog.Messages(string(content))
	if err != nil {
		return nil, fmt.Errorf("reading commit messages: %w", err)
	}

	return messages, nil
}

func fail(err error) {
	fmt.Fprintf(os.Stderr, "nextversion: %v\n", err)
	fmt.Fprintln(os.Stderr, "\nLabel the pull request with release:patch, release:minor or release:major.")
	os.Exit(1)
}
