// Package commitcheck enforces the authorship rule of the project: every commit
// is authored by a person, and no commit, branch name or trailer credits an AI
// assistant.
//
// The rule bans attribution, not vocabulary. A commit that mentions a file named
// claude.md, or a package called ai, is legitimate and must pass — a check that
// fires on those teaches people to route around it.
package commitcheck

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// ErrInvalidRevisionRange is returned for a range that must not reach Git.
var ErrInvalidRevisionRange = errors.New("invalid revision range")

// Commit is a commit as read from the history.
type Commit struct {
	Hash    string
	Message string
}

// Violation is one broken rule, tied to the text that broke it.
type Violation struct {
	Hash string
	Rule string
	Line string
}

// String renders the violation the way the CI job prints it.
func (v Violation) String() string {
	where := v.Hash
	if where == "" {
		where = "commit"
	}

	return fmt.Sprintf("%s: %s: %q", where, v.Rule, v.Line)
}

// Names of assistants and vendors that must never appear as an author.
const vendors = `claude|anthropic|chatgpt|openai|copilot|gemini|\bllm\b|\bai\b`

var (
	// A Co-Authored-By trailer, whoever it credits: authorship of this project
	// is exclusive, so the trailer itself is the violation.
	coAuthoredBy = regexp.MustCompile(`(?i)^\s*co-?authored-by\s*:`)

	// Attribution in prose: a verb of authorship, a preposition, and a vendor
	// close behind. The preposition is what separates "generated with Claude"
	// from "create the ai package".
	attribution = regexp.MustCompile(`(?i)\b(generated|created|written|authored|assisted|made|produced)\s+(with|by)\b[^.\n]{0,40}?(` + vendors + `)`)

	// A Git trailer, which by convention has a hyphenated key. Requiring the
	// hyphen is what keeps "docs: update the claude.md path" from reading as a
	// trailer whose value names a vendor.
	trailer = regexp.MustCompile(`^([A-Za-z][A-Za-z0-9]*(?:-[A-Za-z0-9]+)+)\s*:\s*(\S.*)$`)

	vendorWord = regexp.MustCompile(`(?i)(` + vendors + `)`)
)

// Commits reports every violation found in the given commits, in order.
func Commits(commits []Commit) []Violation {
	var violations []Violation

	for _, commit := range commits {
		violations = append(violations, checkMessage(commit)...)
	}

	return violations
}

func checkMessage(commit Commit) []Violation {
	var violations []Violation

	for i, line := range strings.Split(commit.Message, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		switch {
		case coAuthoredBy.MatchString(trimmed):
			violations = append(violations, Violation{
				Hash: commit.Hash,
				Rule: "commit carries a Co-Authored-By trailer",
				Line: trimmed,
			})
		case attribution.MatchString(trimmed):
			violations = append(violations, Violation{
				Hash: commit.Hash,
				Rule: "commit credits an AI assistant",
				Line: trimmed,
			})
		case i > 0 && isVendorTrailer(trimmed):
			violations = append(violations, Violation{
				Hash: commit.Hash,
				Rule: "commit carries a trailer naming an AI assistant",
				Line: trimmed,
			})
		}
	}

	return violations
}

// isVendorTrailer reports whether the line is a Git trailer whose key or value
// names an assistant. The subject line is never a trailer, so callers skip it.
func isVendorTrailer(line string) bool {
	match := trailer.FindStringSubmatch(line)
	if match == nil {
		return false
	}

	return vendorWord.MatchString(match[1]) || vendorWord.MatchString(match[2])
}

// Branch reports whether a branch name credits an assistant.
func Branch(name string) []Violation {
	normalized := strings.ReplaceAll(strings.ToLower(name), "-", " ")
	normalized = strings.ReplaceAll(normalized, "/", " ")

	if !vendorWord.MatchString(normalized) {
		return nil
	}

	return []Violation{{
		Rule: "branch name credits an AI assistant",
		Line: name,
	}}
}

// revisionRange is the subset of Git revision syntax the checker accepts:
// names, hashes, tags and the two range operators. Everything else — spaces,
// flags, shell metacharacters — is refused.
var revisionRange = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/~^-]*(\.\.\.?[A-Za-z0-9][A-Za-z0-9._/~^-]*)?$`)

// ValidateRevisionRange rejects a range before it is handed to a subprocess.
func ValidateRevisionRange(value string) error {
	if !revisionRange.MatchString(value) {
		return fmt.Errorf("%w: %q", ErrInvalidRevisionRange, value)
	}

	return nil
}
