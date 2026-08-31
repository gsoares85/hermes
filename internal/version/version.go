// Package version resolves the build information of the binary and computes the
// next release version.
//
// Two rules drive this package. The version of the product is the release, so
// nothing here is hardcoded: the build injects the real values with -ldflags and
// the defaults stay obvious placeholders. And the increment is decided by a
// human through the pull request label, so the arithmetic is a pure function
// with tests instead of a shell one-liner.
package version

import (
	"errors"
	"fmt"
	"regexp"
	"runtime"
	"strconv"
	"strings"
)

// Build information. Overridden at link time by the release pipeline with
// -ldflags "-X github.com/gsoares85/hermes/internal/version.Version=v1.2.3".
var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

// Sentinel errors so callers can react to the cause instead of matching strings.
var (
	ErrInvalidVersion = errors.New("invalid version")
	ErrInvalidBump    = errors.New("invalid version bump")
	ErrNoBumpLabel    = errors.New("no release label on the pull request")
	ErrAmbiguousBump  = errors.New("cannot decide the version bump")
)

// Info is the build information exposed to the UI and to the CLI.
type Info struct {
	Version  string `json:"version"`
	Commit   string `json:"commit"`
	Date     string `json:"date"`
	Platform string `json:"platform"`
}

// Current returns the build information of the running binary.
func Current() Info {
	return Info{
		Version:  Version,
		Commit:   Commit,
		Date:     Date,
		Platform: runtime.GOOS + "/" + runtime.GOARCH,
	}
}

// String renders the build information in a single human-readable line.
func (i Info) String() string {
	return fmt.Sprintf("hermes %s (%s, %s, %s)", i.Version, i.Commit, i.Date, i.Platform)
}

// Semver is a released version. Pre-release and build metadata are deliberately
// unsupported: the pipeline only ever publishes vX.Y.Z tags.
type Semver struct {
	Major int
	Minor int
	Patch int
}

// String renders the version the way it is tagged in Git.
func (s Semver) String() string {
	return fmt.Sprintf("v%d.%d.%d", s.Major, s.Minor, s.Patch)
}

// Bump is the size of the increment applied to the previous release.
type Bump string

// The three increments accepted by the release pipeline, matching the pull
// request labels release:patch, release:minor and release:major.
const (
	BumpPatch Bump = "patch"
	BumpMinor Bump = "minor"
	BumpMajor Bump = "major"
)

// Parse reads a version tag such as "v1.2.3". The leading "v" and surrounding
// whitespace are optional, everything else is rejected.
func Parse(tag string) (Semver, error) {
	raw := strings.TrimSpace(tag)
	raw = strings.TrimPrefix(raw, "v")

	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return Semver{}, fmt.Errorf("%w: %q is not in the form vX.Y.Z", ErrInvalidVersion, tag)
	}

	numbers := make([]int, len(parts))
	for i, part := range parts {
		n, err := parseComponent(part)
		if err != nil {
			return Semver{}, fmt.Errorf("%w: %q has an invalid component %q", ErrInvalidVersion, tag, part)
		}
		numbers[i] = n
	}

	return Semver{Major: numbers[0], Minor: numbers[1], Patch: numbers[2]}, nil
}

// parseComponent accepts a canonical decimal number: no sign, no padding, no
// suffix. Rejecting "01" and "3-beta" here is what keeps tag comparison total.
func parseComponent(part string) (int, error) {
	if part == "" {
		return 0, errors.New("empty component")
	}
	if len(part) > 1 && part[0] == '0' {
		return 0, errors.New("leading zero")
	}
	for _, r := range part {
		if r < '0' || r > '9' {
			return 0, errors.New("not a decimal number")
		}
	}

	n, err := strconv.Atoi(part)
	if err != nil {
		return 0, fmt.Errorf("component out of range: %w", err)
	}

	return n, nil
}

// ParseBump reads an increment, accepting both the bare name and the pull
// request label it comes from ("release:minor").
func ParseBump(value string) (Bump, error) {
	raw := strings.ToLower(strings.TrimSpace(value))
	raw = strings.TrimPrefix(raw, "release:")

	switch Bump(raw) {
	case BumpPatch, BumpMinor, BumpMajor:
		return Bump(raw), nil
	default:
		return "", fmt.Errorf("%w: %q is not patch, minor or major", ErrInvalidBump, value)
	}
}

// Bumped returns the version that follows s for the given increment.
func (s Semver) Bumped(bump Bump) (Semver, error) {
	switch bump {
	case BumpPatch:
		return Semver{Major: s.Major, Minor: s.Minor, Patch: s.Patch + 1}, nil
	case BumpMinor:
		return Semver{Major: s.Major, Minor: s.Minor + 1}, nil
	case BumpMajor:
		return Semver{Major: s.Major + 1}, nil
	default:
		return Semver{}, fmt.Errorf("%w: %q is not patch, minor or major", ErrInvalidBump, bump)
	}
}

