package commitcheck_test

import (
	"strings"
	"testing"

	"github.com/gsoares85/hermes/internal/tooling/commitcheck"
)

func TestCommitsRejectsAttribution(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"co-authored-by trailer": "feat: add thing\n\nBody.\n\nCo-Authored-By: Some Assistant <noreply@example.com>",
		"lowercase trailer":      "feat: add thing\n\nco-authored-by: someone <a@b.c>",
		"generated with claude":  "feat: add thing\n\n🤖 Generated with Claude Code",
		"written by an llm":      "fix: correct path\n\nWritten by Claude, reviewed by a human.",
		"assisted by chatgpt":    "chore: tidy\n\nAssisted by ChatGPT.",
		"session trailer":        "feat: add thing\n\nClaude-Session: https://example.com/session",
		"vendor trailer value":   "feat: add thing\n\nGenerated-With: Anthropic Claude",
		"copilot attribution":    "feat: add thing\n\nCreated by GitHub Copilot",
		"implemented using":      "feat: add thing\n\nImplemented using Claude Code",
		"written via":            "fix: correct path\n\nWritten via ChatGPT.",
		"trailer without colon":  "feat: add thing\n\nCo-authored-by Claude <noreply@example.com>",
		"with the help of":       "chore: tidy\n\nRefactored with the help of Gemini.",
	}

	for name, message := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := commitcheck.Commits([]commitcheck.Commit{{Hash: "abc1234", Message: message}})
			if len(got) == 0 {
				t.Fatalf("message %q produced no violation, want one", message)
			}
			if got[0].Hash != "abc1234" {
				t.Errorf("violation hash = %q, want %q", got[0].Hash, "abc1234")
			}
		})
	}
}

// The rule bans attribution, not the substring. A commit that legitimately talks
// about a file or a tool must still pass, otherwise people learn to work around
// the check instead of respecting it.
func TestCommitsAcceptsLegitimateMentions(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"conventional subject":   "feat: add version resolution and semver bump rules",
		"body mentioning a file": "docs: update the claude.md path used by the docs script\n\nThe file moved.",
		"word inside another":    "fix: stop the email retry loop from failing silently",
		"signed off":             "feat: add thing\n\nSigned-off-by: Guilherme Soares <guilherme@example.com>",
		"ai in prose":            "feat: create the ai package placeholder for a later task",
		"generated code":         "chore: regenerate the parser from the grammar file",
		// Writing about the rule is not breaking it. A trailer names an
		// identity; a sentence that merely opens with the words does not.
		"trailer named in prose": "fix: tighten the checker\n\nCo-authored-by trailers are rejected even without their colon.",
		"rule described":         "docs: explain the co-authored-by rule and why it exists",
	}

	for name, message := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := commitcheck.Commits([]commitcheck.Commit{{Message: message}}); len(got) != 0 {
				t.Errorf("message %q produced %d violations (%v), want none", message, len(got), got)
			}
		})
	}
}

// The rule is about the metadata too, not only the prose. A tool configured
// with its own Git identity writes an impeccable message and still signs the
// commit as itself, which is exactly the case a message-only check misses.
func TestCommitsRejectsAnAssistantIdentity(t *testing.T) {
	t.Parallel()

	cases := map[string]commitcheck.Commit{
		"author name": {
			Hash: "abc", Message: "feat: add thing",
			AuthorName: "Claude", AuthorEmail: "noreply@anthropic.com",
		},
		"author email only": {
			Hash: "abc", Message: "feat: add thing",
			AuthorName: "Someone", AuthorEmail: "noreply@anthropic.com",
		},
		"committer name": {
			Hash: "abc", Message: "feat: add thing",
			AuthorName: "Guilherme Soares", AuthorEmail: "guilherme@example.com",
			CommitterName: "GitHub Copilot", CommitterEmail: "copilot@example.com",
		},
		"assistant handle": {
			Hash: "abc", Message: "feat: add thing",
			AuthorName: "chatgpt-bot", AuthorEmail: "bot@example.com",
		},
	}

	for name, commit := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := commitcheck.Commits([]commitcheck.Commit{commit})
			if len(got) == 0 {
				t.Fatalf("commit %+v produced no violation, want one", commit)
			}
			if got[0].Hash != "abc" {
				t.Errorf("violation hash = %q, want %q", got[0].Hash, "abc")
			}
		})
	}
}

func TestCommitsAcceptsAHumanIdentity(t *testing.T) {
	t.Parallel()

	got := commitcheck.Commits([]commitcheck.Commit{{
		Hash: "abc", Message: "feat: add version resolution",
		AuthorName: "Guilherme Soares", AuthorEmail: "guilhermeluzsoares@gmail.com",
		CommitterName: "Guilherme Soares", CommitterEmail: "guilhermeluzsoares@gmail.com",
	}})
	if len(got) != 0 {
		t.Errorf("a human identity produced %d violations (%v), want none", len(got), got)
	}
}

// The release falls back to Conventional Commits when a pull request carries no
// label, so the convention has to be a gate. Trusting it to discipline is how
// "Fix stuff" ends up deciding a version.
func TestCommitsRejectsANonConventionalSubject(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"no type":           "Fix stuff",
		"unknown type":      "improvement: make it faster",
		"missing colon":     "feat add the thing",
		"uppercase type":    "Feat: add the thing",
		"empty description": "feat:",
		"too long":          "feat: add a subject that is definitely longer than the seventy two character limit",
	}

	for name, subject := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := commitcheck.Commits([]commitcheck.Commit{{Hash: "abc", Message: subject}})
			if len(got) == 0 {
				t.Errorf("subject %q produced no violation, want one", subject)
			}
		})
	}
}

