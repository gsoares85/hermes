package coverage_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/gsoares85/hermes/internal/tooling/coverage"
)

const header = "mode: set\n"

func TestParseCountsStatements(t *testing.T) {
	t.Parallel()

	profile := header +
		"github.com/gsoares85/hermes/internal/a/a.go:1.1,3.2 4 1\n" +
		"github.com/gsoares85/hermes/internal/a/a.go:5.1,7.2 6 0\n"

	got, err := coverage.Parse(strings.NewReader(profile))
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if got.Total != 10 {
		t.Errorf("Total = %d, want 10", got.Total)
	}
	if got.Covered != 4 {
		t.Errorf("Covered = %d, want 4", got.Covered)
	}
	if want := 40.0; got.Percent() != want {
		t.Errorf("Percent() = %v, want %v", got.Percent(), want)
	}
}

// Blocks repeat across runs of the same package; counting a block twice would
// quietly inflate the total.
func TestParseSumsRepeatedBlocksOnce(t *testing.T) {
	t.Parallel()

	profile := header +
		"pkg/a.go:1.1,3.2 2 0\n" +
		"pkg/a.go:1.1,3.2 2 1\n"

	got, err := coverage.Parse(strings.NewReader(profile))
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if got.Total != 2 {
		t.Errorf("Total = %d, want 2", got.Total)
	}
	if got.Covered != 2 {
		t.Errorf("Covered = %d, want 2", got.Covered)
	}
}

// A profile with no statements must never read as success: that is exactly the
// shape a misconfigured -coverpkg produces, and it would make the gate vacuous.
// The pipeline runs with -coverpkg, so several test binaries report the same
// block and the profile is written in count mode: the same region appears with
// different hit counts. Counting it once, and as covered if any run reached it,
// is what keeps the percentage honest.
func TestParseCountsARepeatedBlockOnceInCountMode(t *testing.T) {
	t.Parallel()

	profile := "mode: count\n" +
		"pkg/a.go:1.1,3.2 4 0\n" +
		"pkg/a.go:1.1,3.2 4 7\n" +
		"pkg/a.go:1.1,3.2 4 2\n" +
		"pkg/b.go:1.1,2.2 6 0\n" +
		"pkg/b.go:1.1,2.2 6 0\n"

	got, err := coverage.Parse(strings.NewReader(profile))
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if got.Total != 10 {
		t.Errorf("Total = %d, want 10: each block counts once", got.Total)
	}
	if got.Covered != 4 {
		t.Errorf("Covered = %d, want 4: a block is covered if any run reached it", got.Covered)
	}
	if want := 40.0; got.Percent() != want {
		t.Errorf("Percent() = %v, want %v", got.Percent(), want)
	}
}

// Two blocks of the same file are different blocks, however similar they look.
func TestParseKeepsDistinctBlocksApart(t *testing.T) {
	t.Parallel()

	profile := "mode: count\n" +
		"pkg/a.go:1.1,3.2 2 1\n" +
		"pkg/a.go:4.1,6.2 2 1\n"

	got, err := coverage.Parse(strings.NewReader(profile))
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if got.Total != 4 {
		t.Errorf("Total = %d, want 4", got.Total)
	}
}

func TestParseRejectsProfileWithoutStatements(t *testing.T) {
	t.Parallel()

	for name, profile := range map[string]string{
		"only header": header,
		"empty file":  "",
		"zero blocks": header + "pkg/a.go:1.1,3.2 0 0\n",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if _, err := coverage.Parse(strings.NewReader(profile)); !errors.Is(err, coverage.ErrNoStatements) {
				t.Errorf("Parse(%q) error = %v, want ErrNoStatements", name, err)
			}
		})
	}
}

func TestParseRejectsMalformedLines(t *testing.T) {
	t.Parallel()

	for name, profile := range map[string]string{
		"missing count":      header + "pkg/a.go:1.1,3.2 2\n",
		"statements not int": header + "pkg/a.go:1.1,3.2 two 1\n",
		"count not int":      header + "pkg/a.go:1.1,3.2 2 once\n",
		"no position":        header + "pkg/a.go 2 1\n",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if _, err := coverage.Parse(strings.NewReader(profile)); !errors.Is(err, coverage.ErrMalformedProfile) {
				t.Errorf("Parse(%q) error = %v, want ErrMalformedProfile", name, err)
			}
		})
	}
}

// A negative statement count subtracts from the total, which turns a large
// numerator over a small denominator: 100 covered statements against a total of
// 5 reads as 2000% and clears any floor. The gate exists so that it cannot be
// passed vacuously, so the parser refuses the line rather than doing the
// arithmetic.
func TestParseRejectsNegativeCounts(t *testing.T) {
	t.Parallel()

	for name, profile := range map[string]string{
		"negative statement count": header + "pkg/a.go:1.1,3.2 -5 1\n",
		"negative hit count":       header + "pkg/a.go:1.1,3.2 5 -1\n",
		"negative block cancelling a real one": header +
			"pkg/a.go:1.1,3.2 100 1\n" +
			"pkg/b.go:1.1,3.2 -95 0\n",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if _, err := coverage.Parse(strings.NewReader(profile)); !errors.Is(err, coverage.ErrMalformedProfile) {
				t.Errorf("Parse(%q) error = %v, want ErrMalformedProfile", name, err)
			}
		})
	}
}

func TestCheckAppliesTheFloor(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		summary   coverage.Summary
		minimum   float64
		wantError bool
	}{
		{"just below the floor", coverage.Summary{Total: 1000, Covered: 849}, 85, true},
		{"exactly at the floor", coverage.Summary{Total: 1000, Covered: 850}, 85, false},
		{"above the floor", coverage.Summary{Total: 1000, Covered: 999}, 85, false},
		{"nothing covered", coverage.Summary{Total: 10}, 85, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := coverage.Check(tc.summary, tc.minimum)
			if tc.wantError && !errors.Is(err, coverage.ErrBelowMinimum) {
				t.Errorf("Check() error = %v, want ErrBelowMinimum", err)
			}
			if !tc.wantError && err != nil {
				t.Errorf("Check() error = %v, want nil", err)
			}
		})
	}
}