// Next computes the version that follows lastTag. An empty lastTag means the
// repository has no release yet, and the increment is applied to v0.0.0.
func Next(lastTag string, bump Bump) (Semver, error) {
	last := Semver{}
	if strings.TrimSpace(lastTag) != "" {
		parsed, err := Parse(lastTag)
		if err != nil {
			return Semver{}, err
		}
		last = parsed
	}

	return last.Bumped(bump)
}

// SplitLabels reads the comma-separated label list the release workflow passes
// on the command line. Blank entries are dropped: a pull request with no labels
// arrives as an empty string, and joining an empty list can leave stray commas.
func SplitLabels(list string) []string {
	var labels []string

	for _, label := range strings.Split(list, ",") {
		if trimmed := strings.TrimSpace(label); trimmed != "" {
			labels = append(labels, trimmed)
		}
	}

	return labels
}

// BumpFromLabels reads the increment from the labels of a pull request. Exactly
// one release: label is expected — none means the author has not decided yet,
// and two is a mistake worth reporting rather than a tie to break silently.
func BumpFromLabels(labels []string) (Bump, error) {
	var found []Bump

	for _, label := range labels {
		if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(label)), "release:") {
			continue
		}

		bump, err := ParseBump(label)
		if err != nil {
			return "", err
		}
		found = append(found, bump)
	}

	switch len(found) {
	case 0:
		return "", fmt.Errorf("%w: expected one of release:patch, release:minor or release:major", ErrNoBumpLabel)
	case 1:
		return found[0], nil
	default:
		return "", fmt.Errorf("%w: %d release labels on the same pull request", ErrAmbiguousBump, len(found))
	}
}

// Conventional Commit types that carry a release on their own. Everything else
// — docs, chore, style, test, ci, build — says nothing about the version.
var patchTypes = map[string]bool{"fix": true, "perf": true, "refactor": true, "revert": true}

// A type, an optional scope, an optional "!" for a break, and a description
// that is actually there. The specification requires the space and the text
// after the colon, and "feat:" alone says nothing worth releasing.
var conventionalSubject = regexp.MustCompile(`^([a-z]+)(\([^)]*\))?(!)?: \S`)

// ConventionalSubject reads a Conventional Commit subject line. It reports the
// type, whether the subject declares a break with "!", and whether the subject
// is well formed at all. The commit checker uses it to gate the convention that
// this package depends on when a pull request carries no release label.
func ConventionalSubject(subject string) (commitType string, breaking bool, ok bool) {
	match := conventionalSubject.FindStringSubmatch(strings.TrimSpace(subject))
	if match == nil {
		return "", false, false
	}

	return match[1], match[3] == "!", true
}

// A breaking change is declared by a footer, so the match is anchored to the
// start of a line. Searching the whole message for the words instead would let a
// commit that merely writes about the convention publish a major release, and
// both spellings count: the hyphenated one is what Git parses as a trailer.
var breakingChangeFooter = regexp.MustCompile(`(?m)^BREAKING[ -]CHANGE\s*:`)

// BumpFromCommits derives the increment from Conventional Commit messages. It
// refuses to answer when the history says nothing about the version: publishing
// a release nobody asked for is worse than failing and asking for the label.
func BumpFromCommits(messages []string) (Bump, error) {
	best := Bump("")

	for _, message := range messages {
		switch commitBump(message) {
		case BumpMajor:
			return BumpMajor, nil
		case BumpMinor:
			best = BumpMinor
		case BumpPatch:
			if best == "" {
				best = BumpPatch
			}
		}
	}

	if best == "" {
		return "", fmt.Errorf("%w: no commit in the range carries a release type", ErrAmbiguousBump)
	}

	return best, nil
}

func commitBump(message string) Bump {
	if breakingChangeFooter.MatchString(message) {
		return BumpMajor
	}

	subject, _, _ := strings.Cut(message, "\n")
	commitType, breaking, ok := ConventionalSubject(subject)
	if !ok {
		return ""
	}

	if breaking {
		return BumpMajor
	}

	switch {
	case commitType == "feat":
		return BumpMinor
	case patchTypes[commitType]:
		return BumpPatch
	default:
		return ""
	}
}

// ResolveBump decides the increment for a release: the label if there is one,
// the commit history otherwise, and an error when neither is conclusive.
func ResolveBump(labels []string, messages []string) (Bump, error) {
	bump, err := BumpFromLabels(labels)
	if err == nil {
		return bump, nil
	}
	if !errors.Is(err, ErrNoBumpLabel) {
		return "", err
	}

	return BumpFromCommits(messages)
}
