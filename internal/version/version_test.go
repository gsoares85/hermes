package version_test

import (
	"errors"
	"testing"

	"github.com/gsoares85/hermes/internal/version"
)

func TestParse(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   string
		want version.Semver
	}{
		{"with v prefix", "v1.2.3", version.Semver{Major: 1, Minor: 2, Patch: 3}},
		{"without v prefix", "1.2.3", version.Semver{Major: 1, Minor: 2, Patch: 3}},
		{"zero version", "v0.0.0", version.Semver{}},
		{"multi digit", "v10.20.30", version.Semver{Major: 10, Minor: 20, Patch: 30}},
		{"surrounding spaces", "  v1.0.0\n", version.Semver{Major: 1}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := version.Parse(tc.in)
			if err != nil {
				t.Fatalf("Parse(%q) returned error: %v", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("Parse(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestParseRejectsMalformedTags(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   string
	}{
		{"empty", ""},
		{"missing patch", "v1.2"},
		{"extra component", "v1.2.3.4"},
		{"pre-release suffix", "v1.2.3-beta.1"},
		{"build metadata", "v1.2.3+deadbeef"},
		{"not a number", "vx.2.3"},
		{"negative", "v-1.2.3"},
		{"leading zero", "v01.2.3"},
		{"only text", "release"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if _, err := version.Parse(tc.in); !errors.Is(err, version.ErrInvalidVersion) {
				t.Errorf("Parse(%q) error = %v, want ErrInvalidVersion", tc.in, err)
			}
		})
	}
}

func TestSemverString(t *testing.T) {
	t.Parallel()

	got := version.Semver{Major: 1, Minor: 20, Patch: 3}.String()
	if want := "v1.20.3"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}

func TestParseBump(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in   string
		want version.Bump
	}{
		{"patch", version.BumpPatch},
		{"minor", version.BumpMinor},
		{"major", version.BumpMajor},
		{"MINOR", version.BumpMinor},
		{" release:major ", version.BumpMajor},
		{"release:patch", version.BumpPatch},
	}

	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()

			got, err := version.ParseBump(tc.in)
			if err != nil {
				t.Fatalf("ParseBump(%q) returned error: %v", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("ParseBump(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestParseBumpRejectsUnknownValues(t *testing.T) {
	t.Parallel()

	for _, in := range []string{"", "release", "breaking", "release:minor:extra", "v1.2.3"} {
		if _, err := version.ParseBump(in); !errors.Is(err, version.ErrInvalidBump) {
			t.Errorf("ParseBump(%q) error = %v, want ErrInvalidBump", in, err)
		}
	}
}

func TestNext(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		last string
		bump version.Bump
		want string
	}{
		{"first release is minor", "", version.BumpMinor, "v0.1.0"},
		{"first release is patch", "", version.BumpPatch, "v0.0.1"},
		{"first release is major", "", version.BumpMajor, "v1.0.0"},
		{"patch keeps minor", "v0.4.2", version.BumpPatch, "v0.4.3"},
		{"minor resets patch", "v0.4.2", version.BumpMinor, "v0.5.0"},
		{"major resets minor and patch", "v0.4.2", version.BumpMajor, "v1.0.0"},
		{"major after 1.0", "v1.9.9", version.BumpMajor, "v2.0.0"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := version.Next(tc.last, tc.bump)
			if err != nil {
				t.Fatalf("Next(%q, %q) returned error: %v", tc.last, tc.bump, err)
			}
			if got.String() != tc.want {
				t.Errorf("Next(%q, %q) = %q, want %q", tc.last, tc.bump, got, tc.want)
			}
		})
	}
}

func TestNextRejectsInvalidInput(t *testing.T) {
	t.Parallel()

	if _, err := version.Next("not-a-tag", version.BumpPatch); !errors.Is(err, version.ErrInvalidVersion) {
		t.Errorf("Next with malformed tag error = %v, want ErrInvalidVersion", err)
	}
	if _, err := version.Next("v1.0.0", version.Bump("sideways")); !errors.Is(err, version.ErrInvalidBump) {
		t.Errorf("Next with unknown bump error = %v, want ErrInvalidBump", err)
	}
}

// The release pipeline injects the version with -ldflags. A hardcoded number in
// the source would silently outlive the tag it came from, so the default must
// stay a non-numeric placeholder.
func TestDefaultBuildInfoIsNotAVersionNumber(t *testing.T) {
	t.Parallel()

	info := version.Current()
	if _, err := version.Parse(info.Version); err == nil {
		t.Errorf("Current().Version = %q, want a placeholder, not a parseable version", info.Version)
	}
	if info.Version != "dev" {
		t.Errorf("Current().Version = %q, want %q", info.Version, "dev")
	}
	if info.Platform == "" {
		t.Error("Current().Platform is empty, want os/arch")
	}
}

func TestInfoString(t *testing.T) {
	t.Parallel()

	info := version.Info{Version: "v1.2.3", Commit: "abc1234", Date: "2026-08-30", Platform: "linux/amd64"}
	if got, want := info.String(), "hermes v1.2.3 (abc1234, 2026-08-30, linux/amd64)"; got != want {
		t.Errorf("Info.String() = %q, want %q", got, want)
	}
}
