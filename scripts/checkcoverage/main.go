// Command checkcoverage fails when a coverage profile is below the project
// floor. The parsing and the threshold live in internal/tooling/coverage, where
// they are covered by tests; this is only the command line around them.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/gsoares85/hermes/internal/tooling/coverage"
)

func main() {
	profilePath := flag.String("profile", "coverage.out", "coverage profile to read")
	minimum := flag.Float64("min", 85, "minimum percentage of covered statements")
	ignore := flag.String("ignore", "", "comma-separated package prefixes to leave out of the count")
	flag.Parse()

	file, err := os.Open(*profilePath)
	if err != nil {
		fail(err)
	}
	defer func() { _ = file.Close() }()

	ignored := splitPrefixes(*ignore)

	summary, err := coverage.Parse(file, ignored...)
	if err != nil {
		fail(err)
	}

	fmt.Printf("coverage: %.1f%% of %d statements\n", summary.Percent(), summary.Total)

	// Printed on every run, not only when it matters. An exclusion nobody sees
	// is how a gate quietly stops covering half the codebase.
	if summary.Ignored > 0 {
		fmt.Printf("ignored: %d statements in %s\n", summary.Ignored, strings.Join(ignored, ", "))
	}

	if err := coverage.Check(summary, *minimum); err != nil {
		fail(err)
	}
}

func splitPrefixes(list string) []string {
	var prefixes []string

	for _, prefix := range strings.Split(list, ",") {
		if trimmed := strings.TrimSpace(prefix); trimmed != "" {
			prefixes = append(prefixes, trimmed)
		}
	}

	return prefixes
}

func fail(err error) {
	fmt.Fprintf(os.Stderr, "checkcoverage: %v\n", err)
	os.Exit(1)
}
