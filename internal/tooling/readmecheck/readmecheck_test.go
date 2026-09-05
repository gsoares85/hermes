package readmecheck_test

import (
	"os"
	"strings"
	"testing"

	"github.com/gsoares85/hermes/internal/tooling/readmecheck"
)

// The three shapes the gate exists to catch. Each one is a link that renders as
// a link on GitHub and leads nowhere, because the directory it names is not in
// the repository.
func TestCheckReportsAReferenceToSomethingUnpublished(t *testing.T) {
	t.Parallel()

	cases := map[string]struct{ text, match string }{
		"a path under docs": {
			"See `docs/technical/ARCHITECTURE.md` for the module map.", "docs/",
		},
		// The reason the pattern names the directory rather than spelling out
		// the shapes a reference takes: the second segment here is uppercase,
		// and a pattern built from examples missed it.
		"the readme of docs": {
			"The documentation system is described in docs/README.md.", "docs/",
		},
		"the instructions file": {
			"The conventions are in CLAUDE.md.", "CLAUDE.md",
		},
		"the agent directory": {
			"Skills live in `.claude/skills/`.", ".claude/",
		},
		"a mention in prose": {
			"Anything under docs/ is ignored by Git.", "docs/",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			found := readmecheck.Check(tc.text)
			if len(found) != 1 {
				t.Fatalf("Check(%q) found %d references, want 1: %v", tc.text, len(found), found)
			}
			if found[0].Match != tc.match {
				t.Errorf("Check(%q) matched %q, want %q", tc.text, found[0].Match, tc.match)
			}
			if found[0].Line != 1 {
				t.Errorf("Check(%q) reported line %d, want 1", tc.text, found[0].Line)
			}
		})
	}
}

// A README that names nothing unpublished passes. The words themselves are not
// the problem — a sentence about documentation is fine, a link into a directory
// nobody can open is not.
func TestCheckPassesTextThatNamesNothingUnpublished(t *testing.T) {
	t.Parallel()

	for _, text := range []string{
		"Read [CONTRIBUTING.md](CONTRIBUTING.md) — it describes the whole process.",
		"The documentation of PostgreSQL explains sslmode.",
		"Every release publishes binaries plus SHA256SUMS.",
		// Escaped in the pattern, so this is a different file and not a match.
		"CLAUDEXMD is not the instructions file.",
		"",
	} {
		if found := readmecheck.Check(text); len(found) != 0 {
			t.Errorf("Check(%q) found %v, want nothing", text, found)
		}
	}
}

// The point of reporting rather than just failing: someone has to be able to go
// and fix the line, so every reference carries its number and its text, in the
// order they appear.
func TestCheckReportsEveryReferenceInOrder(t *testing.T) {
	t.Parallel()

	text := strings.Join([]string{
		"# Hermes",
		"",
		"See docs/technical/TESTING.md.",
		"Nothing wrong on this line.",
		"The conventions are in CLAUDE.md.",
	}, "\n")

	found := readmecheck.Check(text)
	if len(found) != 2 {
		t.Fatalf("Check(...) found %d references, want 2: %v", len(found), found)
	}

	if found[0].Line != 3 || found[0].Match != "docs/" {
		t.Errorf("the first reference is %+v, want docs/ on line 3", found[0])
	}
	if found[1].Line != 5 || found[1].Match != "CLAUDE.md" {
		t.Errorf("the second reference is %+v, want CLAUDE.md on line 5", found[1])
	}
	if !strings.Contains(found[0].Text, "TESTING.md") {
		t.Errorf("the first reference carries %q, want the line it was found on", found[0].Text)
	}
}

// One line can carry more than one, and a gate that reported the first and
// stopped would send someone round the loop twice.
func TestCheckReportsMoreThanOneReferenceOnALine(t *testing.T) {
	t.Parallel()

	found := readmecheck.Check("Both docs/README.md and CLAUDE.md are ignored by Git.")
	if len(found) != 2 {
		t.Fatalf("Check(...) found %d references, want 2: %v", len(found), found)
	}
	for _, reference := range found {
		if reference.Line != 1 {
			t.Errorf("reference %+v is on line %d, want 1", reference, reference.Line)
		}
	}
}

// The gate applied to the file it guards. This is what fails the build the day
// someone adds a link to docs/ in the README, which is the whole point.
func TestTheReadmeOfThisRepositoryNamesNothingUnpublished(t *testing.T) {
	t.Parallel()

	readme, err := os.ReadFile("../../../README.md")
	if err != nil {
		t.Fatalf("reading the README: %v", err)
	}

	for _, reference := range readmecheck.Check(string(readme)) {
		t.Errorf("README.md:%d references %q: %s", reference.Line, reference.Match, reference.Text)
	}
}
