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

	// Ignored counts the statements skipped by an ignore prefix. It is
	// reported rather than discarded so that every run says how much of the
	// codebase the number does not speak for.
	Ignored int
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
//
// Blocks in a package matching one of the ignore prefixes are left out of the
// count entirely. That exists for packages the unit run cannot exercise — an
// engine adapter behind a thin interface, covered by the integration suite the
// gate does not run — whose statements would otherwise count while their
// coverage would not, dragging the number down for code that is in fact tested.
//
// It is not a way to switch the gate off: ignoring every statement leaves
// nothing measured, which is refused exactly like an empty profile.
func Parse(r io.Reader, ignore ...string) (Summary, error) {
	statements := make(map[block]int)
	covered := make(map[block]bool)
	// Keyed by block for the same reason the counted ones are: with -coverpkg
	// every test binary reports the same block, and adding them up line by
	// line would report several times what the package actually holds.
	ignored := make(map[block]int)

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

		if isIgnored(key.name, ignore) {
			ignored[key] = count
			continue
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
	for _, count := range ignored {
		summary.Ignored += count
	}
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

// isIgnored reports whether a block belongs to an ignored package. The match is
// on a path boundary, so ignoring internal/driver does not also silence
// internal/drivers.
func isIgnored(name string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if prefix == "" {
			continue
		}
		trimmed := strings.TrimSuffix(prefix, "/")
		if name == trimmed || strings.HasPrefix(name, trimmed+"/") {
			return true
		}
	}

	return false
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

	count, err := parseCount(fields[1], line, "statement count")
	if err != nil {
		return block{}, 0, 0, err
	}

	hits, err := parseCount(fields[2], line, "hit count")
	if err != nil {
		return block{}, 0, 0, err
	}

	return block{name: name, position: position}, count, hits, nil
}

// parseCount reads a field that counts something. Neither count can be
// negative, and rejecting that is not pedantry: a negative statement count
// shrinks the total, so a hundred covered statements against a total of five
// report 2000% and clear any floor. A profile that can do that defeats the
// gate as surely as an empty one.
func parseCount(field, line, what string) (int, error) {
	value, err := strconv.Atoi(field)
	if err != nil {
		return 0, fmt.Errorf("%w: %q has a non-numeric %s", ErrMalformedProfile, line, what)
	}
	if value < 0 {
		return 0, fmt.Errorf("%w: %q has a negative %s", ErrMalformedProfile, line, what)
	}

	return value, nil
}

// Check reports whether the summary meets the minimum percentage.
func Check(summary Summary, minimum float64) error {
	if summary.Percent() < minimum {
		return fmt.Errorf("%w: %.1f%% of %d statements, want at least %.1f%%",
			ErrBelowMinimum, summary.Percent(), summary.Total, minimum)
	}

	return nil
}
