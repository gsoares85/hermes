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
	"strconv"
	"strings"

	"github.com/gsoares85/hermes/internal/version"
)

// ErrInvalidRevisionRange is returned for a range that must not reach Git.
var ErrInvalidRevisionRange = errors.New("invalid revision range")

// Commit is a commit as read from the history. The identity fields matter as
// much as the message: a tool configured with its own Git identity writes an
// impeccable message and still signs the commit as itself.
type Commit struct {
	Hash           string
	Merge          bool
	AuthorName     string
	AuthorEmail    string
	CommitterName  string
	CommitterEmail string
	Message        string
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
	// is exclusive, so the trailer itself is the violation. Without its colon
	// it still credits, but then an identity has to follow — otherwise the rule
	// fires on a sentence that merely opens with the words, and writing about
	// the rule is not breaking it.
	coAuthoredBy = regexp.MustCompile(`(?i)^\s*co-?authored-by\s*(:|[^<]*<[^>]+@[^>]+>)`)

	// Attribution in prose: a verb of authorship, a preposition, and a vendor
	// close behind. The preposition is what separates "generated with Claude"
	// from "create the ai package", and the vendor is what keeps "implemented
	// using the new parser" out of it.
	attribution = regexp.MustCompile(`(?i)\b(generated|created|written|authored|assisted|made|produced|implemented|refactored)\s+(with|by|using|via)\b[^.\n]{0,40}?(` + vendors + `)`)

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
		violations = append(violations, checkIdentity(commit)...)
		violations = append(violations, checkMessage(commit)...)
		violations = append(violations, checkSubject(commit)...)
	}

	return violations
}

// checkIdentity looks at who Git says wrote and recorded the commit. A message
// check alone never sees this, which is the gap it exists to close.
func checkIdentity(commit Commit) []Violation {
	identities := []struct{ role, value string }{
		{"author", commit.AuthorName},
		{"author", commit.AuthorEmail},
		{"committer", commit.CommitterName},
		{"committer", commit.CommitterEmail},
	}

	for _, identity := range identities {
		if identity.value == "" || !vendorWord.MatchString(identity.value) {
			continue
		}

		return []Violation{{
			Hash: commit.Hash,
			Rule: "commit " + identity.role + " is an AI assistant",
			Line: identity.value,
		}}
	}

	return nil
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

// Conventional Commit types this repository writes. The release falls back to
// these when a pull request carries no label, so an unknown type is not a style
// preference: it is a commit the version resolver cannot read.
var conventionalTypes = map[string]bool{
	"feat": true, "fix": true, "docs": true, "style": true, "refactor": true,
	"perf": true, "test": true, "build": true, "ci": true, "chore": true, "revert": true,
}

// subjectLimit is the maximum length of a subject line, as CONTRIBUTING states.
const subjectLimit = 72

// checkSubject applies the Conventional Commits rule to the subject line. Merge
// commits are exempt: Git writes those, not a person.
func checkSubject(commit Commit) []Violation {
	if commit.Merge {
		return nil
	}

	subject, _, _ := strings.Cut(commit.Message, "\n")
	subject = strings.TrimSpace(subject)

	commitType, _, ok := version.ConventionalSubject(subject)
	switch {
	case !ok:
		return subjectViolation(commit, "subject is not a Conventional Commit", subject)
	case !conventionalTypes[commitType]:
		return subjectViolation(commit, "subject uses an unknown type "+strconv.Quote(commitType), subject)
	case len(subject) > subjectLimit:
		return subjectViolation(commit, fmt.Sprintf("subject is %d characters, the limit is %d", len(subject), subjectLimit), subject)
	}

	return nil
}

func subjectViolation(commit Commit, rule, subject string) []Violation {
	return []Violation{{Hash: commit.Hash, Rule: rule, Line: subject}}
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

// Separators in a branch name, turned into spaces so that a vendor becomes a
// word of its own. The underscore has to be here with the other two: it is a
// word character to the regexp engine, so \bai\b never matches inside
// ai_generated_tests, and that name would pass while its hyphenated twin is
// rejected.
var branchSeparators = strings.NewReplacer("-", " ", "/", " ", "_", " ")

// Branch reports whether a branch name credits an assistant.
//
// A branch name has no grammar to read, so this is stricter than the rule for a
// message: any vendor name in it is a violation. That does bar a branch about,
// say, an OpenAI-compatible endpoint. The trade is deliberate — a branch is
// renamed in one command, while a check that let vendor names through here
// would have nothing left to stand on.
func Branch(name string) []Violation {
	normalized := branchSeparators.Replace(strings.ToLower(name))

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
