// Command checkreadme fails when the README points at files the repository does
// not publish. The rule lives in internal/tooling/readmecheck, where it is
// covered by tests; this is only the file handling around it.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/gsoares85/hermes/internal/tooling/readmecheck"
)

func main() {
	path := flag.String("file", "README.md", "the README to check")
	flag.Parse()

	content, err := os.ReadFile(*path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "checkreadme: %v\n", err)
		os.Exit(1)
	}

	found := readmecheck.Check(string(content))
	if len(found) == 0 {
		fmt.Printf("%s references nothing outside the repository\n", *path)

		return
	}

	// Every one of them, not the first: someone fixing this should have the
	// whole list rather than a line at a time.
	for _, reference := range found {
		fmt.Fprintf(os.Stderr, "%s:%d: references %q — %s\n", *path, reference.Line, reference.Match, reference.Text)
	}

	fmt.Fprintf(os.Stderr,
		"\ncheckreadme: the README must not reference files that are not in the repository.\n"+
			"docs/, CLAUDE.md and .claude/ are ignored by Git, so a link to one of them is a broken\n"+
			"link for everyone reading the project on GitHub. Copy the content in instead.\n")
	os.Exit(1)
}
