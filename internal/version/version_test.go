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

func TestBumpFromLabels(t *testing.T) {
	t.Parallel()

	got, err := version.BumpFromLabels([]string{"bug", "release:minor", "needs-review"})
	if err != nil {
		t.Fatalf("BumpFromLabels returned error: %v", err)
	}
	if got != version.BumpMinor {
		t.Errorf("BumpFromLabels = %q, want %q", got, version.BumpMinor)
	}
}

func TestBumpFromLabelsRequiresExactlyOne(t *testing.T) {
	t.Parallel()

	if _, err := version.BumpFromLabels([]string{"bug"}); !errors.Is(err, version.ErrNoBumpLabel) {
		t.Errorf("no release label: error = %v, want ErrNoBumpLabel", err)
	}
	if _, err := version.BumpFromLabels(nil); !errors.Is(err, version.ErrNoBumpLabel) {
		t.Errorf("nil labels: error = %v, want ErrNoBumpLabel", err)
	}
	// Two labels is not a tie to break silently: it is a mistake to report.
	if _, err := version.BumpFromLabels([]string{"release:minor", "release:patch"}); !errors.Is(err, version.ErrAmbiguousBump) {
		t.Errorf("two release labels: error = %v, want ErrAmbiguousBump", err)
	}
}

func TestSplitLabels(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"single label", "release:minor", []string{"release:minor"}},
		{"several labels", "bug,release:patch,needs-review", []string{"bug", "release:patch", "needs-review"}},
		{"surrounding spaces", " bug , release:major ", []string{"bug", "release:major"}},
		// A pull request with no labels arrives as an empty string, and a join
		// of an empty list can leave stray commas. Neither is a label.
		{"no labels", "", nil},
		{"only spaces", "   ", nil},
		{"empty entries", "bug,,release:minor,", []string{"bug", "release:minor"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := version.SplitLabels(tc.in)
			if len(got) != len(tc.want) {
				t.Fatalf("SplitLabels(%q) = %q, want %q", tc.in, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("SplitLabels(%q)[%d] = %q, want %q", tc.in, i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestBumpFromCommits(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		messages []string
		want     version.Bump
	}{
		{"feature wins over fix", []string{"fix: a", "feat: b"}, version.BumpMinor},
		{"fix alone", []string{"fix: a", "docs: b"}, version.BumpPatch},
		{"refactor counts as patch", []string{"refactor: a"}, version.BumpPatch},
		{"bang marks a break", []string{"feat!: drop the old profile format"}, version.BumpMajor},
		{"scoped bang", []string{"feat(profile)!: rename a field"}, version.BumpMajor},
		{"breaking change footer", []string{"feat: a\n\nBREAKING CHANGE: the CLI flag is gone"}, version.BumpMajor},
		// Both spellings are footers in the Conventional Commits specification,
		// and the hyphenated one is the form Git trailers actually parse.
		{"hyphenated breaking change footer", []string{"fix: a\n\nBREAKING-CHANGE: the CLI flag is gone"}, version.BumpMajor},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := version.BumpFromCommits(tc.messages)
			if err != nil {
				t.Fatalf("BumpFromCommits returned error: %v", err)
			}
			if got != tc.want {
				t.Errorf("BumpFromCommits(%v) = %q, want %q", tc.messages, got, tc.want)
			}
		})
	}
}

// Guessing a release from commits nobody wrote for that purpose is how a
// pipeline publishes a version nobody meant. When the history says nothing, the
// workflow must stop and ask for the label.
func TestBumpFromCommitsRefusesToGuess(t *testing.T) {
	t.Parallel()

	for _, messages := range [][]string{
		nil,
		{"chore: tidy"},
		{"docs: fix a typo", "style: reformat"},
		{"whatever this is"},
	} {
		if _, err := version.BumpFromCommits(messages); !errors.Is(err, version.ErrAmbiguousBump) {
			t.Errorf("BumpFromCommits(%v) error = %v, want ErrAmbiguousBump", messages, err)
		}
	}
}

// A breaking change is declared by a footer, which is a line of its own. Writing
// about the convention is not declaring one, and a commit that only mentions the
// words must never publish a major release nobody asked for.
func TestBumpFromCommitsIgnoresBreakingChangeInProse(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		message string
		want    version.Bump
	}{
		{"documented in the subject", "docs: explain that BREAKING CHANGE bumps major", ""},
		{"mentioned mid-sentence", "fix: a\n\nThis is not a BREAKING CHANGE: the flag still works.", version.BumpPatch},
		{"quoted in the body", "feat: a\n\nThe release notes say \"BREAKING CHANGE\" only when it breaks.", version.BumpMinor},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := version.BumpFromCommits([]string{tc.message})
			if tc.want == "" {
				if !errors.Is(err, version.ErrAmbiguousBump) {
					t.Fatalf("BumpFromCommits(%q) error = %v, want ErrAmbiguousBump", tc.message, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("BumpFromCommits(%q) returned error: %v", tc.message, err)
			}
			if got != tc.want {
				t.Errorf("BumpFromCommits(%q) = %q, want %q", tc.message, got, tc.want)
			}
		})
	}
}

func TestResolveBumpPrefersTheLabel(t *testing.T) {
	t.Parallel()

	got, err := version.ResolveBump([]string{"release:patch"}, []string{"feat: a big feature"})
	if err != nil {
		t.Fatalf("ResolveBump returned error: %v", err)
	}
	if got != version.BumpPatch {
		t.Errorf("ResolveBump = %q, want the label to win with %q", got, version.BumpPatch)
	}
}

func TestResolveBumpFallsBackToCommits(t *testing.T) {
	t.Parallel()

	got, err := version.ResolveBump(nil, []string{"feat: a new capability"})
	if err != nil {
		t.Fatalf("ResolveBump returned error: %v", err)
	}
	if got != version.BumpMinor {
		t.Errorf("ResolveBump = %q, want %q", got, version.BumpMinor)
	}

	if _, err := version.ResolveBump(nil, []string{"chore: tidy"}); !errors.Is(err, version.ErrAmbiguousBump) {
		t.Errorf("with nothing to go on: error = %v, want ErrAmbiguousBump", err)
	}
}

// A release: label that names no known increment is a typo on the pull request,
// not an absent decision. Falling back to the commits there would publish a
// version the author did not ask for, so the mistake has to surface.
func TestResolveBumpReportsAnUnknownReleaseLabel(t *testing.T) {
	t.Parallel()

	_, err := version.ResolveBump([]string{"release:candidate"}, []string{"feat: a new capability"})
	if !errors.Is(err, version.ErrInvalidBump) {
		t.Errorf("ResolveBump with an unknown release label: error = %v, want ErrInvalidBump", err)
	}
}
