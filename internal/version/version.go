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

	return strconv.Atoi(part)
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
