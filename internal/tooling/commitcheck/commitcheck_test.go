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

	for _, name := range []string{"claude/fix-thing", "feat/ai-generated-tests", "anthropic-experiment"} {
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