func TestCommitsAcceptsConventionalSubjects(t *testing.T) {
	t.Parallel()

	for _, subject := range []string{
		"feat: add version resolution and semver bump rules",
		"fix(release): tag the commit that lands on main",
		"feat!: drop the old profile format",
		"refactor(core)!: rename a field",
		"ci: pin actions by commit",
		"docs: add project readme with status badges",
		"chore: tidy",
		"build: scaffold go module and package layout",
		"test: add testcontainers harness",
		"perf: keyset the grid",
		"style: reformat",
		"revert: undo the parser change",
	} {
		t.Run(subject, func(t *testing.T) {
			t.Parallel()

			body := subject + "\n\nA body explaining what and why."
			if got := commitcheck.Commits([]commitcheck.Commit{{Message: body}}); len(got) != 0 {
				t.Errorf("subject %q produced %d violations (%v), want none", subject, len(got), got)
			}
		})
	}
}

// A merge commit is written by Git, not by a person, so the subject convention
// does not apply to it. The attribution rules still do.
func TestCommitsExemptsMergeCommitsFromTheSubjectRule(t *testing.T) {
	t.Parallel()

	merge := commitcheck.Commit{Hash: "abc", Merge: true, Message: "Merge branch 'main' into feat/TASK-0001"}
	if got := commitcheck.Commits([]commitcheck.Commit{merge}); len(got) != 0 {
		t.Errorf("merge commit produced %d violations (%v), want none", len(got), got)
	}

	credited := commitcheck.Commit{Hash: "abc", Merge: true, Message: "Merge pull request #1\n\nCo-Authored-By: x <x@y.z>"}
	if got := commitcheck.Commits([]commitcheck.Commit{credited}); len(got) == 0 {
		t.Error("merge commit with a trailer produced no violation, want one")
	}
}

func TestCommitsReportsEveryOffendingCommit(t *testing.T) {
	t.Parallel()

	got := commitcheck.Commits([]commitcheck.Commit{
		{Hash: "aaa", Message: "feat: fine"},
		{Hash: "bbb", Message: "feat: bad\n\nCo-Authored-By: x <x@y.z>"},
		{Hash: "ccc", Message: "feat: also bad\n\nGenerated with Claude Code"},
	})

	if len(got) != 2 {
		t.Fatalf("got %d violations, want 2: %v", len(got), got)
	}
	if got[0].Hash != "bbb" || got[1].Hash != "ccc" {
		t.Errorf("violations = %v, want bbb then ccc", got)
	}
}

func TestViolationExplainsItself(t *testing.T) {
	t.Parallel()

	got := commitcheck.Commits([]commitcheck.Commit{
		{Hash: "abc1234", Message: "feat: x\n\nCo-Authored-By: Some Assistant <a@b.c>"},
	})

	if len(got) != 1 {
		t.Fatalf("got %d violations, want 1", len(got))
	}
	message := got[0].String()
	if !strings.Contains(message, "abc1234") {
		t.Errorf("violation %q does not name the commit", message)
	}
	if !strings.Contains(strings.ToLower(message), "co-authored-by") {
		t.Errorf("violation %q does not quote the offending line", message)
	}
}

func TestBranchRejectsAttribution(t *testing.T) {
	t.Parallel()

	// The underscore separates words as much as the hyphen does, but it is a
	// word character to the regexp engine, so a name that uses it must not be
	// allowed to hide the word its hyphenated twin is rejected for.
	for _, name := range []string{
		"claude/fix-thing",
		"feat/ai-generated-tests",
		"feat/ai_generated_tests",
		"anthropic-experiment",
		"feat/llm_helper",
		"feat/generated_by_copilot",
	} {
		if got := commitcheck.Branch(name); len(got) == 0 {
			t.Errorf("branch %q produced no violation, want one", name)
		}
	}
}

func TestBranchAcceptsProjectNaming(t *testing.T) {
	t.Parallel()

	for _, name := range []string{
		"feat/TASK-0001-scaffolding-e-pipeline",
		"fix/TASK-0007-statement-parser",
		"main",
		"refactor/TASK-0004-catalog-normalization",
		// Treating the underscore as a separator must not start rejecting
		// names that merely use one.
		"feat/TASK-0009-connection_pool",
		"fix/retry_email_loop",
	} {
		if got := commitcheck.Branch(name); len(got) != 0 {
			t.Errorf("branch %q produced %d violations (%v), want none", name, len(got), got)
		}
	}
}

func TestValidateRevisionRange(t *testing.T) {
	t.Parallel()

	for _, valid := range []string{"main..HEAD", "origin/main..HEAD", "v0.1.0...HEAD", "HEAD~3..HEAD", "abc1234"} {
		if err := commitcheck.ValidateRevisionRange(valid); err != nil {
			t.Errorf("ValidateRevisionRange(%q) = %v, want nil", valid, err)
		}
	}

	// The range reaches a subprocess, so anything that could turn into another
	// argument or another command is refused before it gets there.
	for _, invalid := range []string{"", "main..HEAD; rm -rf /", "--output=/etc/passwd", "main..HEAD && echo", "$(whoami)", "a b"} {
		if err := commitcheck.ValidateRevisionRange(invalid); err == nil {
			t.Errorf("ValidateRevisionRange(%q) = nil, want an error", invalid)
		}
	}
}
