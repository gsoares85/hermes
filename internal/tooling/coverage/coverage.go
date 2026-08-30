// Package coverage reads a Go coverage profile and applies the project floor.
//
// The floor exists to fail builds, so the parser is deliberately strict: a
// profile it cannot understand, or one that carries no statements at all, is an
// error rather than a pass. A gate that approves an empty measurement is worse
// than no gate, because it reports confidence nobody earned.
package coverage

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Errors returned by this package.
var (
	ErrMalformedProfile = errors.New("malformed coverage profile")
	ErrNoStatements     = errors.New("coverage profile contains no statements")
	ErrBelowMinimum     = errors.New("coverage below the required minimum")
)

// Summary is the statement count of a profile.
type Summary struct {
	Total   int
	Covered int
}

// Percent is the share of covered statements, from 0 to 100.
func (s Summary) Percent() float64 {
	if s.Total == 0 {
		return 0
	}

	return float64(s.Covered) / float64(s.Total) * 100
}

// block identifies a region of a file, so that the same region reported by
// several test binaries is counted once.
type block struct {
	name     string
	position string
}

// Parse reads a profile in the format written by `go test -coverprofile`.
func Parse(r io.Reader) (Summary, error) {
	statements := make(map[block]int)
	covered := make(map[block]bool)

	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "mode:") {
			continue
		}

		key, count, hits, err := parseLine(line)
		if err != nil {
			return Summary{}, err
		}

		statements[key] = count
		if hits > 0 {
			covered[key] = true
		}
	}
	if err := scanner.Err(); err != nil {
		return Summary{}, fmt.Errorf("reading coverage profile: %w", err)
	}

	summary := Summary{}
	for key, count := range statements {
		summary.Total += count
		if covered[key] {
			summary.Covered += count
		}
	}

	if summary.Total == 0 {
		return Summary{}, ErrNoStatements
	}

	return summary, nil
}

// parseLine reads one entry: "path/file.go:1.2,3.4 5 6".
func parseLine(line string) (block, int, int, error) {
	fields := strings.Fields(line)
	if len(fields) != 3 {
		return block{}, 0, 0, fmt.Errorf("%w: %q has %d fields, want 3", ErrMalformedProfile, line, len(fields))
	}

	name, position, found := strings.Cut(fields[0], ":")
	if !found || position == "" {
		return block{}, 0, 0, fmt.Errorf("%w: %q has no block position", ErrMalformedProfile, line)
	}

	count, err := strconv.Atoi(fields[1])
	if err != nil {
		return block{}, 0, 0, fmt.Errorf("%w: %q has a non-numeric statement count", ErrMalformedProfile, line)
	}

	hits, err := strconv.Atoi(fields[2])
	if err != nil {
		return block{}, 0, 0, fmt.Errorf("%w: %q has a non-numeric hit count", ErrMalformedProfile, line)
	}

	return block{name: name, position: position}, count, hits, nil
}

// Check reports whether the summary meets the minimum percentage.
func Check(summary Summary, minimum float64) error {
	if summary.Percent() < minimum {
		return fmt.Errorf("%w: %.1f%% of %d statements, want at least %.1f%%",
			ErrBelowMinimum, summary.Percent(), summary.Total, minimum)
	}

	return nil
}
