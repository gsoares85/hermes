// Command checkcoverage fails when a coverage profile is below the project
// floor. The parsing and the threshold live in internal/tooling/coverage, where
// they are covered by tests; this is only the command line around them.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/gsoares85/hermes/internal/tooling/coverage"
)

func main() {
	profilePath := flag.String("profile", "coverage.out", "coverage profile to read")
	minimum := flag.Float64("min", 85, "minimum percentage of covered statements")
	flag.Parse()

	file, err := os.Open(*profilePath)
	if err != nil {
		fail(err)
	}
	defer func() { _ = file.Close() }()

	summary, err := coverage.Parse(file)
	if err != nil {
		fail(err)
	}

	fmt.Printf("coverage: %.1f%% of %d statements\n", summary.Percent(), summary.Total)

	if err := coverage.Check(summary, *minimum); err != nil {
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintf(os.Stderr, "checkcoverage: %v\n", err)
	os.Exit(1)
}
